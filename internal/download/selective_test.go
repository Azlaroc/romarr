package download

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/qbit"
)

// packFixture registers a complete-and-seeding 3-file pack torrent plus its
// on-disk payload, returning the content dir. Target for tests is B.sfc (idx 1).
func packFixture(t *testing.T, qm *qbitMock, hash string) string {
	t.Helper()
	content := filepath.Join(t.TempDir(), "Pack Vol 1")
	for _, fn := range []string{"A.sfc", "B.sfc", "C.sfc"} {
		writeFileT(t, filepath.Join(content, fn), []byte("rom-"+fn))
	}
	qm.setFiles([]qbit.TorrentFile{
		{Index: 0, Name: "Pack Vol 1/A.sfc", Size: 9, Priority: 1, Progress: 1.0},
		{Index: 1, Name: "Pack Vol 1/B.sfc", Size: 9, Priority: 1, Progress: 1.0},
		{Index: 2, Name: "Pack Vol 1/C.sfc", Size: 9, Priority: 1, Progress: 1.0},
	})
	qm.setTorrents([]qbit.Torrent{{
		Name: "Pack Vol 1", Hash: hash, Progress: 1.0, State: "uploading",
		SavePath: filepath.Dir(content), ContentPath: content,
	}})
	return content
}

func TestSelectiveDownloadPlucksTarget(t *testing.T) {
	hash := strings.Repeat("11", 20)
	qm := newQbitMock(t)
	content := packFixture(t, qm, hash)
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "job-pluck", map[string]interface{}{
		"torrent_hash": hash,
		"target_file":  "Pack Vol 1/B.sfc", // exact in-torrent path
	})

	// Tick 1: selection only — no import may fire before the prio-0 pass.
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "job-pluck")
	if done, _ := job["selection_done"].(bool); !done {
		t.Fatal("selection_done not persisted")
	}
	if got, _ := job["target_file_resolved"].(string); got != "Pack Vol 1/B.sfc" {
		t.Fatalf("target_file_resolved = %q", got)
	}
	if idx := int64Value(job["target_index"]); idx != 1 {
		t.Fatalf("target_index = %d, want 1", idx)
	}
	calls := qm.filePrioCalls()
	if len(calls) != 1 {
		t.Fatalf("filePrio calls = %d, want 1", len(calls))
	}
	if got := calls[0].Get("id"); got != "0|2" && got != "2|0" {
		t.Errorf("filePrio ids = %q, want 0|2", got)
	}
	if got := calls[0].Get("priority"); got != "0" {
		t.Errorf("filePrio priority = %q, want 0", got)
	}
	if len(qm.startedHashes()) == 0 {
		t.Error("torrent not (re)started after selection")
	}

	// Tick 2: completion gate passes (target at 100%) — import runs and copies
	// ONLY the target.
	w.tick()
	waitFor(t, 10*time.Second, "imported_at", func() bool {
		j, _ := jobFromDB(t, m.Jobs(), "job-pluck")
		return int64Value(j["imported_at"]) > 0
	})

	dest := filepath.Join(m.cfg.GamesRomsPath, "snes", "B.sfc")
	if !pathExists(dest) {
		t.Errorf("plucked file not imported to %s", dest)
	}
	for _, other := range []string{"A.sfc", "C.sfc", "Pack Vol 1"} {
		if pathExists(filepath.Join(m.cfg.GamesRomsPath, "snes", other)) {
			t.Errorf("non-target %q leaked into the library", other)
		}
	}
	// Pack payload untouched (import copied the single file).
	for _, fn := range []string{"A.sfc", "B.sfc", "C.sfc"} {
		if !pathExists(filepath.Join(content, fn)) {
			t.Errorf("pack payload file %q vanished", fn)
		}
	}
	if got := qm.deletedHashes(); len(got) != 0 {
		t.Errorf("pack torrent deleted: %v", got)
	}
}

func TestSelectiveDownloadBasenameMatch(t *testing.T) {
	hash := strings.Repeat("22", 20)
	qm := newQbitMock(t)
	packFixture(t, qm, hash)
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "job-base", map[string]interface{}{
		"torrent_hash": hash,
		"target_file":  "b.SFC", // bare name, wrong case
	})
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "job-base")
	if got, _ := job["target_file_resolved"].(string); got != "Pack Vol 1/B.sfc" {
		t.Errorf("target_file_resolved = %q, want basename match on B.sfc", got)
	}
}

