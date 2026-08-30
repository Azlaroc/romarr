package db

import (
	"encoding/json"
	"testing"
)

// Regression for the first cutover sync in production: a legacy fs-scan row
// on the SAME path as an incoming rom must be displaced, not treated as an
// adoption owner — the original ordering adopted against 6,091 scan rows and
// then purged them, leaving those roms with no row at all.

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
