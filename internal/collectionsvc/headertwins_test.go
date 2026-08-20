package collectionsvc

import (
	"testing"

	"gamarr/internal/collection"
	"gamarr/internal/db"
)

func twin(name, ext, md5 string, size int64) db.DatSetMember {
	return db.DatSetMember{
		Name: name, BareTitle: bareTitleOf(name), Region: "usa", TotalSize: size,
		Roms: []db.DatRomRow{{Name: name + ext, Size: size, MD5: md5}},
	}
}

func TestCollapseHeaderTwins(t *testing.T) {
	rows := []db.DatSetMember{
		twin("10-Yard Fight (USA, Europe)", ".nes", "headered", 40976),
		twin("10-Yard Fight (USA, Europe)", ".unh", "payload", 40960),
		twin("Solo Twin (USA)", ".nes", "lonely", 100),
	}
	rows[0].GameID, rows[1].GameID, rows[2].GameID = 1, 2, 3

	got := collapseHeaderTwins(rows)
	if len(got) != 2 {
		t.Fatalf("collapsed to %d members, want 2", len(got))
	}
	merged := got[0]
	if merged.GameID != 1 {
		t.Errorf("merged member kept game %d, want the headered one (1)", merged.GameID)
	}
	if merged.TotalSize != 40976 {
		t.Errorf("total_size = %d, want the headered size the library actually holds", merged.TotalSize)
	}
	if len(merged.Roms) != 2 {
		t.Fatalf("merged member has %d roms, want both publications", len(merged.Roms))
	}
	seen := map[string]bool{}
	for _, r := range merged.Roms {
		seen[r.MD5] = true
	}
	if !seen["headered"] || !seen["payload"] {
		t.Errorf("merged roms = %+v, want both hashes offered to the ownership tier", merged.Roms)
	}
	if got[1].Name != "Solo Twin (USA)" || len(got[1].Roms) != 1 {
		t.Errorf("a twinless game was disturbed: %+v", got[1])
	}
}

func TestCollapseHeaderTwinsLeavesAmbiguousRowsAlone(t *testing.T) {
	// Every case here is one the catalog gives no evidence for. Merging on a
	// guess would silently drop a real dump, which is strictly worse than
	// leaving a duplicate member in the group.
	cases := []struct {
		name string
		rows []db.DatSetMember
		why  string
	}{
		{
			name: "no unh half",
			rows: []db.DatSetMember{twin("Dup (USA)", ".nes", "a", 1), twin("Dup (USA)", ".nes", "b", 1)},
			why:  "two same-named headered games are two dumps, not one",
		},
		{
			name: "both unh",
			rows: []db.DatSetMember{twin("Dup (USA)", ".unh", "a", 1), twin("Dup (USA)", ".unh", "b", 1)},
			why:  "neither is the headered publication of the other",
		},
		{
			name: "three same-named",
			rows: []db.DatSetMember{
				twin("Trip (USA)", ".nes", "a", 1),
				twin("Trip (USA)", ".unh", "b", 1),
				twin("Trip (USA)", ".nes", "c", 1),
			},
			why: "which .nes does the .unh belong to?",
		},
		{
			name: "different names",
			rows: []db.DatSetMember{twin("A (USA)", ".nes", "a", 1), twin("B (USA)", ".unh", "b", 1)},
			why:  "different games",
		},
		{
			name: "multi-rom game",
			rows: func() []db.DatSetMember {
				a := twin("Disc (USA)", ".nes", "a", 1)
				a.Roms = append(a.Roms, db.DatRomRow{Name: "Disc (USA).bin", MD5: "extra"})
				return []db.DatSetMember{a, twin("Disc (USA)", ".unh", "b", 1)}
			}(),
			why: "a multi-file dump is not a header variant",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collapseHeaderTwins(tc.rows)
			if len(got) != len(tc.rows) {
				t.Errorf("collapsed %d rows to %d (%s)", len(tc.rows), len(got), tc.why)
			}
		})
	}
}

