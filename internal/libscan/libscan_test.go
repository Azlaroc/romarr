package libscan

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/platform"
	"gamarr/internal/romfile"
)

// The package's behaviour depends on the platform vocabulary, so the harness
// says what the vocabulary is — in TestMain, not per-test: a per-test
// cleanup would silently detach it for every later test in the file.
func TestMain(m *testing.M) {
	platform.SetRegistry(platform.StaticRegistry{
		{Slug: "nes", DisplayName: "NES", RommFSSlug: "nes"},
		{Slug: "genesis", DisplayName: "Genesis", RommFSSlug: "genesis-slash-megadrive"},
		{Slug: "switch", DisplayName: "Switch", RommFSSlug: "switch"},
	})
	os.Exit(m.Run())
}

type env struct {
	runner *Runner
	store  *db.JobStore
	roms   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	roms := filepath.Join(dir, "roms")
	if err := os.MkdirAll(roms, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := db.New(filepath.Join(dir, "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{DataDir: dir, GamesRomsPath: roms}
	cfg.AttachSettings(store)
	return &env{runner: New(cfg, store), store: store, roms: roms}
}

func (e *env) file(t *testing.T, rel string, body []byte) string {
	t.Helper()
	path := filepath.Join(e.roms, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *env) row(t *testing.T, slug, title, path, source, meta string) int64 {
	t.Helper()
	if meta == "" {
		meta = "{}"
	}
	id, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: title, Platform: slug, PlatformSlug: slug, FilePath: path,
		FileSize: 1, Source: source, SourceType: source,
		SourceID: source + ":" + title, Metadata: meta,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func (e *env) runSync(t *testing.T, scope string, opts Opts) map[string]interface{} {
	t.Helper()
	if !e.runner.Trigger(scope, opts) {
		t.Fatal("Trigger returned false with no run in flight")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := e.runner.Status()
		if running, _ := st["running"].(bool); !running {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) meta(t *testing.T, id int64) map[string]interface{} {
	t.Helper()
	item, err := e.store.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &out); err != nil {
		t.Fatalf("metadata not JSON: %v (%s)", err, item.Metadata)
	}
	return out
}

func (e *env) catalogOf(t *testing.T, id int64) string {
	t.Helper()
	g, _ := e.meta(t, id)["gamarr"].(map[string]interface{})
	if g == nil {
		return ""
	}
	s, _ := g["catalog"].(string)
	return s
}

func (e *env) rowsByStatus(status string) []Row {
	all, _ := e.runner.ResultsPage(1, 500)
	var out []Row
	for _, r := range all {
		if r.Status == status {
			out = append(out, r)
		}
	}
	return out
}

func hashesOf(body []byte) (crc, md5hex, sha1hex string) {
	c := crc32.ChecksumIEEE(body)
	m := md5.Sum(body)
	s := sha1.Sum(body)
	return fmt.Sprintf("%08x", c), hex.EncodeToString(m[:]), hex.EncodeToString(s[:])
}

func seedCatalog(t *testing.T, store *db.JobStore, slug string, entries ...[2]string) {
	t.Helper()
	var games []db.DatGameRow
	for _, e := range entries {
		games = append(games, db.DatGameRow{
			Name: e[0], BareTitle: e[0], TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: e[0] + ".nes", Size: 1024, CRC: e[1]}},
		})
	}
	meta := db.DatSnapshotMeta{Authority: "no-intro", PlatformSlug: slug, Version: "2026.08.01"}
	if _, err := store.InsertDatSnapshot(meta, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
}

func TestScanAdoptsCreatesReports(t *testing.T) {
	e := newEnv(t)

	// A file no row tracks, whose bytes the catalog knows.
	newBytes := []byte("brand new cartridge")
	newCRC, _, _ := hashesOf(newBytes)
	e.file(t, "nes/Fresh Arrival (USA).nes", newBytes)

	// A row with STORED $.gamarr hashes that match the catalog, over a file
	// whose bytes deliberately do NOT: a verified verdict proves the fast
	// path used the stored hashes and never re-read the file.
	storedBytes := []byte("the bytes the hashes were measured from")
	sCRC, sMD5, sSHA1 := hashesOf(storedBytes)
	adoptPath := e.file(t, "nes/Adopted (USA).nes", []byte("different bytes on disk"))
	adoptMeta := fmt.Sprintf(`{"gamarr":{"crc":%q,"md5":%q,"sha1":%q,"hashed_at":"2026-08-01T00:00:00Z"}}`, sCRC, sMD5, sSHA1)
	adoptID := e.row(t, "nes", "Adopted", adoptPath, "romm", adoptMeta)

	// A row whose file is gone.
	goneID := e.row(t, "nes", "Gone", filepath.Join(e.roms, "nes", "Gone (USA).nes"), "romm", "")

	// A directory the registry cannot name, and a file at the tree root.
	e.file(t, "junkdrawer/mystery.bin", []byte("x"))
	e.file(t, "stray.bin", []byte("y"))

	seedCatalog(t, e.store, "nes",
		[2]string{"Fresh Arrival (USA)", newCRC},
		[2]string{"Adopted (USA)", sCRC},
	)

	assertShape := func(t *testing.T, st map[string]interface{}) {
		t.Helper()
		if st["created"] != 1 || st["adopted"] != 1 || st["missing"] != 1 {
			t.Fatalf("status = %+v, want 1 created / 1 adopted / 1 missing", st)
		}
		if len(e.rowsByStatus(StatusUnknownPlatform)) != 1 {
			t.Error("junkdrawer/ not reported as unknown-platform")
		}
		if len(e.rowsByStatus(StatusUnsorted)) != 1 {
			t.Error("root-level file not reported as unsorted")
		}
		for _, r := range append(e.rowsByStatus(StatusCreated), e.rowsByStatus(StatusAdopted)...) {
			if r.Catalog != db.CatalogVerified {
				t.Errorf("%s: catalog = %q, want verified", r.Path, r.Catalog)
			}
		}
	}

	// Dry run: identical report, zero writes.
	st := e.runSync(t, "all", Opts{DryRun: true})
	assertShape(t, st)
	if total := e.store.LibraryTotal(); total != 2 {
		t.Fatalf("dry run changed the library: total=%d, want 2", total)
	}
	if got := e.catalogOf(t, adoptID); got != "" {
		t.Fatalf("dry run wrote a verdict: %q", got)
	}

	// Real run.
	st = e.runSync(t, "all", Opts{})
	assertShape(t, st)

	created := e.store.LibraryItemByFilePath(filepath.Join(e.roms, "nes", "Fresh Arrival (USA).nes"))
	if created == nil {
		t.Fatal("no row created for the out-of-band arrival")
	}
	if created.Source != "libscan" || created.SourceType != "libscan" {
		t.Errorf("created row source = %q/%q, want libscan — 'scan' rows are purged by the sync's reconcile", created.Source, created.SourceType)
	}
	if created.Title != "Fresh Arrival (USA)" || created.Platform != "NES" {
		t.Errorf("created row identity: %+v", created)
	}
	g, _ := e.meta(t, created.ID)["gamarr"].(map[string]interface{})
	if g == nil || g["crc"] != newCRC || g["catalog"] != db.CatalogVerified {
		t.Errorf("created row's hashes/verdict not persisted: %+v", g)
	}

	adopted, _ := e.store.GetLibraryItem(adoptID)
	if adopted.Source != "romm" {
		t.Errorf("adopted row source flipped to %q — adopted rows keep their source", adopted.Source)
	}
	if got := e.catalogOf(t, adoptID); got != db.CatalogVerified {
		t.Errorf("adopted row verdict = %q, want verified from STORED hashes", got)
	}

	if _, err := e.store.GetLibraryItem(goneID); err != nil {
		t.Fatal("missing-file row was deleted — the scanner never deletes")
	}
	if e.store.LibraryItemByFilePath(filepath.Join(e.roms, "junkdrawer", "mystery.bin")) != nil {
		t.Error("a row was minted under an unregistered platform dir")
	}
	if total := e.store.LibraryTotal(); total != 3 {
		t.Errorf("library total = %d, want 3", total)
	}

	// Second run: the created row is now adopted, verdicts are banked, and
	// nothing new is written.
	st = e.runSync(t, "all", Opts{})
	if st["created"] != 0 || st["adopted"] != 2 {
		t.Fatalf("re-run status = %+v, want 0 created / 2 adopted", st)
	}
	for _, r := range e.rowsByStatus(StatusAdopted) {
		if r.Catalog != "" {
			t.Errorf("%s: verdict re-recorded on a banked row", r.Path)
		}
	}
}

func TestScanSlugRepair(t *testing.T) {
	e := newEnv(t)

	// The registry knows 'genesis' whose directory is the IGDB-derived
	// alias. A row stranded on the DIRECTORY name as its slug is repaired.
	path := e.file(t, "genesis-slash-megadrive/Sonic (USA).md", []byte("ring"))
	brokenID := e.row(t, "genesis-slash-megadrive", "Sonic", path, "romm", "")

	// A registry-known slug is someone's decision, even under a directory
	// that names a different one.
	other := e.file(t, "genesis-slash-megadrive/Port (USA).md", []byte("port"))
	keptID := e.row(t, "switch", "Port", other, "romm", "")

	e.runSync(t, "all", Opts{})

	repaired, _ := e.store.GetLibraryItem(brokenID)
	if repaired.PlatformSlug != "genesis" || repaired.Platform != "Genesis" {
		t.Errorf("slug not repaired: %q/%q", repaired.PlatformSlug, repaired.Platform)
	}
	kept, _ := e.store.GetLibraryItem(keptID)
	if kept.PlatformSlug != "switch" {
		t.Errorf("registry-known slug rewritten to %q", kept.PlatformSlug)
	}
}

func TestScanNeverDowngradesBankedVerdict(t *testing.T) {
	e := newEnv(t)

	// A converted-format shape: the verdict was banked pre-conversion, the
	// bytes on disk can no longer match anything, and there are no stored
	// hashes to answer from.
	path := e.file(t, "nes/Converted (USA).nes", []byte("post-conversion bytes"))
	id := e.row(t, "nes", "Converted", path, "romm", `{"gamarr":{"catalog":"verified"}}`)

	e.runSync(t, "all", Opts{})
	if got := e.catalogOf(t, id); got != db.CatalogVerified {
		t.Fatalf("banked verdict downgraded to %q", got)
	}

	// Force re-measures — and still refuses the downgrade.
	e.runSync(t, "all", Opts{Force: true})
	if got := e.catalogOf(t, id); got != db.CatalogVerified {
		t.Fatalf("Force downgraded a banked verdict to %q", got)
	}
}

func TestScanGameDirAndNestedRows(t *testing.T) {
	e := newEnv(t)

	// A directory that directly holds game files is ONE entry.
	e.file(t, "nes/Disc Set/a.nes", []byte("aa"))
	e.file(t, "nes/Disc Set/b.nes", []byte("bb"))
	// A row tracking a file INSIDE it is accounted for, not missing.
	innerID := e.row(t, "nes", "Inner", filepath.Join(e.roms, "nes", "Disc Set", "a.nes"), "ddl", "")

	st := e.runSync(t, "all", Opts{})
	if st["created"] != 1 {
		t.Fatalf("status = %+v, want the dir as one created entry", st)
	}
	dirRow := e.store.LibraryItemByFilePath(filepath.Join(e.roms, "nes", "Disc Set"))
	if dirRow == nil {
		t.Fatal("no row for the game directory")
	}
	if got := e.catalogOf(t, dirRow.ID); got != db.CatalogUnknown {
		t.Errorf("dir verdict = %q, want unknown", got)
	}
	if st["missing"] != 0 || st["unvisited"] != 0 {
		t.Errorf("nested row misread: %+v", st)
	}
	if _, err := e.store.GetLibraryItem(innerID); err != nil {
		t.Error("nested row deleted")
	}
}

func TestScanReportsUnvisited(t *testing.T) {
	e := newEnv(t)

	// A row tracking a file the walk deliberately skips (a sidecar): the
	// enumeration gap must be LOUD, not silent.
	path := e.file(t, "nes/playlist.m3u", []byte("#"))
	e.row(t, "nes", "Playlist", path, "romm", "")

	st := e.runSync(t, "all", Opts{})
	if st["unvisited"] != 1 {
		t.Fatalf("status = %+v, want 1 unvisited", st)
	}
}

// 🔴 A stored hash_skipped marker is a measurement that already happened:
// the scan must not re-extract the archive to re-learn it. Pinned by
// stubbing the extractor to prove it is never invoked.
func TestScanHonorsHashSkipMarker(t *testing.T) {
	e := newEnv(t)

	path := e.file(t, "nes/Pack (USA).zip", []byte("PK\x03\x04 pretend archive"))
	id := e.row(t, "nes", "Pack", path, "romm", `{"gamarr":{"hash_skipped":"multi-file-archive"}}`)

	extractorCalled := false
	orig := romfile.Exec7z
	romfile.Exec7z = func(ctx context.Context, archive, destDir string) *exec.Cmd {
		extractorCalled = true
		return orig(ctx, archive, destDir)
	}
	t.Cleanup(func() { romfile.Exec7z = orig })

	st := e.runSync(t, "all", Opts{})
	if st["adopted"] != 1 {
		t.Fatalf("status = %+v", st)
	}
	if extractorCalled {
		t.Fatal("scan re-extracted an entry the backfill already classified")
	}
	if got := e.catalogOf(t, id); got != db.CatalogUnknown {
		t.Errorf("marked row verdict = %q, want unknown", got)
	}
}

// A created row with no single-ROM identity gets the same permanent marker
// the hash backfill writes, so no later sweep re-measures it.
func TestScanMarksCreatedDirRows(t *testing.T) {
	e := newEnv(t)
	e.file(t, "nes/Boxed Set/a.nes", []byte("aa"))

	e.runSync(t, "all", Opts{})
	row := e.store.LibraryItemByFilePath(filepath.Join(e.roms, "nes", "Boxed Set"))
	if row == nil {
		t.Fatal("dir row not created")
	}
	if got := db.ParseHashSkip(row.Metadata); got != db.HashSkipDirectory {
		t.Errorf("hash_skipped = %q, want %q", got, db.HashSkipDirectory)
	}
}

func TestScanScopedToPlatform(t *testing.T) {
	e := newEnv(t)
	e.file(t, "nes/One (USA).nes", []byte("one"))
	e.file(t, "genesis-slash-megadrive/Two (USA).md", []byte("two"))
	// A gone-file row on the OTHER platform must not be reported by a
	// nes-scoped scan.
	e.row(t, "genesis", "Gone", filepath.Join(e.roms, "genesis-slash-megadrive", "Gone.md"), "romm", "")

	st := e.runSync(t, "nes", Opts{})
	if st["created"] != 1 || st["missing"] != 0 {
		t.Fatalf("scoped status = %+v, want 1 created / 0 missing", st)
	}
	if e.store.LibraryItemByFilePath(filepath.Join(e.roms, "genesis-slash-megadrive", "Two (USA).md")) != nil {
		t.Error("scoped scan created a row on another platform")
	}
}
