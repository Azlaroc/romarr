package db

import (
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

func TestFindLibraryByTitleFsNameFallback(t *testing.T) {
	store := newTestStore(t)
	// No $.romm.search_key: the fallback keys off file_path, which every row
	// has — the retired sync's stash only existed on rows it had touched.
	store.AddLibraryItem(&LibraryItem{
		Title: "Castlevania: Symphony of the Night", PlatformSlug: "psx",
		FilePath: "/roms/psx/Castlevania - Symphony of the Night (USA).chd",
		Source:   "romm", SourceID: "romm:1", Metadata: "{}",
	})

	// Release-name shaped input: fs name + a DIFFERENT archive extension than
	// the on-disk file carries.
	item := store.FindLibraryByTitle("Castlevania - Symphony of the Night (USA).zip", "psx")
	if item == nil {
		t.Fatal("fs-name fallback found nothing")
	}
	if item.Title != "Castlevania: Symphony of the Night" {
		t.Errorf("wrong item: %+v", item)
	}

	// Exact title still wins directly.
	if store.FindLibraryByTitle("Castlevania: Symphony of the Night", "psx") == nil {
		t.Error("exact title lookup broke")
	}
}

func TestGetAllLibraryTitlesIsTitlePure(t *testing.T) {
	store := newTestStore(t)
	store.AddLibraryItem(&LibraryItem{
		Title: "Tetris Plus", PlatformSlug: "psx",
		FilePath: "/roms/psx/Tetris Plus (USA).zip",
		Source:   "romm", SourceID: "romm:1", Metadata: "{}",
	})

	titles := store.GetAllLibraryTitles()
	if _, ok := titles["tetris plus|psx"]; !ok {
		t.Error("title key missing")
	}
	// File names must NOT appear here: the set engine consumes this map as
	// its TITLE tier, and a file name's claim there outranks its real
	// precedence (a hack named like a game would cover the game's set slot).
	// Release-name ownership lives in the scheduler's owned index and the
	// name index instead.
	if _, ok := titles["tetris plus (usa)|psx"]; ok {
		t.Error("fs-name key leaked into the title map")
	}
}

// Pin CRUD matches its row case-insensitively, like DeleteWishlistByTitle
// always has: an upsert against "TETRIS" gains the pin on the "Tetris" row
// rather than minting a case-twin, and clear/get find it whatever the case.
func TestWishlistOverrideCRUDIsCaseInsensitive(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Tetris", "GB", "gb"); err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertWishlistOverride("TETRIS", "GB", "gb", "Tetris (World) (Rev 1)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows := store.GetWishlist(); len(rows) != 1 {
		t.Fatalf("wishlist has %d rows, want 1 (no case-twin minted)", len(rows))
	}
	if w, ok := store.GetWishlistOverrideForTitle("tetris", "gb"); !ok || w.ID != id {
		t.Fatalf("lowercase lookup missed the pin: ok=%v w=%+v", ok, w)
	}
	if !store.ClearWishlistOverride("TeTrIs", "gb") {
		t.Fatal("mixed-case clear missed the pin")
	}
	if _, ok := store.GetWishlistOverrideForTitle("Tetris", "gb"); ok {
		t.Fatal("pin survived its clear")
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
