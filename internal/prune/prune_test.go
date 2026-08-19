package prune

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/collectionsvc"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// env builds a runner over a real store and a real roms tree, so the apply
// pass moves actual files.
type env struct {
	runner *Runner
	store  *db.JobStore
	roms   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	roms := filepath.Join(dir, "roms")
	if err := os.MkdirAll(filepath.Join(roms, "atari7800"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := db.New(filepath.Join(dir, "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{DataDir: dir, GamesRomsPath: roms}
	cfg.AttachSettings(store)
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })

	return &env{runner: New(cfg, store, collectionsvc.New(cfg, store), nil), store: store, roms: roms}
}

func (e *env) rom(t *testing.T, slug, name string) string {
	t.Helper()
	path := filepath.Join(e.roms, slug, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rom bytes for "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *env) own(t *testing.T, slug, title, name, md5 string) int64 {
	t.Helper()
	path := e.rom(t, slug, name)
	id, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: title, PlatformSlug: slug, FilePath: path,
		Metadata: `{"romm":{"md5":"` + md5 + `"}}`, Source: "scan", SourceID: path,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func (e *env) catalog(t *testing.T, slug string, games []db.DatGameRow) {
	t.Helper()
	if _, err := e.store.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: slug, Version: "v1",
	}, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
}

func catGame(name, region, md5, flags string) db.DatGameRow {
	bare := name
	if i := strings.Index(name, " ("); i > 0 {
		bare = name[:i]
	}
	return db.DatGameRow{
		Name: name, BareTitle: bare, Region: region, Flags: flags, TotalSize: 1024,
		Roms: []db.DatRomRow{{Name: name + ".a78", Size: 1024, MD5: md5}},
	}
}

func (e *env) preview(t *testing.T, scope string, includeOut bool) []PreviewRow {
	t.Helper()
	if !e.runner.TriggerPreview(scope, includeOut) {
		t.Fatal("preview did not start")
	}
	e.wait(t, "preview")
	rows, _ := e.runner.PreviewPage(1, 500)
	return rows
}

func (e *env) apply(t *testing.T, exclude ...int64) {
	t.Helper()
	if !e.runner.TriggerApply(exclude) {
		t.Fatal("apply did not start")
	}
	e.wait(t, "apply")
}

func (e *env) wait(t *testing.T, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if e.runner.Status()["running"] == false {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not finish", what)
}

func rowFor(rows []PreviewRow, name string) (PreviewRow, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return PreviewRow{}, false
}

func TestSurplusIsPlannedAndTheKeeperIsNot(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa", ""),
		catGame("Ace of Aces (Europe)", "europe", "bbb", ""),
	})
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (USA).a78", "aaa")
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (Europe).a78", "bbb")

	rows := e.preview(t, "atari7800", false)
	if _, ok := rowFor(rows, "Ace of Aces (USA).a78"); ok {
		t.Error("the keeper appeared in the prune list")
	}
	surplus, ok := rowFor(rows, "Ace of Aces (Europe).a78")
	if !ok {
		t.Fatalf("surplus missing from the preview: %+v", rows)
	}
	if surplus.Verdict != VerdictArchive || surplus.Status != StatusPlanned {
		t.Errorf("verdict/status = %q/%q, want archive/planned", surplus.Verdict, surplus.Status)
	}
	if surplus.Keeper != "Ace of Aces (USA)" || surplus.MatchedBy != "hash" {
		t.Errorf("row = %+v, want it to name the keeper and the evidence", surplus)
	}
}

// 🔴 The rule that matters most: a surplus dump is only surplus when the dump
// the set actually keeps is on disk. Otherwise the copy you have IS the
// collection, whatever its region.
func TestNeverPruneTheOnlyCopy(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa", ""),
		catGame("Ace of Aces (Europe)", "europe", "bbb", ""),
	})
	// Only the European dump is owned; the USA keeper is a gap.
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (Europe).a78", "bbb")

	rows := e.preview(t, "atari7800", false)
	row, ok := rowFor(rows, "Ace of Aces (Europe).a78")
	if !ok {
		t.Fatalf("row missing: %+v", rows)
	}
	if row.Verdict != VerdictReview || row.Status == StatusPlanned {
		t.Fatalf("verdict/status = %q/%q, want a review that is never applied", row.Verdict, row.Status)
	}
	if !strings.Contains(row.Reason, "only copy") {
		t.Errorf("reason = %q, want it to say this is the only copy", row.Reason)
	}

	// And an apply must find nothing to do.
	if e.runner.TriggerApply(nil) {
		t.Error("apply started with nothing planned")
	}
	if _, err := os.Stat(filepath.Join(e.roms, "atari7800", "Ace of Aces (Europe).a78")); err != nil {
		t.Errorf("the only copy was moved: %v", err)
	}
}

// 🔴 The catalog's silence is not evidence. A file no catalog knows is
// reported and counted, never planned.
func TestUncataloguedIsReportedNeverPlanned(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{catGame("Ace of Aces (USA)", "usa", "aaa", "")})
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (USA).a78", "aaa")
	e.own(t, "atari7800", "Some Hack", "Totally Unknown Hack.a78", "zzz")

	rows := e.preview(t, "atari7800", false)
	row, ok := rowFor(rows, "Totally Unknown Hack.a78")
	if !ok {
		t.Fatalf("uncatalogued file missing from the report: %+v", rows)
	}
	if row.Verdict != VerdictUncatalogued || row.Status == StatusPlanned {
		t.Errorf("verdict/status = %q/%q, want it reported and never planned", row.Verdict, row.Status)
	}
	if got := e.runner.Status()["uncatalogued"]; got != 1 {
		t.Errorf("uncatalogued count = %v, want 1", got)
	}
	if got := e.runner.Status()["total"]; got != 0 {
		t.Errorf("planned total = %v, want 0 — nothing here is safe to archive", got)
	}
}

