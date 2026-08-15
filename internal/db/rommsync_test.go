package db

import (
	"encoding/json"
	"strings"
	"testing"
)

func rommItem(id int, title, slug, path string, size int64) RommSyncItem {
	meta, _ := json.Marshal(map[string]interface{}{"romm": map[string]interface{}{
		"rom_id":     id,
		"search_key": strings.ToLower(title),
	}})
	return RommSyncItem{Item: LibraryItem{
		Title:        title,
		Platform:     strings.ToUpper(slug),
		PlatformSlug: slug,
		FilePath:     path,
		FileSize:     size,
		Source:       "romm",
		SourceType:   "romm",
		SourceID:     "romm:" + title,
		Metadata:     string(meta),
	}}
}

func TestSyncRommItemsInsertUpdateRemove(t *testing.T) {
	store := newTestStore(t)

	a := rommItem(1, "Alpha", "psx", "/roms/psx/Alpha.chd", 100)
	b := rommItem(2, "Beta", "genesis", "/roms/genesis-slash-megadrive/Beta.md", 200)
	added, updated, removed, err := store.SyncRommItems([]RommSyncItem{a, b}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 2 || updated != 0 || removed != 0 {
		t.Errorf("counts = %d/%d/%d, want 2/0/0", added, updated, removed)
	}

	// Second pull: Alpha grew, Beta gone (hard delete upstream).
	a.Item.FileSize = 150
	added, updated, removed, err = store.SyncRommItems([]RommSyncItem{a}, true)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if added != 0 || updated != 1 || removed != 1 {
		t.Errorf("counts = %d/%d/%d, want 0/1/1", added, updated, removed)
	}
	page := store.GetLibraryPage(1, 50, "", "")
	if page.Total != 1 || page.Items[0].FileSize != 150 {
		t.Errorf("unexpected library state: %+v", page)
	}
}

func TestSyncRommItemsAdoptsImportRows(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{
		Title: "American Pool", PlatformSlug: "psx",
		FilePath: "/roms/psx/American Pool (USA)", FileSize: 42,
		Source: "ddl", SourceType: "ddl", SourceID: "ddl:/roms/psx/American Pool (USA)",
		Metadata: "{}",
	})

	// RomM scans the same file later — must not double-count it.
	it := rommItem(9, "American Pool", "psx", "/roms/psx/American Pool (USA)", 42)
	added, _, _, err := store.SyncRommItems([]RommSyncItem{it}, false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 0 {
		t.Errorf("added=%d, want 0 (adopted)", added)
	}
	if total := store.LibraryTotal(); total != 1 {
		t.Errorf("total=%d, want 1", total)
	}
	// The import row survives untouched.
	if item := store.FindLibraryByTitle("American Pool", "psx"); item == nil || item.Source != "ddl" {
		t.Errorf("import row lost: %+v", item)
	}
}

func TestSyncRommItemsImportRowsSurviveReconcile(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{
		Title: "Fresh Grab", PlatformSlug: "psx", FilePath: "/roms/psx/Fresh Grab",
		Source: "manual", SourceType: "import", Metadata: "{}",
	})

	// Full reconcile with a catalog that does NOT contain the grab (RomM has
	// not scanned it yet) must leave the import row alone.
	_, _, removed, err := store.SyncRommItems([]RommSyncItem{
		rommItem(1, "Alpha", "psx", "/roms/psx/Alpha.chd", 100),
	}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0", removed)
	}
	if item := store.FindLibraryByTitle("Fresh Grab", "psx"); item == nil {
		t.Error("manual import row was removed by reconcile")
	}
}

func TestSyncRommItemsMissingFromFS(t *testing.T) {
	store := newTestStore(t)
	it := rommItem(5, "Ghost", "nes", "/roms/nes/Ghost.nes", 10)
	if _, _, _, err := store.SyncRommItems([]RommSyncItem{it}, false); err != nil {
		t.Fatal(err)
	}

	it.MissingFromFS = true
	_, _, removed, err := store.SyncRommItems([]RommSyncItem{it}, false)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || store.LibraryTotal() != 0 {
		t.Errorf("missing_from_fs row not removed: removed=%d total=%d", removed, store.LibraryTotal())
	}
}

