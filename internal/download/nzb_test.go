package download

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/db"
	"gamarr/internal/sabnzbd"
)

// sabMock is a fake SABnzbd API server.
type sabMock struct {
	srv        *httptest.Server
	addStatus  bool
	addError   string
	nzoID      string
	queueSlots []map[string]interface{}
	histSlots  []map[string]interface{}
}

func newSabMock(t *testing.T) *sabMock {
	t.Helper()
	s := &sabMock{addStatus: true, nzoID: "SABnzbd_nzo_test1"}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "addurl":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  s.addStatus,
				"nzo_ids": []string{s.nzoID},
				"error":   s.addError,
			})
		case "queue":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"queue": map[string]interface{}{"slots": s.queueSlots},
			})
		case "history":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"history": map[string]interface{}{"slots": s.histSlots},
			})
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{})
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sabMock) client() *sabnzbd.Client {
	return sabnzbd.New(s.srv.URL, "apikey")
}

func TestDownloadNZBNilClient(t *testing.T) {
	m := New(newTestConfig(t), newTestJobs(t), nil)
	_, err := m.DownloadNZB(nil, "http://x/nzb", "Game", "PC", "", true)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want not configured", err)
	}
}

func TestDownloadNZBAddError(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	sab := newSabMock(t)
	sab.addStatus = false
	sab.addError = "invalid api key"

	m := New(cfg, jobs, nil)
	jobID, err := m.DownloadNZB(sab.client(), "http://x/file.nzb", "Bad Game", "PC", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job, ok := jobs.Get(jobID)
	if !ok {
		t.Fatal("job not created")
	}
	if status, _ := job["status"].(string); status != "error" {
		t.Errorf("status = %q, want error", status)
	}
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "invalid api key") {
		t.Errorf("error = %q, want invalid api key", errMsg)
	}
	if st, _ := job["source_type"].(string); st != "nzb" {
		t.Errorf("source_type = %q, want nzb", st)
	}
}

func TestDownloadNZBCompletedFlow(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)

	storage := filepath.Join(t.TempDir(), "Usenet Game")
	writeFileT(t, filepath.Join(storage, "rom.sfc"), []byte("rom"))

	sab := newSabMock(t)
	// String-form mb/mbleft: the shape the real SABnzbd API serves (#294).
	sab.queueSlots = []map[string]interface{}{
		{"nzo_id": sab.nzoID, "mb": "100.00", "mbleft": "25.00", "status": "Downloading"},
	}
	sab.histSlots = []map[string]interface{}{
		{"nzo_id": sab.nzoID, "status": "Completed", "storage": storage},
	}

	m := New(cfg, jobs, nil)
	jobID, err := m.DownloadNZB(sab.client(), "http://x/game.nzb", "Usenet Game", "SNES", "snes", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dest := filepath.Join(cfg.GamesRomsPath, "snes", "Usenet Game")
	waitFor(t, 10*time.Second, "nzb library tracking", func() bool {
		return jobs.LibraryHasSourceID("nzb:" + dest)
	})
	job := waitJobStatus(t, jobs, jobID, "completed", 5*time.Second)
	if detail, _ := job["detail"].(string); !strings.Contains(detail, "RomM (SNES)") {
		t.Errorf("detail = %q, want RomM (SNES)", detail)
	}
	if got, _ := job["nzo_id"].(string); got != sab.nzoID {
		t.Errorf("nzo_id = %q, want %q persisted for restart recovery", got, sab.nzoID)
	}
	if !pathExists(filepath.Join(dest, "rom.sfc")) {
		t.Error("nzb content not moved to library")
	}
	if !pathExists(filepath.Join(dest, ".gamarr.json")) {
		t.Error("sidecar not written")
	}
}

func TestDownloadNZBFailedFlow(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	sab := newSabMock(t)
	sab.histSlots = []map[string]interface{}{
		{"nzo_id": sab.nzoID, "status": "Failed"},
	}

	m := New(cfg, jobs, nil)
	jobID, err := m.DownloadNZB(sab.client(), "http://x/game.nzb", "Doomed", "PC", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job := waitJobStatus(t, jobs, jobID, "error", 5*time.Second)
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "failed") {
		t.Errorf("error = %q, want download failed", errMsg)
	}
}

