package download

import (
	"log/slog"

	"gamarr/internal/db"
)

// FailCause names who is responsible for a failed job, which is the only
// question that decides whether the release gets blocklisted.
//
// The split is the arr family's, and ADR D6's: a failed *download* is the
// release's fault — the link is dead, the payload is not what it claimed, the
// transfer stalled — so the release is blocklisted and the selector moves to
// the next candidate. A failed *import* is ours — a missing mount, a full
// disk, an organize error — and blocklisting there would punish a good
// release for a local problem, quietly shrinking the candidate pool every
// time a disk filled up.
type FailCause int

const (
	// FailLocal is our side: paths, download clients, disk, scan
	// infrastructure, organize. The job fails; the release stays selectable.
	FailLocal FailCause = iota
	// FailRelease is the release's side. The job fails AND the release is
	// blocklisted, so the selector cannot pick it again.
	FailRelease
)

// failJob puts a job into its terminal error state, and blocklists the
// release when the failure is the release's fault.
//
// Every failure in this package goes through here, so "which failures
// blocklist" is one readable list at the call sites rather than a policy
// smeared across thirty map literals — and so a terminal failure always
// reaches the activity log, which before this only happened for disc sets.
func (m *Manager) failJob(jobID, reason string, cause FailCause) {
	m.failJobDetail(jobID, reason, "", cause)
}

// failJobDetail is failJob with the user-facing detail line some failures
// carry ("Infected files found - download quarantined").
func (m *Manager) failJobDetail(jobID, reason, detail string, cause FailCause) {
	if jobID == "" {
		return
	}
	job, ok := m.jobs.Get(jobID)
	if !ok {
		return
	}
	if status, _ := job["status"].(string); status == "error" {
		// Already terminal. Re-reporting would re-log and, worse, could
		// blocklist on a second pass over the same dead job.
		return
	}
	fields := map[string]interface{}{"status": "error", "error": reason}
	if detail != "" {
		fields["detail"] = detail
	}
	m.jobs.UpdateMulti(jobID, fields)
	m.jobs.LogActivity("download_failed", strVal(job, "title"), reason, jobID, nil)
	if cause == FailRelease {
		m.blocklistRelease(job, jobID, reason)
	}
}

// blocklistRelease writes the blocklist entry the selector filters on. The
// keys are exactly the two fields selection.Pipeline compares
// (SearchResult.DownloadURL and .InfoHash), so an entry written here is one
// the next search actually drops.
func (m *Manager) blocklistRelease(job map[string]interface{}, jobID, reason string) {
	url := strVal(job, "download_url")
	hash := strVal(job, "torrent_hash")
	if url == "" && hash == "" {
		// IsBlocklisted matches on URL or infohash; an entry with neither
		// changes no decision and would sit on the Blocklist screen looking
		// like protection that is not there. Reaching this means a download
		// path did not persist the identity it grabbed from.
		slog.Warn("release failure not blocklisted: job carries no download URL or infohash",
			"job", jobID, "reason", reason)
		return
	}
	if m.jobs.IsBlocklisted(url, hash) {
		return
	}
	title := strVal(job, "title")
	entry := &db.BlocklistEntry{
		Title:       title,
		Source:      blocklistSource(job),
		DownloadURL: url,
		InfoHash:    hash,
		Reason:      reason,
	}
	if _, err := m.jobs.AddBlocklistEntry(entry); err != nil {
		slog.Warn("blocklist write failed", "job", jobID, "error", err)
		return
	}
	m.jobs.LogActivity("release_blocklisted", title, reason, jobID, nil)
	slog.Info("release blocklisted", "job", jobID, "title", sanitizeLog(title), "reason", reason)
}

// blocklistSource labels the entry with where the release came from, falling
// back to the transport when the client is unknown.
func blocklistSource(job map[string]interface{}) string {
	if c := strVal(job, "source_client"); c != "" {
		return c
	}
	if t := strVal(job, "source_type"); t != "" {
		return t
	}
	return "unknown"
}
