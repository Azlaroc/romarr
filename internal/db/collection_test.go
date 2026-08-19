package db

import (
	"strings"
	"testing"
)

// 🔴 The clone-list locators are a DIFFERENT upstream vocabulary from the DAT
// codes, and five pairs disagree. Two of them are exact reversals of each
// other, which is precisely the kind of thing a future session would "fix"
// from memory into a 404. Verified against both live listings 2026-08-19.
func TestDatPlatformSeedVocabularies(t *testing.T) {
	byslug := map[string]DatPlatformRow{}
	for _, p := range datPlatformSeed {
		byslug[p.PlatformSlug] = p
	}
	for _, tc := range []struct{ slug, datCode, cloneList string }{
		{"atari2600", "Atari - 2600", "Atari - Atari 2600 (No-Intro)"},
		{"atari7800", "Atari - 7800", "Atari - Atari 7800 (No-Intro)"},
		{"lynx", "Atari - Lynx", "Atari - Atari Lynx (No-Intro)"},
		{"tg16", "NEC - PC Engine - TurboGrafx 16", "NEC - PC Engine - TurboGrafx-16 (No-Intro)"},
		{"neo-geo-pocket-color", "SNK - Neo Geo Pocket Color", "SNK - NeoGeo Pocket Color (No-Intro)"},
	} {
		row, ok := byslug[tc.slug]
		if !ok {
			t.Errorf("%s missing from the seed", tc.slug)
			continue
		}
		if row.DatCode != tc.datCode {
			t.Errorf("%s dat_code = %q, want %q", tc.slug, row.DatCode, tc.datCode)
		}
		if row.CloneListName != tc.cloneList {
			t.Errorf("%s clonelist_name = %q, want %q", tc.slug, row.CloneListName, tc.cloneList)
		}
		if row.DatCode == strings.TrimSuffix(row.CloneListName, " (No-Intro)") {
			t.Errorf("%s: the two vocabularies agree here, so this case no longer pins anything", tc.slug)
		}
	}
	for _, p := range datPlatformSeed {
		if p.CloneListName == "" {
			t.Errorf("%s ships without a clone-list locator", p.PlatformSlug)
		}
	}
}

