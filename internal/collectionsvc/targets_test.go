package collectionsvc

import (
	"testing"

	"gamarr/internal/db"
	"gamarr/internal/platform"
)

func TestSyncTargetsWritesTheGapList(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "atari7800", []db.DatGameRow{
		catGame("Xevious (USA)", "usa", "x1"),
		catGame("Ballblazer (USA)", "usa", "b1"),
	})
	addLibraryItem(t, store, "atari7800", "Ballblazer", "/roms/atari7800/Ballblazer (USA).zip", "b1")

	res := svc.NewCycle().SyncTargets("atari7800")
	if res.Added != 1 || res.Removed != 0 {
		t.Fatalf("sync = %d added / %d removed, want 1 / 0", res.Added, res.Removed)
	}
	rows, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "atari7800", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("targets = %d, want the one gap", len(rows))
	}
	if rows[0].Title != "Xevious" {
		t.Errorf("target = %q, want the unowned title", rows[0].Title)
	}
	// The canonical dump name rides along: it is what the set actually wants,
	// and what the import-side gate will recognise.
	if rows[0].DumpName != "Xevious (USA)" {
		t.Errorf("dump_name = %q, want the keeper's catalogue name", rows[0].DumpName)
	}
	if res.Counts.Gaps != 1 || res.Counts.Owned != 1 {
		t.Errorf("counts = %+v, want 1 gap / 1 owned", res.Counts)
	}
}

func TestSyncAllOnlyCoversCollectionModePlatforms(t *testing.T) {
	svc, store, _ := newTestService(t)
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })

	seedCatalog(t, store, "atari7800", []db.DatGameRow{catGame("Xevious (USA)", "usa", "x1")})
	seedCatalog(t, store, "gb", []db.DatGameRow{catGame("Tetris (World)", "world", "t1")})

	on := true
	if err := store.PatchPlatform("atari7800", db.PlatformPatch{CollectionMode: &on}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}

	results := svc.SyncAll()
	if len(results) != 1 || results[0].Platform != "atari7800" {
		t.Fatalf("results = %+v, want only the collection-mode platform", results)
	}
	if rows, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "gb", Limit: 10}); len(rows) != 0 {
		t.Errorf("a platform not in collection mode got %d targets", len(rows))
	}
	if got := svc.CollectionPlatforms(); len(got) != 1 || got[0] != "atari7800" {
		t.Errorf("CollectionPlatforms = %v", got)
	}

	// 🔴 Leaving collection mode has to stop generating work, not merely stop
	// adding to it: a stale queue keeps the scheduler busy with a policy the
	// operator switched off.
	off := false
	if err := store.PatchPlatform("atari7800", db.PlatformPatch{CollectionMode: &off}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}
	if results := svc.SyncAll(); len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if rows, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10}); len(rows) != 0 {
		t.Errorf("targets = %d after leaving collection mode, want 0", len(rows))
	}
}
