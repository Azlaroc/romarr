package download

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// discSetTimeout is how long an incomplete disc set may wait for its missing
// members before the sweep finalizes it degraded over whatever landed
// (SELECTOR_SET_TIMEOUT_HOURS, default 24h).
func (m *Manager) discSetTimeout() time.Duration {
	return time.Duration(m.cfg.DiscSetTimeoutHours()) * time.Hour
}

// DiscSet identifies one member of a multi-disc download group. All members
// share ID/Total/Dir and converge into one game directory; fulfillment defers
// normalize/convert/m3u/library-track until the last member lands (or the
// sweep gives up). The zero value means "not a set member".
type DiscSet struct {
	ID    string // set identity, minted by the caller (selector or API client)
	Index int    // 1-based disc number
	Total int    // member count the set is complete at
	Dir   string // shared game-dir name (disc token stripped, single component)
}

func (d DiscSet) valid() bool { return d.ID != "" }

// applyDiscSetJobData stamps a set membership onto a job blob at create time.
// set_started_at anchors the sweep's degrade timeout: not every job path
// writes started_at, and set semantics need one uniform clock.
func applyDiscSetJobData(jobData map[string]interface{}, set DiscSet) {
	if !set.valid() {
		return
	}
	jobData["disc_set_id"] = set.ID
	jobData["disc_index"] = set.Index
	jobData["disc_total"] = set.Total
	jobData["set_dir"] = set.Dir
	jobData["set_started_at"] = time.Now().Unix()
}

// discSetFromJob reads a job's set membership; zero DiscSet for non-members.
func discSetFromJob(job map[string]interface{}) DiscSet {
	id, _ := job["disc_set_id"].(string)
	if id == "" {
		return DiscSet{}
	}
	dir, _ := job["set_dir"].(string)
	return DiscSet{
		ID:    id,
		Index: int(int64Value(job["disc_index"])),
		Total: int(int64Value(job["disc_total"])),
		Dir:   dir,
	}
}

// discSetMember is one member job as seen by the barrier/sweep.
type discSetMember struct {
	jobID string
	data  map[string]interface{}
	set   DiscSet
}

func (m discSetMember) done() bool {
	v, _ := m.data["set_member_done"].(bool)
	return v
}

func (m discSetMember) finalized() bool {
	v, _ := m.data["set_finalized"].(bool)
	return v
}

func (m discSetMember) terminal() bool {
	s, _ := m.data["status"].(string)
	return s == "error" || s == "interrupted"
}

// discSetMembers scans the job store for members of one set, sorted by disc
// index. Linear scan over the in-memory job map — jobs number in the dozens.
func (m *Manager) discSetMembers(discSetID string) []discSetMember {
	var members []discSetMember
	for _, item := range m.jobs.Items() {
		set := discSetFromJob(item.Data)
		if set.ID == discSetID {
			members = append(members, discSetMember{jobID: item.ID, data: item.Data, set: set})
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].set.Index < members[j].set.Index })
	return members
}

// maybeFinalizeDiscSet runs after every member import: when every declared
// member has landed, the deferred pipeline tail runs once over the shared set
// directory. Safe to call redundantly — membership is re-read from the job
// store and finalize is single-flighted and idempotent.
func (m *Manager) maybeFinalizeDiscSet(discSetID string) {
	if discSetID == "" {
		return
	}
	members := m.discSetMembers(discSetID)
	if len(members) == 0 {
		return
	}
	total := 0
	done := 0
	for _, mem := range members {
		if mem.set.Total > total {
			total = mem.set.Total
		}
		if mem.done() {
			done++
		}
		if mem.finalized() {
			return // a prior finalize already ran to completion
		}
	}
	if total == 0 || done < total {
		return
	}
	m.finalizeDiscSet(discSetID, members, false)
}

