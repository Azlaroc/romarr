package db

import "testing"

// romGame builds a catalogue entry whose single file carries all three hash
// families, so a test can pick whichever one it wants to match on.
func romGame(name, romName, crc, md5, sha1 string) DatGameRow {
	return DatGameRow{
		Name: name, BareTitle: name, TotalSize: 1024,
		Roms: []DatRomRow{{Name: romName, Size: 1024, CRC: crc, MD5: md5, SHA1: sha1}},
	}
}

func seedCatalog(t *testing.T, store *JobStore, slug string, games ...DatGameRow) {
	t.Helper()
	meta := DatSnapshotMeta{Authority: "no-intro", PlatformSlug: slug, Version: "2026.08.01"}
	if _, err := store.InsertDatSnapshot(meta, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
}

func TestBrowseDatGames(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()
	seedCatalog(t, store, "gb",
		romGame("Tetris (World)", "Tetris (World).gb", "aabbccdd", "", ""),
		romGame("Tetris 2 (USA)", "Tetris 2 (USA).gb", "11223344", "", ""),
		romGame("Wario Land (World)", "Wario Land (World).gb", "55667788", "", ""),
	)
	seedCatalog(t, store, "nes", romGame("Metroid (USA)", "Metroid (USA).nes", "99aabbcc", "", ""))

	t.Run("scoped to one platform", func(t *testing.T) {
		games, total := store.BrowseDatGames(DatGameQuery{PlatformSlug: "gb"})
		if total != 3 || len(games) != 3 {
			t.Fatalf("games = %d (total %d), want 3", len(games), total)
		}
		if games[0].Name != "Tetris (World)" {
			t.Errorf("first = %q, want name order", games[0].Name)
		}
		if games[0].ID == 0 {
			t.Error("browse must return row ids — the roms call keys on them")
		}
	})

	t.Run("text search", func(t *testing.T) {
		games, total := store.BrowseDatGames(DatGameQuery{PlatformSlug: "gb", Text: "wario"})
		if total != 1 || len(games) != 1 || games[0].Name != "Wario Land (World)" {
			t.Fatalf("search = %v (total %d)", games, total)
		}
	})

	t.Run("total is the match count, not the page length", func(t *testing.T) {
		games, total := store.BrowseDatGames(DatGameQuery{PlatformSlug: "gb", Limit: 2})
		if len(games) != 2 || total != 3 {
			t.Fatalf("page = %d, total = %d, want 2 and 3", len(games), total)
		}
		next, _ := store.BrowseDatGames(DatGameQuery{PlatformSlug: "gb", Limit: 2, Offset: 2})
		if len(next) != 1 || next[0].Name != "Wario Land (World)" {
			t.Fatalf("second page = %v", next)
		}
	})

	t.Run("superseded snapshots are invisible", func(t *testing.T) {
		// Only one snapshot per platform is active; the browse must not show
		// the catalogue a refresh replaced.
		seedCatalog(t, store, "gb", romGame("Kirby (World)", "Kirby (World).gb", "deadbeef", "", ""))
		games, total := store.BrowseDatGames(DatGameQuery{PlatformSlug: "gb"})
		if total != 1 || len(games) != 1 || games[0].Name != "Kirby (World)" {
			t.Fatalf("after refresh = %v (total %d), want only the active snapshot", games, total)
		}
	})
}

func TestDatGameRoms(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()
	// A disc: a cue plus two tracks, which is why roms are their own call.
	disc := DatGameRow{
		Name: "Some Disc (USA)", BareTitle: "Some Disc", TotalSize: 3000,
		Roms: []DatRomRow{
			{Name: "Some Disc (USA).cue", Size: 100, CRC: "aa"},
			{Name: "Some Disc (USA) (Track 1).bin", Size: 1900, CRC: "bb"},
			{Name: "Some Disc (USA) (Track 2).bin", Size: 1000, CRC: "cc"},
		},
	}
	seedCatalog(t, store, "psx", disc)

	games, _ := store.BrowseDatGames(DatGameQuery{PlatformSlug: "psx"})
	if len(games) != 1 {
		t.Fatalf("games = %v", games)
	}
	roms := store.DatGameRoms(games[0].ID)
	if len(roms) != 3 {
		t.Fatalf("roms = %d, want the cue and both tracks", len(roms))
	}
}

// The gate's whole meaning lives in this function: what counts as verified,
// what counts as the catalogue disagreeing, and what counts as silence.
func TestMatchDatRom(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()
	seedCatalog(t, store, "gb",
		romGame("Tetris (World)", "Tetris (World).gb", "aabbccdd", "d41d8cd98f00b204e9800998ecf8427e", ""),
	)

	t.Run("hash match is verified", func(t *testing.T) {
		v := store.MatchDatRom("gb", "Tetris (World).gb", "aabbccdd", "", "")
		if v.Status != CatalogVerified || v.GameName != "Tetris (World)" {
			t.Fatalf("verdict = %+v, want verified", v)
		}
	})

	t.Run("a renamed file is still verified", func(t *testing.T) {
		// The hash is the evidence; the filename is not. A correctly dumped
		// ROM someone renamed must not be accused.
		v := store.MatchDatRom("gb", "tetris.gb", "aabbccdd", "", "")
		if v.Status != CatalogVerified {
			t.Fatalf("verdict = %+v, want verified", v)
		}
	})

	t.Run("md5 alone is enough", func(t *testing.T) {
		v := store.MatchDatRom("gb", "whatever.gb", "", "d41d8cd98f00b204e9800998ecf8427e", "")
		if v.Status != CatalogVerified {
			t.Fatalf("verdict = %+v, want verified", v)
		}
	})

	t.Run("same name, different hash is a mismatch", func(t *testing.T) {
		v := store.MatchDatRom("gb", "Tetris (World).gb", "ffffffff", "", "")
		if v.Status != CatalogMismatch {
			t.Fatalf("verdict = %+v, want mismatch", v)
		}
		if v.Expected != "aabbccdd" || v.Got != "ffffffff" {
			t.Errorf("verdict = %+v, want the evidence for the rejection", v)
		}
	})

	t.Run("an uncatalogued file is unknown, not a failure", func(t *testing.T) {
		// Hacks, homebrew, translations and dumps newer than the snapshot all
		// land here, and on some platforms they outnumber the catalogued ones.
		v := store.MatchDatRom("gb", "Some Homebrew (2026).gb", "12345678", "", "")
		if v.Status != CatalogUnknown {
			t.Fatalf("verdict = %+v, want unknown", v)
		}
	})

	t.Run("another platform's catalogue does not answer", func(t *testing.T) {
		v := store.MatchDatRom("nes", "Tetris (World).gb", "aabbccdd", "", "")
		if v.Status != CatalogUnknown {
			t.Fatalf("verdict = %+v, want unknown", v)
		}
	})

	t.Run("no hashes means no verdict", func(t *testing.T) {
		// A file that could not be hashed is not evidence of anything.
		// Accusing it because its NAME is catalogued would turn an unreadable
		// file into a blocklisted release.
		v := store.MatchDatRom("gb", "Tetris (World).gb", "", "", "")
		if v.Status != CatalogUnknown {
			t.Fatalf("verdict = %+v, want unknown", v)
		}
	})
}
