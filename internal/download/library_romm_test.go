package download

import (
	"path/filepath"
	"testing"

	"gamarr/internal/db"
)

// When the RomM sync is configured it owns the ROM side: the scanner walks
// only the vault and leaves ROM rows (romm's and legacy scan's) alone.
func TestScanLibraryDirsRommOwned(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.RomMURL = "http://romm:8080"
	cfg.RomMAPIUser = "romarr"
	cfg.RomMAPIPass = "pw"
	cfg.RomMSyncEnabled = true
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	writeFileT(t, filepath.Join(cfg.GamesVaultPath, "Vault Game.zip"), []byte("archive"))
	snes := filepath.Join(cfg.GamesRomsPath, "snes")
	writeFileT(t, filepath.Join(snes, "Mario World.sfc"), bigROM())

	// A romm-synced row and a legacy ROM scan row both predate the rescan.
	jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Synced", PlatformSlug: "snes", Source: "romm", SourceType: "romm",
		SourceID: "romm:1", Metadata: "{}",
	})
	jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Legacy ROM scan", PlatformSlug: "snes", Source: "scan", Metadata: "{}",
	})

	m.ScanLibraryDirs()

	if !jobs.LibraryHasSourceID("scan:" + filepath.Join(cfg.GamesVaultPath, "Vault Game.zip")) {
		t.Error("vault entry not scanned")
	}
	if jobs.LibraryHasSourceID("scan:" + filepath.Join(snes, "Mario World.sfc")) {
		t.Error("ROM directory scanned despite RomM owning the library")
	}
	if jobs.FindLibraryByTitle("Synced", "snes") == nil {
		t.Error("romm row cleared by vault rescan")
	}
	// Legacy ROM scan rows are the sync's to retire (full reconcile), not the
	// vault rescan's.
	if jobs.FindLibraryByTitle("Legacy ROM scan", "snes") == nil {
		t.Error("legacy ROM scan row cleared by vault-only rescan")
	}
}

// A file already tracked by a download import must not be double-counted by
// a rescan under a scan: source id.
func TestScanSkipsImportTrackedPath(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	snes := filepath.Join(cfg.GamesRomsPath, "snes")
	romPath := filepath.Join(snes, "Grabbed Game.sfc")
	writeFileT(t, romPath, bigROM())

	m.TrackInLibrary("Grabbed Game", "SNES", "snes", false, romPath, 42, "torrent", "prowlarr", "torrent:abc", "")

	m.ScanLibraryDirs()

	if jobs.LibraryHasSourceID("scan:" + romPath) {
		t.Error("import-tracked path re-added under scan: source id")
	}
	if total := jobs.LibraryTotal(); total != 1 {
		t.Errorf("library total = %d, want 1 (the import row only)", total)
	}
}
