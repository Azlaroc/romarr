package retitle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/db"
)

type env struct {
	runner *Runner
	store  *db.JobStore
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := db.New(filepath.Join(dir, "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &env{runner: New(store), store: store}
}

func (e *env) own(t *testing.T, title, name, md5 string) int64 {
	t.Helper()
	id, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: title, PlatformSlug: "gb", FilePath: "/roms/gb/" + name,
		Metadata: `{"romm":{"md5":"` + md5 + `"}}`, Source: "scan", SourceID: "/roms/gb/" + name,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func (e *env) catalog(t *testing.T, gameName, romName, md5 string) {
	t.Helper()
	bare := gameName
	if i := strings.Index(gameName, " ("); i > 0 {
		bare = gameName[:i]
	}
	if _, err := e.store.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "gb", Version: "v1",
	}, []db.DatGameRow{{
		Name: gameName, BareTitle: bare, Region: "world", TotalSize: 1024,
		Roms: []db.DatRomRow{{Name: romName, Size: 1024, MD5: md5}},
	}}); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
}

func (e *env) preview(t *testing.T) []PreviewRow {
	t.Helper()
	if !e.runner.TriggerPreview() {
		t.Fatal("preview did not start")
	}
	e.wait(t)
	rows, _ := e.runner.PreviewPage(1, 500)
	return rows
}

func (e *env) apply(t *testing.T, exclude ...int64) {
	t.Helper()
	if !e.runner.TriggerApply(exclude) {
		t.Fatal("apply did not start")
	}
	e.wait(t)
}

func (e *env) wait(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := e.runner.Status()
		switch st["state"] {
		case "done":
			return
		case "error":
			t.Fatalf("runner error: %v", st["error"])
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("runner did not finish")
}

func rowFor(rows []PreviewRow, id int64) (PreviewRow, bool) {
	for _, r := range rows {
		if r.LibraryID == id {
			return r, true
		}
	}
	return PreviewRow{}, false
}

// The Tetris shape: a katakana display title whose file the catalog names.
// The catalog's FULL game name wins; the stem never fires when a hash
// resolves — and the decision keyed on the catalog, not on character class.
func TestPreviewCatalogNameWinsOverStem(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "Tetris (World) (Rev 1)", "Tetris (World) (Rev 1).gb", "aaa111")
	id := e.own(t, "テトリス", "Tetris (World) (Rev 1).zip", "aaa111")

	rows := e.preview(t)
	row, ok := rowFor(rows, id)
	if !ok {
		t.Fatalf("row missing: %+v", rows)
	}
	if row.Status != StatusRetitle || row.Source != SourceDat || row.New != "Tetris (World) (Rev 1)" {
		t.Errorf("row = %+v, want a dat-sourced retitle to the full game name", row)
	}
}

func TestPreviewStemFallbackAndNoop(t *testing.T) {
	e := newEnv(t)
	// Unhashed row: falls back to the file-name stem.
	unhashedID, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: "weird internal name", PlatformSlug: "gb",
		FilePath: "/roms/gb/Some Game (USA).zip", Metadata: "{}",
		Source: "scan", SourceID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Already canonical: noop.
	noopID := e.own(t, "Kirby's Dream Land (USA, Europe)", "Kirby's Dream Land (USA, Europe).zip", "bbb222")
	// PC rows are out of scope entirely.
	pcID, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: "Doom", PlatformSlug: "pc", IsPC: true,
		FilePath: "/vault/Doom", Metadata: "{}", Source: "scan", SourceID: "s2",
	})
	if err != nil {
		t.Fatal(err)
	}

	rows := e.preview(t)
	if row, ok := rowFor(rows, unhashedID); !ok || row.Status != StatusRetitle ||
		row.Source != SourceStem || row.New != "Some Game (USA)" {
		t.Errorf("unhashed row = %+v, want stem fallback", row)
	}
	if row, ok := rowFor(rows, noopID); !ok || row.Status != StatusNoop {
		t.Errorf("canonical row = %+v, want noop", row)
	}
	if _, ok := rowFor(rows, pcID); ok {
		t.Error("PC row leaked into the retitle worklist")
	}
}

// Apply writes the title, the revert breadcrumb, and follows the wishlist —
// pins ride untouched. Revert undoes all three.
func TestApplyFollowsWishlistAndRevertUndoes(t *testing.T) {
	e := newEnv(t)
	e.catalog(t, "Tetris (World) (Rev 1)", "Tetris (World) (Rev 1).gb", "aaa111")
	id := e.own(t, "テトリス", "Tetris (World) (Rev 1).zip", "aaa111")
	if _, err := e.store.UpsertWishlistOverride("テトリス", "Game Boy", "gb",
		"Tetris (World) (Rev 1)", []string{"aaa111"}); err != nil {
		t.Fatal(err)
	}

	e.preview(t)
	e.apply(t)

	item, err := e.store.GetLibraryItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "Tetris (World) (Rev 1)" {
		t.Errorf("title = %q, want the catalog name", item.Title)
	}
	if !strings.Contains(item.Metadata, `"retitle"`) || !strings.Contains(item.Metadata, "テトリス") {
		t.Errorf("metadata = %q, want the retitle.from breadcrumb", item.Metadata)
	}
	w, ok := e.store.GetWishlistOverrideForTitle("Tetris (World) (Rev 1)", "gb")
	if !ok {
		t.Fatal("wishlist row did not follow the retitle")
	}
	if w.OverrideDumpName != "Tetris (World) (Rev 1)" || len(w.OverrideHashes) != 1 {
		t.Errorf("pin did not ride: %+v", w)
	}

	n, err := e.runner.Revert()
	if err != nil || n != 1 {
		t.Fatalf("revert = %d, %v", n, err)
	}
	item, _ = e.store.GetLibraryItem(id)
	if item.Title != "テトリス" || strings.Contains(item.Metadata, `"retitle"`) {
		t.Errorf("revert left title=%q metadata=%q", item.Title, item.Metadata)
	}
	if _, ok := e.store.GetWishlistOverrideForTitle("テトリス", "gb"); !ok {
		t.Error("wishlist row did not follow the revert")
	}
}

// Two rows sharing an old title but diverging on the new one retitle, but
// the wishlist follow is suppressed — a silent last-writer-wins on the
// wishlist is worse than an unfollowed row.
func TestSharedOldTitleSuppressesWishlistFollow(t *testing.T) {
	e := newEnv(t)
	id1, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: "Twin", PlatformSlug: "gb", FilePath: "/roms/gb/Twin A (USA).zip",
		Metadata: "{}", Source: "scan", SourceID: "t1",
	})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: "Twin", PlatformSlug: "gb", FilePath: "/roms/gb/Twin B (USA).zip",
		Metadata: "{}", Source: "scan", SourceID: "t2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.AddWishlistItem("Twin", "Game Boy", "gb"); err != nil {
		t.Fatal(err)
	}

	e.preview(t)
	e.apply(t)

	for _, id := range []int64{id1, id2} {
		item, _ := e.store.GetLibraryItem(id)
		if item.Title == "Twin" {
			t.Errorf("row %d kept its old title", id)
		}
	}
	wl := e.store.GetWishlist()
	if len(wl) != 1 || wl[0].Title != "Twin" {
		t.Errorf("wishlist = %+v, want the row unfollowed (suppressed)", wl)
	}
}