// Regression for the first cutover sync in production: a legacy fs-scan row
// on the SAME path as an incoming rom must be displaced, not treated as an
// adoption owner — the original ordering adopted against 6,091 scan rows and
// then purged them, leaving those roms with no row at all.
func TestSyncRommItemsLegacyScanPathCollision(t *testing.T) {
	for _, full := range []bool{true, false} {
		store := newTestStore(t)
		store.AddLibraryItem(&LibraryItem{
			Title: "Old Scan Name", PlatformSlug: "nes", IsPC: false,
			FilePath: "/roms/nes/Duck Tales (USA).nes",
			Source:   "scan", SourceType: "scan", SourceID: "scan:/roms/nes/Duck Tales (USA).nes",
			Metadata: "{}",
		})

		it := rommItem(77, "DuckTales", "nes", "/roms/nes/Duck Tales (USA).nes", 99)
		added, _, removed, err := store.SyncRommItems([]RommSyncItem{it}, full)
		if err != nil {
			t.Fatalf("full=%v sync: %v", full, err)
		}
		if added != 1 {
			t.Errorf("full=%v added=%d, want 1 (scan row must not swallow the rom)", full, added)
		}
		if removed != 1 {
			t.Errorf("full=%v removed=%d, want 1 (the displaced scan row)", full, removed)
		}
		if total := store.LibraryTotal(); total != 1 {
			t.Errorf("full=%v total=%d, want 1", full, total)
		}
		item := store.FindLibraryByTitle("DuckTales", "nes")
		if item == nil || item.Source != "romm" {
			t.Errorf("full=%v rom row wrong: %+v", full, item)
		}
	}
}

func TestSyncRommItemsLegacyPurgeKeepsVaultRows(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{Title: "Old ROM scan", Source: "scan", IsPC: false, PlatformSlug: "nes", Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "Vault Game", Source: "scan", IsPC: true, Metadata: "{}"})

	if _, _, _, err := store.SyncRommItems(nil, true); err != nil {
		t.Fatal(err)
	}
	if item := store.FindLibraryByTitle("Vault Game", "pc"); item == nil {
		t.Error("vault scan row purged — is_pc discrimination broken")
	}
	if item := store.FindLibraryByTitle("Old ROM scan", "nes"); item != nil {
		t.Error("legacy ROM scan row survived full reconcile")
	}
}

func TestSyncRommItemsMergesMetadata(t *testing.T) {
	store := newTestStore(t)
	it := rommItem(7, "Enriched", "snes", "/roms/snes/Enriched.sfc", 10)
	if _, _, _, err := store.SyncRommItems([]RommSyncItem{it}, false); err != nil {
		t.Fatal(err)
	}

	// A RAWG enrich adds a sibling key…
	item := store.FindLibraryByTitle("Enriched", "snes")
	var meta map[string]json.RawMessage
	json.Unmarshal([]byte(item.Metadata), &meta)
	meta["rawg"] = json.RawMessage(`{"rating": 4.5}`)
	blob, _ := json.Marshal(meta)
	store.UpdateLibraryItemMetadata(item.ID, string(blob))

	// …and the next sync update must not clobber it.
	it.Item.FileSize = 20
	if _, _, _, err := store.SyncRommItems([]RommSyncItem{it}, false); err != nil {
		t.Fatal(err)
	}
	item = store.FindLibraryByTitle("Enriched", "snes")
	var after map[string]json.RawMessage
	json.Unmarshal([]byte(item.Metadata), &after)
	if _, ok := after["rawg"]; !ok {
		t.Errorf("sibling metadata key lost on sync update: %s", item.Metadata)
	}
	if _, ok := after["romm"]; !ok {
		t.Errorf("romm metadata key missing after update: %s", item.Metadata)
	}
	if item.FileSize != 20 {
		t.Errorf("file_size not updated: %d", item.FileSize)
	}
}

func TestLibraryHasFilePath(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{Title: "X", FilePath: "/roms/nes/X.nes", Source: "torrent", Metadata: "{}"})
	if !store.LibraryHasFilePath("/roms/nes/X.nes") {
		t.Error("existing path not found")
	}
	if store.LibraryHasFilePath("/roms/nes/Y.nes") {
		t.Error("phantom path found")
	}
	if store.LibraryHasFilePath("") {
		t.Error("empty path must not match")
	}
}

func TestLibraryPlatforms(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{Title: "A", PlatformSlug: "arcade", Platform: "Arcade", Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "B", PlatformSlug: "arcade", Platform: "Arcade", Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "C", PlatformSlug: "nes", Platform: "NES", Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "PC Game", IsPC: true, Metadata: "{}"})

	plats := store.LibraryPlatforms()
	if len(plats) != 2 {
		t.Fatalf("platforms=%d, want 2 (%+v)", len(plats), plats)
	}
	if plats[0].Slug != "arcade" || plats[1].Slug != "nes" {
		t.Errorf("unexpected order/content: %+v", plats)
	}
}

func TestClearVaultScanEntries(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{Title: "ROM scan", Source: "scan", IsPC: false, Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "Vault scan", Source: "scan", IsPC: true, Metadata: "{}"})
	store.AddLibraryItem(&LibraryItem{Title: "Grab", Source: "torrent", Metadata: "{}"})

	store.ClearVaultScanEntries()

	if store.LibraryTotal() != 2 {
		t.Errorf("total=%d, want 2 (only the vault scan row cleared)", store.LibraryTotal())
	}
	if item := store.FindLibraryByTitle("Vault scan", ""); item != nil {
		t.Error("vault scan row survived ClearVaultScanEntries")
	}
}

