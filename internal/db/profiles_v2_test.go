package db

import (
	"path/filepath"
	"testing"

	"gamarr/internal/platform"
)

// prodProfileFixture is the shape a real deployment carries today: one global
// default and a per-platform profile for each platform that has been tuned.
// The names and platform slugs are the live ones — the point of the replay
// below is that profiles v2 resolves exactly what the platform_slug column
// resolved before, for every platform, with nothing silently re-pointed.
var prodProfileFixture = []QualityProfile{
	{Name: "PC Default", PlatformSlug: "pc", SourceRanking: []string{"FitGirl", "DODI", "PLAZA", "Vimm"},
		PreferredSizeMin: 52428800, PreferredSizeMax: 107374182400, UpgradeAllowed: true, CutoffSource: "FitGirl"},
	{Name: "ROM Default", IsDefault: true, SourceRanking: []string{"Vimm"},
		RegionPriority: []string{"usa", "world", "uk", "canada", "australia", "europe", "japan"}},
	{Name: "PSX Default", PlatformSlug: "psx", SourceRanking: []string{"Vimm"}},
	{Name: "PS2 Default", PlatformSlug: "ps2", SourceRanking: []string{"Vimm"}},
	{Name: "PSP Default", PlatformSlug: "psp", SourceRanking: []string{"Vimm"}},
	{Name: "Dreamcast Default", PlatformSlug: "dc", SourceRanking: []string{"Vimm"}},
	{Name: "Saturn Default", PlatformSlug: "saturn", SourceRanking: []string{"Vimm"}},
	{Name: "GameCube Default", PlatformSlug: "ngc", SourceRanking: []string{"Vimm"}},
	{Name: "Wii Default", PlatformSlug: "wii", SourceRanking: []string{"Vimm"}},
	{Name: "Atari 2600 Default", PlatformSlug: "atari2600"},
}

func profilesV2Store(t *testing.T) *JobStore {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })
	return store
}

// TestResolutionUnchangedForEveryPlatform is the replay: seed a live-shaped
// profile set, run the link migration, and check that every platform in the
// registry still resolves to the profile it resolved to under the old
// platform_slug lookup. A migration that silently re-points a platform at the
// global default would be invisible in production until a grab came back
// wrong, so it gets asserted for all of them, not for a sample.
func TestResolutionUnchangedForEveryPlatform(t *testing.T) {
	store := profilesV2Store(t)

	// Clear the shipped seed so the fixture is the whole population.
	if _, err := store.db.Exec("DELETE FROM quality_profiles WHERE is_template = 0"); err != nil {
		t.Fatalf("clear seed: %v", err)
	}
	byName := map[string]int64{}
	for i := range prodProfileFixture {
		p := prodProfileFixture[i]
		id, err := store.AddQualityProfile(&p)
		if err != nil {
			t.Fatalf("seed %s: %v", p.Name, err)
		}
		byName[p.Name] = id
	}

	// The expectation is computed from the fixture, not restated: whatever
	// the old rule (exact platform_slug match, else the global default) would
	// have picked.
	globalID := byName["ROM Default"]
	want := map[string]int64{}
	for _, p := range platform.Rows() {
		want[p.Slug] = globalID
		for _, f := range prodProfileFixture {
			if f.PlatformSlug == p.Slug {
				want[p.Slug] = byName[f.Name]
			}
		}
	}

	// Re-run the link migration against this population.
	store.DeleteSetting("profiles_v2_linked")
	store.linkPlatformDefaults()

	for slug, wantID := range want {
		got := store.ResolveProfileForItem(0, slug)
		if got == nil {
			t.Fatalf("%s resolved to nil", slug)
		}
		if got.ID != wantID {
			t.Errorf("%s resolved to %q (id %d), want id %d", slug, got.Name, got.ID, wantID)
		}
	}

	// And the link is what did it: the tuned platforms now carry the mapping
	// on the registry row rather than on the profile.
	row, _ := store.GetPlatformRow("psx")
	if row.DefaultProfileID != byName["PSX Default"] {
		t.Errorf("psx default_profile_id = %d, want %d", row.DefaultProfileID, byName["PSX Default"])
	}
}