func TestRecoverOrphanedSABnzbdDownload(t *testing.T) {
	// A SAB job that persisted its nzo_id survives a restart: recovery
	// reattaches the watcher, which finds the finished download in SAB's
	// history and organizes it.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	storage := filepath.Join(t.TempDir(), "Recovered SAB")
	writeFileT(t, filepath.Join(storage, "rom.gba"), []byte("rom"))

	mock := newSabMock(t)
	mock.histSlots = []map[string]interface{}{
		{"nzo_id": mock.nzoID, "status": "Completed", "storage": storage},
	}
	cfg.SABnzbdURL = mock.srv.URL
	cfg.SABnzbdAPIKey = "apikey"

	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{
		"status":        "downloading",
		"title":         "Recovered SAB",
		"platform":      "Game Boy Advance",
		"platform_slug": "gba",
		"is_pc":         false,
		"source_type":   "nzb",
		"source_client": "sabnzbd",
		"nzo_id":        mock.nzoID,
	})

	m := New(cfg, jobs, nil)
	m.RecoverOrphanedNZBDownloads()

	dest := filepath.Join(cfg.GamesRomsPath, "gba", "Recovered SAB")
	waitFor(t, 5*time.Second, "recovered SABnzbd watcher", func() bool {
		job, ok := jobs.Get(jobID)
		return ok && job["status"] == "completed"
	})
	if !pathExists(filepath.Join(dest, "rom.gba")) {
		t.Fatal("recovered SABnzbd content was not organized")
	}
}

func TestRecoverSABnzbdMissingNZOID(t *testing.T) {
	// Defensive path: a pre-persistence SAB job (no nzo_id) cannot be
	// recovered — recovery must fail it explicitly, not spin a dead watcher.
	// (In practice loadAll already interrupts these; recovery double-checks.)
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	mock := newSabMock(t)
	cfg.SABnzbdURL = mock.srv.URL
	cfg.SABnzbdAPIKey = "apikey"

	jobs.Set("sab-noid", map[string]interface{}{
		"status": "downloading", "title": "Old SAB Job",
		"source_type": "nzb", "source_client": "sabnzbd",
	})

	m := New(cfg, jobs, nil)
	m.RecoverOrphanedNZBDownloads()

	job, _ := jobs.Get("sab-noid")
	if job["status"] != "error" {
		t.Errorf("status = %v, want error", job["status"])
	}
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "missing NZO ID") {
		t.Errorf("error = %q", errMsg)
	}
}

func TestRecoverSABnzbdSkipsWhenUnconfigured(t *testing.T) {
	// No SAB client configured: the job is left untouched rather than
	// errored — the client may come back.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	jobs.Set("sab-stranded", map[string]interface{}{
		"status": "downloading", "title": "Stranded SAB Job",
		"source_type": "nzb", "source_client": "sabnzbd", "nzo_id": "SABnzbd_nzo_z",
	})

	m := New(cfg, jobs, nil)
	m.RecoverOrphanedNZBDownloads()

	job, _ := jobs.Get("sab-stranded")
	if job["status"] != "downloading" {
		t.Errorf("status = %v, want untouched downloading", job["status"])
	}
}

func TestOrganizeNZBDownload(t *testing.T) {
	newFixture := func(t *testing.T) (*Manager, string) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := newJobID()
		m.Jobs().Set(jobID, map[string]interface{}{"status": "organizing", "error": nil})
		return m, jobID
	}

	t.Run("missing storage path", func(t *testing.T) {
		m, jobID := newFixture(t)
		m.organizeNZBDownload(jobID, "/no/such/storage", "G", "PC", "", true)
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
	})

	t.Run("empty storage path", func(t *testing.T) {
		m, jobID := newFixture(t)
		m.organizeNZBDownload(jobID, "", "G", "PC", "", true)
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
	})

	// A restart between moveContent and the status update leaves the staging
	// path gone while the content already sits in the library. Re-entering
	// organize must finish the job, not report the import as failed.
	t.Run("already moved content completes", func(t *testing.T) {
		m, jobID := newFixture(t)
		storage := filepath.Join(t.TempDir(), "Recovered Game")
		dest := filepath.Join(m.cfg.GamesRomsPath, "gba", "Recovered Game")
		writeFileT(t, filepath.Join(dest, "rom.gba"), []byte("rom"))

		m.organizeNZBDownloadWithClient(jobID, storage, "Recovered Game", "GBA", "gba", false, "sabnzbd")

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
		if !m.Jobs().LibraryHasSourceID("nzb:" + dest) {
			t.Error("already-moved content was not tracked in the library")
		}
	})

	t.Run("pc content to vault", func(t *testing.T) {
		m, jobID := newFixture(t)
		storage := filepath.Join(t.TempDir(), "PC Game")
		writeFileT(t, filepath.Join(storage, "setup.exe"), []byte("x"))
		m.organizeNZBDownload(jobID, storage, "PC Game", "PC", "", true)
		job, _ := m.Jobs().Get(jobID)
		if detail, _ := job["detail"].(string); !strings.Contains(detail, "library") {
			t.Errorf("detail = %q, want library", detail)
		}
		if !pathExists(filepath.Join(m.cfg.GamesVaultPath, "PC Game", "setup.exe")) {
			t.Error("content not moved to vault")
		}
	})

	t.Run("unknown platform stays in staging", func(t *testing.T) {
		m, jobID := newFixture(t)
		storage := filepath.Join(t.TempDir(), "Mystery")
		writeFileT(t, filepath.Join(storage, "file.dat"), []byte("x"))
		m.organizeNZBDownload(jobID, storage, "Mystery", "", "", false)
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
		if !pathExists(storage) {
			t.Error("content should remain in staging")
		}
	})

	t.Run("move failure sets error", func(t *testing.T) {
		m, jobID := newFixture(t)
		writeFileT(t, filepath.Join(m.cfg.GamesRomsPath, "snes"), []byte("blocking file"))
		storage := filepath.Join(t.TempDir(), "Rom Game")
		writeFileT(t, filepath.Join(storage, "rom.sfc"), []byte("x"))
		m.organizeNZBDownload(jobID, storage, "Rom Game", "SNES", "snes", false)
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error (job=%v)", status, job)
		}
	})
}

