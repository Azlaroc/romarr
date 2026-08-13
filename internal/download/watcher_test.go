package download

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/db"
	"gamarr/internal/qbit"
)

func newTestWatcher(t *testing.T, qm *qbitMock) (*Watcher, *Manager) {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.QBURL = "configured"
	jobs := newTestJobs(t)
	m := New(cfg, jobs, qm.client())
	return NewWatcher(cfg, m), m
}

// seedTorrentJob persists a torrent job the watcher should drive.
func seedTorrentJob(t *testing.T, m *Manager, jobID string, fields map[string]interface{}) {
	t.Helper()
	data := map[string]interface{}{
		"status": "downloading", "title": "T", "platform": "SNES",
		"platform_slug": "snes", "is_pc": false, "error": nil, "detail": "",
		"source_type": "torrent", "source_client": "qbittorrent",
		"torrent_hash": "", "qb_tag": "", "started_at": time.Now().Unix(),
	}
	for k, v := range fields {
		data[k] = v
	}
	m.Jobs().Set(jobID, data)
}

// snapshotTree maps rel-path -> content for every file under root.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotTree(%s): %v", root, err)
	}
	return out
}

func TestWatcherStartDisabled(t *testing.T) {
	t.Run("watcher disabled by flag", func(t *testing.T) {
		qm := newQbitMock(t)
		w, _ := newTestWatcher(t, qm)
		w.cfg.WatcherEnabled = false
		w.Start() // must return immediately without panicking
		w.Stop()
	})

	t.Run("no qbittorrent configured", func(t *testing.T) {
		cfg := newTestConfig(t)
		cfg.WatcherEnabled = true
		cfg.QBURL = ""
		m := New(cfg, newTestJobs(t), nil)
		w := NewWatcher(cfg, m)
		w.Start()
		w.Stop()
	})
}

func TestWatcherStartStop(t *testing.T) {
	qm := newQbitMock(t) // empty torrent list
	w, _ := newTestWatcher(t, qm)
	w.cfg.WatcherEnabled = true
	w.cfg.WatcherIntervalS = 0 // clamps to the 30s default

	w.Start()
	w.Stop()
	w.Stop() // double stop must not panic
}