func TestPerTitleProfileOverridesThePlatformDefault(t *testing.T) {
	store := profilesV2Store(t)

	jp, err := store.AddQualityProfile(&QualityProfile{
		Name: "PSX Japan", RegionPriority: []string{"japan", "usa"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	platformDefault := store.ResolveProfileForItem(0, "psx")

	got := store.ResolveProfileForItem(jp, "psx")
	if got.ID != jp {
		t.Errorf("per-title profile ignored: got %q", got.Name)
	}
	if platformDefault.ID == jp {
		t.Fatal("fixture is degenerate — the platform default already IS the override")
	}

	// A profile deleted out from under a row falls back rather than failing
	// the cycle.
	if err := store.DeleteQualityProfile(jp); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := store.ResolveProfileForItem(jp, "psx"); got.ID != platformDefault.ID {
		t.Errorf("orphaned override resolved to %q, want the platform default", got.Name)
	}
}

// TestMaterializationIsSilentAndClassed covers the acceptance clause "adding a
// title on a never-acquired platform requires zero prior setup".
func TestMaterializationIsSilentAndClassed(t *testing.T) {
	store := profilesV2Store(t)

	row, _ := store.GetPlatformRow("gbc")
	if row.DefaultProfileID != 0 {
		t.Fatal("fixture: gbc should start with no default profile")
	}

	prof, created := store.EnsurePlatformProfile("gbc")
	if !created {
		t.Fatal("first add on an untouched platform should materialize a profile")
	}
	if prof.Name != "Game Boy Color Default" {
		t.Errorf("materialized name = %q", prof.Name)
	}
	if prof.IsTemplate || prof.TemplateClass != "" {
		t.Error("a materialized profile must not itself be a template")
	}
	// gbc's DAT lane is No-Intro, whose kind is carts — so it inherits the
	// cart template's formats, not the disc ones.
	if len(prof.FormatPreference) == 0 || prof.FormatPreference[0] != "zip" {
		t.Errorf("gbc formats = %v, want the carts template", prof.FormatPreference)
	}

	row, _ = store.GetPlatformRow("gbc")
	if row.DefaultProfileID != prof.ID {
		t.Errorf("platform default_profile_id = %d, want %d", row.DefaultProfileID, prof.ID)
	}

	// Idempotent: the second add finds it rather than making another.
	again, created := store.EnsurePlatformProfile("gbc")
	if created || again.ID != prof.ID {
		t.Errorf("second add materialized again: created=%v id=%d", created, again.ID)
	}

	// A disc platform gets the disc template — CHD first, which is the whole
	// reason the class is taken from the DAT authority.
	disc, created := store.EnsurePlatformProfile("saturn")
	if !created {
		t.Fatal("saturn should materialize")
	}
	if len(disc.FormatPreference) == 0 || disc.FormatPreference[0] != "chd" {
		t.Errorf("saturn formats = %v, want the discs template", disc.FormatPreference)
	}
}

func TestWishlistCarriesItsProfile(t *testing.T) {
	store := profilesV2Store(t)

	id, err := store.AddWishlistItemWithProfile("Rondo of Blood", "TurboGrafx-16", "tg16", 42)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var got WishlistItem
	for _, w := range store.GetWishlist() {
		if w.ID == id {
			got = w
		}
	}
	if got.ProfileID != 42 {
		t.Errorf("profile_id = %d, want 42", got.ProfileID)
	}

	if ok, err := store.SetWishlistProfile(id, 0); err != nil || !ok {
		t.Fatalf("clear override: ok=%v err=%v", ok, err)
	}
	for _, w := range store.GetWishlist() {
		if w.ID == id && w.ProfileID != 0 {
			t.Errorf("override not cleared: %d", w.ProfileID)
		}
	}
	if ok, _ := store.SetWishlistProfile(99999, 1); ok {
		t.Error("updating a missing row should report no rows changed")
	}
}

// TestLinkMigrationRunsOnceEvenAfterAnOperatorClearsADefault pins the reason
// the guard is a settings key rather than "no platform has a default yet".
func TestLinkMigrationRunsOnceEvenAfterAnOperatorClearsADefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "link.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })

	if _, err := store.AddQualityProfile(&QualityProfile{Name: "SNES Legacy", PlatformSlug: "snes"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	store.DeleteSetting("profiles_v2_linked")
	store.linkPlatformDefaults()

	row, _ := store.GetPlatformRow("snes")
	if row.DefaultProfileID == 0 {
		t.Fatal("link migration did not attach the legacy mapping")
	}

	// The operator clears it deliberately.
	zero := int64(0)
	if err := store.PatchPlatform("snes", PlatformPatch{DefaultProfileID: &zero}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	store.Close()

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	row, _ = reopened.GetPlatformRow("snes")
	if row.DefaultProfileID != 0 {
		t.Error("restart resurrected a default the operator had cleared")
	}
}
