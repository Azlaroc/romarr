package download

import (
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/qbit"
	"gamarr/internal/safety"
	"gamarr/internal/search"
)

// completionStates a torrent must NOT be in for its download to count as
// finished: qBittorrent reports Progress 1.0 while it is still verifying,
// allocating, or moving data, and importing then would copy a payload qBit is
// still writing.
var notCompleteStates = map[string]bool{
	"checkingDL":         true,
	"checkingUP":         true,
	"checkingResumeData": true,
	"allocating":         true,
	"moving":             true,
	"metaDL":             true,
}

// torrentComplete is the completion gate: every byte on disk (amount_left is
// exact where Progress is a rounded float) and the client not mid-check/move.
func torrentComplete(t *qbit.Torrent) bool {
	return t.AmountLeft == 0 && t.Progress >= 1.0 && !notCompleteStates[t.State]
}

// associationGrace is how long a job may wait for its torrent to appear in
// qBittorrent before the watcher declares the add failed.
const associationGrace = 10 * time.Minute

// vanishTicks is how many consecutive ticks a job's known hash may be absent
// from qBittorrent before the torrent counts as externally removed.
const vanishTicks = 3

// Watcher is the single driver for every torrent job: it associates jobs with
// torrents (by persisted infohash, learning it from the per-job tag when the
// submit couldn't know it), runs the safety file-list scan, detects
// completion, launches the seeding-safe import, mints jobs for out-of-band
// torrents, and optionally harvests seeded-out imported torrents. All state
// it needs lives in the job store, so a restart resumes every in-flight
// torrent with no duplicate jobs.
type Watcher struct {
	cfg    *config.Config
	mgr    *Manager
	stopCh chan struct{}

	// missing counts consecutive ticks a job's hash was absent from qBit.
	// Only the watcher goroutine touches it.
	missing map[string]int
}

// NewWatcher creates a torrent completion watcher.
func NewWatcher(cfg *config.Config, mgr *Manager) *Watcher {
	return &Watcher{
		cfg:     cfg,
		mgr:     mgr,
		stopCh:  make(chan struct{}),
		missing: make(map[string]int),
	}
}

// Start begins watching. The interval floor is 1s (test/e2e); default 30s.
// Returns whether a watch loop was started (false = disabled/unconfigured).
func (w *Watcher) Start() bool {
	if !w.cfg.WatcherOn() || !w.cfg.HasQBittorrent() {
		slog.Info("torrent watcher disabled")
		return false
	}

	interval := time.Duration(w.cfg.WatcherIntervalSeconds()) * time.Second

	slog.Info("torrent watcher started", "interval", interval, "category", w.cfg.QBCategory)

	go func() {
		w.tick()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.tick()
				// Interval changes hot-apply on the next tick: ticks are
				// short (seconds), so a boundary re-read is prompt and the
				// per-instance vanish counters survive untouched.
				if next := time.Duration(w.cfg.WatcherIntervalSeconds()) * time.Second; next != interval {
					interval = next
					ticker.Reset(interval)
				}
			case <-w.stopCh:
				slog.Info("torrent watcher stopped")
				return
			}
		}
	}()
	return true
}

// Stop signals the watcher to stop.
func (w *Watcher) Stop() {
	select {
	case <-w.stopCh:
		// Already closed.
	default:
		close(w.stopCh)
	}
}

// tick runs one watcher pass: drive every active torrent job, sweep for
// out-of-band completed torrents, and (when enabled) harvest seeded-out
// imported torrents.
func (w *Watcher) tick() {
	torrents := w.mgr.QB().GetTorrents(w.cfg.QBCategory)
	byHash := make(map[string]*qbit.Torrent, len(torrents))
	byTag := make(map[string]*qbit.Torrent)
	for i := range torrents {
		t := &torrents[i]
		byHash[strings.ToLower(t.Hash)] = t
		for _, tag := range strings.Split(t.Tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				byTag[tag] = t
			}
		}
	}

	items := w.mgr.Jobs().Items()

	// Hashes any job already claims (any status) — the orphan sweep must not
	// re-mint a torrent that a live, completed, or errored job owns.
	claimed := make(map[string]bool)
	for _, item := range items {
		if h, _ := item.Data["torrent_hash"].(string); h != "" {
			claimed[strings.ToLower(h)] = true
		}
		if h, _ := item.Data["orphan_hash"].(string); h != "" {
			claimed[strings.ToLower(h)] = true
		}
	}

	for _, item := range items {
		if st, _ := item.Data["source_type"].(string); st != "torrent" {
			continue
		}
		status, _ := item.Data["status"].(string)
		if status != "downloading" && status != "scanning" && status != "importing" {
			delete(w.missing, item.ID)
			continue
		}
		if h := w.driveJob(item.ID, item.Data, byHash, byTag); h != "" {
			claimed[h] = true
		}
	}

	// Out-of-band completed torrents in our category: mint ONE
	// completed_unorganized job carrying orphan_hash. Platform comes from a
	// library-title hint when one exists; otherwise it stays Unknown for the
	// operator to pick in the organize dialog — auto-importing strangers as
	// "PC" mis-shelved every unhinted ROM torrent.
	for i := range torrents {
		t := &torrents[i]
		if !torrentComplete(t) || claimed[strings.ToLower(t.Hash)] {
			continue
		}
		w.mintOrphanJob(t)
		claimed[strings.ToLower(t.Hash)] = true
	}

	if w.cfg.SeedJanitor() {
		w.janitor(torrents, items)
	}
}

