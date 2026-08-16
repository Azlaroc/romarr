package download

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gamarr/internal/qbit"
)

func TestNewJobID(t *testing.T) {
	seen := map[string]bool{}
	hexRe := regexp.MustCompile(`^[0-9a-f]{8}$`)
	for i := 0; i < 100; i++ {
		id := newJobID()
		if !hexRe.MatchString(id) {
			t.Fatalf("newJobID() = %q, want 8 hex chars", id)
		}
		if seen[id] {
			t.Fatalf("duplicate job ID %q", id)
		}
		seen[id] = true
	}
}

func TestNewManager(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	qb := qbit.New("http://127.0.0.1:1", "u", "p")
	m := New(cfg, jobs, qb)
	if m.Jobs() != jobs {
		t.Error("Jobs() should return the injected store")
	}
	if m.QB() != qb {
		t.Error("QB() should return the injected client")
	}
}

func TestDownloadTorrentValidation(t *testing.T) {
	cfg := newTestConfig(t)
	m := New(cfg, newTestJobs(t), nil)
	if _, err := m.DownloadTorrent(TorrentSpec{Title: "Title", Platform: "PC", IsPC: true}); err == nil {
		t.Fatal("empty URL should return an error")
	}
}

func TestDownloadTorrentNoClientAvailable(t *testing.T) {
	// No qBittorrent configured: rejected outright. The old Transmission/
	// Deluge fallbacks were silent black holes (nothing ever watched those
	// clients), so they are gone.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	_, err := m.DownloadTorrent(TorrentSpec{URL: "magnet:x", Title: "Some Game", Platform: "PC", IsPC: true})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want qBittorrent-not-configured error", err)
	}
	if n := len(jobs.Items()); n != 0 {
		t.Errorf("jobs created = %d, want 0", n)
	}
}

