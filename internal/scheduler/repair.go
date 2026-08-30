// Degraded disc-set repair: a set that finalized with fewer discs than
// declared has a library row (so ownership skips it), no wishlist row (the
// degraded finalize consumed it), and a set_finalized latch on every member —
// nothing retries it. This pass drives re-grabs of ONLY the missing disc
// indices off the durable $.gamarr.set markers, then re-arms the finalize
// barrier so the landing discs re-finalize the set complete.
package scheduler

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"gamarr/internal/db"
	"gamarr/internal/selection"
)

// maxSetRepairAttempts caps repair cycles per degraded set. Every evaluated
// cycle costs one attempt — including "no candidates" — so an unfindable disc
// stops burning searches. The exhausted latch is one-way; manual reset means
// deleting the row + set dir and re-wishlisting the title.
const maxSetRepairAttempts = 5

// repairDegradedSets runs one repair pass and returns the grabs dispatched.
// Enforce-only: it reuses the selector machinery, and shadow/off deployments
// have not opted into selector-driven downloads.
func (s *Scheduler) repairDegradedSets(mode string) int {
	if mode != "enforce" || !s.cfg.AutoDownload() {
		return 0
	}
	rows := s.jobs.ListDegradedSets()
	if len(rows) == 0 {
		return 0
	}
	minScore := s.cfg.MinScore()
	stop := s.stopChan()
	grabbedTotal := 0
	for _, item := range rows {
		select {
		case <-stop:
			return grabbedTotal
		default:
		}
		mk, ok := db.ParseSetMarker(item.Metadata)
		if !ok || !mk.Degraded || mk.Exhausted || mk.Total <= 0 {
			continue
		}
		setID := setIDFromSourceID(item.SourceID)
		if setID == "" {
			continue
		}

		// One member scan per set: in-flight members block a duplicate repair,
		// and done members supplement the marker's Have — a straggler that
		// landed AFTER the degraded finalize (timeout arm) is on disk but not
		// in the marker, and re-grabbing it would collide in the set dir.
		inFlight, doneIdx := s.setMemberState(setID)
		if inFlight {
			continue
		}
		want := map[int]bool{}
		for i := 1; i <= mk.Total; i++ {
			want[i] = true
		}
		for _, i := range mk.Have {
			delete(want, i)
		}
		for i := range doneIdx {
			delete(want, i)
		}
		if len(want) == 0 {
			// Every disc is accounted for (marker drift or late landers):
			// re-arm the barrier with no dispatch and let the next sweep's
			// done>=total arm re-finalize the set complete.
			slog.Info("scheduler: repair found set whole; reopening for re-finalize", "set", setID, "title", item.Title)
			s.reopenSet(setID, item, mk)
			continue
		}
		if mk.RepairAttempts >= maxSetRepairAttempts {
			mk.Exhausted = true
			if err := s.jobs.SaveSetMarker(item.ID, mk); err != nil {
				slog.Warn("scheduler: repair marker save failed", "set", setID, "error", err)
			}
			s.jobs.LogActivity("repair_exhausted", item.Title,
				fmt.Sprintf("Disc-set repair gave up after %d attempts (missing discs %v)",
					mk.RepairAttempts, sortedIndices(want)), "", nil)
			slog.Warn("scheduler: disc-set repair exhausted", "set", setID, "title", item.Title,
				"attempts", mk.RepairAttempts, "missing", sortedIndices(want))
			continue
		}

		// Rate-limit between repair searches, same as the wishlist loop; a
		// Stop() interrupts the wait immediately.
		select {
		case <-stop:
			return grabbedTotal
		case <-time.After(s.rateLimit):
		}

		// The set row's title is the set dir name; BareTitle reproduces the
		// original query space. SetDir is stamped from the row's actual
		// on-disk dir — normalize may have renamed it since the original grab.
		query := selection.BareTitle(item.Title)
		setDir := filepath.Base(item.FilePath)
		// Repair is library-driven: the wishlist row is long gone, so the
		// platform default is the only profile there is.
		prof := s.jobs.ResolveProfileForItem(0, item.PlatformSlug)
		results := s.searchFn(query, item.PlatformSlug, prof)
		dec := selection.Select(results, selection.SelectOpts{
			Query:        query,
			PlatformSlug: item.PlatformSlug,
			MinScore:     minScore,
			Profile:      prof,
			Collection:   s.jobs.ResolveCollectionProfile(item.PlatformSlug),
			Repair:       &selection.RepairSet{ID: setID, Dir: setDir, Total: mk.Total, Want: want},
		})

		mk.RepairAttempts++
		if err := s.jobs.SaveSetMarker(item.ID, mk); err != nil {
			slog.Warn("scheduler: repair marker save failed", "set", setID, "error", err)
		}
		slog.Info("scheduler: repair decision", "set", setID, "title", item.Title,
			"action", dec.Action.String(), "reason", dec.Reason, "grabs", len(dec.Grabs),
			"want", sortedIndices(want), "attempt", mk.RepairAttempts)
		s.jobs.LogActivity("selector_decision", item.Title,
			fmt.Sprintf("[repair] %s: %s (want %v, attempt %d/%d)", dec.Action.String(),
				dec.Reason, sortedIndices(want), mk.RepairAttempts, maxSetRepairAttempts), "", nil)
		for _, rej := range dec.Rejected {
			slog.Info("selector_rejected", "mode", "repair", "set", setID, "title", item.Title,
				"platform", item.PlatformSlug, "candidate", rej.Title, "reason", rej.Reason)
		}
		if dec.Action != selection.ActionGrabSet {
			continue
		}

		grabbed := 0
		for _, g := range dec.Grabs {
			jobID, err := s.downloadFn(g)
			if err != nil {
				slog.Warn("scheduler: repair download failed", "title", g.Result.Title, "error", err)
				continue
			}
			grabbed++
			s.jobs.LogActivity("scheduler_download", item.Title,
				"Repair re-grab: "+g.Result.Title, jobID, nil)
		}
		if grabbed == 0 {
			continue // set stays finalized; retried next cycle
		}
		// Reopen AFTER dispatch. Reopen-then-dispatch would let a sweep in the
		// window degrade-finalize the all-done reopened set and permanently
		// strand the incoming member behind the finalized guards; in this
		// order a racing sweep merely skips (a member is still finalized), and
		// a member landing before the reopen completes self-heals at the next
		// sweep's done>=total arm.
		s.reopenSet(setID, item, mk)
		grabbedTotal += grabbed
	}
	return grabbedTotal
}

