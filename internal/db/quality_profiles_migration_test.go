package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// legacyQualityProfilesDB builds a DB file whose quality_profiles table has
// the pre-F4 shape and the two legacy seeded rows, simulating a production
// database from before the per-platform columns existed.
func legacyQualityProfilesDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer raw.Close()
	ddl := `CREATE TABLE quality_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		source_ranking TEXT NOT NULL DEFAULT '[]',
		preferred_size_min INTEGER NOT NULL DEFAULT 0,
		preferred_size_max INTEGER NOT NULL DEFAULT 0,
		upgrade_allowed INTEGER NOT NULL DEFAULT 0,
		cutoff_source TEXT NOT NULL DEFAULT ''
	)`
	if _, err := raw.Exec(ddl); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO quality_profiles (name, source_ranking, preferred_size_min, preferred_size_max, upgrade_allowed, cutoff_source)
		 VALUES ('PC Default', '["FitGirl","DODI","PLAZA","Vimm"]', 52428800, 107374182400, 1, 'FitGirl'),
		        ('ROM Default', '["Vimm"]', 0, 0, 0, 'Vimm')`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	return path
}

func TestQualityProfiles_LegacyMigrationBackfill(t *testing.T) {
	path := legacyQualityProfilesDB(t)

	store, err := New(path)
	if err != nil {
		t.Fatalf("New on legacy db: %v", err)
	}
	defer store.Close()

	byName := map[string]QualityProfile{}
	userRows := 0
	for _, p := range store.GetQualityProfiles() {
		byName[p.Name] = p
		if !p.IsTemplate {
			userRows++
		}
	}
	// Templates are seeded alongside; the migration must not add or drop a
	// user-facing row.
	if userRows != 2 {
		t.Fatalf("expected the 2 legacy rows to survive, got %d", userRows)
	}

	rom, ok := byName["ROM Default"]
	if !ok {
		t.Fatal("ROM Default row missing after migration")
	}
	if !rom.IsDefault {
		t.Error("ROM Default should be backfilled as the global default")
	}
	if rom.PlatformSlug != "" {
		t.Errorf("ROM Default platform_slug = %q, want global ''", rom.PlatformSlug)
	}
	if len(rom.FormatPreference) == 0 || rom.FormatPreference[0] != "chd" {
		t.Errorf("ROM Default format_preference = %v, want chd-first", rom.FormatPreference)
	}
	if !rom.Prefer1G1R {
		t.Error("ROM Default should be backfilled with prefer_1g1r=1")
	}
	// Legacy fields untouched by the backfill.
	if rom.CutoffSource != "Vimm" || len(rom.SourceRanking) != 1 {
		t.Errorf("ROM Default legacy fields changed: cutoff=%q ranking=%v", rom.CutoffSource, rom.SourceRanking)
	}

	pc, ok := byName["PC Default"]
	if !ok {
		t.Fatal("PC Default row missing after migration")
	}
	if pc.PlatformSlug != "pc" {
		t.Errorf("PC Default platform_slug = %q, want 'pc'", pc.PlatformSlug)
	}
	if pc.IsDefault {
		t.Error("PC Default must not be the global default")
	}
	if pc.CutoffSource != "FitGirl" || !pc.UpgradeAllowed {
		t.Errorf("PC Default legacy fields changed: cutoff=%q upgrade=%v", pc.CutoffSource, pc.UpgradeAllowed)
	}

	// The retired size plane is dropped on upgrade: the legacy table carried
	// preferred_size_* and this database predates the plane's removal, so the
	// migration must strip both the columns and the definitions table while
	// every other value above survives untouched.
	var n int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('quality_profiles') WHERE name LIKE 'preferred_size%'`).Scan(&n); err != nil {
		t.Fatalf("probe size columns: %v", err)
	}
	if n != 0 {
		t.Errorf("%d preferred_size columns survive the migration, want 0", n)
	}
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='platform_size_definitions'`).Scan(&n); err != nil {
		t.Fatalf("probe definitions table: %v", err)
	}
	if n != 0 {
		t.Error("platform_size_definitions still exists after migration")
	}

	// Migration is idempotent: reopening must not duplicate or re-backfill.
	store.Close()
	store2, err := New(path)
	if err != nil {
		t.Fatalf("reopen migrated db: %v", err)
	}
	defer store2.Close()
	// Idempotent in both directions: no duplicated user row, and the template
	// seed does not run a second time.
	users, templates := 0, 0
	for _, p := range store2.GetQualityProfiles() {
		if p.IsTemplate {
			templates++
		} else {
			users++
		}
	}
	if users != 2 {
		t.Errorf("after reopen: %d user profiles, want 2", users)
	}
	if templates != len(templateSeed) {
		t.Errorf("after reopen: %d templates, want %d", templates, len(templateSeed))
	}
}

func TestQualityProfiles_FreshSeedShape(t *testing.T) {
	store := newTestStore(t)

	byName := map[string]QualityProfile{}
	for _, p := range store.GetQualityProfiles() {
		byName[p.Name] = p
	}
	rom := byName["ROM Default"]
	if !rom.IsDefault || rom.PlatformSlug != "" {
		t.Errorf("fresh ROM Default: is_default=%v slug=%q, want default global", rom.IsDefault, rom.PlatformSlug)
	}
	if len(rom.FormatPreference) != 7 || rom.FormatPreference[0] != "chd" {
		t.Errorf("fresh ROM Default format_preference = %v", rom.FormatPreference)
	}
	pc := byName["PC Default"]
	if pc.PlatformSlug != "pc" || pc.IsDefault {
		t.Errorf("fresh PC Default: slug=%q is_default=%v, want pc/non-default", pc.PlatformSlug, pc.IsDefault)
	}
}

func TestResolveQualityProfile_Precedence(t *testing.T) {
	store := newTestStore(t)

	// Seeded state: ROM Default (global, is_default), PC Default (slug pc).
	// 1) Exact platform override wins.
	psxID, err := store.AddQualityProfile(&QualityProfile{
		Name:         "PSX Override",
		PlatformSlug: "psx",
	})
	if err != nil {
		t.Fatalf("add psx override: %v", err)
	}
	if got := store.ResolveQualityProfile("psx"); got.ID != psxID {
		t.Errorf("psx resolved to %q (id %d), want the override", got.Name, got.ID)
	}

	// 2) No override -> the is_default global row.
	if got := store.ResolveQualityProfile("gb"); got.Name != "ROM Default" {
		t.Errorf("gb resolved to %q, want ROM Default", got.Name)
	}

	// 3) Default flag cleared -> lowest-id global row still wins over nothing.
	profiles := store.GetQualityProfiles()
	for _, p := range profiles {
		if p.IsDefault {
			p.IsDefault = false
			if err := store.UpdateQualityProfile(&p); err != nil {
				t.Fatalf("clear default: %v", err)
			}
		}
	}
	got := store.ResolveQualityProfile("gb")
	if got.PlatformSlug != "" {
		t.Errorf("with no default, resolved slug=%q, want a global row", got.PlatformSlug)
	}

	// 4) Empty table -> hardcoded built-in, never nil.
	for _, p := range store.GetQualityProfiles() {
		if err := store.DeleteQualityProfile(p.ID); err != nil {
			t.Fatalf("delete profile: %v", err)
		}
	}
	got = store.ResolveQualityProfile("gb")
	if got == nil {
		t.Fatal("ResolveQualityProfile returned nil on empty table")
	}
	if got.Name != "Built-in Default" || len(got.FormatPreference) == 0 {
		t.Errorf("empty-table fallback = %q %v", got.Name, got.FormatPreference)
	}
}

func TestQualityProfiles_DefaultFlagUnique(t *testing.T) {
	store := newTestStore(t)

	id, err := store.AddQualityProfile(&QualityProfile{Name: "New Default", IsDefault: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	defaults := 0
	for _, p := range store.GetQualityProfiles() {
		if p.IsDefault {
			defaults++
			if p.ID != id {
				t.Errorf("stale default flag on %q", p.Name)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("%d default profiles, want exactly 1", defaults)
	}
}

// TestQualityProfiles_TwoProfilesPerPlatform replaces the old unique-index
// test. idx_qp_platform allowed exactly one profile per platform, which is
// the constraint profiles v2 removes: "PSX CHD" and "PSX raw" are both
// legitimate, a title picks one, and which one the PLATFORM defaults to lives
// on the platform row rather than being inferred from the profile.
func TestQualityProfiles_TwoProfilesPerPlatform(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.AddQualityProfile(&QualityProfile{Name: "SNES A", PlatformSlug: "snes"}); err != nil {
		t.Fatalf("first snes profile: %v", err)
	}
	if _, err := store.AddQualityProfile(&QualityProfile{Name: "SNES B", PlatformSlug: "snes"}); err != nil {
		t.Fatalf("a platform may now carry more than one profile: %v", err)
	}
	if _, err := store.AddQualityProfile(&QualityProfile{Name: "Global A"}); err != nil {
		t.Fatalf("global profile A: %v", err)
	}
	if _, err := store.AddQualityProfile(&QualityProfile{Name: "Global B"}); err != nil {
		t.Fatalf("global profile B: %v", err)
	}

	// The index is gone, not merely unenforced on this path.
	var count int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_qp_platform'`).Scan(&count); err != nil {
		t.Fatalf("query index: %v", err)
	}
	if count != 0 {
		t.Error("idx_qp_platform still exists — one profile per platform is still enforced")
	}
}