func TestSelectiveDownloadMissFallsBackToWholePack(t *testing.T) {
	hash := strings.Repeat("33", 20)
	qm := newQbitMock(t)
	packFixture(t, qm, hash)
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "job-miss", map[string]interface{}{
		"torrent_hash": hash,
		"target_file":  "Not In This Pack.sfc",
	})
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "job-miss")
	if got, _ := job["target_file"].(string); got != "" {
		t.Errorf("target_file = %q, want cleared on miss", got)
	}
	if done, _ := job["selection_done"].(bool); !done {
		t.Error("selection_done not set on miss")
	}
	if len(qm.filePrioCalls()) != 0 {
		t.Error("filePrio must not run on a miss")
	}
	if len(qm.startedHashes()) == 0 {
		t.Error("torrent not started after fallback")
	}

	// Next tick: whole pack imports as one game dir.
	w.tick()
	waitFor(t, 10*time.Second, "whole-pack import", func() bool {
		j, _ := jobFromDB(t, m.Jobs(), "job-miss")
		return int64Value(j["imported_at"]) > 0
	})
	if !pathExists(filepath.Join(m.cfg.GamesRomsPath, "snes", "Pack Vol 1", "B.sfc")) {
		t.Error("whole-pack fallback did not import the pack dir")
	}
}

func TestSelectiveDownloadFilePrioRejectedFallsBack(t *testing.T) {
	hash := strings.Repeat("44", 20)
	qm := newQbitMock(t)
	packFixture(t, qm, hash)
	qm.mu.Lock()
	qm.prioOK = false
	qm.mu.Unlock()
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "job-prio", map[string]interface{}{
		"torrent_hash": hash,
		"target_file":  "Pack Vol 1/B.sfc",
	})
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "job-prio")
	if got, _ := job["target_file"].(string); got != "" {
		t.Errorf("target_file = %q, want cleared when filePrio is rejected", got)
	}
	if resolved, _ := job["target_file_resolved"].(string); resolved != "" {
		t.Errorf("target_file_resolved = %q, want empty on fallback", resolved)
	}
}

func TestSelectiveDownloadWaitsForTargetProgress(t *testing.T) {
	hash := strings.Repeat("55", 20)
	qm := newQbitMock(t)
	packFixture(t, qm, hash)
	w, m := newTestWatcher(t, qm)

	seedTorrentJob(t, m, "job-wait", map[string]interface{}{
		"torrent_hash": hash,
		"target_file":  "Pack Vol 1/B.sfc",
	})
	w.tick() // selection

	// Torrent-level gate says done (amount_left excludes prio-0 files), but the
	// target itself is mid-download: the belt-and-braces check must hold import.
	qm.setFiles([]qbit.TorrentFile{
		{Index: 0, Name: "Pack Vol 1/A.sfc", Priority: 0, Progress: 0},
		{Index: 1, Name: "Pack Vol 1/B.sfc", Priority: 1, Progress: 0.5},
		{Index: 2, Name: "Pack Vol 1/C.sfc", Priority: 0, Progress: 0},
	})
	w.tick()
	job, _ := jobFromDB(t, m.Jobs(), "job-wait")
	if status, _ := job["status"].(string); status != "downloading" {
		t.Fatalf("status = %q, want still downloading while target incomplete", status)
	}

	qm.setFiles([]qbit.TorrentFile{
		{Index: 0, Name: "Pack Vol 1/A.sfc", Priority: 0, Progress: 0},
		{Index: 1, Name: "Pack Vol 1/B.sfc", Priority: 1, Progress: 1.0},
		{Index: 2, Name: "Pack Vol 1/C.sfc", Priority: 0, Progress: 0},
	})
	w.tick()
	waitFor(t, 10*time.Second, "import after target complete", func() bool {
		j, _ := jobFromDB(t, m.Jobs(), "job-wait")
		return int64Value(j["imported_at"]) > 0
	})
}

func TestJanitorSkipsPluckedPacks(t *testing.T) {
	packHash := strings.Repeat("66", 20)
	plainHash := strings.Repeat("77", 20)
	qm := newQbitMock(t)
	w, m := newTestWatcher(t, qm)
	w.cfg.SeedJanitorEnabled = true

	qm.setTorrents([]qbit.Torrent{
		{Name: "Plucked Pack", Hash: packHash, Progress: 1.0, State: "stoppedUP"},
		{Name: "Plain Grab", Hash: plainHash, Progress: 1.0, State: "stoppedUP"},
	})
	seedTorrentJob(t, m, "job-packed", map[string]interface{}{
		"status": "completed", "torrent_hash": packHash,
		"target_file_resolved": "Plucked Pack/B.sfc", "target_index": 1,
		"selection_done": true, "imported_at": time.Now().Unix(),
	})
	seedTorrentJob(t, m, "job-plain", map[string]interface{}{
		"status": "completed", "torrent_hash": plainHash,
		"imported_at": time.Now().Unix(),
	})

	w.tick()
	deleted := qm.deletedHashes()
	if len(deleted) != 1 || !strings.Contains(deleted[0], plainHash) {
		t.Fatalf("deleted = %v, want only the plain grab harvested", deleted)
	}
}