// driveJob advances one active torrent job. Returns the job's (lowercase)
// hash once known, so the caller can mark it claimed within this tick.
func (w *Watcher) driveJob(jobID string, data map[string]interface{}, byHash, byTag map[string]*qbit.Torrent) string {
	jobs := w.mgr.Jobs()
	hash, _ := data["torrent_hash"].(string)
	hash = strings.ToLower(hash)

	// Associate: learn the hash from the per-job tag when submit couldn't
	// parse one (.torrent URL adds).
	if hash == "" {
		tag, _ := data["qb_tag"].(string)
		t := byTag[tag]
		if t == nil {
			started := int64Value(data["started_at"])
			if started > 0 && time.Now().Unix()-started > int64(associationGrace.Seconds()) {
				// The client never took it. That is an add-side failure, and
				// the same release usually adds fine on the next attempt.
				w.mgr.failJob(jobID, "Torrent never appeared in qBittorrent (add failed or duplicate?)", FailLocal)
			}
			return ""
		}
		hash = strings.ToLower(t.Hash)
		jobs.Update(jobID, "torrent_hash", hash)
		slog.Info("watcher: associated torrent by tag", "job", jobID, "hash", hash)
	}

	t := byHash[hash]
	if t == nil {
		w.missing[jobID]++
		if w.missing[jobID] >= vanishTicks {
			delete(w.missing, jobID)
			// Someone deleted it in the client. Nothing about the release.
			w.mgr.failJob(jobID, "Torrent removed from qBittorrent externally", FailLocal)
		}
		return hash
	}
	delete(w.missing, jobID)

	// Layer 1: file-list safety scan, once, as soon as metadata exists.
	if done, _ := data["file_scan_done"].(bool); !done && t.Progress > 0 {
		isSafe, issues := safety.ScanTorrentFileList(w.mgr.QB(), t.Hash)
		jobs.Update(jobID, "file_scan_done", true)
		if !isSafe {
			slog.Warn("file list scan failed", "job", jobID, "issues", issues)
			w.mgr.failJobDetail(jobID, fmt.Sprintf("Blocked: %s", strings.Join(issues, "; ")),
				"Dangerous files detected - download cancelled", FailRelease)
			w.mgr.QB().DeleteTorrent(t.Hash, true)
			return hash
		}
	}

	// Selective download (#256): once metadata exists, prio-0 everything but
	// the target BEFORE the completion gate can fire — an instantly-seeded
	// torrent must never import the whole pack because selection hadn't run.
	if target, _ := data["target_file"].(string); target != "" {
		if done, _ := data["selection_done"].(bool); !done {
			if t.State == "metaDL" {
				return hash // magnet metadata still resolving
			}
			files := w.mgr.QB().GetTorrentFiles(t.Hash)
			if len(files) == 0 {
				return hash // file list not available yet
			}
			w.selectTargetFile(jobID, t, target, files)
			return hash // (re)selection this tick; completion evaluates next tick
		}
	}

	status, _ := data["status"].(string)
	if status == "downloading" {
		if w.jobComplete(data, t) {
			jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "scanning",
				"detail": "Download complete. Scanning...",
			})
			w.launchImport(jobID, t)
		} else {
			// Progress write every tick — also keeps updated_at fresh so
			// CleanupStaleDownloads never reaps a live long download.
			jobs.Update(jobID, "detail", fmt.Sprintf("Downloading... %.1f%% (%s/s)",
				t.Progress*100, search.HumanSize(t.DLSpeed)))
		}
		return hash
	}

	// scanning/importing persisted but no import goroutine running — a
	// restart interrupted the import; relaunch it.
	if w.jobComplete(data, t) {
		w.launchImport(jobID, t)
	}
	return hash
}

// jobComplete is the per-job completion gate: the torrent-level gate, plus —
// for a plucked job — the target file itself at 100% (belt-and-braces on top
// of amount_left, which qBittorrent already excludes prio-0 files from).
func (w *Watcher) jobComplete(data map[string]interface{}, t *qbit.Torrent) bool {
	if !torrentComplete(t) {
		return false
	}
	resolved, _ := data["target_file_resolved"].(string)
	if resolved == "" {
		return true
	}
	idx := int(int64Value(data["target_index"]))
	for _, f := range w.mgr.QB().GetTorrentFiles(t.Hash) {
		if f.Index == idx {
			return f.Progress >= 1.0
		}
	}
	return false
}