// A policy-excluded group is a different decision: archiving its dump removes a
// game rather than a duplicate, so it needs opt-in.
func TestExcludedGroupNeedsOptIn(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{catGame("Some Proto (USA)", "usa", "ppp", "proto")})
	e.own(t, "atari7800", "Some Proto", "Some Proto (USA).a78", "ppp")

	rows := e.preview(t, "atari7800", false)
	row, ok := rowFor(rows, "Some Proto (USA).a78")
	if !ok {
		t.Fatalf("row missing: %+v", rows)
	}
	if row.Verdict != VerdictExcludedGroup || row.Status == StatusPlanned {
		t.Errorf("verdict/status = %q/%q, want reported without opt-in", row.Verdict, row.Status)
	}

	rows = e.preview(t, "atari7800", true)
	row, _ = rowFor(rows, "Some Proto (USA).a78")
	if row.Status != StatusPlanned {
		t.Errorf("status with opt-in = %q, want planned", row.Status)
	}
}

func TestApplyArchivesMovesAndRecords(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa", ""),
		catGame("Ace of Aces (Europe)", "europe", "bbb", ""),
	})
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (USA).a78", "aaa")
	surplusID := e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (Europe).a78", "bbb")
	// A sidecar rides along, exactly as it does for a rename.
	sidecar := filepath.Join(e.roms, "atari7800", "Ace of Aces (Europe).a78.gamarr.json")
	if err := os.WriteFile(sidecar, []byte(`{"gamarr":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	e.preview(t, "atari7800", false)
	e.apply(t)

	dest := filepath.Join(e.roms, ".archive", "atari7800", "Ace of Aces (Europe).a78")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.roms, "atari7800", "Ace of Aces (Europe).a78")); !os.IsNotExist(err) {
		t.Error("the original is still in the library tree")
	}
	if _, err := os.Stat(dest + ".gamarr.json"); err != nil {
		t.Errorf("sidecar did not ride along: %v", err)
	}
	// The keeper is untouched.
	if _, err := os.Stat(filepath.Join(e.roms, "atari7800", "Ace of Aces (USA).a78")); err != nil {
		t.Errorf("the keeper was moved: %v", err)
	}
	// The library row goes with the file.
	rows := e.store.ListLibraryItemsForRename("atari7800")
	for _, r := range rows {
		if r.ID == surplusID {
			t.Error("the archived file still has a library row")
		}
	}

	// The manifest is what makes this reversible by hand.
	data, err := os.ReadFile(filepath.Join(e.roms, ".archive", "manifest.jsonl"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	var entry manifestEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("manifest line is not JSON: %v", err)
	}
	if entry.From == "" || entry.To != dest || entry.Keeper != "Ace of Aces (USA)" {
		t.Errorf("manifest entry = %+v, want where it came from, where it went, and what replaced it", entry)
	}
	if got := e.runner.Status()["archived"]; got != 1 {
		t.Errorf("archived = %v, want 1", got)
	}
}

func TestApplyHonoursExclusions(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa", ""),
		catGame("Ace of Aces (Europe)", "europe", "bbb", ""),
	})
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (USA).a78", "aaa")
	keepID := e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (Europe).a78", "bbb")

	e.preview(t, "atari7800", false)
	e.apply(t, keepID)

	if _, err := os.Stat(filepath.Join(e.roms, "atari7800", "Ace of Aces (Europe).a78")); err != nil {
		t.Errorf("an excluded row was archived anyway: %v", err)
	}
	if got := e.runner.Status()["archived"]; got != 0 {
		t.Errorf("archived = %v, want 0", got)
	}
}

func TestArchiveRootIsOverridable(t *testing.T) {
	e := newEnv(t)
	if got := e.runner.ArchiveRoot(); got != filepath.Join(e.roms, ".archive") {
		t.Errorf("default archive root = %q", got)
	}
	custom := filepath.Join(t.TempDir(), "elsewhere")
	if err := e.store.SetSetting("prune_archive_path", custom); err != nil {
		t.Fatal(err)
	}
	if got := e.runner.ArchiveRoot(); got != custom {
		t.Errorf("archive root = %q, want the operator's %q", got, custom)
	}
}

// A file that vanished between preview and apply is a skip with a reason, not
// a crash and not a silent success.
func TestApplySkipsAFileThatVanished(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa", ""),
		catGame("Ace of Aces (Europe)", "europe", "bbb", ""),
	})
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (USA).a78", "aaa")
	e.own(t, "atari7800", "Ace of Aces", "Ace of Aces (Europe).a78", "bbb")

	e.preview(t, "atari7800", false)
	if err := os.Remove(filepath.Join(e.roms, "atari7800", "Ace of Aces (Europe).a78")); err != nil {
		t.Fatal(err)
	}
	e.apply(t)

	rows, _ := e.runner.PreviewPage(1, 100)
	row, _ := rowFor(rows, "Ace of Aces (Europe).a78")
	if row.Status != StatusSkipped || !strings.Contains(row.Reason, "gone since the preview") {
		t.Errorf("row = %+v, want a skip that says the file vanished", row)
	}
}
