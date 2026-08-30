package db

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// The collection-profile plane: WHAT a platform collects out of its DAT.
//
// A collection profile is the declared slice of the full catalog — region
// order, language preference, category gates — the Lidarr metadata-profile
// analog in Retool's 1G1R vocabulary. It is read by both directions of the
// reconciliation (collection fill and the declutter) and, once the search
// rewire lands, by selection. Quality profiles keep the release-side concerns
// (format preference, source ranking); the catalog-side vocabulary lives
// here, with one owner.
//
// The profile is a FORWARD filter: editing one changes what the set wants,
// never what is on disk. Files outside the slice are surfaced, not evicted —
// eviction stays the declutter's explicit preview→approve→archive flow.

// defaultRegionPriority is the built-in keeper-choice order: US/World first.
// It lived on the quality profiles until the catalog-side vocabulary moved
// here wholesale.
var defaultRegionPriority = []string{"usa", "world", "europe", "japan"}

// CollectionProfile is one named slice of a catalog.
type CollectionProfile struct {
	ID int64 `json:"id"`
	// IsDefault marks the row unassigned platforms resolve to (exactly one,
	// seed-managed: the API never writes it — operators re-point platforms
	// rather than moving the flag, the quality-profile convention).
	IsDefault bool   `json:"is_default"`
	Name      string `json:"name"`
	// RegionPriority ORDERS keeper choice and never excludes: a Japan-only
	// game keeps its Japanese dump under any order. Empty = no region
	// opinion.
	RegionPriority []string `json:"region_priority"`
	// EnglishPreferred keeps the English-tag tier in keeper choice.
	// KeepWithoutEnglish admits games with no English release at all (the
	// Retool "include titles without an English-language release" toggle);
	// off = such groups leave the set entirely.
	EnglishPreferred   bool `json:"english_preferred"`
	KeepWithoutEnglish bool `json:"keep_without_english"`
	// The category gates, Retool's exclusion vocabulary. false = the set
	// leaves those dumps out (they classify as outside-profile, never
	// deleted).
	AllowProto       bool `json:"allow_proto"`       // (Proto)/(Beta)/(Sample)
	AllowDemo        bool `json:"allow_demo"`        // (Demo…)
	AllowBIOS        bool `json:"allow_bios"`        // [BIOS]
	AllowUnlicensed  bool `json:"allow_unlicensed"`  // (Unl)
	AllowAftermarket bool `json:"allow_aftermarket"` // (Aftermarket) homebrew
	AllowPirate      bool `json:"allow_pirate"`      // (Pirate)
	// VerifiedOnly narrows the set to dumps the authority marks verified.
	VerifiedOnly bool `json:"verified_only"`
	// ExcludeCategories are clone-list categories left out of the set
	// ("Applications", "Educational"…).
	ExcludeCategories []string `json:"exclude_categories"`
}

// DefaultCollectionProfile is the hardcoded last-resort profile: ID 0, never
// persisted, and identical to the shipped "Standard" row — which is itself
// the exact policy the set builder applied before profiles existed. The
// backward-compat guarantee lives here: a platform with
// collection_profile_id 0 (or a dangling id) behaves exactly as it always
// has.
func DefaultCollectionProfile() *CollectionProfile {
	return &CollectionProfile{
		Name:               "Standard — Licensed Retail",
		RegionPriority:     append([]string(nil), defaultRegionPriority...),
		EnglishPreferred:   true,
		KeepWithoutEnglish: true,
		ExcludeCategories:  []string{"Applications"},
	}
}

