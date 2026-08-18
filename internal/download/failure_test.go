package download

import (
	"strings"
	"testing"
)

// failedJob seeds a job in flight with the release identity a real grab
// stores, so the tests exercise the same fields the failure tail reads.
func failedJob(t *testing.T, m *Manager, fields map[string]interface{}) string {
	t.Helper()
	jobID := newJobID()
	data := map[string]interface{}{
		"status": "downloading", "title": "Some Game (USA)",
		"platform_slug": "psx", "source_type": "ddl", "source_client": "ddl",
	}
	for k, v := range fields {
		data[k] = v
	}
	m.Jobs().Set(jobID, data)
	return jobID
}

func activityTypes(t *testing.T, m *Manager) []string {
	t.Helper()
	entries, _ := m.Jobs().GetActivity(1, 50)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.EventType)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// The whole point of the split: a release-caused failure must stop the
// selector from choosing that release again, and a local failure must not.
func TestFailJobBlocklistsOnlyReleaseFaults(t *testing.T) {
	const url = "https://archive.org/download/item/Some%20Game%20(USA).zip"

	t.Run("release fault blocklists", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := failedJob(t, m, map[string]interface{}{"download_url": url})

		m.failJob(jobID, "Download failed: 404", FailRelease)

		if !m.Jobs().IsBlocklisted(url, "") {
			t.Fatal("release not blocklisted; the selector can re-pick it forever")
		}
		entries := m.Jobs().GetBlocklist()
		if len(entries) != 1 {
			t.Fatalf("blocklist has %d entries, want 1", len(entries))
		}
		if entries[0].Reason != "Download failed: 404" {
			t.Errorf("reason = %q, want the failure that caused it", entries[0].Reason)
		}
		if entries[0].Source != "ddl" {
			t.Errorf("source = %q, want the download client", entries[0].Source)
		}
		if types := activityTypes(t, m); !contains(types, "release_blocklisted") || !contains(types, "download_failed") {
			t.Errorf("activity = %v, want both download_failed and release_blocklisted", types)
		}
	})

	t.Run("local fault does not blocklist", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := failedJob(t, m, map[string]interface{}{"download_url": url})

		m.failJob(jobID, "Import copy failed: no space left on device", FailLocal)

		if m.Jobs().IsBlocklisted(url, "") {
			t.Fatal("a full disk blocklisted a good release; every local failure would shrink the candidate pool")
		}
		if types := activityTypes(t, m); !contains(types, "download_failed") {
			t.Errorf("activity = %v, want download_failed even without a blocklist entry", types)
		}
	})

	t.Run("torrent blocklists on infohash", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		hash := strings.Repeat("b", 40)
		jobID := failedJob(t, m, map[string]interface{}{
			"source_type": "torrent", "source_client": "qbittorrent", "torrent_hash": hash,
		})

		m.failJob(jobID, "Blocked: executable in payload", FailRelease)

		if !m.Jobs().IsBlocklisted("", hash) {
			t.Fatal("torrent not blocklisted by infohash")
		}
	})
}

// A blocklist row with neither key matches nothing in IsBlocklisted. Writing
// one would put a row on the Blocklist screen that protects nothing.
func TestFailJobSkipsBlocklistWithoutIdentity(t *testing.T) {
	m := New(newTestConfig(t), newTestJobs(t), nil)
	jobID := failedJob(t, m, nil)

	m.failJob(jobID, "Download failed", FailRelease)

	if entries := m.Jobs().GetBlocklist(); len(entries) != 0 {
		t.Fatalf("wrote %d unmatched blocklist entries, want 0", len(entries))
	}
	job, _ := m.Jobs().Get(jobID)
	if status, _ := job["status"].(string); status != "error" {
		t.Errorf("status = %q, want error — the job still failed", status)
	}
}

// Several paths can report the same dead job (a watcher tick and an import
// pass, say). The first failure is the one that sticks.
func TestFailJobIsTerminalOnce(t *testing.T) {
	const url = "https://example.test/game.zip"
	m := New(newTestConfig(t), newTestJobs(t), nil)
	jobID := failedJob(t, m, map[string]interface{}{"download_url": url})

	m.failJob(jobID, "Download failed: connection reset", FailRelease)
	m.failJob(jobID, "Cannot find downloaded files", FailLocal)

	job, _ := m.Jobs().Get(jobID)
	if got, _ := job["error"].(string); got != "Download failed: connection reset" {
		t.Errorf("error = %q, want the first failure", got)
	}
	if entries := m.Jobs().GetBlocklist(); len(entries) != 1 {
		t.Fatalf("blocklist has %d entries, want 1", len(entries))
	}
}

// The detail line some failures carry is user-facing on the Queue screen.
func TestFailJobDetailCarriesDetail(t *testing.T) {
	m := New(newTestConfig(t), newTestJobs(t), nil)
	jobID := failedJob(t, m, map[string]interface{}{"download_url": "https://example.test/x.zip"})

	m.failJobDetail(jobID, "Virus detected: eicar", "Infected files found - download quarantined", FailRelease)

	job, _ := m.Jobs().Get(jobID)
	if got, _ := job["detail"].(string); got != "Infected files found - download quarantined" {
		t.Errorf("detail = %q", got)
	}
}
