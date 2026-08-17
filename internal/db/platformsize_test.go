package db

import (
	"path/filepath"
	"testing"
)

func sizeStore(t *testing.T) *JobStore {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "sizes.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestPlatformSizesShipEmpty(t *testing.T) {
	store := sizeStore(t)

	// Nothing is seeded: a platform with no opinion has no row, which is what
	// makes "we decline to measure this" the default rather than a state an
	// operator has to reach.
	if got := store.ListPlatformSizes(); len(got) != 0 {
		t.Errorf("definitions = %v, want none on a fresh database", got)
	}
	if _, ok := store.GetPlatformSize("atari2600"); ok {
		t.Error("GetPlatformSize reported a row that was never written")
	}
	if got := store.PlatformSizeBands(); len(got) != 0 {
		t.Errorf("bands = %v, want empty", got)
	}
}

func TestPlatformSizeRoundTripAndOverwrite(t *testing.T) {
	store := sizeStore(t)

	derived := PlatformSizeRow{
		PlatformSlug: "atari2600", MinSize: 256, MaxSize: 65536,
		Source: SizeSourceCatalog, SnapshotVersion: "2026.08.01",
	}
	if err := store.SetPlatformSize(derived); err != nil {
		t.Fatalf("set derived: %v", err)
	}

	got, ok := store.GetPlatformSize("atari2600")
	if !ok {
		t.Fatal("stored definition not found")
	}
	if got.MinSize != 256 || got.MaxSize != 65536 {
		t.Errorf("bounds = %d/%d, want 256/65536", got.MinSize, got.MaxSize)
	}
	if got.Source != SizeSourceCatalog || got.SnapshotVersion != "2026.08.01" {
		t.Errorf("provenance = %q/%q, want catalog/2026.08.01", got.Source, got.SnapshotVersion)
	}
	if got.UpdatedAt == "" {
		t.Error("updated_at was not stamped")
	}

	// An operator edit replaces the derived row in place rather than
	// accumulating a second one — platform_slug is the primary key.
	manual := PlatformSizeRow{PlatformSlug: "atari2600", MinSize: 1024, MaxSize: 0, Source: SizeSourceManual}
	if err := store.SetPlatformSize(manual); err != nil {
		t.Fatalf("set manual: %v", err)
	}
	if rows := store.ListPlatformSizes(); len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 after an overwrite", len(rows))
	}
	got, _ = store.GetPlatformSize("atari2600")
	if got.MinSize != 1024 || got.MaxSize != 0 || got.Source != SizeSourceManual {
		t.Errorf("after overwrite = %d/%d %q, want 1024/0 manual", got.MinSize, got.MaxSize, got.Source)
	}
	// The stale provenance must not survive: the numbers are no longer the
	// catalog's, so claiming a snapshot version would be a lie on screen.
	if got.SnapshotVersion != "" {
		t.Errorf("snapshot_version = %q, want cleared once the row is manual", got.SnapshotVersion)
	}
}

func TestPlatformSizeZeroMeansUnlimited(t *testing.T) {
	store := sizeStore(t)

	// A ceiling of zero is a legitimate stored value, not a validation
	// failure: it is how "no upper limit" is expressed, so min > max must not
	// trip when max is zero.
	if err := store.SetPlatformSize(PlatformSizeRow{
		PlatformSlug: "arcade", MinSize: 4096, MaxSize: 0, Source: SizeSourceManual,
	}); err != nil {
		t.Fatalf("floor-only definition rejected: %v", err)
	}
	if got := store.PlatformSizeBands()["arcade"]; got != [2]int64{4096, 0} {
		t.Errorf("band = %v, want [4096 0]", got)
	}
}

func TestPlatformSizeRejectsNonsense(t *testing.T) {
	store := sizeStore(t)

	cases := []struct {
		name string
		row  PlatformSizeRow
	}{
		{"empty slug", PlatformSizeRow{PlatformSlug: "  ", MinSize: 1, MaxSize: 2}},
		{"negative floor", PlatformSizeRow{PlatformSlug: "nes", MinSize: -1}},
		{"negative ceiling", PlatformSizeRow{PlatformSlug: "nes", MaxSize: -1}},
		{"inverted band", PlatformSizeRow{PlatformSlug: "nes", MinSize: 5000, MaxSize: 4000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.SetPlatformSize(tc.row); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
	if got := store.ListPlatformSizes(); len(got) != 0 {
		t.Errorf("rejected rows still landed: %v", got)
	}
}

func TestPlatformSizeDeleteRestoresNoOpinion(t *testing.T) {
	store := sizeStore(t)

	if err := store.SetPlatformSize(PlatformSizeRow{
		PlatformSlug: "ngc", MinSize: 0, MaxSize: 2919956480, Source: SizeSourceCatalog,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := store.DeletePlatformSize("ngc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.GetPlatformSize("ngc"); ok {
		t.Error("row survived deletion")
	}
	// Deleting a row that was never there is not an error: reset is
	// idempotent, and the caller should not have to look first.
	if err := store.DeletePlatformSize("ngc"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}