// setMemberState scans the job store once for a set's members: whether any is
// still in flight (the member-level analog of activeGrab), and which disc
// indices already imported.
func (s *Scheduler) setMemberState(setID string) (inFlight bool, doneIdx map[int]bool) {
	doneIdx = map[int]bool{}
	for _, item := range s.jobs.Items() {
		if id, _ := item.Data["disc_set_id"].(string); id != setID {
			continue
		}
		if jobInFlight(item.Data) {
			inFlight = true
		}
		if done, _ := item.Data["set_member_done"].(bool); done {
			doneIdx[jobInt(item.Data["disc_index"])] = true
		}
	}
	return inFlight, doneIdx
}

// reopenSet re-arms the finalize barrier for a set being repaired. Order
// matters: phantoms first, then clear the set_finalized latch — the barrier
// stays closed (every path bails on a finalized member) until membership is
// complete. The job-blob keys mirror download.applyDiscSetJobData /
// fulfillLocalROM's member-done update; keep them in sync.
func (s *Scheduler) reopenSet(setID string, item db.LibraryItem, mk db.SetMarker) {
	now := time.Now().Unix()
	setDir := filepath.Base(item.FilePath)
	seen := map[int]bool{}
	var memberIDs []string
	for _, job := range s.jobs.Items() {
		if id, _ := job.Data["disc_set_id"].(string); id != setID {
			continue
		}
		memberIDs = append(memberIDs, job.ID)
		if done, _ := job.Data["set_member_done"].(bool); done {
			seen[jobInt(job.Data["disc_index"])] = true
		}
	}
	// 1) Phantom done-members stand in for imported discs whose job rows the
	//    7-day cleanup purged, so the barrier's done>=total arithmetic still
	//    adds up. This is the exact job shape the sweep's crashed-finalize
	//    recovery already finalizes cleanly.
	for _, idx := range mk.Have {
		if seen[idx] {
			continue
		}
		s.jobs.Set(fmt.Sprintf("repair-%s-d%d", setID, idx), map[string]interface{}{
			"status":          "completed",
			"detail":          fmt.Sprintf("Disc %d of %d imported; set reopened for repair", idx, mk.Total),
			"title":           fmt.Sprintf("%s (Disc %d)", setDir, idx),
			"platform":        item.Platform,
			"platform_slug":   item.PlatformSlug,
			"is_pc":           false,
			"disc_set_id":     setID,
			"disc_index":      idx,
			"disc_total":      mk.Total,
			"set_dir":         setDir,
			"set_started_at":  now,
			"set_member_done": true,
			"imported_path":   item.FilePath,
		})
	}
	// 2) Clear the latch LAST and restart the degrade clock — a stale
	//    set_started_at would trip the sweep's timeout arm instantly.
	for _, id := range memberIDs {
		s.jobs.UpdateMulti(id, map[string]interface{}{
			"set_finalized": false, "set_started_at": now,
		})
	}
}

// setIDFromSourceID strips the "set:" scheme off a disc-set row's source_id;
// "" for anything else.
func setIDFromSourceID(sourceID string) string {
	const prefix = "set:"
	if len(sourceID) > len(prefix) && sourceID[:len(prefix)] == prefix {
		return sourceID[len(prefix):]
	}
	return ""
}

// jobInt reads an int job-blob value tolerantly: ints round-trip SQLite's
// JSON as float64 after a restart.
func jobInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// sortedIndices renders a want-set as ascending indices for logs.
func sortedIndices(want map[int]bool) []int {
	out := make([]int, 0, len(want))
	for i := range want {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}