func (s *JobStore) migrateCollectionProfiles() {
	ddl := `CREATE TABLE IF NOT EXISTS collection_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_default INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		region_priority TEXT NOT NULL DEFAULT '[]',
		english_preferred INTEGER NOT NULL DEFAULT 1,
		keep_without_english INTEGER NOT NULL DEFAULT 1,
		allow_proto INTEGER NOT NULL DEFAULT 0,
		allow_demo INTEGER NOT NULL DEFAULT 0,
		allow_bios INTEGER NOT NULL DEFAULT 0,
		allow_unlicensed INTEGER NOT NULL DEFAULT 0,
		allow_aftermarket INTEGER NOT NULL DEFAULT 0,
		allow_pirate INTEGER NOT NULL DEFAULT 0,
		verified_only INTEGER NOT NULL DEFAULT 0,
		exclude_categories TEXT NOT NULL DEFAULT '[]'
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate collection_profiles", "error", err)
	}
	// Backfill at column birth for tables the pre-is_default migration
	// created: the shipped Standard row (still under its seeded name) becomes
	// the default unassigned platforms resolve to.
	if !s.columnExists("collection_profiles", "is_default") {
		if _, err := s.db.Exec(`ALTER TABLE collection_profiles ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`); err != nil {
			slog.Warn("migrate collection_profiles is_default", "error", err)
		} else if _, err := s.db.Exec(`UPDATE collection_profiles SET is_default = 1
			WHERE name = 'Standard — Licensed Retail'`); err != nil {
			slog.Warn("seed collection_profiles is_default", "error", err)
		}
	}
	s.seedCollectionProfiles()
	s.foldInCollectionProfiles()
	// The legacy catalog-side columns go ONLY after the fold above had its
	// chance to read them — dropping them in migrateQualityProfiles would
	// break any install that jumps several versions in one deploy. Plain
	// defaulted columns, no index: safe for SQLite's DROP COLUMN.
	for _, col := range []string{"region_priority", "allow_proto", "allow_demo", "allow_bios"} {
		if s.qpColumnExists(col) {
			if _, err := s.db.Exec("ALTER TABLE quality_profiles DROP COLUMN " + col); err != nil {
				slog.Warn("drop retired quality column", "column", col, "error", err)
			}
		}
	}
}

// seedCollectionProfiles ships the named defaults, virgin-table-guarded like
// the quality-profile seed: an operator who deletes a shipped row must not
// have it resurrected (the ID-0 built-in already covers the fallback).
func (s *JobStore) seedCollectionProfiles() {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM collection_profiles").Scan(&count); err != nil || count > 0 {
		return
	}
	std := DefaultCollectionProfile()
	std.IsDefault = true
	everything := &CollectionProfile{
		Name:               "Everything (incl. aftermarket)",
		RegionPriority:     append([]string(nil), defaultRegionPriority...),
		EnglishPreferred:   true,
		KeepWithoutEnglish: true,
		AllowProto:         true, AllowDemo: true, AllowBIOS: true,
		AllowUnlicensed: true, AllowAftermarket: true, AllowPirate: true,
		ExcludeCategories: []string{},
	}
	verified := DefaultCollectionProfile()
	verified.Name = "Verified Only"
	verified.VerifiedOnly = true
	for _, p := range []*CollectionProfile{std, everything, verified} {
		if _, err := s.AddCollectionProfile(p); err != nil {
			slog.Warn("seed collection profile", "name", p.Name, "error", err)
		}
	}
	slog.Info("seeded collection profiles", "profiles", 3)
}

// foldInCollectionProfiles migrates the effective per-platform policy off the
// LEGACY quality-profile columns, once, guarded by a settings key (the
// linkPlatformDefaults discipline: an operator later re-pointing a platform
// at the default must not be re-folded).
//
// 🔴 It reads the RAW region_priority/allow_* columns rather than the
// QualityProfile struct, because those fields are retired and this file
// drops the columns immediately after the fold — reading the struct would
// fold zeroes on any install that jumps several versions in one deploy. On a
// fresh install the columns never exist, and the only work is preserving the
// one semantic the old seed carried: pc searches with NO region filtering.
func (s *JobStore) foldInCollectionProfiles() {
	if _, ok := s.GetSetting("collection_profiles_folded"); ok {
		return
	}
	if s.qpColumnExists("region_priority") {
		s.foldLegacyQualityTuples()
	} else {
		s.seedPCCollectionProfile()
	}
	if err := s.SetSetting("collection_profiles_folded", "1"); err != nil {
		slog.Warn("record collection profile fold", "error", err)
	}
}

// legacyQPTuple is one quality profile's catalog-side policy, read straight
// off the retired columns.
type legacyQPTuple struct {
	id             int64
	name           string
	platformSlug   string
	isDefault      bool
	isTemplate     bool
	regionPriority []string
	allowProto     bool
	allowDemo      bool
	allowBIOS      bool
}

// foldLegacyQualityTuples resolves each platform's effective tuple the way
// ResolveProfileForItem used to — platform default link → legacy
// platform_slug → global default → lowest-id global — over raw rows, and
// folds every tuple that differs from Standard into a "Migrated:" profile.
func (s *JobStore) foldLegacyQualityTuples() {
	rows, err := s.db.Query(`SELECT id, name, platform_slug, is_default, is_template,
		region_priority, allow_proto, allow_demo, allow_bios FROM quality_profiles ORDER BY id`)
	if err != nil {
		slog.Warn("fold: read legacy quality tuples", "error", err)
		return
	}
	var tuples []legacyQPTuple
	for rows.Next() {
		var t legacyQPTuple
		var regions string
		var isDefault, isTemplate, proto, demo, bios int
		if err := rows.Scan(&t.id, &t.name, &t.platformSlug, &isDefault, &isTemplate,
			&regions, &proto, &demo, &bios); err != nil {
			continue
		}
		t.isDefault, t.isTemplate = isDefault == 1, isTemplate == 1
		t.allowProto, t.allowDemo, t.allowBIOS = proto == 1, demo == 1, bios == 1
		_ = json.Unmarshal([]byte(regions), &t.regionPriority)
		tuples = append(tuples, t)
	}
	rows.Close()

	byID := map[int64]legacyQPTuple{}
	for _, t := range tuples {
		byID[t.id] = t
	}
	resolve := func(row platformRowLite) (legacyQPTuple, bool) {
		if row.defaultProfileID != 0 {
			if t, ok := byID[row.defaultProfileID]; ok && !t.isTemplate {
				return t, true
			}
		}
		for _, t := range tuples {
			if t.platformSlug == row.slug && !t.isTemplate {
				return t, true
			}
		}
		for _, t := range tuples {
			if t.isDefault && !t.isTemplate {
				return t, true
			}
		}
		for _, t := range tuples {
			if t.platformSlug == "" && !t.isTemplate {
				return t, true
			}
		}
		return legacyQPTuple{}, false
	}

	std := DefaultCollectionProfile()
	folded := 0
	for _, row := range s.platformRowsLite() {
		t, ok := resolve(row)
		if !ok {
			continue
		}
		if equalStringSlices(t.regionPriority, std.RegionPriority) &&
			!t.allowProto && !t.allowDemo && !t.allowBIOS {
			continue
		}
		cp := DefaultCollectionProfile()
		cp.Name = "Migrated: " + t.name
		cp.RegionPriority = append([]string(nil), t.regionPriority...)
		cp.AllowProto, cp.AllowDemo, cp.AllowBIOS = t.allowProto, t.allowDemo, t.allowBIOS
		id, err := s.findOrCreateCollectionProfile(cp)
		if err != nil {
			slog.Warn("fold collection profile", "platform", row.slug, "error", err)
			continue
		}
		if err := s.PatchPlatform(row.slug, PlatformPatch{CollectionProfileID: &id}); err != nil {
			slog.Warn("link collection profile", "platform", row.slug, "error", err)
			continue
		}
		folded++
	}
	if folded > 0 {
		slog.Info("folded platform policies into collection profiles", "platforms", folded)
	}
}

// platformRowLite is the two columns the fold needs; the full registry scan
// would drag in every column this migration must not depend on.
type platformRowLite struct {
	slug             string
	defaultProfileID int64
}

func (s *JobStore) platformRowsLite() []platformRowLite {
	rows, err := s.db.Query(`SELECT slug, default_profile_id FROM platforms ORDER BY slug`)
	if err != nil {
		slog.Warn("fold: read platforms", "error", err)
		return nil
	}
	defer rows.Close()
	var out []platformRowLite
	for rows.Next() {
		var r platformRowLite
		if err := rows.Scan(&r.slug, &r.defaultProfileID); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// seedPCCollectionProfile preserves the one catalog-side semantic the legacy
// seed carried, on installs born after the columns died: pc has no region
// order (a PC release rarely carries No-Intro region tags, and filtering on
// them would reject most of the catalog).
func (s *JobStore) seedPCCollectionProfile() {
	if row, ok := s.GetPlatformRow("pc"); !ok || row.CollectionProfileID != 0 {
		return
	}
	cp := DefaultCollectionProfile()
	cp.Name = "PC — No Region Order"
	cp.RegionPriority = []string{}
	id, err := s.findOrCreateCollectionProfile(cp)
	if err != nil {
		slog.Warn("seed pc collection profile", "error", err)
		return
	}
	if err := s.PatchPlatform("pc", PlatformPatch{CollectionProfileID: &id}); err != nil {
		slog.Warn("link pc collection profile", "error", err)
	}
}

func (s *JobStore) findOrCreateCollectionProfile(cp *CollectionProfile) (int64, error) {
	for _, have := range s.GetCollectionProfiles() {
		if have.Name == cp.Name {
			return have.ID, nil
		}
	}
	return s.AddCollectionProfile(cp)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const cpCols = `id, is_default, name, region_priority, english_preferred, keep_without_english,
	allow_proto, allow_demo, allow_bios, allow_unlicensed, allow_aftermarket,
	allow_pirate, verified_only, exclude_categories`

func scanCollectionProfile(scan func(dest ...any) error) (*CollectionProfile, error) {
	var p CollectionProfile
	var regions, cats string
	var isDefault, englishPref, keepNoEnglish, proto, demo, bios, unl, after, pirate, verified int
	if err := scan(&p.ID, &isDefault, &p.Name, &regions, &englishPref, &keepNoEnglish,
		&proto, &demo, &bios, &unl, &after, &pirate, &verified, &cats); err != nil {
		return nil, err
	}
	p.IsDefault = isDefault == 1
	p.EnglishPreferred, p.KeepWithoutEnglish = englishPref == 1, keepNoEnglish == 1
	p.AllowProto, p.AllowDemo, p.AllowBIOS = proto == 1, demo == 1, bios == 1
	p.AllowUnlicensed, p.AllowAftermarket, p.AllowPirate = unl == 1, after == 1, pirate == 1
	p.VerifiedOnly = verified == 1
	_ = json.Unmarshal([]byte(regions), &p.RegionPriority)
	_ = json.Unmarshal([]byte(cats), &p.ExcludeCategories)
	if p.RegionPriority == nil {
		p.RegionPriority = []string{}
	}
	if p.ExcludeCategories == nil {
		p.ExcludeCategories = []string{}
	}
	return &p, nil
}

// GetCollectionProfiles lists every stored profile, by id.
func (s *JobStore) GetCollectionProfiles() []*CollectionProfile {
	rows, err := s.db.Query(`SELECT ` + cpCols + ` FROM collection_profiles ORDER BY id`)
	if err != nil {
		slog.Warn("list collection profiles", "error", err)
		return nil
	}
	defer rows.Close()
	var out []*CollectionProfile
	for rows.Next() {
		p, err := scanCollectionProfile(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// GetCollectionProfile returns one profile by id.
func (s *JobStore) GetCollectionProfile(id int64) (*CollectionProfile, error) {
	row := s.db.QueryRow(`SELECT `+cpCols+` FROM collection_profiles WHERE id = ?`, id)
	p, err := scanCollectionProfile(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("collection profile %d: %w", id, err)
	}
	return p, nil
}

// AddCollectionProfile inserts a profile and returns its id.
func (s *JobStore) AddCollectionProfile(p *CollectionProfile) (int64, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return 0, fmt.Errorf("collection profile needs a name")
	}
	regions, _ := json.Marshal(orEmpty(p.RegionPriority))
	cats, _ := json.Marshal(orEmpty(p.ExcludeCategories))
	res, err := s.db.Exec(`INSERT INTO collection_profiles
		(is_default, name, region_priority, english_preferred, keep_without_english,
		 allow_proto, allow_demo, allow_bios, allow_unlicensed, allow_aftermarket,
		 allow_pirate, verified_only, exclude_categories)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		boolInt(p.IsDefault), name, string(regions), boolInt(p.EnglishPreferred), boolInt(p.KeepWithoutEnglish),
		boolInt(p.AllowProto), boolInt(p.AllowDemo), boolInt(p.AllowBIOS),
		boolInt(p.AllowUnlicensed), boolInt(p.AllowAftermarket), boolInt(p.AllowPirate),
		boolInt(p.VerifiedOnly), string(cats))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCollectionProfile replaces a stored profile's fields.