func TestTorrentComplete(t *testing.T) {
	tests := []struct {
		name string
		t    qbit.Torrent
		want bool
	}{
		{"seeding done", qbit.Torrent{Progress: 1.0, AmountLeft: 0, State: "uploading"}, true},
		{"stopped after seed", qbit.Torrent{Progress: 1.0, AmountLeft: 0, State: "stoppedUP"}, true},
		{"still downloading", qbit.Torrent{Progress: 0.9, AmountLeft: 100, State: "downloading"}, false},
		{"progress rounded up but bytes left", qbit.Torrent{Progress: 1.0, AmountLeft: 512, State: "downloading"}, false},
		{"rechecking", qbit.Torrent{Progress: 1.0, AmountLeft: 0, State: "checkingUP"}, false},
		{"moving", qbit.Torrent{Progress: 1.0, AmountLeft: 0, State: "moving"}, false},
		{"fetching metadata", qbit.Torrent{Progress: 0, AmountLeft: 0, State: "metaDL"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := torrentComplete(&tt.t); got != tt.want {
				t.Errorf("torrentComplete = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWatcherAssociatesByTag(t *testing.T) {
	qm := newQbitMock(t)
	w, m := newTestWatcher(t, qm)
	hash := strings.Repeat("aa", 20)

	seedTorrentJob(t, m, "job1", map[string]interface{}{"qb_tag": "gamarr-job1"})
	qm.setTorrents([]qbit.Torrent{{
		Name: "T", Hash: hash, Progress: 0.4, State: "downloading",
		Tags: "gamarr-job1, other-tag",
	}})

	w.tick()

	job, _ := jobFromDB(t, m.Jobs(), "job1")
	if got, _ := job["torrent_hash"].(string); got != hash {
		t.Errorf("torrent_hash = %q, want %q (learned from tag)", got, hash)
	}
	if status, _ := job["status"].(string); status != "downloading" {
		t.Errorf("status = %q, want downloading", status)
	}
}

func TestWatcherAssociationTimeout(t *testing.T) {
	qm := newQbitMock(t) // no torrents at all
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "stale", map[string]interface{}{
		"qb_tag":     "gamarr-stale",
		"started_at": time.Now().Add(-11 * time.Minute).Unix(),
	})
	seedTorrentJob(t, m, "fresh", map[string]interface{}{
		"qb_tag": "gamarr-fresh",
	})

	w.tick()

	stale, _ := jobFromDB(t, m.Jobs(), "stale")
	if status, _ := stale["status"].(string); status != "error" {
		t.Errorf("stale job status = %q, want error", status)
	}
	fresh, _ := jobFromDB(t, m.Jobs(), "fresh")
	if status, _ := fresh["status"].(string); status != "downloading" {
		t.Errorf("fresh job status = %q, want downloading (still in grace)", status)
	}
}

func TestWatcherVanishDetection(t *testing.T) {
	qm := newQbitMock(t) // hash never present
	w, m := newTestWatcher(t, qm)
	hash := strings.Repeat("bb", 20)
	seedTorrentJob(t, m, "gone", map[string]interface{}{"torrent_hash": hash})

	w.tick()
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "gone")
	if status, _ := job["status"].(string); status != "downloading" {
		t.Fatalf("status after 2 ticks = %q, want downloading (grace)", status)
	}

	w.tick()
	job, _ = jobFromDB(t, m.Jobs(), "gone")
	if status, _ := job["status"].(string); status != "error" {
		t.Errorf("status after 3 ticks = %q, want error", status)
	}
	if errMsg, _ := job["error"].(string); !strings.Contains(errMsg, "removed") {
		t.Errorf("error = %q, want removed-externally message", errMsg)
	}
}

func TestWatcherImportsAndSeedSurvives(t *testing.T) {
	qm := newQbitMock(t)
	w, m := newTestWatcher(t, qm)
	hash := strings.Repeat("cc", 20)

	content := filepath.Join(t.TempDir(), "Seed Game (USA)")
	writeFileT(t, filepath.Join(content, "game.sfc"), []byte("rom-bytes"))
	writeFileT(t, filepath.Join(content, "extra", "manual.txt"), []byte("docs"))
	writeFileT(t, filepath.Join(content, "partial.bin.!qB"), []byte("scratch"))
	before := snapshotTree(t, content)

	seedTorrentJob(t, m, "imp1", map[string]interface{}{"torrent_hash": hash})
	qm.setFiles([]qbit.TorrentFile{{Name: "Seed Game (USA)/game.sfc"}})
	qm.setTorrents([]qbit.Torrent{{
		Name: "Seed Game (USA)", Hash: hash, Progress: 1.0,
		AmountLeft: 0, State: "uploading", ContentPath: content,
	}})

	w.tick()
	waitJobStatus(t, m.Jobs(), "imp1", "completed", 10*time.Second)
	// imported_at is the goroutine's last write; waiting on it makes every
	// assertion below race-free.
	waitFor(t, 10*time.Second, "imported_at", func() bool {
		j, _ := jobFromDB(t, m.Jobs(), "imp1")
		return int64Value(j["imported_at"]) > 0
	})

	dest := filepath.Join(w.cfg.GamesRomsPath, "snes", "Seed Game (USA)")
	if !pathExists(filepath.Join(dest, "game.sfc")) {
		t.Error("ROM not imported")
	}
	if pathExists(filepath.Join(dest, "partial.bin.!qB")) {
		t.Error("qBit scratch file copied into the library")
	}
	// The seeding payload must be byte-identical to its pre-import snapshot.
	after := snapshotTree(t, content)
	if len(after) != len(before) {
		t.Fatalf("payload tree changed: before=%d files after=%d", len(before), len(after))
	}
	for rel, want := range before {
		if after[rel] != want {
			t.Errorf("payload file %s modified", rel)
		}
	}
	if got := qm.deletedHashes(); len(got) != 0 {
		t.Errorf("torrent deleted: %v", got)
	}
}

func TestWatcherRemoveAfterImport(t *testing.T) {
	qm := newQbitMock(t)
	w, m := newTestWatcher(t, qm)
	w.cfg.RemoveAfterImport = true
	hash := strings.Repeat("dd", 20)

	content := filepath.Join(t.TempDir(), "Ratio Free Game")
	writeFileT(t, filepath.Join(content, "game.gba"), []byte("rom"))

	seedTorrentJob(t, m, "rm1", map[string]interface{}{
		"torrent_hash": hash, "platform": "Game Boy Advance", "platform_slug": "gba",
	})
	qm.setTorrents([]qbit.Torrent{{
		Name: "Ratio Free Game", Hash: hash, Progress: 1.0,
		AmountLeft: 0, State: "uploading", ContentPath: content,
	}})

	w.tick()
	waitJobStatus(t, m.Jobs(), "rm1", "completed", 10*time.Second)

	waitFor(t, 10*time.Second, "torrent deletion after import", func() bool {
		return len(qm.deletedHashes()) == 1
	})
	if got := qm.deletedHashes(); got[0] != hash {
		t.Errorf("deleted hash = %q, want %q", got[0], hash)
	}
}

func TestWatcherOrphanSweep(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "jobs.db")
	jobs1, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newTestConfig(t)
	cfg.QBURL = "configured"
	qm := newQbitMock(t)
	m1 := New(cfg, jobs1, qm.client())
	w1 := NewWatcher(cfg, m1)

	// Library hint: this title exists as a SNES game.
	if _, err := jobs1.AddLibraryItem(&db.LibraryItem{
		Title: "Super Metroid", Platform: "SNES", PlatformSlug: "snes",
		FilePath: "/old/path", FileSize: 1, Source: "romm", SourceType: "romm",
		SourceID: "romm:1", Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	hinted := strings.Repeat("11", 20)
	unknown := strings.Repeat("22", 20)
	qm.setTorrents([]qbit.Torrent{
		{Name: "Super Metroid", Hash: hinted, Progress: 1.0, AmountLeft: 0, State: "stoppedUP"},
		{Name: "Totally Mysterious Thing", Hash: unknown, Progress: 1.0, AmountLeft: 0, State: "uploading"},
		{Name: "Still Downloading", Hash: strings.Repeat("33", 20), Progress: 0.5, AmountLeft: 99, State: "downloading"},
	})

	countOrphans := func(jobs *db.JobStore) map[string]map[string]interface{} {
		out := map[string]map[string]interface{}{}
		for _, item := range jobs.Items() {
			if status, _ := item.Data["status"].(string); status == "completed_unorganized" {
				h, _ := item.Data["orphan_hash"].(string)
				out[h] = item.Data
			}
		}
		return out
	}

	// Two ticks: minted exactly once.
	w1.tick()
	w1.tick()
	orphans := countOrphans(jobs1)
	if len(orphans) != 2 {
		t.Fatalf("orphan jobs = %d, want 2 (completed torrents only)", len(orphans))
	}
	if platf, _ := orphans[hinted]["platform"].(string); platf != "SNES" {
		t.Errorf("hinted orphan platform = %q, want SNES from library", platf)
	}
	if platf, _ := orphans[unknown]["platform"].(string); platf != "Unknown" {
		t.Errorf("unhinted orphan platform = %q, want Unknown (never auto-PC)", platf)
	}

	// Simulated restart: a fresh store on the same DB must not re-mint.
	jobs1.Close()
	jobs2, err := db.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs2.Close()
	m2 := New(cfg, jobs2, qm.client())
	w2 := NewWatcher(cfg, m2)
	w2.tick()
	if got := countOrphans(jobs2); len(got) != 2 {
		t.Errorf("orphan jobs after restart = %d, want 2 (no re-mint)", len(got))
	}
}

func TestWatcherResumesImportAfterRestart(t *testing.T) {
	// A job persisted mid-import (status importing, no goroutine alive — the
	// process died) must be re-driven to completion by a tick.
	qm := newQbitMock(t)
	w, m := newTestWatcher(t, qm)
	hash := strings.Repeat("ee", 20)

	content := filepath.Join(t.TempDir(), "Interrupted Game")
	writeFileT(t, filepath.Join(content, "rom.sfc"), []byte("rom"))

	seedTorrentJob(t, m, "res1", map[string]interface{}{
		"torrent_hash": hash, "status": "importing", "file_scan_done": true,
	})
	qm.setTorrents([]qbit.Torrent{{
		Name: "Interrupted Game", Hash: hash, Progress: 1.0,
		AmountLeft: 0, State: "uploading", ContentPath: content,
	}})

	w.tick()
	waitJobStatus(t, m.Jobs(), "res1", "completed", 10*time.Second)
	if !pathExists(filepath.Join(w.cfg.GamesRomsPath, "snes", "Interrupted Game", "rom.sfc")) {
		t.Error("resumed import did not land")
	}
}

func TestWatcherJanitor(t *testing.T) {
	hash := func(b string) string { return strings.Repeat(b, 20) }
	mkTorrents := func() []qbit.Torrent {
		return []qbit.Torrent{
			{Name: "Harvest Me", Hash: hash("a1"), Progress: 1.0, AmountLeft: 0, State: "stoppedUP", Ratio: 2.0},
			{Name: "Still Seeding", Hash: hash("b2"), Progress: 1.0, AmountLeft: 0, State: "uploading", Ratio: 0.5},
			{Name: "Not Imported", Hash: hash("c3"), Progress: 1.0, AmountLeft: 0, State: "stoppedUP", Ratio: 3.0},
		}
	}
	seedJobs := func(m *Manager) {
		seedTorrentJob(t, m, "ja", map[string]interface{}{
			"status": "completed", "torrent_hash": hash("a1"), "imported_at": time.Now().Unix(),
		})
		seedTorrentJob(t, m, "jb", map[string]interface{}{
			"status": "completed", "torrent_hash": hash("b2"), "imported_at": time.Now().Unix(),
		})
		seedTorrentJob(t, m, "jc", map[string]interface{}{
			"status": "completed", "torrent_hash": hash("c3"),
		})
	}

	t.Run("disabled by default", func(t *testing.T) {
		qm := newQbitMock(t)
		w, m := newTestWatcher(t, qm)
		seedJobs(m)
		qm.setTorrents(mkTorrents())
		w.tick()
		if got := qm.deletedHashes(); len(got) != 0 {
			t.Errorf("janitor deleted %v while disabled", got)
		}
	})

	t.Run("harvests only imported and seed-complete", func(t *testing.T) {
		qm := newQbitMock(t)
		w, m := newTestWatcher(t, qm)
		w.cfg.SeedJanitorEnabled = true
		seedJobs(m)
		qm.setTorrents(mkTorrents())
		w.tick()
		got := qm.deletedHashes()
		if len(got) != 1 || got[0] != hash("a1") {
			t.Errorf("deleted = %v, want only the imported stoppedUP torrent", got)
		}
	})
}