// finalizeDiscSet runs the deferred tail — normalize (DAT rename + m3u),
// convert, sidecar, one library row — over the set directory, then marks every
// member finalized. degraded=true finalizes an incomplete set (sweep path):
// same tail over whatever landed, flagged in detail and activity.
func (m *Manager) finalizeDiscSet(discSetID string, members []discSetMember, degraded bool) {
	if _, busy := m.importing.LoadOrStore("set:"+discSetID, struct{}{}); busy {
		return
	}
	defer m.importing.Delete("set:" + discSetID)

	// Re-read membership under the flight: a concurrent finalize may have
	// completed between the caller's scan and the lock.
	members = m.discSetMembers(discSetID)
	if len(members) == 0 {
		return
	}
	for _, mem := range members {
		if mem.finalized() {
			return
		}
	}

	lead := members[0]
	total := lead.set.Total
	imported := 0
	hashStatus := ""
	anyOK := false
	title := lead.set.Dir
	platf, _ := lead.data["platform"].(string)
	platSlug, _ := lead.data["platform_slug"].(string)
	source, _ := lead.data["source_type"].(string)
	if source == "" {
		source = "ddl" // the one job path that writes no source_type
	}
	sourceClient, _ := lead.data["source_client"].(string)
	if sourceClient == "" {
		sourceClient = source
	}
	for _, mem := range members {
		if !mem.done() {
			continue
		}
		imported++
		switch hs, _ := mem.data["convert_hash_status"].(string); hs {
		case "mismatch":
			hashStatus = "mismatch"
		case "ok":
			anyOK = true
		}
	}
	if hashStatus != "mismatch" && anyOK {
		hashStatus = "ok"
	}

	markAll := func(detail string) {
		for _, mem := range members {
			m.jobs.UpdateMulti(mem.jobID, map[string]interface{}{
				"set_finalized": true, "detail": detail,
			})
		}
	}

	if imported == 0 {
		markAll(fmt.Sprintf("Disc set failed (0 of %d imported)", total))
		m.jobs.LogActivity("download_failed", title,
			fmt.Sprintf("Disc set failed: no discs imported (%s)", platf), lead.jobID, nil)
		slog.Warn("disc set failed with no imports", "set", discSetID, "title", sanitizeLog(title))
		return
	}

	setPath := filepath.Join(m.romDestDir(platSlug), sanitizeFilename(lead.set.Dir))
	if !pathExists(setPath) {
		markAll("Disc set directory missing at finalize")
		slog.Error("disc set dir missing at finalize", "set", discSetID, "path", sanitizeLog(setPath))
		return
	}

	// The deferred tail, once, over the whole game dir: DAT rename + .m3u
	// (normalize is directory-aware), CHD convert per disc + m3u regen,
	// sidecar, ONE library row keyed on the set — idempotent via the
	// source_id dedupe in TrackInLibrary.
	finalPath := m.MaybeNormalize(lead.jobID, setPath, platSlug)
	finalPath = m.MaybeConvert(lead.jobID, finalPath, platSlug, hashStatus)
	writeMetadataSidecar(finalPath, title, platf, platSlug, false, source)
	size := contentSize(finalPath)
	// Per-disc release hashes stay unpersisted (one row vs many hashes is
	// ambiguous); the set's durable record is the $.gamarr.set marker below.
	added := m.TrackInLibrary(title, platf, platSlug, false, finalPath,
		size, source, sourceClient, "set:"+discSetID, lead.jobID, "", "")

	// Durable set record: the member job blobs are the only other evidence of
	// a degrade and the 7-day job cleanup purges them, so the composition and
	// repair state live on the library row's metadata under $.gamarr.set.
	if item := m.jobs.FindLibraryBySourceID("set:" + discSetID); item != nil {
		prev, _ := db.ParseSetMarker(item.Metadata)
		mk := db.SetMarker{
			ID:             discSetID,
			Total:          total,
			Degraded:       degraded,
			RepairAttempts: prev.RepairAttempts,
			DegradedAt:     prev.DegradedAt,
			RepairedAt:     prev.RepairedAt,
		}
		for _, mem := range members {
			if mem.done() {
				mk.Have = append(mk.Have, mem.set.Index)
			}
		}
		if degraded {
			mk.Exhausted = prev.Exhausted // a re-degrade keeps the give-up latch
			if mk.DegradedAt == "" {
				mk.DegradedAt = time.Now().UTC().Format(time.RFC3339)
			}
		} else if prev.Degraded {
			mk.RepairedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if err := m.jobs.SaveSetMarker(item.ID, mk); err != nil {
			slog.Warn("disc set marker save failed", "set", discSetID, "error", err)
		}
		if !added {
			// Re-finalize over an existing row (repair completion or a
			// re-degrade): TrackInLibrary deduped on source_id, so refresh
			// what it would have written and tell RomM the platform dir
			// changed.
			if err := m.jobs.UpdateLibraryItemFileSize(item.ID, size); err != nil {
				slog.Warn("disc set size refresh failed", "set", discSetID, "error", err)
			}
			if !degraded && m.ImportNotify != nil {
				m.ImportNotify(platform.ToRommFSSlug(platSlug))
			}
		}
	}

	detail := fmt.Sprintf("Disc set complete (%d discs)", imported)
	if degraded {
		detail = fmt.Sprintf("Incomplete disc set (%d of %d imported)", imported, total)
	}
	markAll(detail)
	m.jobs.LogActivity("download_completed", title,
		fmt.Sprintf("%s (%s)", detail, platf), lead.jobID, nil)
	slog.Info("disc set finalized", "set", discSetID, "title", sanitizeLog(title),
		"discs", imported, "degraded", degraded)
}

// flattenSetMemberDir hoists an extracted disc's top-level entries out of its
// per-disc subdir into the shared set dir, so sibling discs' .cue/.gdi files
// end up side by side — the layout the m3u writer groups on. Any name
// collision keeps the subdir untouched (Redump disc files are uniquely named,
// so a collision means something unexpected). Returns the resulting path the
// member's content lives at.
func flattenSetMemberDir(memberDir, setDir string) string {
	entries, err := os.ReadDir(memberDir)
	if err != nil {
		return memberDir
	}
	for _, e := range entries {
		if pathExists(filepath.Join(setDir, e.Name())) {
			slog.Warn("disc-set flatten skipped: name collision in set dir",
				"entry", sanitizeLog(e.Name()), "dir", sanitizeLog(memberDir))
			return memberDir
		}
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(memberDir, e.Name()), filepath.Join(setDir, e.Name())); err != nil {
			slog.Warn("disc-set flatten failed mid-move; keeping remainder in subdir",
				"entry", sanitizeLog(e.Name()), "error", err)
			return memberDir
		}
	}
	_ = os.Remove(memberDir)
	return setDir
}