// selectTargetFile matches the wanted file inside the torrent — exact
// in-torrent path first, then case-insensitive basename — sets every other
// file to priority 0, persists the resolved selection, and (re)starts the
// torrent. A miss falls back to the whole pack (target cleared) rather than
// wedging the job.
func (w *Watcher) selectTargetFile(jobID string, t *qbit.Torrent, target string, files []qbit.TorrentFile) {
	jobs := w.mgr.Jobs()

	match := -1
	for i := range files {
		if files[i].Name == target {
			match = i
			break
		}
	}
	if match < 0 {
		want := strings.ToLower(path.Base(filepath.ToSlash(target)))
		for i := range files {
			if strings.ToLower(path.Base(filepath.ToSlash(files[i].Name))) == want {
				match = i
				break
			}
		}
	}

	fallback := func(reason string) {
		slog.Warn("watcher: selective download fell back to whole pack",
			"job", jobID, "target", target, "reason", reason)
		jobs.UpdateMulti(jobID, map[string]interface{}{
			"target_file":    "",
			"selection_done": true,
			"detail":         "Target file not isolatable - downloading whole pack",
		})
		w.mgr.QB().StartTorrents(t.Hash)
	}

	if match < 0 {
		fallback("target not in torrent file list")
		return
	}

	var others []int
	for i := range files {
		if i != match {
			others = append(others, files[i].Index)
		}
	}
	if !w.mgr.QB().SetFilePriority(t.Hash, others, 0) {
		fallback("filePrio rejected (older qBittorrent?)")
		return
	}

	jobs.UpdateMulti(jobID, map[string]interface{}{
		"target_index":         files[match].Index,
		"target_file_resolved": files[match].Name,
		"selection_done":       true,
		"detail":               fmt.Sprintf("Selective download: %s", path.Base(filepath.ToSlash(files[match].Name))),
	})
	w.mgr.QB().StartTorrents(t.Hash)
	slog.Info("watcher: selective download armed",
		"job", jobID, "target", files[match].Name, "excluded", len(others))
}

// launchImport starts the import goroutine unless one is already running for
// this job (importTorrentJob itself single-flights as the final gate).
func (w *Watcher) launchImport(jobID string, t *qbit.Torrent) {
	if _, busy := w.mgr.importing.Load(jobID); busy {
		return
	}
	tc := *t
	go w.mgr.importTorrentJob(jobID, &tc)
}

// mintOrphanJob records an out-of-band completed torrent as needing a manual
// organize. Persisted (orphan_hash) so neither later ticks nor restarts mint
// a duplicate.
func (w *Watcher) mintOrphanJob(t *qbit.Torrent) {
	platf, platSlug, isPC := "Unknown", "", false
	if existing := w.mgr.Jobs().FindLibraryByTitle(t.Name, ""); existing != nil {
		platf, platSlug, isPC = existing.Platform, existing.PlatformSlug, existing.IsPC
	}
	jobID := newJobID()
	w.mgr.Jobs().Set(jobID, map[string]interface{}{
		"status":        "completed_unorganized",
		"title":         t.Name,
		"platform":      platf,
		"platform_slug": platSlug,
		"is_pc":         isPC,
		"error":         nil,
		"detail":        "Completed - needs organizing (use organize button)",
		"source_type":   "torrent",
		"source_client": "qbittorrent",
		"orphan_hash":   strings.ToLower(t.Hash),
	})
	slog.Info("watcher: recorded out-of-band completed torrent", "name", t.Name, "hash", t.Hash)
}

// janitor harvests torrents that are both imported (their job carries
// imported_at) and done seeding per qBittorrent's own share limits
// (stoppedUP/pausedUP) — deleting torrent AND files to reclaim disk. Opt-in
// via SEED_JANITOR_ENABLED; deletion is unrecoverable, so the default leaves
// seed lifecycle entirely to qBittorrent.
func (w *Watcher) janitor(torrents []qbit.Torrent, items []struct {
	ID   string
	Data map[string]interface{}
}) {
	importedHashes := make(map[string]string)
	for _, item := range items {
		if int64Value(item.Data["imported_at"]) <= 0 {
			continue
		}
		// A plucked pack stays harvestable by the operator only: the same pack
		// may be plucked again for a different title later (#256).
		if tf, _ := item.Data["target_file"].(string); tf != "" {
			continue
		}
		if tfr, _ := item.Data["target_file_resolved"].(string); tfr != "" {
			continue
		}
		if h, _ := item.Data["torrent_hash"].(string); h != "" {
			importedHashes[strings.ToLower(h)] = item.ID
		}
	}
	for i := range torrents {
		t := &torrents[i]
		if t.State != "stoppedUP" && t.State != "pausedUP" {
			continue
		}
		jobID, ok := importedHashes[strings.ToLower(t.Hash)]
		if !ok {
			continue
		}
		w.mgr.QB().DeleteTorrent(t.Hash, true)
		w.mgr.Jobs().LogActivity("torrent_harvested", t.Name,
			fmt.Sprintf("Seed complete (ratio %.2f) - torrent and files removed", t.Ratio), jobID, nil)
		slog.Info("janitor: harvested seeded-out torrent", "name", t.Name, "ratio", t.Ratio)
	}
}