func TestDownloadTorrentAddFailure(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	cfg.QBURL = "configured"
	qm := newQbitMock(t)
	qm.mu.Lock()
	qm.addOK = false
	qm.mu.Unlock()

	// A .torrent URL is fetched server-side asynchronously; a dead URL falls
	// back to the URL-add, which the mock rejects — the job must error.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	m := New(cfg, jobs, qm.client())
	jobID, err := m.DownloadTorrent(TorrentSpec{URL: dead.URL + "/some.torrent", Title: "Some Game", Platform: "SNES", PlatformSlug: "snes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job := waitJobStatus(t, jobs, jobID, "error", 10*time.Second)
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "qBittorrent") {
		t.Errorf("error = %q, want qBittorrent add-failed message", errMsg)
	}
}

func TestDownloadTorrentFetchesAndUploadsFile(t *testing.T) {
	// A .torrent URL is fetched server-side and handed to qBittorrent as a
	// file blob (qBittorrent's own URL fetching is dead on VPN-tunneled
	// boxes), with the infohash extracted up front.
	cfg := newTestConfig(t)
	cfg.QBURL = "configured"
	jobs := newTestJobs(t)
	qm := newQbitMock(t)
	blob, wantHash := testTorrentFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(blob)
	}))
	defer srv.Close()

	m := New(cfg, jobs, qm.client())
	jobID, err := m.DownloadTorrent(TorrentSpec{URL: srv.URL + "/release.torrent", Title: "FileAdd", Platform: "SNES", PlatformSlug: "snes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, 10*time.Second, "file-blob add", func() bool { return qm.addCallCount() == 1 })
	qm.mu.Lock()
	form := qm.addForms[0]
	qm.mu.Unlock()
	if form.Get("__file") != string(blob) {
		t.Error("torrent bytes were not uploaded as a file blob")
	}
	if form.Has("urls") {
		t.Errorf("expected file add, got URL add: %v", form)
	}
	waitFor(t, 5*time.Second, "hash persisted", func() bool {
		j, _ := jobFromDB(t, jobs, jobID)
		h, _ := j["torrent_hash"].(string)
		return h == wantHash
	})
}

func TestDownloadTorrentURLAddFallbackWhenFetchFails(t *testing.T) {
	// When the server-side fetch fails (e.g. the indexer only lets the
	// download client fetch it), fall back to the legacy URL-add so
	// deployments where qBittorrent CAN fetch keep working.
	cfg := newTestConfig(t)
	cfg.QBURL = "configured"
	jobs := newTestJobs(t)
	qm := newQbitMock(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	m := New(cfg, jobs, qm.client())
	torrentURL := srv.URL + "/some.torrent"
	jobID, err := m.DownloadTorrent(TorrentSpec{URL: torrentURL, Title: "Fallback", Platform: "SNES", PlatformSlug: "snes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, 10*time.Second, "fallback URL add", func() bool { return qm.addCallCount() == 1 })
	qm.mu.Lock()
	form := qm.addForms[0]
	qm.mu.Unlock()
	if form.Get("urls") != torrentURL {
		t.Errorf("fallback did not URL-add: %v", form)
	}
	job, _ := jobFromDB(t, jobs, jobID)
	if status, _ := job["status"].(string); status != "downloading" {
		t.Errorf("status = %q, want downloading", status)
	}
}

func TestDownloadTorrentAdoptsExisting(t *testing.T) {
	// A duplicate add silently no-ops in qBittorrent, so a submit whose hash
	// already exists must adopt the torrent instead of adding again.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	cfg.QBURL = "configured"
	hash := strings.Repeat("cd", 20)
	qm := newQbitMock(t)
	qm.setTorrents([]qbit.Torrent{{Name: "Existing", Hash: hash, Progress: 0.5}})

	m := New(cfg, jobs, qm.client())
	jobID, err := m.DownloadTorrent(TorrentSpec{URL: "magnet:?xt=urn:btih:" + hash, Title: "Existing", Platform: "SNES", PlatformSlug: "snes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if qm.addCallCount() != 0 {
		t.Errorf("addCalls = %d, want 0 (adopt, not re-add)", qm.addCallCount())
	}
	job, _ := jobFromDB(t, jobs, jobID)
	if got, _ := job["torrent_hash"].(string); got != hash {
		t.Errorf("torrent_hash = %q, want %q", got, hash)
	}
}

func TestParseBTIH(t *testing.T) {
	hexHash := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		in, want string
	}{
		{"magnet:?xt=urn:btih:" + hexHash + "&dn=Game", hexHash},
		{"magnet:?xt=urn:btih:" + strings.ToUpper(hexHash), hexHash},
		// base32 form of the same 20 bytes.
		{"magnet:?xt=urn:btih:AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH", hexHash},
		{"http://indexer.test/file.torrent", ""},
		{"magnet:?xt=urn:btih:tooshort", ""},
	}
	for _, tt := range tests {
		if got := parseBTIH(tt.in); got != tt.want {
			t.Errorf("parseBTIH(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDownloadTorrentQBitFullFlow(t *testing.T) {
	// End-to-end with the persistent watcher: submit by magnet (hash known up
	// front), the watcher tick sees the completed torrent, and the import
	// COPIES the payload into the ROM library — the seeding payload and the
	// torrent itself must survive.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	cfg.QBURL = "configured"

	content := filepath.Join(t.TempDir(), "Super Game (USA)")
	writeFileT(t, filepath.Join(content, "game.sfc"), []byte("rom-data"))

	hash := strings.Repeat("ab", 20)
	qm := newQbitMock(t)
	qm.setFiles([]qbit.TorrentFile{{Name: "Super Game (USA)/game.sfc"}})

	m := New(cfg, jobs, qm.client())
	jobID, err := m.DownloadTorrent(TorrentSpec{
		URL:   "magnet:?xt=urn:btih:" + hash,
		Title: "Super Game (USA)", Platform: "SNES", PlatformSlug: "snes",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job, _ := jobFromDB(t, jobs, jobID)
	if got, _ := job["torrent_hash"].(string); got != hash {
		t.Fatalf("torrent_hash = %q, want %q", got, hash)
	}

	// The torrent appears in qBittorrent, fully downloaded and seeding.
	qm.setTorrents([]qbit.Torrent{{
		Name: "Super Game (USA)", Hash: hash, Progress: 1.0,
		State: "uploading", ContentPath: content,
	}})

	w := NewWatcher(cfg, m)
	w.tick()

	job = waitJobStatus(t, jobs, jobID, "completed", 10*time.Second)
	if detail, _ := job["detail"].(string); !strings.Contains(detail, "RomM (SNES)") {
		t.Errorf("detail = %q, want RomM (SNES)", detail)
	}
	// imported_at is the import goroutine's LAST write (after library
	// tracking and staging cleanup) — waiting on it makes everything below
	// race-free.
	waitFor(t, 10*time.Second, "imported_at", func() bool {
		j, _ := jobFromDB(t, jobs, jobID)
		return int64Value(j["imported_at"]) > 0
	})

	dest := filepath.Join(cfg.GamesRomsPath, "snes", "Super Game (USA)")
	if !pathExists(filepath.Join(dest, "game.sfc")) {
		t.Errorf("game file not imported to %s", dest)
	}
	if !pathExists(filepath.Join(dest, ".gamarr.json")) {
		t.Error("metadata sidecar not written")
	}
	// Seed-safety: the payload is untouched and the torrent survives.
	data, err := os.ReadFile(filepath.Join(content, "game.sfc"))
	if err != nil || string(data) != "rom-data" {
		t.Errorf("seeding payload was touched: %q err=%v", data, err)
	}
	if got := qm.deletedHashes(); len(got) != 0 {
		t.Errorf("torrent deleted after import: %v", got)
	}
	if !jobs.LibraryHasSourceID("torrent:" + hash) {
		t.Error("library item not tracked")
	}
	items := jobs.RecentLibraryItems(1)
	if len(items) != 1 || items[0].FileSize <= 0 {
		t.Errorf("library file_size not populated: %+v", items)
	}
	if pathExists(filepath.Join(cfg.GamesRomsPath, ".gamarr-tmp", jobID)) {
		t.Error("import staging dir not cleaned up")
	}
}

func TestDownloadTorrentBlocksDangerousFiles(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	cfg.QBURL = "configured"

	hash := strings.Repeat("ee", 20)
	qm := newQbitMock(t)
	qm.setFiles([]qbit.TorrentFile{{Name: "Game/keygen.bat"}, {Name: "Game/setup.scr"}})

	m := New(cfg, jobs, qm.client())
	jobID, err := m.DownloadTorrent(TorrentSpec{
		URL:   "magnet:?xt=urn:btih:" + hash,
		Title: "Evil Game", Platform: "PC", IsPC: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qm.setTorrents([]qbit.Torrent{{
		Name: "Evil Game", Hash: hash, Progress: 0.5, State: "downloading",
	}})

	w := NewWatcher(cfg, m)
	w.tick()

	job := waitJobStatus(t, jobs, jobID, "error", 5*time.Second)
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "Blocked") {
		t.Errorf("error = %q, want Blocked message", errMsg)
	}
	if got := qm.deletedHashes(); len(got) != 1 || got[0] != hash {
		t.Errorf("deleted hashes = %v, want the dangerous torrent removed", got)
	}
}

func TestOrganizeTorrent(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		cfg := newTestConfig(t)
		qm := newQbitMock(t)
		m := New(cfg, newTestJobs(t), qm.client())
		if _, err := m.OrganizeTorrent("missing-hash", "PC", "", true); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v, want not found", err)
		}
	})

	t.Run("not complete", func(t *testing.T) {
		cfg := newTestConfig(t)
		qm := newQbitMock(t)
		qm.setTorrents([]qbit.Torrent{{Name: "G", Hash: "h1", Progress: 0.4}})
		m := New(cfg, newTestJobs(t), qm.client())
		if _, err := m.OrganizeTorrent("h1", "PC", "", true); err == nil || !strings.Contains(err.Error(), "not yet complete") {
			t.Fatalf("err = %v, want not yet complete", err)
		}
	})

	t.Run("organizes completed PC torrent by copy", func(t *testing.T) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		content := filepath.Join(t.TempDir(), "Cool.Game-FitGirl")
		writeFileT(t, filepath.Join(content, "setup.exe"), []byte("installer"))

		qm := newQbitMock(t)
		qm.setTorrents([]qbit.Torrent{{
			Name: "Cool.Game-FitGirl", Hash: "h2", Progress: 1.0, ContentPath: content,
		}})
		m := New(cfg, jobs, qm.client())

		jobID, err := m.OrganizeTorrent("h2", "PC", "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		job := waitJobStatus(t, jobs, jobID, "completed", 10*time.Second)
		if detail, _ := job["detail"].(string); !strings.Contains(detail, "library") {
			t.Errorf("detail = %q, want library", detail)
		}
		if !pathExists(filepath.Join(cfg.GamesVaultPath, "Cool.Game-FitGirl", "setup.exe")) {
			t.Error("game not imported to vault")
		}
		// Manual organize must not kill seeding either: payload + torrent live.
		if !pathExists(filepath.Join(content, "setup.exe")) {
			t.Error("seeding payload was touched")
		}
		if got := qm.deletedHashes(); len(got) != 0 {
			t.Errorf("torrent deleted: %v", got)
		}
	})
}

// setupImportJob creates a manager plus a pre-seeded torrent job ready for
// importTorrentJob.
func setupImportJob(t *testing.T, platf, platSlug string, isPC bool) (*Manager, *qbitMock, string) {
	t.Helper()
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	qm := newQbitMock(t)
	m := New(cfg, jobs, qm.client())
	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{
		"status": "scanning", "title": "T", "platform": platf,
		"platform_slug": platSlug, "is_pc": isPC, "error": nil, "detail": "",
		"source_type": "torrent", "source_client": "qbittorrent",
	})
	return m, qm, jobID
}

func TestImportTorrentJob(t *testing.T) {
	t.Run("missing content path errors", func(t *testing.T) {
		m, _, jobID := setupImportJob(t, "PC", "", true)
		torrent := &qbit.Torrent{Name: "Ghost", Hash: "gh", ContentPath: "/nonexistent/nope"}
		m.importTorrentJob(jobID, torrent)
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
	})

	t.Run("unknown platform left in staging", func(t *testing.T) {
		m, qm, jobID := setupImportJob(t, "", "", false)
		content := filepath.Join(t.TempDir(), "mysterious-thing")
		writeFileT(t, filepath.Join(content, "data.dat"), []byte("???"))
		torrent := &qbit.Torrent{Name: "mysterious-thing", Hash: "mh", ContentPath: content}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
		if detail, _ := job["detail"].(string); !strings.Contains(detail, "unknown platform") {
			t.Errorf("detail = %q, want unknown platform", detail)
		}
		if !pathExists(content) {
			t.Error("content should stay in staging")
		}
		if len(qm.deletedHashes()) != 0 {
			t.Error("torrent must not be deleted for unknown platform")
		}
	})

	t.Run("platform detected from file extension, payload survives", func(t *testing.T) {
		m, qm, jobID := setupImportJob(t, "", "", false)
		content := filepath.Join(t.TempDir(), "handheld-game")
		writeFileT(t, filepath.Join(content, "game.gba"), []byte("gba-rom"))
		torrent := &qbit.Torrent{Name: "handheld-game", Hash: "dh", ContentPath: content}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Fatalf("status = %q, want completed (job=%v)", status, job)
		}
		if slug, _ := job["platform_slug"].(string); slug != "gba" {
			t.Errorf("platform_slug = %q, want gba", slug)
		}
		if !pathExists(filepath.Join(m.cfg.GamesRomsPath, "gba", "handheld-game", "game.gba")) {
			t.Error("ROM not imported to gba library dir")
		}
		// Copy, not move: the seeding payload survives byte-identical.
		data, err := os.ReadFile(filepath.Join(content, "game.gba"))
		if err != nil || string(data) != "gba-rom" {
			t.Errorf("seeding payload touched: %q err=%v", data, err)
		}
		if len(qm.deletedHashes()) != 0 {
			t.Error("torrent must not be deleted")
		}
	})

	t.Run("platform detected from metadata.json", func(t *testing.T) {
		m, _, jobID := setupImportJob(t, "", "", false)
		content := filepath.Join(t.TempDir(), "meta-game")
		writeFileT(t, filepath.Join(content, "metadata.json"), []byte(`{"platform":"snes"}`))
		writeFileT(t, filepath.Join(content, "game.bin2"), []byte("rom"))
		torrent := &qbit.Torrent{Name: "meta-game", Hash: "mm", ContentPath: content}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if slug, _ := job["platform_slug"].(string); slug != "snes" {
			t.Errorf("platform_slug = %q, want snes", slug)
		}
	})

	t.Run("falls back to save path when content path empty", func(t *testing.T) {
		m, _, jobID := setupImportJob(t, "SNES", "snes", false)
		savePath := t.TempDir()
		writeFileT(t, filepath.Join(savePath, "SavedGame", "rom.sfc"), []byte("rom"))
		torrent := &qbit.Torrent{Name: "SavedGame", Hash: "sp", SavePath: savePath}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
		if !pathExists(filepath.Join(m.cfg.GamesRomsPath, "snes", "SavedGame", "rom.sfc")) {
			t.Error("ROM not imported from save path")
		}
	})

	t.Run("staging failure sets error and leaves seed intact", func(t *testing.T) {
		m, _, jobID := setupImportJob(t, "PC", "", true)
		// Make the vault path a regular file so the .gamarr-tmp MkdirAll fails.
		os.RemoveAll(m.cfg.GamesVaultPath)
		writeFileT(t, m.cfg.GamesVaultPath, []byte("not a dir"))

		content := filepath.Join(t.TempDir(), "Blocked.Game-CODEX")
		writeFileT(t, filepath.Join(content, "setup.exe"), []byte("x"))
		torrent := &qbit.Torrent{Name: "Blocked.Game-CODEX", Hash: "bf", ContentPath: content}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
		if !pathExists(filepath.Join(content, "setup.exe")) {
			t.Error("seeding payload touched on failure")
		}
	})

	t.Run("existing destination is never clobbered", func(t *testing.T) {
		m, _, jobID := setupImportJob(t, "SNES", "snes", false)
		content := filepath.Join(t.TempDir(), "Dupe Game")
		writeFileT(t, filepath.Join(content, "rom.sfc"), []byte("new"))
		dest := filepath.Join(m.cfg.GamesRomsPath, "snes", "Dupe Game")
		writeFileT(t, filepath.Join(dest, "rom.sfc"), []byte("original"))
		torrent := &qbit.Torrent{Name: "Dupe Game", Hash: "dg", ContentPath: content}

		m.importTorrentJob(jobID, torrent)

		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
		if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "already exists") {
			t.Errorf("error = %q, want already-exists message", errMsg)
		}
		data, _ := os.ReadFile(filepath.Join(dest, "rom.sfc"))
		if string(data) != "original" {
			t.Errorf("existing destination clobbered: %q", data)
		}
	})
}

func TestDownloadDDL(t *testing.T) {
	t.Run("full flow with content-disposition filename", func(t *testing.T) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Disposition", `attachment; filename="Mario World.sfc"`)
			w.Write([]byte("rom-bytes"))
		}))
		defer srv.Close()

		m := New(cfg, jobs, nil)
		jobID := m.DownloadDDL(srv.URL+"/dl", "", "Mario World", "SNES", "snes", false, "", "")

		waitFor(t, 10*time.Second, "library tracking", func() bool {
			return jobs.LibraryHasSourceID("ddl:" + filepath.Join(cfg.GamesRomsPath, "snes", "Mario World.sfc"))
		})
		job := waitJobStatus(t, jobs, jobID, "completed", 5*time.Second)
		if detail, _ := job["detail"].(string); !strings.Contains(detail, "RomM (SNES)") {
			t.Errorf("detail = %q, want RomM (SNES)", detail)
		}
		dest := filepath.Join(cfg.GamesRomsPath, "snes", "Mario World.sfc")
		data, err := os.ReadFile(dest)
		if err != nil || string(data) != "rom-bytes" {
			t.Errorf("dest content = %q err=%v, want rom-bytes", data, err)
		}
		if !pathExists(dest + ".gamarr.json") {
			t.Error("sidecar not written for file dest")
		}
	})

	t.Run("http error fails the job with cause", func(t *testing.T) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		m := New(cfg, jobs, nil)
		jobID := m.DownloadDDL(srv.URL+"/gone", "", "Missing Game", "PC", "", true, "", "")
		job := waitJobStatus(t, jobs, jobID, "error", 5*time.Second)
		errMsg, _ := job["error"].(string)
		if !strings.Contains(errMsg, "Download failed") {
			t.Errorf("error = %q, want Download failed", errMsg)
		}
		if !strings.Contains(errMsg, "404") {
			t.Errorf("error = %q, want HTTP 404 cause included", errMsg)
		}
	})

	t.Run("uncreatable staging dir fails the job with cause", func(t *testing.T) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		// A regular file where the staging dir's parent should be makes
		// os.MkdirAll fail with ENOTDIR.
		blocker := filepath.Join(t.TempDir(), "blocker")
		writeFileT(t, blocker, []byte("not a dir"))
		cfg.QBSavePath = filepath.Join(blocker, "staging")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("rom-bytes"))
		}))
		defer srv.Close()

		m := New(cfg, jobs, nil)
		jobID := m.DownloadDDL(srv.URL+"/dl", "", "Blocked Game", "PC", "", true, "", "")
		job := waitJobStatus(t, jobs, jobID, "error", 5*time.Second)
		errMsg, _ := job["error"].(string)
		if !strings.Contains(errMsg, "cannot create staging dir") {
			t.Errorf("error = %q, want 'cannot create staging dir' cause", errMsg)
		}
		if !strings.Contains(errMsg, cfg.QBSavePath) {
			t.Errorf("error = %q, want staging path %q included", errMsg, cfg.QBSavePath)
		}
	})

	t.Run("no url and no vimm id fails", func(t *testing.T) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		m := New(cfg, jobs, nil)
		jobID := m.DownloadDDL("", "", "Nothing", "PC", "", true, "", "")
		waitJobStatus(t, jobs, jobID, "error", 5*time.Second)
	})
}

