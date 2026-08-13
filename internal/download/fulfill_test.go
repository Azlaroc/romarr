package download

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gamarr/internal/romm"
)

// writeZipT creates a real zip archive at path with a single named entry.
func writeZipT(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFulfillLocalROMDirPayloadExtractsTopLevelArchives(t *testing.T) {
	// A directory payload (multi-file torrent) with extraction enabled: each
	// top-level archive unpacks into a clean game-named subdir (no
	// ".extracted" wrapper), the consumed archive is removed, loose files
	// survive, and the library row carries the real recursive size.
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not installed")
	}
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	m.SaveSettings(&Settings{ExtractArchives: true})

	staging := filepath.Join(t.TempDir(), "Pack Game (USA)")
	writeZipT(t, filepath.Join(staging, "Game (USA).zip"), "Game (USA).bin", []byte("disc-data"))
	writeFileT(t, filepath.Join(staging, "loose.sfc"), []byte("rom"))

	finalPath, err := m.fulfillLocalROM(staging, fulfillMeta{
		Title: "Pack Game (USA)", Platform: "SNES", PlatformSlug: "snes",
		Source: "torrent", SourceClient: "prowlarr", SourceID: "torrent:pack-hash",
	})
	if err != nil {
		t.Fatalf("fulfillLocalROM: %v", err)
	}

	dest := filepath.Join(cfg.GamesRomsPath, "snes", "Pack Game (USA)")
	if finalPath != dest {
		t.Errorf("finalPath = %q, want %q", finalPath, dest)
	}
	if pathExists(filepath.Join(dest, "Game (USA).zip")) {
		t.Error("consumed archive should be removed")
	}
	if !pathExists(filepath.Join(dest, "Game (USA)", "Game (USA).bin")) {
		t.Error("archive not extracted into a clean game dir")
	}
	if pathExists(filepath.Join(dest, "Game (USA).zip.extracted")) {
		t.Error("legacy .extracted wrapper must not appear")
	}
	if !pathExists(filepath.Join(dest, "loose.sfc")) {
		t.Error("loose file lost during extraction")
	}
	items := jobs.RecentLibraryItems(1)
	if len(items) != 1 {
		t.Fatalf("library rows = %d, want 1", len(items))
	}
	if items[0].FileSize <= 0 {
		t.Errorf("file_size = %d, want > 0", items[0].FileSize)
	}
	if items[0].SourceID != "torrent:pack-hash" {
		t.Errorf("source_id = %q", items[0].SourceID)
	}
}

func TestFulfillLocalROMReentryIsIdempotent(t *testing.T) {
	// Restart-recovery re-entry: calling fulfill again with the staging path
	// already AT the destination must skip the move, re-run the idempotent
	// stages, and not add a second library row (source_id dedupe).
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	staging := filepath.Join(t.TempDir(), "Game.sfc")
	writeFileT(t, staging, []byte("rom-data"))

	meta := fulfillMeta{
		Title: "Game", Platform: "SNES", PlatformSlug: "snes",
		Source: "nzb", SourceClient: "sabnzbd",
	}
	finalPath, err := m.fulfillLocalROM(staging, meta)
	if err != nil {
		t.Fatalf("first fulfill: %v", err)
	}

	again, err := m.fulfillLocalROM(finalPath, meta)
	if err != nil {
		t.Fatalf("re-entry fulfill: %v", err)
	}
	if again != finalPath {
		t.Errorf("re-entry path = %q, want %q", again, finalPath)
	}
	if !pathExists(finalPath) {
		t.Error("content vanished on re-entry")
	}
	if total := jobs.LibraryTotal(); total != 1 {
		t.Errorf("library rows = %d, want 1 (dedupe on source_id)", total)
	}
}

func TestFulfillLocalROMPathIdentityMatchesRommSync(t *testing.T) {
	// The anti-duplicate-row regression: the path fulfill writes into the
	// library must be byte-identical to what the RomM sync computes for the
	// same rom (romm.LocalPath), or the sync's adopt-on-path-collision check
	// misses and inserts a second row for the same file. Uses a slug with a
	// RomM fs_slug translation (genesis -> genesis-slash-megadrive) so the
	// seam is actually exercised.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)

	staging := filepath.Join(t.TempDir(), "Sonic (USA).md")
	writeFileT(t, staging, []byte("rom-data"))

	finalPath, err := m.fulfillLocalROM(staging, fulfillMeta{
		Title: "Sonic (USA)", Platform: "Genesis", PlatformSlug: "genesis",
		Source: "torrent", SourceClient: "prowlarr", SourceID: "torrent:sonic-hash",
	})
	if err != nil {
		t.Fatalf("fulfillLocalROM: %v", err)
	}

	rommView := romm.LocalPath(cfg.GamesRomsPath, &romm.Rom{
		PlatformFSSlug: "genesis-slash-megadrive",
		FSPath:         "roms/genesis-slash-megadrive",
		FSName:         "Sonic (USA).md",
	})
	if finalPath != rommView {
		t.Errorf("tracked path %q != romm.LocalPath %q", finalPath, rommView)
	}
	if !jobs.LibraryHasFilePath(rommView) {
		t.Error("library row not findable by the RomM sync's path-collision probe")
	}
}
