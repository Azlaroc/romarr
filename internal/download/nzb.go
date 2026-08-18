package download

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"gamarr/internal/sabnzbd"
)

// DownloadNZB starts a Usenet/NZB download via SABnzbd. The optional
// trailing DiscSet (at most one) marks a disc-set member (F4).
func (m *Manager) DownloadNZB(sab *sabnzbd.Client, nzbURL, title, platf, platSlug string, isPC bool, set ...DiscSet) (string, error) {
	var ds DiscSet
	if len(set) > 0 {
		ds = set[0]
	}
	if sab != nil {
		return m.downloadSABnzbd(sab, nzbURL, title, platf, platSlug, isPC, ds)
	}
	return "", fmt.Errorf("usenet download client not configured")
}

func (m *Manager) downloadSABnzbd(sab *sabnzbd.Client, nzbURL, title, platf, platSlug string, isPC bool, set DiscSet) (string, error) {
	jobID := newJobID()
	jobData := map[string]interface{}{
		"status":        "downloading",
		"title":         title,
		"platform":      platf,
		"platform_slug": platSlug,
		"is_pc":         isPC,
		"error":         nil,
		"detail":        "Sending to SABnzbd...",
		"source_type":   "nzb",
		"source_client": "sabnzbd",
		// Kept for the same reason the DDL path keeps it: a failed grab can
		// only be blocklisted against the URL the selector will see again.
		"download_url": nzbURL,
	}
	applyDiscSetJobData(jobData, set)
	m.jobs.Set(jobID, jobData)
	m.jobs.LogActivity("download_started", title, "NZB via SABnzbd", jobID, nil)

	nzoID, err := sab.AddNZBByURL(nzbURL, title, m.cfg.SABnzbdCategory)
	if err != nil {
		m.failJob(jobID, fmt.Sprintf("SABnzbd error: %v", err), FailLocal)
		return jobID, nil
	}
	m.jobs.Update(jobID, "detail", "Downloading via Usenet...")
	// Persisted so a restart can reattach the watcher (recoverableInFlight +
	// RecoverOrphanedNZBDownloads key on it) — without it the job dies as
	// interrupted with the download still running in SABnzbd.
	m.jobs.Update(jobID, "nzo_id", nzoID)

	go m.watchSABnzbdDownload(sab, jobID, nzoID, title, platf, platSlug, isPC)
	return jobID, nil
}

// RecoverOrphanedNZBDownloads restarts watchers for persisted Usenet jobs
// after a Gamarr restart. The download client (SABnzbd via nzo_id) owns the
// transfer, so reconnecting the watcher is enough to resume progress tracking
// and final organization — including a download that finished while Gamarr
// was down (the watcher's history check organizes it).
func (m *Manager) RecoverOrphanedNZBDownloads() {
	for _, item := range m.jobs.Items() {
		status, _ := item.Data["status"].(string)
		if status != "downloading" && status != "organizing" {
			continue
		}
		client, _ := item.Data["source_client"].(string)

		title, _ := item.Data["title"].(string)
		platf, _ := item.Data["platform"].(string)
		platSlug, _ := item.Data["platform_slug"].(string)
		isPC, _ := item.Data["is_pc"].(bool)

		switch client {
		case "sabnzbd":
			if m.sab == nil {
				continue
			}
			nzoID, _ := item.Data["nzo_id"].(string)
			if nzoID == "" {
				m.failJob(item.ID, "Cannot recover SABnzbd download: missing NZO ID", FailLocal)
				continue
			}
			m.jobs.Update(item.ID, "detail", "Recovered SABnzbd download; reconnecting watcher...")
			go m.watchSABnzbdDownload(m.sab, item.ID, nzoID, title, platf, platSlug, isPC)
		}
	}
}

func int64Value(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	default:
		return 0
	}
}

