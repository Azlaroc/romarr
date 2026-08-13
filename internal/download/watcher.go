package download

import (
	"fmt"
	"log/slog"
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
func (w *Watcher) Start() {
	if !w.cfg.WatcherEnabled || !w.cfg.HasQBittorrent() {
		slog.Info("torrent watcher disabled")
		return
	}

	interval := time.Duration(w.cfg.WatcherIntervalS) * time.Second
	if interval < time.Second {
		interval = 30 * time.Second
	}

	slog.Info("torrent watcher started", "interval", interval, "category", w.cfg.QBCategory)

	go func() {
		w.tick()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.tick()
			case <-w.stopCh:
				slog.Info("torrent watcher stopped")
				return
			}
		}
	}()
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

	if w.cfg.SeedJanitorEnabled {
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
				jobs.UpdateMulti(jobID, map[string]interface{}{
					"status": "error",
					"error":  "Torrent never appeared in qBittorrent (add failed or duplicate?)",
				})
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
			jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "error",
				"error":  "Torrent removed from qBittorrent externally",
			})
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
			jobs.UpdateMulti(jobID, map[string]interface{}{
				"status": "error",
				"error":  fmt.Sprintf("Blocked: %s", strings.Join(issues, "; ")),
				"detail": "Dangerous files detected - download cancelled",
			})
			w.mgr.QB().DeleteTorrent(t.Hash, true)
			return hash
		}
	}

	status, _ := data["status"].(string)
	if status == "downloading" {
		if torrentComplete(t) {
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
	if torrentComplete(t) {
		w.launchImport(jobID, t)
	}
	return hash
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