func TestCollapseHeaderTwinsDifferingStems(t *testing.T) {
	// Same game NAME, different rom stems: the catalog is saying these files
	// are not two publications of one dump.
	rows := []db.DatSetMember{
		twin("Odd (USA)", ".nes", "a", 1),
		{Name: "Odd (USA)", Roms: []db.DatRomRow{{Name: "Something Else (USA).unh", MD5: "b"}}},
	}
	if got := collapseHeaderTwins(rows); len(got) != 2 {
		t.Errorf("merged across differing rom stems: %+v", got)
	}
}

func TestCollapseHeaderTwinsIsANoOpWithoutTwins(t *testing.T) {
	rows := []db.DatSetMember{
		twin("Ace of Aces (USA)", ".a78", "aaa", 1),
		twin("Ace of Aces (Europe)", ".a78", "bbb", 1),
	}
	got := collapseHeaderTwins(rows)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i := range got {
		if len(got[i].Roms) != 1 || got[i].Roms[0].MD5 != rows[i].Roms[0].MD5 {
			t.Errorf("member %d changed: %+v", i, got[i])
		}
	}
}

// TestHeaderedLibraryOwnsItsKeeper is the reason this file exists: the whole
// point of collapsing is that a headered library's payload hash satisfies the
// KEEPER rather than claiming a losing twin and leaving a gap behind.
func TestHeaderedLibraryOwnsItsKeeper(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "nes", []db.DatGameRow{
		{
			Name: "10-Yard Fight (USA, Europe)", BareTitle: "10-Yard Fight", Region: "usa",
			TotalSize: 40976,
			Roms:      []db.DatRomRow{{Name: "10-Yard Fight (USA, Europe).nes", Size: 40976, MD5: "headered"}},
		},
		{
			Name: "10-Yard Fight (USA, Europe)", BareTitle: "10-Yard Fight", Region: "usa",
			TotalSize: 40960,
			Roms:      []db.DatRomRow{{Name: "10-Yard Fight (USA, Europe).unh", Size: 40960, MD5: "payload"}},
		},
		{
			Name: "10-Yard Fight (Japan) (En)", BareTitle: "10-Yard Fight", Region: "japan",
			TotalSize: 24592,
			Roms:      []db.DatRomRow{{Name: "10-Yard Fight (Japan) (En).nes", Size: 24592, MD5: "jp-headered"}},
		},
		{
			Name: "10-Yard Fight (Japan) (En)", BareTitle: "10-Yard Fight", Region: "japan",
			TotalSize: 24576,
			Roms:      []db.DatRomRow{{Name: "10-Yard Fight (Japan) (En).unh", Size: 24576, MD5: "jp-payload"}},
		},
	})
	// The library holds the USA dump as a headered file, so the only hash it
	// can present is the PAYLOAD's — the .unh row's, not the keeper's own.
	addLibraryItem(t, store, "nes", "10-Yard Fight (U)", "/roms/nes/10-Yard Fight (U)_nes.7z", "payload")

	res := svc.Set("nes")
	if res.Counts.Groups != 1 {
		t.Fatalf("groups = %d, want the twins folded into one group", res.Counts.Groups)
	}
	e := res.Entries[0]
	if len(e.Members) != 2 {
		t.Errorf("group has %d members, want 2 dumps rather than 4 publications", len(e.Members))
	}
	if e.Status != collection.StatusOwned {
		t.Fatalf("status = %q, want owned — the payload hash must satisfy the keeper", e.Status)
	}
	keeper, ok := e.Keeper()
	if !ok || keeper.Owned == nil {
		t.Fatalf("keeper unowned: %+v", keeper)
	}
	if keeper.Owned.MatchedBy != collection.MatchHash {
		t.Errorf("keeper matched by %q, want a hash match", keeper.Owned.MatchedBy)
	}
	if len(e.Surplus) != 0 {
		t.Errorf("surplus = %+v, want the single file counted once", e.Surplus)
	}
}