func (m *Manager) watchSABnzbdDownload(sab *sabnzbd.Client, jobID, nzoID, title, platf, platSlug string, isPC bool) {
	slog.Info("watching SABnzbd download", "title", title, "nzo_id", nzoID)
	maxWait := 7 * 24 * time.Hour
	start := time.Now()

	for time.Since(start) < maxWait {
		// Check queue first
		queue, err := sab.GetQueue()
		if err == nil {
			for _, slot := range queue {
				if slot.NZOID == nzoID {
					if slot.MB > 0 {
						pct := ((float64(slot.MB) - float64(slot.MBLeft)) / float64(slot.MB)) * 100
						m.jobs.Update(jobID, "detail",
							fmt.Sprintf("Downloading... %.1f%%", pct))
					}
					break
				}
			}
		}

		// Check history for completion
		history, err := sab.GetHistory(50)
		if err == nil {
			for _, slot := range history {
				if slot.NZOID == nzoID {
					if slot.Status == "Completed" {
						slog.Info("SABnzbd download completed", "title", title, "path", slot.Storage)
						m.jobs.UpdateMulti(jobID, map[string]interface{}{
							"status": "organizing",
							"detail": "NZB download complete. Organizing...",
						})
						m.organizeNZBDownloadWithClient(jobID, slot.Storage, title, platf, platSlug, isPC, "sabnzbd")
						return
					} else if slot.Status == "Failed" {
						// SABnzbd exhausted its own retries on this nzb.
						m.failJob(jobID, "SABnzbd download failed", FailRelease)
						return
					}
				}
			}
		}

		time.Sleep(10 * time.Second)
	}
	// A stalled grab is the release's problem, not ours: the next candidate
	// is the answer, and re-picking this one would stall again.
	m.failJob(jobID, "Timed out waiting for SABnzbd download", FailRelease)
}

func (m *Manager) organizeNZBDownload(jobID, storagePath, title, platf, platSlug string, isPC bool) {
	m.organizeNZBDownloadWithClient(jobID, storagePath, title, platf, platSlug, isPC, "sabnzbd")
}

func (m *Manager) organizeNZBDownloadWithClient(jobID, storagePath, title, platf, platSlug string, isPC bool, sourceClient string) {
	if storagePath == "" {
		m.failNZBOrganize(jobID)
		return
	}

	if !pathExists(storagePath) {
		// Gamarr can restart between moveContent and the job status update, and
		// the recovered watcher then re-enters organize with the staging path
		// already gone. When the content is sitting at its destination the
		// import did succeed, so finish the job instead of reporting a
		// completed import as a failure.
		if dest, ok := m.nzbDestPath(storagePath, platSlug, isPC); ok && pathExists(dest) {
			m.completeNZBOrganize(jobID, dest, title, platf, platSlug, isPC, sourceClient)
			return
		}
		m.failNZBOrganize(jobID)
		return
	}

	if !isPC && platSlug != "" {
		// ROM: hand the staging path straight to the shared fulfillment
		// pipeline — it moves the content into the library itself.
		m.completeNZBOrganize(jobID, storagePath, title, platf, platSlug, isPC, sourceClient)
		return
	}

	dest, ok := m.nzbDestPath(storagePath, platSlug, isPC)
	if !ok {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "completed",
			"detail": "Downloaded (unknown platform, left in staging)",
		})
		return
	}

	if err := moveContent(storagePath, dest); err != nil {
		m.failJob(jobID, fmt.Sprintf("Organize failed: %v", err), FailLocal)
		return
	}

	m.completeNZBOrganize(jobID, dest, title, platf, platSlug, isPC, sourceClient)
}

// nzbDestPath returns the library destination for a finished Usenet download.
// The second return is false when the platform is unknown, in which case the
// content stays in staging.
func (m *Manager) nzbDestPath(storagePath, platSlug string, isPC bool) (string, bool) {
	base := filepath.Base(storagePath)
	switch {
	case isPC:
		return filepath.Join(m.cfg.GamesVaultPath, base), true
	case platSlug != "":
		// Must mirror fulfillLocalROM's destination computation exactly: this
		// path is what the restart-recovery re-entry check probes for.
		return filepath.Join(m.romDestDir(platSlug), sanitizeFilename(base)), true
	default:
		return "", false
	}
}

func (m *Manager) failNZBOrganize(jobID string) {
	m.failJob(jobID, "Usenet storage path not found", FailLocal)
}