// SweepDiscSets finalizes stuck disc sets: complete sets whose finalize never
// ran (crash between last import and barrier), sets whose missing members all
// terminally failed, and sets older than discSetTimeout. Called at boot after
// job recovery and from the periodic cleanup loop — the effective degrade time
// rounds up to that loop's 6h cadence.
func (m *Manager) SweepDiscSets() {
	byID := map[string][]discSetMember{}
	for _, item := range m.jobs.Items() {
		set := discSetFromJob(item.Data)
		if set.ID != "" {
			byID[set.ID] = append(byID[set.ID], discSetMember{jobID: item.ID, data: item.Data, set: set})
		}
	}
	for id, members := range byID {
		sort.Slice(members, func(i, j int) bool { return members[i].set.Index < members[j].set.Index })
		total, done, pending := 0, 0, 0
		oldest := int64(0)
		skip := false
		for _, mem := range members {
			if mem.finalized() {
				skip = true
				break
			}
			if mem.set.Total > total {
				total = mem.set.Total
			}
			switch {
			case mem.done():
				done++
			case mem.terminal():
			default:
				pending++
			}
			if ts := int64Value(mem.data["set_started_at"]); oldest == 0 || (ts > 0 && ts < oldest) {
				oldest = ts
			}
		}
		if skip {
			continue
		}
		switch {
		case total > 0 && done >= total:
			// Complete but never finalized (crash between import and barrier).
			m.finalizeDiscSet(id, members, false)
		case pending == 0:
			// Nothing in flight and the set can never complete.
			m.finalizeDiscSet(id, members, true)
		case oldest > 0 && time.Since(time.Unix(oldest, 0)) > m.discSetTimeout():
			m.finalizeDiscSet(id, members, true)
		}
	}
}
