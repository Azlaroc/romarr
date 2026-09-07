package db

import "testing"

func TestPlatformRollupUpsert(t *testing.T) {
	store := newTestStore(t)
	if err := store.SavePlatformRollup(PlatformRollup{PlatformSlug: "nes", Owned: 10, Gaps: 5}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SavePlatformRollup(PlatformRollup{PlatformSlug: "nes", Owned: 11, Covered: 2, Gaps: 4}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rollups := store.GetPlatformRollups()
	r, ok := rollups["nes"]
	if !ok || r.Owned != 11 || r.Covered != 2 || r.Gaps != 4 {
		t.Fatalf("rollup=%+v", r)
	}
	if r.ComputedAt == "" {
		t.Fatal("rollup carries no stamp — the stamp is the honesty")
	}
}

func TestLibraryPlatformTotals(t *testing.T) {
	store := newTestStore(t)
	addBrowseItem(t, store, "A", "nes", "/roms/nes/a.nes", "")
	addBrowseItem(t, store, "B", "nes", "/roms/nes/b.nes", "")
	// PC rows fold under "pc" regardless of their own slug.
	id, err := store.AddLibraryItem(&LibraryItem{
		Title: "Doom", Platform: "PC", PlatformSlug: "win", IsPC: true,
		FilePath: "/roms/pc/doom.zip", FileSize: 5000,
		Source: "scan", SourceType: "manual", SourceID: "manual:doom", Metadata: "{}",
	})
	if err != nil || id <= 0 {
		t.Fatalf("add pc row: %v", err)
	}

	totals := store.LibraryPlatformTotals()
	if totals["nes"].Count != 2 || totals["nes"].SizeBytes != 2048 {
		t.Fatalf("nes total=%+v (fixture rows are 1024 bytes each)", totals["nes"])
	}
	if totals["pc"].Count != 1 || totals["pc"].SizeBytes != 5000 {
		t.Fatalf("pc total=%+v, want the folded is_pc row", totals["pc"])
	}
	if _, ok := totals["win"]; ok {
		t.Fatal("is_pc row leaked under its raw slug")
	}
}