// completeNZBOrganize marks the job done and registers the content in the
// library. For ROMs, path may be either the staging path (normal flow — the
// shared pipeline moves it) or the library destination (restart re-entry — the
// pipeline's move-skip makes it idempotent). TrackInLibrary dedupes on source
// ID, so re-entering after a restart is safe.
func (m *Manager) completeNZBOrganize(jobID, path, title, platf, platSlug string, isPC bool, sourceClient string) {
	if !isPC && platSlug != "" {
		var set DiscSet
		if job, ok := m.jobs.Get(jobID); ok {
			set = discSetFromJob(job)
		}
		_, _ = m.fulfillLocalROM(path, fulfillMeta{
			JobID:        jobID,
			Title:        title,
			Platform:     platf,
			PlatformSlug: platSlug,
			Source:       "nzb",
			SourceClient: sourceClient,
			DiscSet:      set,
		})
		return
	}
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "completed",
		"detail": "Moved to library",
	})
	writeMetadataSidecar(path, title, platf, platSlug, isPC, "nzb")
	m.TrackInLibrary(title, platf, platSlug, isPC, path, 0, "nzb", sourceClient, "nzb:"+path, jobID, "", "")
	m.jobs.LogActivity("download_completed", title, "NZB to library", jobID, nil)
}

// RetryJob re-drives a failed job's release.
//
// It used to flip the job to status "queued" — a status nothing consumes — so
// the Retry button's entire effect was a job that read "Retry #1 queued"
// forever. A retry now does what the button says: it clears any blocklist
// entry the failure tail wrote for this release (a manual retry is an
// explicit human override of an automatic decision, and leaving the entry
// would have the selector filter out the release the operator just asked
// for), then starts a fresh download from the identity stored on the job.
func (m *Manager) RetryJob(jobID string) (bool, string) {
	job, ok := m.jobs.Get(jobID)
	if !ok {
		return false, "Job not found"
	}
	status, _ := job["status"].(string)
	if status != "error" && status != "interrupted" && status != "dead_letter" {
		return false, fmt.Sprintf("Job not in failed state (status=%s)", status)
	}

	url := strVal(job, "download_url")
	hash := strVal(job, "torrent_hash")
	vimmID := strVal(job, "vimm_id")
	if url == "" && vimmID == "" && hash == "" {
		// Jobs grabbed before the release identity was persisted, and any
		// path that still forgets to store it. Say so instead of reporting a
		// retry that cannot happen.
		return false, "Job has no stored release to retry"
	}

	// retry_count comes back as float64 through the JSON round-trip but as an
	// int from the in-process cache, so a type assertion on one of them reads
	// zero for the other — which is why a second retry in the same process
	// used to announce itself as "retry #1".
	retryCount := int(int64Value(job["retry_count"]))
	if removed := m.jobs.RemoveBlocklistFor(url, hash); removed > 0 {
		slog.Info("retry cleared blocklist entries", "job", jobID, "removed", removed)
	}

	title := strVal(job, "title")
	platf := strVal(job, "platform")
	platSlug := strVal(job, "platform_slug")
	isPC, _ := job["is_pc"].(bool)
	set := discSetFromJob(job)

	var newID string
	switch strVal(job, "source_type") {
	case "torrent":
		id, err := m.DownloadTorrent(TorrentSpec{
			URL:          url,
			InfoHash:     hash,
			Title:        title,
			Platform:     platf,
			PlatformSlug: platSlug,
			IsPC:         isPC,
			TargetFile:   strVal(job, "target_file"),
			DiscSet:      set,
		})
		if err != nil {
			return false, fmt.Sprintf("Retry failed: %v", err)
		}
		newID = id
	case "nzb":
		if m.sab == nil {
			return false, "Retry failed: usenet download client not configured"
		}
		id, err := m.DownloadNZB(m.sab, url, title, platf, platSlug, isPC, set)
		if err != nil {
			return false, fmt.Sprintf("Retry failed: %v", err)
		}
		newID = id
	default:
		newID = m.DownloadDDL(url, vimmID, title, platf, platSlug, isPC,
			strVal(job, "md5"), strVal(job, "sha1"), set)
	}

	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"detail":      fmt.Sprintf("Retried as job %s", newID),
		"retry_count": float64(retryCount + 1),
	})
	m.jobs.LogActivity("download_retried", title,
		fmt.Sprintf("Retry #%d as job %s", retryCount+1, newID), jobID, nil)
	return true, fmt.Sprintf("Retrying as job %s (retry #%d)", newID, retryCount+1)
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}
