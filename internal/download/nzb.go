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
	}
	applyDiscSetJobData(jobData, set)
	m.jobs.Set(jobID, jobData)
	m.jobs.LogActivity("download_started", title, "NZB via SABnzbd", jobID, nil)

	nzoID, err := sab.AddNZBByURL(nzbURL, title, m.cfg.SABnzbdCategory)
	if err != nil {
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("SABnzbd error: %v", err),
		})
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
				m.jobs.UpdateMulti(item.ID, map[string]interface{}{
					"status": "error",
					"error":  "Cannot recover SABnzbd download: missing NZO ID",
				})
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
						m.jobs.UpdateMulti(jobID, map[string]interface{}{
							"status": "error",
							"error":  "SABnzbd download failed",
						})
						return
					}
				}
			}
		}

		time.Sleep(10 * time.Second)
	}
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "error",
		"error":  "Timed out waiting for SABnzbd download",
	})
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
		m.jobs.UpdateMulti(jobID, map[string]interface{}{
			"status": "error",
			"error":  fmt.Sprintf("Organize failed: %v", err),
		})
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
	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status": "error",
		"error":  "Usenet storage path not found",
	})
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

// RetryJob retries a failed download job.
func (m *Manager) RetryJob(jobID string) (bool, string) {
	job, ok := m.jobs.Get(jobID)
	if !ok {
		return false, "Job not found"
	}
	status, _ := job["status"].(string)
	if status != "error" && status != "interrupted" && status != "dead_letter" {
		return false, fmt.Sprintf("Job not in failed state (status=%s)", status)
	}

	retryCount := 0
	if rc, ok := job["retry_count"].(float64); ok {
		retryCount = int(rc)
	}

	m.jobs.UpdateMulti(jobID, map[string]interface{}{
		"status":      "queued",
		"error":       nil,
		"detail":      fmt.Sprintf("Retry #%d queued", retryCount+1),
		"retry_count": retryCount + 1,
	})
	m.jobs.LogActivity("download_retried", strVal(job, "title"), fmt.Sprintf("Retry #%d", retryCount+1), jobID, nil)
	return true, fmt.Sprintf("Job re-queued (retry #%d)", retryCount+1)
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// AutoRetryFailed checks for failed jobs and retries those under max_retries.
func (m *Manager) AutoRetryFailed() {
	for _, item := range m.jobs.Items() {
		status, _ := item.Data["status"].(string)
		if status != "error" {
			continue
		}
		retryCount := 0
		if rc, ok := item.Data["retry_count"].(float64); ok {
			retryCount = int(rc)
		}
		if retryCount >= m.cfg.MaxRetries {
			// Move to dead letter (status is always "error" here).
			m.jobs.UpdateMulti(item.ID, map[string]interface{}{
				"status": "dead_letter",
				"detail": fmt.Sprintf("Max retries (%d) exceeded", m.cfg.MaxRetries),
			})
			continue
		}
		// Check if enough time has passed for backoff
		// Simple: don't auto-retry, let the user or monitor do it
	}
}