func TestRetryJob(t *testing.T) {
	t.Run("job not found", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		if ok, msg := m.RetryJob(newJobID()); ok || !strings.Contains(msg, "not found") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})

	t.Run("job not in failed state", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := newJobID()
		m.Jobs().Set(jobID, map[string]interface{}{"status": "downloading"})
		if ok, msg := m.RetryJob(jobID); ok || !strings.Contains(msg, "not in failed state") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})

	// A job with no stored release cannot be retried, and says so. Before the
	// identity was persisted this was every job: the old implementation
	// reported success and moved the job to a status nothing consumed.
	t.Run("no stored release", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := newJobID()
		m.Jobs().Set(jobID, map[string]interface{}{"status": "error", "title": "G"})
		ok, msg := m.RetryJob(jobID)
		if ok || !strings.Contains(msg, "no stored release") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})

	t.Run("ddl job re-drives and clears its blocklist entry", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := newJobID()
		// Port 1 refuses instantly: the retry's download goroutine finishes
		// fast and without touching the network.
		const url = "http://127.0.0.1:1/game.zip"
		m.Jobs().Set(jobID, map[string]interface{}{
			"status": "error", "title": "G", "platform": "PS1", "platform_slug": "psx",
			"source_type": "ddl", "download_url": url, "retry_count": float64(2),
		})
		if _, err := m.Jobs().AddBlocklistEntry(&db.BlocklistEntry{
			Title: "G", DownloadURL: url, Reason: "Download failed",
		}); err != nil {
			t.Fatalf("seed blocklist: %v", err)
		}

		ok, msg := m.RetryJob(jobID)
		if !ok || !strings.Contains(strings.ToLower(msg), "retry #3") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
		if m.Jobs().IsBlocklisted(url, "") {
			t.Error("retry left the release blocklisted; the selector would filter out the release just asked for")
		}
		job, _ := m.Jobs().Get(jobID)
		if detail, _ := job["detail"].(string); !strings.HasPrefix(detail, "Retried as job ") {
			t.Errorf("detail = %q, want the new job id", detail)
		}
		if rc, _ := job["retry_count"].(float64); rc != 3 {
			t.Errorf("retry_count = %v, want 3", rc)
		}
		// The retried job is a NEW job; the old one stays terminal rather
		// than moving to a status no worker reads.
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want the original job to stay error", status)
		}
		waitForTerminalJobs(t, m, jobID)
	})

	t.Run("torrent job without a client reports the failure", func(t *testing.T) {
		m := New(newTestConfig(t), newTestJobs(t), nil)
		jobID := newJobID()
		m.Jobs().Set(jobID, map[string]interface{}{
			"status": "dead_letter", "title": "G", "source_type": "torrent",
			"download_url": "magnet:?xt=urn:btih:" + strings.Repeat("a", 40),
		})
		if ok, msg := m.RetryJob(jobID); ok || !strings.Contains(msg, "Retry failed") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
}

// waitForTerminalJobs lets a retry's download goroutine finish before the
// test's temp dirs and database go away.
func waitForTerminalJobs(t *testing.T, m *Manager, except string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending := false
		for _, item := range m.Jobs().Items() {
			if item.ID == except {
				continue
			}
			if status, _ := item.Data["status"].(string); status == "downloading" {
				pending = true
			}
		}
		if !pending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("retry download goroutine did not finish")
}

func TestStrVal(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		key  string
		want string
	}{
		{"present string", map[string]interface{}{"a": "x"}, "a", "x"},
		{"missing key", map[string]interface{}{}, "a", ""},
		{"non-string value", map[string]interface{}{"a": 3}, "a", ""},
		{"nil value", map[string]interface{}{"a": nil}, "a", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strVal(tt.m, tt.key); got != tt.want {
				t.Errorf("strVal = %q, want %q", got, tt.want)
			}
		})
	}
}
