package db

import (
	"path/filepath"
	"testing"
	"time"
)

func datStore(t *testing.T) (*JobStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dat.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store, path
}

func game(name string, size int64, sha string) DatGameRow {
	return DatGameRow{
		Name: name, BareTitle: name, TotalSize: size,
		Roms: []DatRomRow{{Name: name + ".rom", Size: size, SHA1: sha}},
	}
}

func TestDatSeedShipsAuthoritiesAndAssignments(t *testing.T) {
	store, path := datStore(t)

	auths := store.ListDatAuthorities()
	if len(auths) != 3 {
		t.Fatalf("authorities = %d, want 3", len(auths))
	}
	byName := map[string]DatAuthorityRow{}
	for _, a := range auths {
		byName[a.Name] = a
	}
	// Redump must point at .info: .org has no TLS and lags behind.
	if got := byName["redump"].FetchBase; got != "https://redump.info/datfile/" {
		t.Errorf("redump fetch_base = %q", got)
	}
	if !byName["redump"].Enabled || !byName["no-intro"].Enabled {
		t.Error("redump and no-intro should ship enabled")
	}
	// Arcade is not a RomArr platform yet, so MAME ships dormant.
	if byName["mame"].Enabled {
		t.Error("mame should ship disabled")
	}
	for _, p := range store.ListDatPlatforms() {
		if p.PlatformSlug == "mame" || p.Authority == "mame" {
			t.Errorf("mame should carry no platform assignments, got %+v", p)
		}
	}

	plats := store.ListDatPlatforms()
	if len(plats) < 30 {
		t.Fatalf("platform assignments = %d, want the full seed pack", len(plats))
	}
	byslug := map[string]DatPlatformRow{}
	for _, p := range plats {
		byslug[p.PlatformSlug] = p
	}
	// Cart lane carries the mirror's DAT name; disc lane carries a Redump code.
	if got := byslug["atari2600"]; got.Authority != "no-intro" || got.DatCode != "Atari - 2600" {
		t.Errorf("atari2600 = %+v", got)
	}
	if got := byslug["saturn"]; got.Authority != "redump" || got.DatCode != "ss" {
		t.Errorf("saturn = %+v", got)
	}
	// These two DAT names are easy to write from memory and wrong: the mirror
	// spells it "TurboGrafx 16" with no hyphen before the number, and "Neo Geo"
	// as two words. Either mistake fetches a 404 and silently leaves the lane
	// empty, so the exact strings are pinned here.
	if got := byslug["tg16"]; got.DatCode != "NEC - PC Engine - TurboGrafx 16" {
		t.Errorf("tg16 dat_code = %q", got.DatCode)
	}
	if got := byslug["neo-geo-pocket-color"]; got.DatCode != "SNK - Neo Geo Pocket Color" {
		t.Errorf("neo-geo-pocket-color dat_code = %q", got.DatCode)
	}
	// Platforms with no DAT lane must stay absent rather than mis-assigned.
	if _, ok := byslug["switch"]; ok {
		t.Error("switch is shop-native and should have no DAT assignment")
	}

	// Seeding is once-only: reopening must not duplicate or resurrect edits.
	if err := store.UpdateDatAuthority("redump", DatAuthorityPatch{Enabled: boolPtr(false)}); err != nil {
		t.Fatalf("UpdateDatAuthority: %v", err)
	}
	store.Close()
	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if len(reopened.ListDatAuthorities()) != 3 {
		t.Error("reopen duplicated the seed pack")
	}
	again, err := reopened.GetDatAuthority("redump")
	if err != nil {
		t.Fatalf("GetDatAuthority: %v", err)
	}
	if again.Enabled {
		t.Error("operator's disable was overwritten by re-seeding")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestDatSnapshotInsertActivatesAndReportsDiff(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()

	meta := DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "gb", Version: "2026.08.01",
		SizeMin: 1024, SizeMax: 4096, SizeP01: 1024, SizeP50: 2048, SizeP99: 4096,
	}
	first := []DatGameRow{
		game("Alpha (USA)", 1024, "aaa"),
		game("Beta (USA)", 2048, "bbb"),
		game("Gamma (USA)", 4096, "ccc"),
	}
	snap, err := store.InsertDatSnapshot(meta, first)
	if err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
	if snap.GameCount != 3 || snap.RomCount != 3 {
		t.Errorf("counts = %d/%d, want 3/3", snap.GameCount, snap.RomCount)
	}
	// A first import is all-new, and nothing can be reported as removed.
	if snap.DiffAdded != 3 || snap.DiffRemoved != 0 || snap.DiffChanged != 0 {
		t.Errorf("first diff = +%d/-%d/~%d, want +3/-0/~0", snap.DiffAdded, snap.DiffRemoved, snap.DiffChanged)
	}

	active, ok := store.ActiveDatSnapshot("gb")
	if !ok || active.ID != snap.ID || !active.Active {
		t.Fatalf("active snapshot = %+v, ok=%v", active, ok)
	}

	// Second import: one added, one removed, one re-dumped (same name, new hash).
	second := []DatGameRow{
		game("Alpha (USA)", 1024, "aaa"),
		game("Beta (USA)", 2048, "bbb-redump"),
		game("Delta (USA)", 8192, "ddd"),
	}
	meta.Version = "2026.09.01"
	snap2, err := store.InsertDatSnapshot(meta, second)
	if err != nil {
		t.Fatalf("second InsertDatSnapshot: %v", err)
	}
	if snap2.DiffAdded != 1 || snap2.DiffRemoved != 1 || snap2.DiffChanged != 1 {
		t.Errorf("second diff = +%d/-%d/~%d, want +1/-1/~1",
			snap2.DiffAdded, snap2.DiffRemoved, snap2.DiffChanged)
	}

	// Exactly one snapshot stays active per platform.
	active2, _ := store.ActiveDatSnapshot("gb")
	if active2.ID != snap2.ID {
		t.Errorf("active = %d, want the new snapshot %d", active2.ID, snap2.ID)
	}
	if active2.Version != "2026.09.01" {
		t.Errorf("active version = %q", active2.Version)
	}
}

func TestDatSnapshotPrunesHistory(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()

	meta := DatSnapshotMeta{Authority: "no-intro", PlatformSlug: "gb", SizeP01: 1024, SizeP99: 4096}
	for i := 0; i < 4; i++ {
		meta.Version = time.Now().Format("2006") + "-" + string(rune('a'+i))
		if _, err := store.InsertDatSnapshot(meta, []DatGameRow{game("Alpha (USA)", 1024, "aaa")}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	var snaps, games, roms int
	store.DB().QueryRow(`SELECT COUNT(*) FROM dat_snapshots WHERE platform_slug = 'gb'`).Scan(&snaps)
	store.DB().QueryRow(`SELECT COUNT(*) FROM dat_games WHERE platform_slug = 'gb'`).Scan(&games)
	store.DB().QueryRow(`SELECT COUNT(*) FROM dat_roms`).Scan(&roms)
	if snaps != snapshotRetention {
		t.Errorf("snapshots = %d, want %d", snaps, snapshotRetention)
	}
	// Pruning must take the catalog rows with it — there is no FK cascade.
	if games != snapshotRetention || roms != snapshotRetention {
		t.Errorf("orphaned rows after prune: games=%d roms=%d", games, roms)
	}
}

func TestDatCoverage(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()

	meta := DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "gb", Version: "2026.08.01",
		SizeMin: 1024, SizeMax: 4096, SizeP01: 1024, SizeP50: 2048, SizeP99: 4096,
	}
	if _, err := store.InsertDatSnapshot(meta, []DatGameRow{
		game("Alpha (USA)", 1024, "aaa"),
		game("Beta (USA)", 4096, "bbb"),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	cov := store.DatCoverage()
	var gb *DatCoverageRow
	for i := range cov {
		if cov[i].PlatformSlug == "gb" {
			gb = &cov[i]
		}
	}
	if gb == nil {
		t.Fatal("coverage missing gb")
	}
	if gb.Known != 2 {
		t.Errorf("known = %d, want 2", gb.Known)
	}
	// v1 does not match owned files against the catalog; owned is an
	// independent count and stays 0 on an empty library.
	if gb.Owned != 0 {
		t.Errorf("owned = %d, want 0 on an empty library", gb.Owned)
	}
	if gb.Authority != "no-intro" || gb.SnapshotVersion != "2026.08.01" {
		t.Errorf("coverage = %+v", *gb)
	}
}

func TestDatAuthorityPatchAndPlatformValidation(t *testing.T) {
	store, _ := datStore(t)
	defer store.Close()

	base := "https://cdn.jsdelivr.net/gh/libretro/libretro-database@master/metadat/no-intro/"
	if err := store.UpdateDatAuthority("no-intro", DatAuthorityPatch{FetchBase: &base}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	got, err := store.GetDatAuthority("no-intro")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Repointing at a mirror is a data edit, not a deploy.
	if got.FetchBase != base {
		t.Errorf("fetch_base = %q", got.FetchBase)
	}
	// A sparse patch leaves untouched fields alone.
	if got.FetchDriver != "libretro" || !got.Enabled {
		t.Errorf("sparse patch clobbered other fields: %+v", got)
	}

	if _, err := store.GetDatAuthority("nope"); err == nil {
		t.Error("unknown authority should error")
	}
	if err := store.UpdateDatAuthority("nope", DatAuthorityPatch{}); err == nil {
		t.Error("patching an unknown authority should error")
	}
	// An assignment naming a non-existent authority would fail only at fetch
	// time, so it is rejected on write.
	if err := store.SetDatPlatform(DatPlatformRow{PlatformSlug: "gb", Authority: "nope"}); err == nil {
		t.Error("assignment to unknown authority should error")
	}
	if err := store.SetDatPlatform(DatPlatformRow{PlatformSlug: "", Authority: "no-intro"}); err == nil {
		t.Error("empty slug should error")
	}
	if err := store.SetDatPlatform(DatPlatformRow{PlatformSlug: "lynx", Authority: "no-intro", DatCode: "Atari - Lynx", Enabled: true}); err != nil {
		t.Fatalf("SetDatPlatform: %v", err)
	}

	store.SetDatRefreshResult("no-intro", "ok", "", time.Now())
	after, _ := store.GetDatAuthority("no-intro")
	if after.LastStatus != "ok" || after.LastRefresh == "" {
		t.Errorf("refresh result not recorded: %+v", after)
	}
}
