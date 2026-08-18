package download

import (
	"encoding/hex"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gamarr/internal/db"
)

// catalogFixture writes a ROM payload and returns its path and real crc32, so
// the tests compare the catalogue against a hash they computed rather than one
// they hard-coded.
func catalogFixture(t *testing.T, name string, content []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := crc32.NewIEEE()
	sum.Write(content)
	return path, hex.EncodeToString(sum.Sum(nil))
}

func seedGBCatalog(t *testing.T, jobs *db.JobStore, romName, crc string) {
	t.Helper()
	_, err := jobs.InsertDatSnapshot(
		db.DatSnapshotMeta{Authority: "no-intro", PlatformSlug: "gb", Version: "test"},
		[]db.DatGameRow{{
			Name: "Tetris (World)", BareTitle: "Tetris", TotalSize: int64(len(romName)),
			Roms: []db.DatRomRow{{Name: romName, Size: 8, CRC: crc}},
		}},
	)
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
}

func gateJob(t *testing.T, m *Manager, url string) string {
	t.Helper()
	jobID := newJobID()
	m.Jobs().Set(jobID, map[string]interface{}{
		"status": "downloading", "title": "Tetris (World)", "platform_slug": "gb",
		"source_type": "ddl", "source_client": "ddl", "download_url": url,
	})
	return jobID
}

// The gate's job is to reject a file the catalogue CONTRADICTS — and to leave
// everything else alone.
func TestCatalogGate(t *testing.T) {
	t.Run("a catalogued hash imports and is recorded verified", func(t *testing.T) {
		cfg, jobs := newTestConfig(t), newTestJobs(t)
		m := New(cfg, jobs, nil)
		staging, crc := catalogFixture(t, "Tetris (World).gb", []byte("real-rom"))
		seedGBCatalog(t, jobs, "Tetris (World).gb", crc)
		jobID := gateJob(t, m, "https://example.test/tetris.zip")

		final, err := m.fulfillLocalROM(staging, fulfillMeta{
			JobID: jobID, Title: "Tetris (World)", Platform: "Game Boy", PlatformSlug: "gb",
			Source: "ddl", SourceClient: "ddl", SourceID: "ddl:tetris",
		})
		if err != nil {
			t.Fatalf("fulfillLocalROM: %v", err)
		}
		if !pathExists(final) {
			t.Fatal("verified import was removed")
		}
		item := jobs.FindLibraryBySourceID("ddl:tetris")
		if item == nil {
			t.Fatal("verified import produced no library row")
		}
		if !strings.Contains(item.Metadata, `"catalog":"`+db.CatalogVerified+`"`) {
			t.Errorf("metadata = %s, want the catalog verdict recorded", item.Metadata)
		}
	})

	t.Run("the catalogue disagreeing rejects the release", func(t *testing.T) {
		cfg, jobs := newTestConfig(t), newTestJobs(t)
		m := New(cfg, jobs, nil)
		staging, _ := catalogFixture(t, "Tetris (World).gb", []byte("corrupt-rom"))
		// Same file name, a different hash: this is the one thing that means
		// "the dump you fetched is not the dump that exists".
		seedGBCatalog(t, jobs, "Tetris (World).gb", "deadbeef")
		const url = "https://example.test/bad-tetris.zip"
		jobID := gateJob(t, m, url)

		final, err := m.fulfillLocalROM(staging, fulfillMeta{
			JobID: jobID, Title: "Tetris (World)", Platform: "Game Boy", PlatformSlug: "gb",
			Source: "ddl", SourceClient: "ddl", SourceID: "ddl:bad-tetris",
		})
		if err == nil {
			t.Fatal("a contradicted import must fail")
		}
		if pathExists(final) {
			t.Error("rejected content is still in the library tree")
		}
		if item := jobs.FindLibraryBySourceID("ddl:bad-tetris"); item != nil {
			t.Error("rejected content produced a library row")
		}
		if !jobs.IsBlocklisted(url, "") {
			t.Error("rejected release was not blocklisted; the selector can pick it again")
		}
		job, _ := jobs.Get(jobID)
		if status, _ := job["status"].(string); status != "error" {
			t.Errorf("job status = %q, want error", status)
		}
		if msg, _ := job["error"].(string); !strings.Contains(msg, "Catalog mismatch") ||
			!strings.Contains(msg, "deadbeef") {
			t.Errorf("error = %q, want the evidence for the rejection", msg)
		}
	})

	t.Run("an uncatalogued file imports and is recorded unknown", func(t *testing.T) {
		// Hacks, homebrew and dumps newer than the snapshot. On atari2600 the
		// uncatalogued pile is three times the catalogued one; rejecting it
		// would make most of the platform unacquirable.
		cfg, jobs := newTestConfig(t), newTestJobs(t)
		m := New(cfg, jobs, nil)
		staging, crc := catalogFixture(t, "Some Homebrew (2026).gb", []byte("homebrew"))
		seedGBCatalog(t, jobs, "Tetris (World).gb", crc+"00")
		jobID := gateJob(t, m, "https://example.test/homebrew.zip")

		final, err := m.fulfillLocalROM(staging, fulfillMeta{
			JobID: jobID, Title: "Some Homebrew (2026)", Platform: "Game Boy", PlatformSlug: "gb",
			Source: "ddl", SourceClient: "ddl", SourceID: "ddl:homebrew",
		})
		if err != nil {
			t.Fatalf("fulfillLocalROM: %v", err)
		}
		if !pathExists(final) {
			t.Fatal("an uncatalogued import must still land")
		}
		if jobs.GetBlocklist() != nil && len(jobs.GetBlocklist()) != 0 {
			t.Error("an uncatalogued file must not blocklist the release")
		}
		item := jobs.FindLibraryBySourceID("ddl:homebrew")
		if item == nil || !strings.Contains(item.Metadata, `"catalog":"`+db.CatalogUnknown+`"`) {
			t.Errorf("metadata = %v, want the unknown verdict recorded", item)
		}
	})

	t.Run("a platform with no catalogue is not gated", func(t *testing.T) {
		cfg, jobs := newTestConfig(t), newTestJobs(t)
		m := New(cfg, jobs, nil)
		staging, _ := catalogFixture(t, "PC Game.exe", []byte("whatever"))
		jobID := gateJob(t, m, "https://example.test/pc.zip")

		if _, err := m.fulfillLocalROM(staging, fulfillMeta{
			JobID: jobID, Title: "PC Game", Platform: "PC", PlatformSlug: "pc",
			Source: "ddl", SourceClient: "ddl", SourceID: "ddl:pc",
		}); err != nil {
			t.Fatalf("fulfillLocalROM: %v", err)
		}
		if len(jobs.GetBlocklist()) != 0 {
			t.Error("a platform with no catalogue must not blocklist anything")
		}
	})
}