func TestDownloadDDLFilenameFromURL(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data")) // no Content-Disposition
	}))
	defer srv.Close()

	m := New(cfg, jobs, nil)
	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "downloading"})

	got, err := m.downloadDDL(srv.URL+"/files/zelda.gba?token=1", cfg.QBSavePath, jobID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(got) != "zelda.gba" {
		t.Errorf("filename = %q, want zelda.gba", filepath.Base(got))
	}
	if !pathExists(got) {
		t.Error("downloaded file missing")
	}
}

func TestDownloadDDLCreateFileFailure(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	m := New(cfg, jobs, nil)
	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "downloading"})

	// Destination dir does not exist, so os.Create fails.
	dest := filepath.Join(t.TempDir(), "no-such-dir")
	got, err := m.downloadDDL(srv.URL+"/files/game.bin", dest, jobID)
	if got != "" {
		t.Errorf("path = %q, want empty on create failure", got)
	}
	if err == nil || !strings.Contains(err.Error(), "cannot create file") {
		t.Errorf("err = %v, want 'cannot create file' cause", err)
	}
}

// TestDownloadDDLTruncatedDownloadIsError verifies that a server which declares
// a Content-Length but drops the connection early does not leave a partial file
// reported as a finished download. Without the guard the truncated archive would
// pass to the scan/organize pipeline as complete.
func TestDownloadDDLTruncatedDownloadIsError(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="Truncated.sfc"`)
		w.Header().Set("Content-Length", "1000") // claim far more than we send
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("only-a-few-bytes"))
		// Return without sending the rest; the client sees an unexpected EOF.
	}))
	defer srv.Close()

	m := New(cfg, jobs, nil)
	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "downloading"})

	dest := t.TempDir()
	got, err := m.downloadDDL(srv.URL+"/dl", dest, jobID)
	if err == nil {
		t.Fatal("expected an error for a truncated download, got nil")
	}
	if got != "" {
		t.Errorf("path = %q, want empty on truncated download", got)
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("partial file left behind: %v", entries)
	}
}

func TestOrganizeDDLFile(t *testing.T) {
	newFixture := func(t *testing.T) (*Manager, string, string) {
		cfg := newTestConfig(t)
		jobs := newTestJobs(t)
		m := New(cfg, jobs, nil)
		jobID := newJobID()
		jobs.Set(jobID, map[string]interface{}{"status": "organizing", "error": nil})
		src := filepath.Join(t.TempDir(), "game-file.bin")
		writeFileT(t, src, []byte("payload"))
		return m, jobID, src
	}

	t.Run("pc file goes to vault", func(t *testing.T) {
		m, jobID, src := newFixture(t)
		m.organizeDDLFile(jobID, src, "Great Game", "PC", "", true, "", "")
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "completed" {
			t.Fatalf("status = %q, want completed", status)
		}
		if !pathExists(filepath.Join(m.cfg.GamesVaultPath, "game-file.bin")) {
			t.Error("file not moved to vault")
		}
		if !m.Jobs().LibraryHasSourceID("ddl:" + filepath.Join(m.cfg.GamesVaultPath, "game-file.bin")) {
			t.Error("library item not tracked")
		}
	})

	t.Run("rom goes to platform dir", func(t *testing.T) {
		m, jobID, src := newFixture(t)
		m.organizeDDLFile(jobID, src, "Great Game", "PSP", "psp", false, "", "")
		if !pathExists(filepath.Join(m.cfg.GamesRomsPath, "psp", "game-file.bin")) {
			t.Error("file not moved to psp dir")
		}
	})

	t.Run("unknown platform left in staging", func(t *testing.T) {
		m, jobID, src := newFixture(t)
		m.organizeDDLFile(jobID, src, "Great Game", "", "", false, "", "")
		job, _ := m.Jobs().Get(jobID)
		if detail, _ := job["detail"].(string); !strings.Contains(detail, "unknown platform") {
			t.Errorf("detail = %q, want unknown platform", detail)
		}
		if !pathExists(src) {
			t.Error("file should remain in place")
		}
	})

	t.Run("move failure sets error", func(t *testing.T) {
		m, jobID, src := newFixture(t)
		os.RemoveAll(m.cfg.GamesVaultPath) // vault dir gone: os.Create fails
		m.organizeDDLFile(jobID, src, "Great Game", "PC", "", true, "", "")
		job, _ := m.Jobs().Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("status = %q, want error", status)
		}
	})
}

func TestRecoverOrphanedTorrents(t *testing.T) {
	// Boot recovery no longer mints jobs — it only wakes the qBittorrent
	// session. Torrent jobs persist their infohash and the watcher re-drives
	// them from the database, so duplicate jobs at boot are structurally
	// impossible; out-of-band torrents are the watcher orphan sweep's job.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	qm := newQbitMock(t)
	qm.setTorrents([]qbit.Torrent{
		{Name: "Awesome.Game.v1.2-FitGirl", Hash: "h1", Progress: 1.0},
		{Name: "Totally Mysterious Thing", Hash: "h3", Progress: 1.0},
	})
	cfg.QBURL = qm.srv.URL

	m := New(cfg, jobs, qm.client())
	m.RecoverOrphanedTorrents()

	if n := len(jobs.Items()); n != 0 {
		t.Fatalf("recovery minted %d jobs, want 0", n)
	}
}

func TestMoveHelpers(t *testing.T) {
	t.Run("moveFile renames", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "a.txt")
		dest := filepath.Join(dir, "b.txt")
		writeFileT(t, src, []byte("hello"))
		if err := moveFile(src, dest); err != nil {
			t.Fatalf("moveFile: %v", err)
		}
		if pathExists(src) || !pathExists(dest) {
			t.Error("moveFile did not move the file")
		}
	})

	t.Run("moveFile missing source", func(t *testing.T) {
		dir := t.TempDir()
		if err := moveFile(filepath.Join(dir, "nope"), filepath.Join(dir, "out")); err == nil {
			t.Error("want error for missing source")
		}
	})

	t.Run("moveContent directory", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "gamedir")
		writeFileT(t, filepath.Join(src, "sub", "file.bin"), []byte("data"))
		dest := filepath.Join(dir, "moved")
		if err := moveContent(src, dest); err != nil {
			t.Fatalf("moveContent: %v", err)
		}
		if pathExists(src) {
			t.Error("source dir should be removed")
		}
		data, err := os.ReadFile(filepath.Join(dest, "sub", "file.bin"))
		if err != nil || string(data) != "data" {
			t.Errorf("moved content = %q err=%v", data, err)
		}
	})

	t.Run("moveContent single file", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "single.rom")
		writeFileT(t, src, []byte("x"))
		dest := filepath.Join(dir, "single-moved.rom")
		if err := moveContent(src, dest); err != nil {
			t.Fatalf("moveContent: %v", err)
		}
		if !pathExists(dest) {
			t.Error("file not moved")
		}
	})

	t.Run("moveContent missing source", func(t *testing.T) {
		if err := moveContent("/no/such/path", t.TempDir()); err == nil {
			t.Error("want error for missing source")
		}
	})

	t.Run("copyFile missing source", func(t *testing.T) {
		if err := copyFile("/no/such/file", filepath.Join(t.TempDir(), "out")); err == nil {
			t.Error("want error for missing source")
		}
	})

	t.Run("pathExists", func(t *testing.T) {
		dir := t.TempDir()
		if !pathExists(dir) {
			t.Error("existing dir reported missing")
		}
		if pathExists(filepath.Join(dir, "ghost")) {
			t.Error("missing path reported existing")
		}
	})
}

func TestWriteMetadataSidecar(t *testing.T) {
	t.Run("directory dest writes inside", func(t *testing.T) {
		dir := t.TempDir()
		writeMetadataSidecar(dir, "My Game", "SNES", "snes", false, "torrent")
		data, err := os.ReadFile(filepath.Join(dir, ".gamarr.json"))
		if err != nil {
			t.Fatalf("sidecar missing: %v", err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal(data, &meta); err != nil {
			t.Fatalf("invalid sidecar JSON: %v", err)
		}
		if meta["title"] != "My Game" || meta["platform_slug"] != "snes" || meta["source"] != "torrent" {
			t.Errorf("sidecar meta = %v", meta)
		}
		if meta["is_pc"] != false {
			t.Errorf("is_pc = %v, want false", meta["is_pc"])
		}
	})

	t.Run("file dest writes sibling", func(t *testing.T) {
		fp := filepath.Join(t.TempDir(), "rom.sfc")
		writeFileT(t, fp, []byte("rom"))
		writeMetadataSidecar(fp, "Rom Game", "SNES", "snes", false, "ddl")
		if !pathExists(fp + ".gamarr.json") {
			t.Error("sibling sidecar missing")
		}
	})
}

func TestSettingsRoundTrip(t *testing.T) {
	cfg := newTestConfig(t)
	m := New(cfg, newTestJobs(t), nil)

	t.Run("defaults when no file", func(t *testing.T) {
		s := m.LoadSettings()
		if s.ExtractArchives != cfg.ExtractArchives {
			t.Errorf("ExtractArchives = %v, want config default %v", s.ExtractArchives, cfg.ExtractArchives)
		}
	})

	t.Run("save and load", func(t *testing.T) {
		m.SaveSettings(&Settings{ExtractArchives: true})
		s := m.LoadSettings()
		if !s.ExtractArchives {
			t.Error("ExtractArchives = false after saving true")
		}
	})

	t.Run("corrupt file falls back to default", func(t *testing.T) {
		writeFileT(t, filepath.Join(cfg.DataDir, "settings.json"), []byte("{{{"))
		s := m.LoadSettings()
		if s.ExtractArchives != cfg.ExtractArchives {
			t.Errorf("corrupt settings should fall back to config default")
		}
	})
}

func TestDDLSourcesRoundTrip(t *testing.T) {
	cfg := newTestConfig(t)
	m := New(cfg, newTestJobs(t), nil)

	if got := m.LoadDDLSources(); got != nil {
		t.Errorf("LoadDDLSources with no file = %v, want nil", got)
	}

	sources := []map[string]interface{}{
		{"name": "Myrient", "url": "https://example.test/roms"},
		{"name": "Other", "enabled": true},
	}
	m.SaveDDLSources(sources)
	got := m.LoadDDLSources()
	if len(got) != 2 {
		t.Fatalf("loaded %d sources, want 2", len(got))
	}
	if got[0]["name"] != "Myrient" {
		t.Errorf("first source = %v", got[0])
	}
}

func TestDownloadTorrentTargetFileAddMode(t *testing.T) {
	// #256: a .torrent-URL add with a target goes in stopped (both 5.x
	// "stopped" and 4.x "paused" keys); a magnet must add running or metadata
	// never resolves.
	cfg := newTestConfig(t)
	cfg.QBURL = "configured"
	jobs := newTestJobs(t)
	qm := newQbitMock(t)
	blob, _ := testTorrentFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(blob)
	}))
	defer srv.Close()
	m := New(cfg, jobs, qm.client())

	jobID, err := m.DownloadTorrent(TorrentSpec{
		URL: srv.URL + "/pack.torrent", Title: "Pack", Platform: "SNES",
		PlatformSlug: "snes", TargetFile: "Pack/B.sfc",
	})
	if err != nil {
		t.Fatal(err)
	}
	job, _ := jobFromDB(t, jobs, jobID)
	if got, _ := job["target_file"].(string); got != "Pack/B.sfc" {
		t.Errorf("target_file = %q", got)
	}
	waitFor(t, 10*time.Second, "stopped file add", func() bool { return qm.addCallCount() == 1 })
	forms := func() []url.Values {
		qm.mu.Lock()
		defer qm.mu.Unlock()
		out := make([]url.Values, len(qm.addForms))
		copy(out, qm.addForms)
		return out
	}()
	if forms[0].Get("stopped") != "true" || forms[0].Get("paused") != "true" {
		t.Errorf("torrent-URL target add not stopped: %v", forms[0])
	}
	if forms[0].Get("__file") == "" {
		t.Errorf("target add should upload the torrent file blob: %v", forms[0])
	}

	if _, err := m.DownloadTorrent(TorrentSpec{
		URL: "magnet:?xt=urn:btih:" + strings.Repeat("99", 20), Title: "Pack2",
		Platform: "SNES", PlatformSlug: "snes", TargetFile: "Pack2/C.sfc",
	}); err != nil {
		t.Fatal(err)
	}
	forms = func() []url.Values {
		qm.mu.Lock()
		defer qm.mu.Unlock()
		out := make([]url.Values, len(qm.addForms))
		copy(out, qm.addForms)
		return out
	}()
	if len(forms) != 2 {
		t.Fatalf("addForms = %d, want 2", len(forms))
	}
	if forms[1].Has("stopped") || forms[1].Has("paused") {
		t.Errorf("magnet target add must run for metadata: %v", forms[1])
	}
}
