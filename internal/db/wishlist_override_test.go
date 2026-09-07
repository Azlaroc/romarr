package db

import "testing"

func TestWishlistOverrideRoundTrip(t *testing.T) {
	store := newTestStore(t)

	// An owned-but-unwanted title gets a row minted for the pick.
	id, err := store.UpsertWishlistOverride("Contra", "NES", "nes", "Contra (Europe)", []string{"AABB01", " ", "ccdd02"})
	if err != nil || id <= 0 {
		t.Fatalf("upsert (create): id=%d err=%v", id, err)
	}
	w, ok := store.GetWishlistOverrideForTitle("Contra", "nes")
	if !ok || w.OverrideDumpName != "Contra (Europe)" {
		t.Fatalf("read back: %+v ok=%v", w, ok)
	}
	if len(w.OverrideHashes) != 2 || w.OverrideHashes[0] != "aabb01" {
		t.Fatalf("hashes must be lowered and blank-stripped: %v", w.OverrideHashes)
	}

	// A second pick lands on the SAME row.
	id2, err := store.UpsertWishlistOverride("Contra", "NES", "nes", "Probotector (Europe)", nil)
	if err != nil || id2 != id {
		t.Fatalf("upsert (update): id=%d want %d err=%v", id2, id, err)
	}
	w, _ = store.GetWishlistOverrideForTitle("Contra", "nes")
	if w.OverrideDumpName != "Probotector (Europe)" {
		t.Fatalf("update lost: %+v", w)
	}

	// The row feeds the scheduler through the ordinary wishlist read.
	items := store.GetWishlist()
	if len(items) != 1 || items[0].OverrideDumpName != "Probotector (Europe)" {
		t.Fatalf("wishlist read misses the override: %+v", items)
	}

	// Clear returns the row to policy wanted work; the override is gone.
	if !store.ClearWishlistOverride("Contra", "nes") {
		t.Fatal("clear reported nothing")
	}
	if _, ok := store.GetWishlistOverrideForTitle("Contra", "nes"); ok {
		t.Fatal("override survived the clear")
	}
	if store.ClearWishlistOverride("Contra", "nes") {
		t.Fatal("second clear must be a no-op")
	}
	if items := store.GetWishlist(); len(items) != 1 {
		t.Fatalf("the row itself must survive the clear: %+v", items)
	}
}