// 🔴 The seed must BACKFILL. Every existing install already has its
// dat_platforms rows, so a virgin-table-guarded seed would leave the locator
// empty forever on exactly the installs that have catalogs to reconcile.
func TestCloneListNameBackfillsAnExistingInstall(t *testing.T) {
	store, _ := datStore(t)

	if _, err := store.db.Exec(`UPDATE dat_platforms SET clonelist_name = '' WHERE platform_slug = 'gb'`); err != nil {
		t.Fatalf("clear locator: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE dat_platforms SET clonelist_name = 'My Fork (No-Intro)' WHERE platform_slug = 'nes'`); err != nil {
		t.Fatalf("set operator locator: %v", err)
	}

	store.seedCloneListNames()

	byslug := map[string]DatPlatformRow{}
	for _, p := range store.ListDatPlatforms() {
		byslug[p.PlatformSlug] = p
	}
	want, _ := cloneListNameFor("gb")
	if got := byslug["gb"].CloneListName; got != want {
		t.Errorf("gb locator = %q, want the shipped %q", got, want)
	}
	// An operator's own value is never overwritten by a re-seed.
	if got := byslug["nes"].CloneListName; got != "My Fork (No-Intro)" {
		t.Errorf("nes locator = %q, want the operator's value preserved", got)
	}
}

func TestSetDatPlatformKeepsAndSeedsTheLocator(t *testing.T) {
	store, _ := datStore(t)

	// Editing an existing assignment must not clear the locator.
	before := locator(t, store, "gb")
	if err := store.SetDatPlatform(DatPlatformRow{
		PlatformSlug: "gb", Authority: "no-intro", DatCode: "Nintendo - Game Boy", Enabled: true,
	}); err != nil {
		t.Fatalf("SetDatPlatform: %v", err)
	}
	if after := locator(t, store, "gb"); after != before {
		t.Errorf("locator changed on an assignment edit: %q -> %q", before, after)
	}

	// A lane lit up at runtime arrives with its shipped locator — nine lanes
	// were added exactly this way on a live install with no code change.
	if _, err := store.db.Exec(`DELETE FROM dat_platforms WHERE platform_slug = 'lynx'`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDatPlatform(DatPlatformRow{
		PlatformSlug: "lynx", Authority: "no-intro", DatCode: "Atari - Lynx", Enabled: true,
	}); err != nil {
		t.Fatalf("SetDatPlatform: %v", err)
	}
	want, _ := cloneListNameFor("lynx")
	if got := locator(t, store, "lynx"); got != want {
		t.Errorf("relit lane locator = %q, want %q", got, want)
	}

	// An explicit value wins.
	if err := store.SetDatPlatform(DatPlatformRow{
		PlatformSlug: "lynx", Authority: "no-intro", DatCode: "Atari - Lynx", Enabled: true,
		CloneListName: "Something Else (No-Intro)",
	}); err != nil {
		t.Fatalf("SetDatPlatform: %v", err)
	}
	if got := locator(t, store, "lynx"); got != "Something Else (No-Intro)" {
		t.Errorf("explicit locator = %q", got)
	}
}

func locator(t *testing.T, store *JobStore, slug string) string {
	t.Helper()
	for _, p := range store.ListDatPlatforms() {
		if p.PlatformSlug == slug {
			return p.CloneListName
		}
	}
	return ""
}

func TestReplaceCloneListIsWholesale(t *testing.T) {
	store, _ := datStore(t)

	first := []CloneGroupRow{
		{GroupName: "Operation C", SearchTerm: "Contra", Priority: 1},
		{GroupName: "Operation C", SearchTerm: "Probotector", Priority: 2},
		{GroupName: "Gone Next Time", SearchTerm: "Gone", Priority: 1},
	}
	if err := store.ReplaceCloneList(CloneListRow{PlatformSlug: "gb", ListName: "Nintendo - Game Boy (No-Intro)"}, first); err != nil {
		t.Fatalf("ReplaceCloneList: %v", err)
	}
	meta, ok := store.GetCloneList("gb")
	if !ok {
		t.Fatal("stored list not readable")
	}
	// Counts are computed from the rows, not passed in: groups are DISTINCT
	// group names, titles are rows.
	if meta.GroupCount != 2 || meta.TitleCount != len(first) {
		t.Errorf("counts = %d groups / %d titles, want 2 / %d", meta.GroupCount, meta.TitleCount, len(first))
	}

	second := []CloneGroupRow{{GroupName: "Operation C", SearchTerm: "Contra", Priority: 1}}
	if err := store.ReplaceCloneList(CloneListRow{PlatformSlug: "gb", ListName: "Nintendo - Game Boy (No-Intro)"}, second); err != nil {
		t.Fatalf("ReplaceCloneList: %v", err)
	}
	rows := store.ListCloneGroups("gb")
	if len(rows) != len(second) {
		t.Fatalf("rows after replace = %d, want %d — a group upstream deleted must not survive", len(rows), len(second))
	}
	if meta, _ := store.GetCloneList("gb"); meta.GroupCount != 1 {
		t.Errorf("group count after replace = %d, want 1", meta.GroupCount)
	}
}

func TestDatSetMembersAttachesEveryRom(t *testing.T) {
	store, _ := datStore(t)

	disc := DatGameRow{
		Name: "Some Disc (USA)", BareTitle: "Some Disc", Region: "usa", TotalSize: 700,
		Roms: []DatRomRow{
			{Name: "Some Disc (USA).cue", Size: 100, MD5: "aa"},
			{Name: "Some Disc (USA) (Track 1).bin", Size: 300, MD5: "bb"},
			{Name: "Some Disc (USA) (Track 2).bin", Size: 300, MD5: "cc"},
		},
	}
	cart := game("Some Cart (USA)", 1024, "sha-1")
	if _, err := store.InsertDatSnapshot(DatSnapshotMeta{
		Authority: "redump", PlatformSlug: "psx", Version: "v1",
	}, []DatGameRow{disc, cart}); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}

	members := store.DatSetMembers("psx")
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	byName := map[string]DatSetMember{}
	total := 0
	for _, m := range members {
		byName[m.Name] = m
		total += len(m.Roms)
	}
	// Computed from the fixture, not restated.
	if want := len(disc.Roms) + len(cart.Roms); total != want {
		t.Errorf("roms across members = %d, want %d", total, want)
	}
	if got := len(byName[disc.Name].Roms); got != len(disc.Roms) {
		t.Errorf("disc roms = %d, want %d — a multi-track disc must arrive whole", got, len(disc.Roms))
	}
	if store.DatSetMembers("nes") != nil {
		t.Error("a platform with no snapshot returned members")
	}
}

func TestLibraryNameIndexMatchesArchiveWrappers(t *testing.T) {
	store, _ := datStore(t)
	if _, err := store.db.Exec(
		`INSERT INTO library_items (title, platform_slug, file_path) VALUES (?, ?, ?)`,
		"Tetris", "gb", "/roms/gb/Tetris (World).zip"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	idx := store.LibraryNameIndex("gb")
	// The catalog names the rom inside; the library holds the archive around
	// it. Both readings have to find the same row.
	for _, key := range []string{"tetris (world).zip", "tetris (world)"} {
		if idx[key] == nil {
			t.Errorf("index missing %q: %v", key, keysOf(idx))
		}
	}
	if len(store.LibraryNameIndex("nes")) != 0 {
		t.Error("index leaked rows from another platform")
	}
}

func keysOf(m map[string]*LibraryItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