func (s *JobStore) UpdateCollectionProfile(p *CollectionProfile) error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return fmt.Errorf("collection profile needs a name")
	}
	regions, _ := json.Marshal(orEmpty(p.RegionPriority))
	cats, _ := json.Marshal(orEmpty(p.ExcludeCategories))
	res, err := s.db.Exec(`UPDATE collection_profiles SET
		name = ?, region_priority = ?, english_preferred = ?, keep_without_english = ?,
		allow_proto = ?, allow_demo = ?, allow_bios = ?, allow_unlicensed = ?,
		allow_aftermarket = ?, allow_pirate = ?, verified_only = ?, exclude_categories = ?
		WHERE id = ?`,
		name, string(regions), boolInt(p.EnglishPreferred), boolInt(p.KeepWithoutEnglish),
		boolInt(p.AllowProto), boolInt(p.AllowDemo), boolInt(p.AllowBIOS),
		boolInt(p.AllowUnlicensed), boolInt(p.AllowAftermarket), boolInt(p.AllowPirate),
		boolInt(p.VerifiedOnly), string(cats), p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("collection profile %d not found", p.ID)
	}
	return nil
}

// DeleteCollectionProfile removes a profile. A profile a platform still
// references is refused — re-point the platform (0 = the default) first, so a
// delete can never silently change what a platform collects — and so is the
// default row itself.
func (s *JobStore) DeleteCollectionProfile(id int64) error {
	if p, err := s.GetCollectionProfile(id); err == nil && p.IsDefault {
		return fmt.Errorf("the default collection profile cannot be deleted")
	}
	var refs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM platforms WHERE collection_profile_id = ?`, id).Scan(&refs); err == nil && refs > 0 {
		return fmt.Errorf("collection profile is used by %d platform(s)", refs)
	}
	res, err := s.db.Exec(`DELETE FROM collection_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("collection profile %d not found", id)
	}
	return nil
}

// ResolveCollectionProfile answers "what does this platform collect": the
// platform's assigned profile → the stored default row → the built-in.
// Never returns nil. The built-in is a disaster fallback only (default row
// deleted by hand); the normal unassigned path lands on the EDITABLE stored
// default, so the summary names a real row and editing it retunes every
// unassigned platform at once — the arr convention.
func (s *JobStore) ResolveCollectionProfile(slug string) *CollectionProfile {
	if row, ok := s.GetPlatformRow(slug); ok && row.CollectionProfileID != 0 {
		if p, err := s.GetCollectionProfile(row.CollectionProfileID); err == nil && p != nil {
			return p
		}
		slog.Debug("collection profile missing, falling back to default", "platform", slug, "profile_id", row.CollectionProfileID)
	}
	for _, p := range s.GetCollectionProfiles() {
		if p.IsDefault {
			return p
		}
	}
	return DefaultCollectionProfile()
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
