package collectionsvc

import (
	"testing"

	"gamarr/internal/db"
)

// The cycle just paid for the set numbers — SyncTargets must stamp them so
// the platform tiles never have to re-derive a set per tile.
func TestSyncTargetsStampsTheRollup(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "atari7800", []db.DatGameRow{
		catGame("Xevious (USA)", "usa", "x1"),
		catGame("Ballblazer (USA)", "usa", "b1"),
	})
	addLibraryItem(t, store, "atari7800", "Ballblazer", "/roms/atari7800/Ballblazer (USA).zip", "b1")

	svc.NewCycle().SyncTargets("atari7800")

	r, ok := store.GetPlatformRollups()["atari7800"]
	if !ok {
		t.Fatal("no rollup row after SyncTargets")
	}
	if r.Owned != 1 || r.Gaps != 1 {
		t.Fatalf("rollup=%+v, want 1 owned / 1 gap", r)
	}
	if r.ComputedAt == "" {
		t.Fatal("rollup missing its stamp")
	}
}

// WriteRollup is the explicit path for a platform outside collection mode.
func TestWriteRollup(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "atari7800", []db.DatGameRow{catGame("Xevious (USA)", "usa", "x1")})

	res := svc.WriteRollup("atari7800")
	if res.Counts.Gaps != 1 {
		t.Fatalf("counts=%+v", res.Counts)
	}
	if _, ok := store.GetPlatformRollups()["atari7800"]; !ok {
		t.Fatal("WriteRollup stamped nothing")
	}
}
