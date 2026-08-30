package download

import (
	"path/filepath"
	"testing"

	"gamarr/internal/db"
)

// The boot scan is vault-only, unconditionally: whatever the RomM config
// says, it never walks the ROM tree and never clears a ROM row. The legacy
// version cleared every scan row and re-derived platform slugs from raw
// directory names; both behaviors are pinned dead here.
func TestScanLibraryDirsVaultOnly(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	writeFileT(t, filepath.Join(cfg.GamesVaultPath, "Vault Game.zip"), []byte("archive"))
	snes := filepath.Join(cfg.GamesRomsPath, "snes")
	writeFileT(t, filepath.Join(snes, "Mario World.sfc"), bigROM())

	// Rows from every ROM-side producer predate the rescan.
	jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Synced", PlatformSlug: "snes", Source: "romm", SourceType: "romm",
		SourceID: "romm:1", Metadata: "{}",
	})
	jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Scanner Row", PlatformSlug: "snes", Source: "libscan", SourceType: "libscan",
		SourceID: "libscan:x", Metadata: "{}",
	})
	jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Legacy ROM scan", PlatformSlug: "snes", Source: "scan", Metadata: "{}",
	})

	m.ScanLibraryDirs()

	if !jobs.LibraryHasSourceID("scan:" + filepath.Join(cfg.GamesVaultPath, "Vault Game.zip")) {
		t.Error("vault entry not scanned")
	}
	if jobs.LibraryHasSourceID("scan:" + filepath.Join(snes, "Mario World.sfc")) {
		t.Error("boot scan walked the ROM tree")
	}
	for _, title := range []string{"Synced", "Scanner Row", "Legacy ROM scan"} {
		if jobs.FindLibraryByTitle(title, "snes") == nil {
			t.Errorf("%s row cleared by vault-only rescan", title)
		}
	}
}

// A file already tracked by a download import must not be double-counted by
// a vault rescan under a scan: source id.
func TestScanSkipsImportTrackedPath(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	gamePath := filepath.Join(cfg.GamesVaultPath, "Grabbed Game.zip")
	writeFileT(t, gamePath, []byte("archive"))

	m.TrackInLibrary("Grabbed Game", "PC", "", true, gamePath, 42, "ddl", "manual", "ddl:abc", "", "", "")

	m.ScanLibraryDirs()

	if jobs.LibraryHasSourceID("scan:" + gamePath) {
		t.Error("import-tracked path re-added under scan: source id")
	}
	if total := jobs.LibraryTotal(); total != 1 {
		t.Errorf("library total = %d, want 1 (the import row only)", total)
	}
}