func TestFindLibraryByTitleSearchKeyFallback(t *testing.T) {
	store := newTestStore(t)
	meta, _ := json.Marshal(map[string]interface{}{"romm": map[string]interface{}{
		"rom_id": 1, "search_key": "castlevania - symphony of the night (usa)",
	}})
	store.AddLibraryItem(&LibraryItem{
		Title: "Castlevania: Symphony of the Night", PlatformSlug: "psx",
		Source: "romm", SourceID: "romm:1", Metadata: string(meta),
	})

	// Release-name shaped input: fs name + archive extension.
	item := store.FindLibraryByTitle("Castlevania - Symphony of the Night (USA).zip", "psx")
	if item == nil {
		t.Fatal("search-key fallback found nothing")
	}
	if item.Title != "Castlevania: Symphony of the Night" {
		t.Errorf("wrong item: %+v", item)
	}

	// Exact title still wins directly.
	if store.FindLibraryByTitle("Castlevania: Symphony of the Night", "psx") == nil {
		t.Error("exact title lookup broke")
	}
}

func TestGetAllLibraryTitlesSearchKeys(t *testing.T) {
	store := newTestStore(t)
	meta, _ := json.Marshal(map[string]interface{}{"romm": map[string]interface{}{
		"rom_id": 1, "search_key": "tetris plus (usa)",
	}})
	store.AddLibraryItem(&LibraryItem{
		Title: "Tetris Plus", PlatformSlug: "psx",
		Source: "romm", SourceID: "romm:1", Metadata: string(meta),
	})

	titles := store.GetAllLibraryTitles()
	if _, ok := titles["tetris plus|psx"]; !ok {
		t.Error("title key missing")
	}
	if _, ok := titles["tetris plus (usa)|psx"]; !ok {
		t.Error("search_key key missing")
	}
}

func TestNormalizeTitleKey(t *testing.T) {
	cases := map[string]string{
		"Castlevania (USA).zip":  "castlevania (usa)",
		"  Tetris Plus (USA) ":   "tetris plus (usa)",
		"Wipeout 2097":           "wipeout 2097", // no extension to strip
		"Game (v1.0)":            "game (v1.0)",  // parenthesised suffix is not an extension
		"Some.Game.With.Dots.7z": "some.game.with.dots",
	}
	for in, want := range cases {
		if got := NormalizeTitleKey(in); got != want {
			t.Errorf("NormalizeTitleKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncRommItemsAdoptMergesMetadata(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{
		Title: "Hagane", PlatformSlug: "snes",
		FilePath: "/roms/snes/Hagane (USA)", FileSize: 42,
		Source: "ddl", SourceType: "ddl", SourceID: "ddl:/roms/snes/hagane",
		Metadata: `{"gamarr":{"md5":"aa"}}`,
	})

	it := rommItem(7, "Hagane", "snes", "/roms/snes/Hagane (USA)", 42)
	added, updated, _, err := store.SyncRommItems([]RommSyncItem{it}, false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 0 || updated != 1 {
		t.Errorf("added/updated = %d/%d, want 0/1 (adopt-merge)", added, updated)
	}

	item := store.FindLibraryByTitle("Hagane", "snes")
	if item == nil {
		t.Fatal("adopted row not found")
	}
	if item.Source != "ddl" || item.SourceType != "ddl" || item.SourceID != "ddl:/roms/snes/hagane" {
		t.Errorf("adopt-merge must not touch source identity: %+v", item)
	}
	var meta map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &meta); err != nil {
		t.Fatalf("metadata not JSON: %v", err)
	}
	if meta["gamarr"]["md5"] != "aa" {
		t.Errorf("$.gamarr sibling lost in merge: %s", item.Metadata)
	}
	if meta["romm"]["rom_id"] != float64(7) {
		t.Errorf("$.romm not merged onto adopted row: %s", item.Metadata)
	}

	// Same pull again: metadata identical, so the merge is a no-op write.
	_, updated, _, err = store.SyncRommItems([]RommSyncItem{it}, false)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if updated != 0 {
		t.Errorf("updated=%d, want 0 (idempotent adopt-merge)", updated)
	}

	// Full reconcile without the item: the adopted row is not source='romm',
	// so the stale sweep must leave it (and its merged metadata) alone.
	_, _, removed, err := store.SyncRommItems([]RommSyncItem{}, true)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0", removed)
	}
	if item := store.FindLibraryByTitle("Hagane", "snes"); item == nil || item.Metadata == "{}" {
		t.Errorf("adopted row or merged metadata lost on reconcile: %+v", item)
	}
}
