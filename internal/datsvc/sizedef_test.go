package datsvc

import (
	"strings"
	"testing"

	"gamarr/internal/db"
)

func TestDeriveSizeDefinition(t *testing.T) {
	cases := []struct {
		name    string
		snap    db.DatSnapshotRow
		wantMin int64
		wantMax int64
	}{
		{
			// The shape that motivated all of this: a small-cartridge
			// platform whose real dumps are a couple of kilobytes.
			name:    "small cartridge catalog",
			snap:    snapshot("atari2600", 2048, 32768),
			wantMin: 256,
			wantMax: 65536,
		},
		{
			name:    "disc catalog",
			snap:    snapshot("psx", 37872006, 749685974),
			wantMin: 4734000,
			wantMax: 1499371948,
		},
		{
			// Every disc on this platform is byte-identical in size, so the
			// percentiles collapse and the low end carries no information.
			// A floor derived from it would reject compressed images of
			// ordinary games; the ceiling is still meaningful.
			name:    "flat catalog keeps only its ceiling",
			snap:    snapshot("ngc", 1459978240, 1459978240),
			wantMin: 0,
			wantMax: 2919956480,
		},
		{
			name:    "sizeless catalog says nothing at either end",
			snap:    snapshot("arcade", 0, 0),
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "ceiling only when the floor is missing",
			snap:    snapshot("gb", 0, 2097152),
			wantMin: 0,
			wantMax: 4194304,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveSizeDefinition(tc.snap)
			if got.MinSize != tc.wantMin || got.MaxSize != tc.wantMax {
				t.Errorf("band = %d/%d, want %d/%d", got.MinSize, got.MaxSize, tc.wantMin, tc.wantMax)
			}
			if got.Source != db.SizeSourceCatalog {
				t.Errorf("source = %q, want catalog", got.Source)
			}
			if got.SnapshotVersion != tc.snap.Version {
				t.Errorf("provenance = %q, want %q", got.SnapshotVersion, tc.snap.Version)
			}
		})
	}
}

func snapshot(slug string, p01, p99 int64) db.DatSnapshotRow {
	return db.DatSnapshotRow{DatSnapshotMeta: db.DatSnapshotMeta{
		PlatformSlug: slug, Version: "2026.08.01", SizeP01: p01, SizeP99: p99,
	}}
}

func TestImportMaterializesSizeDefinition(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	// The fixture's three games are 2048, 4096 and 6144 bytes, so nearest-rank
	// percentiles land on 2048 and 4096.
	got, ok := h.store.GetPlatformSize("gb")
	if !ok {
		t.Fatal("import wrote no size definition")
	}
	if got.MinSize != 256 || got.MaxSize != 8192 {
		t.Errorf("band = %d/%d, want 256/8192", got.MinSize, got.MaxSize)
	}
	if got.Source != db.SizeSourceCatalog || got.SnapshotVersion != "2026.08.01" {
		t.Errorf("provenance = %q/%q", got.Source, got.SnapshotVersion)
	}
}

func TestManualDefinitionSurvivesARefresh(t *testing.T) {
	// The second path serves a genuinely different catalog: identical bytes
	// short-circuit before the import, which would let this test pass without
	// the derived path ever running.
	h := newHarness(t, func(path string) ([]byte, bool) {
		if strings.Contains(path, "/moved/") {
			return catalog("2026.09.01", 6, "moved"), true
		}
		return catalog("2026.08.01", 3, ""), true
	})
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")
	before, _ := h.store.ActiveDatSnapshot("gb")

	manual := db.PlatformSizeRow{PlatformSlug: "gb", MinSize: 1, MaxSize: 999, Source: db.SizeSourceManual}
	if err := h.store.SetPlatformSize(manual); err != nil {
		t.Fatalf("set manual: %v", err)
	}

	h.pointAt(t, "no-intro", "/moved/")
	h.refreshAndWait(t, "no-intro")

	after, _ := h.store.ActiveDatSnapshot("gb")
	if after.ID == before.ID {
		t.Fatal("the catalog did not actually move; the test would prove nothing")
	}

	got, ok := h.store.GetPlatformSize("gb")
	if !ok {
		t.Fatal("manual definition vanished")
	}
	if got.MinSize != 1 || got.MaxSize != 999 || got.Source != db.SizeSourceManual {
		t.Errorf("manual definition was overwritten by a refresh: %d/%d %q",
			got.MinSize, got.MaxSize, got.Source)
	}
}

func TestSnapshotHookFiresOnlyForRealImports(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")

	h.refreshAndWait(t, "no-intro")
	first := len(h.hooked)
	if first == 0 {
		t.Fatal("an import fired no snapshot callback")
	}

	// Identical bytes: no snapshot is written, so nothing downstream needs
	// invalidating and the callback must stay quiet.
	h.refreshAndWait(t, "no-intro")
	if len(h.hooked) != first {
		t.Errorf("callbacks = %d, want %d — an unchanged refresh fired the hook",
			len(h.hooked), first)
	}
}
