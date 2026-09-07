package db

import "testing"

func TestSetLibraryItemProfile(t *testing.T) {
	store := newTestStore(t)
	id := addBrowseItem(t, store, "Alpha", "nes", "/roms/nes/Alpha (USA).nes", "")

	ok, err := store.SetLibraryItemProfile(id, 7)
	if err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	item, err := store.GetLibraryItem(id)
	if err != nil || item.ProfileID != 7 {
		t.Fatalf("profile_id=%d err=%v, want 7", item.ProfileID, err)
	}

	// 0 clears — back to the platform default.
	if ok, err := store.SetLibraryItemProfile(id, 0); err != nil || !ok {
		t.Fatalf("clear: ok=%v err=%v", ok, err)
	}
	item, _ = store.GetLibraryItem(id)
	if item.ProfileID != 0 {
		t.Fatalf("profile_id=%d after clear, want 0", item.ProfileID)
	}

	if ok, _ := store.SetLibraryItemProfile(99999, 1); ok {
		t.Fatal("set on a missing row reported ok")
	}
}

func TestGetActivityFiltered(t *testing.T) {
	store := newTestStore(t)
	a := addBrowseItem(t, store, "Alpha", "nes", "/roms/nes/a.nes", "")
	b := addBrowseItem(t, store, "Beta", "nes", "/roms/nes/b.nes", "")

	store.LogActivity("verify", "Alpha", "catalog: verified", "", &a)
	store.LogActivity("import", "Alpha", "imported", "", &a)
	store.LogActivity("verify", "Beta", "catalog: unknown", "", &b)
	store.LogActivity("system", "no item", "unattributed", "", nil)

	entries, total := store.GetActivityFiltered(a, 1, 50)
	if total != 2 || len(entries) != 2 {
		t.Fatalf("filtered total=%d len=%d, want 2/2", total, len(entries))
	}
	for _, e := range entries {
		if e.LibraryItemID == nil || *e.LibraryItemID != a {
			t.Fatalf("entry %v not scoped to item %d", e, a)
		}
	}

	// The unfiltered read still sees everything.
	_, allTotal := store.GetActivity(1, 50)
	if allTotal != 4 {
		t.Fatalf("unfiltered total=%d, want 4", allTotal)
	}
}

func TestDatGameFamily(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()

	// Standard-DAT shape: regional variants share bare_title, clone_of empty.
	usa := romGame("Contra (USA)", "Contra (USA).nes", "aaaa0001", "", "")
	usa.BareTitle = "Contra"
	eur := romGame("Contra (Europe)", "Contra (Europe).nes", "aaaa0002", "", "")
	eur.BareTitle = "Contra"
	other := romGame("Castlevania (USA)", "Castlevania (USA).nes", "bbbb0001", "", "")
	other.BareTitle = "Castlevania"
	seedCatalog(t, store, "nes", usa, eur, other)

	family := store.DatGameFamily("nes", "Contra (USA)")
	if len(family) != 2 {
		t.Fatalf("family=%v, want the two Contra variants", family)
	}
	for _, g := range family {
		if g.BareTitle != "Contra" {
			t.Fatalf("family pulled in %q", g.Name)
		}
		if g.ID == 0 {
			t.Fatal("family rows must carry ids — the roms drill-down keys on them")
		}
	}

	if got := store.DatGameFamily("nes", "Not In Catalog (USA)"); got != nil {
		t.Fatalf("unknown name returned %v, want nil", got)
	}

	// P/C-DAT shape: a clone names its parent; asking from the clone finds
	// the parent and the parent's other clones.
	parent := romGame("Puyo Puyo (Japan)", "Puyo Puyo (Japan).md", "cccc0001", "", "")
	parent.BareTitle = "Puyo Puyo"
	clone := romGame("Dr. Robotnik's Mean Bean Machine (USA)", "Dr. Robotnik's Mean Bean Machine (USA).md", "cccc0002", "", "")
	clone.BareTitle = "Dr. Robotnik's Mean Bean Machine"
	clone.CloneOf = "Puyo Puyo (Japan)"
	seedCatalog(t, store, "genesis", parent, clone)

	fromClone := store.DatGameFamily("genesis", "Dr. Robotnik's Mean Bean Machine (USA)")
	names := map[string]bool{}
	for _, g := range fromClone {
		names[g.Name] = true
	}
	if !names["Puyo Puyo (Japan)"] || !names["Dr. Robotnik's Mean Bean Machine (USA)"] {
		t.Fatalf("clone family=%v, want parent + clone", names)
	}
}
