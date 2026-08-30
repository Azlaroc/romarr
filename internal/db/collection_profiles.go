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

// CollectionProfile is one named slice of a catalog.
type CollectionProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
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
	s.seedCollectionProfiles()
	s.foldInCollectionProfiles()
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
// quality profiles, once, guarded by a settings key (the linkPlatformDefaults
// discipline: an operator later re-pointing a platform at Standard must not
// be re-folded).
//
// For every platform, the tuple the set builder used to scrape from
// ResolveProfileForItem — region priority + the three allow flags — is
// compared to Standard's. Platforms already living the default fold to
// nothing (collection_profile_id stays 0); the rest get a "Migrated:" profile
// carrying their exact tuple, deduped by name. 🔴 This is what keeps pc
// honest: PC Default's empty region_priority means no region filtering today,
// and without the fold-in the search rewire would have started filtering pc
// under Standard's chain.
func (s *JobStore) foldInCollectionProfiles() {
	if _, ok := s.GetSetting("collection_profiles_folded"); ok {
		return
	}
	std := DefaultCollectionProfile()
	folded := 0
	for _, row := range s.PlatformRows() {
		prof := s.ResolveProfileForItem(0, row.Slug)
		if prof == nil {
			continue
		}
		if equalStringSlices(prof.RegionPriority, std.RegionPriority) &&
			!prof.AllowProto && !prof.AllowDemo && !prof.AllowBIOS {
			continue
		}
		cp := DefaultCollectionProfile()
		cp.Name = "Migrated: " + prof.Name
		cp.RegionPriority = append([]string(nil), prof.RegionPriority...)
		cp.AllowProto, cp.AllowDemo, cp.AllowBIOS = prof.AllowProto, prof.AllowDemo, prof.AllowBIOS
		id, err := s.findOrCreateCollectionProfile(cp)
		if err != nil {
			slog.Warn("fold collection profile", "platform", row.Slug, "error", err)
			continue
		}
		if err := s.PatchPlatform(row.Slug, PlatformPatch{CollectionProfileID: &id}); err != nil {
			slog.Warn("link collection profile", "platform", row.Slug, "error", err)
			continue
		}
		folded++
	}
	if err := s.SetSetting("collection_profiles_folded", "1"); err != nil {
		slog.Warn("record collection profile fold", "error", err)
		return
	}
	if folded > 0 {
		slog.Info("folded platform policies into collection profiles", "platforms", folded)
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

const cpCols = `id, name, region_priority, english_preferred, keep_without_english,
	allow_proto, allow_demo, allow_bios, allow_unlicensed, allow_aftermarket,
	allow_pirate, verified_only, exclude_categories`

func scanCollectionProfile(scan func(dest ...any) error) (*CollectionProfile, error) {
	var p CollectionProfile
	var regions, cats string
	var englishPref, keepNoEnglish, proto, demo, bios, unl, after, pirate, verified int
	if err := scan(&p.ID, &p.Name, &regions, &englishPref, &keepNoEnglish,
		&proto, &demo, &bios, &unl, &after, &pirate, &verified, &cats); err != nil {
		return nil, err
	}
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
		(name, region_priority, english_preferred, keep_without_english,
		 allow_proto, allow_demo, allow_bios, allow_unlicensed, allow_aftermarket,
		 allow_pirate, verified_only, exclude_categories)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, string(regions), boolInt(p.EnglishPreferred), boolInt(p.KeepWithoutEnglish),
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
// references is refused — re-point the platform (0 = Standard) first, so a
// delete can never silently change what a platform collects.
func (s *JobStore) DeleteCollectionProfile(id int64) error {
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
// platform's assigned profile, or the built-in Standard when none is set or
// the row is gone. Never returns nil.
func (s *JobStore) ResolveCollectionProfile(slug string) *CollectionProfile {
	if row, ok := s.GetPlatformRow(slug); ok && row.CollectionProfileID != 0 {
		if p, err := s.GetCollectionProfile(row.CollectionProfileID); err == nil && p != nil {
			return p
		}
		slog.Debug("collection profile missing, using built-in", "platform", slug, "profile_id", row.CollectionProfileID)
	}
	return DefaultCollectionProfile()
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
