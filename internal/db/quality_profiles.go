package db

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// QualityProfile defines the per-platform selection policy for downloads:
// which regions and formats to prefer, what release classes to allow, and
// source ranking / size bounds.
//
// Scope: PlatformSlug "" marks a global profile; a non-empty slug overrides
// the global profile for that platform only (one override per platform,
// enforced by idx_qp_platform). IsDefault marks the global fallback row.
// Resolution order is ResolveQualityProfile's.
//
// Semantics of the pre-F4 fields:
//   - PreferredSizeMin/Max: size-sanity bounds in bytes; 0 means "fall back
//     to the built-in per-platform size table" (search.platformSizeRange).
//   - UpgradeAllowed/CutoffSource: reserved for the active upgrade loop
//     (re-grab when a better release appears, stop at CutoffSource). No code
//     consumes them yet — the F4 selector is skip-if-owned only.
type QualityProfile struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	PlatformSlug     string   `json:"platform_slug"`      // "" = global profile
	IsDefault        bool     `json:"is_default"`         // the global fallback row
	RegionPriority   []string `json:"region_priority"`    // ordered list: best first (lowered tokens)
	FormatPreference []string `json:"format_preference"`  // ordered list: best first
	Prefer1G1R       bool     `json:"prefer_1g1r"`        // prefer verified dumps / latest revision
	AllowProto       bool     `json:"allow_proto"`        // allow (Proto)/(Beta)/(Sample) releases
	AllowDemo        bool     `json:"allow_demo"`         // allow (Demo) releases
	AllowBIOS        bool     `json:"allow_bios"`         // allow [BIOS] releases
	SourceRanking    []string `json:"source_ranking"`     // ordered list: best first
	PreferredSizeMin int64    `json:"preferred_size_min"` // bytes; 0 = platform default
	PreferredSizeMax int64    `json:"preferred_size_max"` // bytes; 0 = platform default
	UpgradeAllowed   bool     `json:"upgrade_allowed"`
	CutoffSource     string   `json:"cutoff_source"` // stop upgrading once this source is reached
}

// defaultRegionPriority / defaultFormatPreference are the built-in ROM
// selection policy: US/World first, disc images as CHD first.
var (
	defaultRegionPriority   = []string{"usa", "world", "europe", "japan"}
	defaultFormatPreference = []string{"chd", "cue", "iso", "gdi", "zip", "7z", "raw"}
)

// DefaultQualityProfile is the hardcoded last-resort profile used when the
// table has no usable row. Never persisted; ID 0.
func DefaultQualityProfile() *QualityProfile {
	return &QualityProfile{
		Name:             "Built-in Default",
		RegionPriority:   append([]string(nil), defaultRegionPriority...),
		FormatPreference: append([]string(nil), defaultFormatPreference...),
		Prefer1G1R:       true,
		SourceRanking:    []string{},
	}
}

// qpV2Columns are the per-platform policy columns added for the F4 selector.
// Presence of "platform_slug" doubles as the schema-version probe: a table
// without it is the legacy shape and gets the one-time backfill below.
var qpV2Columns = []struct{ name, def string }{
	{"platform_slug", "TEXT NOT NULL DEFAULT ''"},
	{"is_default", "INTEGER NOT NULL DEFAULT 0"},
	{"region_priority", "TEXT NOT NULL DEFAULT '[]'"},
	{"format_preference", "TEXT NOT NULL DEFAULT '[]'"},
	{"prefer_1g1r", "INTEGER NOT NULL DEFAULT 1"},
	{"allow_proto", "INTEGER NOT NULL DEFAULT 0"},
	{"allow_demo", "INTEGER NOT NULL DEFAULT 0"},
	{"allow_bios", "INTEGER NOT NULL DEFAULT 0"},
}

func (s *JobStore) migrateQualityProfiles() {
	ddl := `CREATE TABLE IF NOT EXISTS quality_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		source_ranking TEXT NOT NULL DEFAULT '[]',
		preferred_size_min INTEGER NOT NULL DEFAULT 0,
		preferred_size_max INTEGER NOT NULL DEFAULT 0,
		upgrade_allowed INTEGER NOT NULL DEFAULT 0,
		cutoff_source TEXT NOT NULL DEFAULT '',
		platform_slug TEXT NOT NULL DEFAULT '',
		is_default INTEGER NOT NULL DEFAULT 0,
		region_priority TEXT NOT NULL DEFAULT '[]',
		format_preference TEXT NOT NULL DEFAULT '[]',
		prefer_1g1r INTEGER NOT NULL DEFAULT 1,
		allow_proto INTEGER NOT NULL DEFAULT 0,
		allow_demo INTEGER NOT NULL DEFAULT 0,
		allow_bios INTEGER NOT NULL DEFAULT 0
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate quality_profiles", "error", err)
	}

	// Upgrade path: a pre-F4 table lacks platform_slug. Add the v2 columns,
	// then backfill the two legacy seeded rows exactly once.
	legacy := !s.qpColumnExists("platform_slug")
	if legacy {
		for _, col := range qpV2Columns {
			s.db.Exec(fmt.Sprintf("ALTER TABLE quality_profiles ADD COLUMN %s %s", col.name, col.def))
		}
		regions, _ := json.Marshal(defaultRegionPriority)
		formats, _ := json.Marshal(defaultFormatPreference)
		s.db.Exec(
			"UPDATE quality_profiles SET is_default = 1, region_priority = ?, format_preference = ?, prefer_1g1r = 1 WHERE name = 'ROM Default'",
			string(regions), string(formats),
		)
		s.db.Exec("UPDATE quality_profiles SET platform_slug = 'pc' WHERE name = 'PC Default'")
	}

	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_qp_platform ON quality_profiles(platform_slug) WHERE platform_slug != ''`); err != nil {
		slog.Warn("migrate quality_profiles index", "error", err)
	}

	// Seed defaults if table is empty
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM quality_profiles").Scan(&count)
	if count == 0 {
		pcRanking, _ := json.Marshal([]string{"FitGirl", "DODI", "PLAZA", "Vimm"})
		// Internet Archive, not Vimm: Vimm is registry-disabled on real
		// deployments (dead scrape target), so a fresh install's ROM profile
		// should prefer the source that actually serves.
		romRanking, _ := json.Marshal([]string{"Internet Archive"})
		regions, _ := json.Marshal(defaultRegionPriority)
		formats, _ := json.Marshal(defaultFormatPreference)
		s.db.Exec(
			"INSERT INTO quality_profiles (name, source_ranking, preferred_size_min, preferred_size_max, upgrade_allowed, cutoff_source, platform_slug, is_default, region_priority, format_preference, prefer_1g1r) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			"PC Default", string(pcRanking), 50*1024*1024, 100*1024*1024*1024, 1, "FitGirl", "pc", 0, "[]", "[]", 1,
		)
		s.db.Exec(
			"INSERT INTO quality_profiles (name, source_ranking, preferred_size_min, preferred_size_max, upgrade_allowed, cutoff_source, platform_slug, is_default, region_priority, format_preference, prefer_1g1r) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			"ROM Default", string(romRanking), 0, 0, 0, "Internet Archive", "", 1, string(regions), string(formats), 1,
		)
	}
}

// qpColumnExists reports whether quality_profiles has the named column.
func (s *JobStore) qpColumnExists(name string) bool {
	rows, err := s.db.Query("PRAGMA table_info(quality_profiles)")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if colName == name {
			return true
		}
	}
	return false
}

const qpSelectColumns = "id, name, source_ranking, preferred_size_min, preferred_size_max, upgrade_allowed, cutoff_source, platform_slug, is_default, region_priority, format_preference, prefer_1g1r, allow_proto, allow_demo, allow_bios"

func scanQualityProfile(scan func(dest ...interface{}) error) (*QualityProfile, error) {
	var p QualityProfile
	var rankingJSON, regionsJSON, formatsJSON string
	var upgradeAllowed, isDefault, prefer1g1r, allowProto, allowDemo, allowBIOS int
	err := scan(&p.ID, &p.Name, &rankingJSON, &p.PreferredSizeMin, &p.PreferredSizeMax, &upgradeAllowed, &p.CutoffSource,
		&p.PlatformSlug, &isDefault, &regionsJSON, &formatsJSON, &prefer1g1r, &allowProto, &allowDemo, &allowBIOS)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(rankingJSON), &p.SourceRanking)
	json.Unmarshal([]byte(regionsJSON), &p.RegionPriority)
	json.Unmarshal([]byte(formatsJSON), &p.FormatPreference)
	p.UpgradeAllowed = upgradeAllowed != 0
	p.IsDefault = isDefault != 0
	p.Prefer1G1R = prefer1g1r != 0
	p.AllowProto = allowProto != 0
	p.AllowDemo = allowDemo != 0
	p.AllowBIOS = allowBIOS != 0
	return &p, nil
}

// GetQualityProfiles returns all quality profiles.
func (s *JobStore) GetQualityProfiles() []QualityProfile {
	rows, err := s.db.Query("SELECT " + qpSelectColumns + " FROM quality_profiles ORDER BY id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var profiles []QualityProfile
	for rows.Next() {
		p, err := scanQualityProfile(rows.Scan)
		if err != nil {
			continue
		}
		profiles = append(profiles, *p)
	}
	return profiles
}

// GetQualityProfile returns a single quality profile by ID.
func (s *JobStore) GetQualityProfile(id int64) (*QualityProfile, error) {
	row := s.db.QueryRow("SELECT "+qpSelectColumns+" FROM quality_profiles WHERE id = ?", id)
	return scanQualityProfile(row.Scan)
}

// ResolveQualityProfile returns the selection policy for a platform:
// the platform's own override row if one exists, else the IsDefault global
// row, else the lowest-id global row, else the hardcoded built-in default.
// Never returns nil.
func (s *JobStore) ResolveQualityProfile(platformSlug string) *QualityProfile {
	profiles := s.GetQualityProfiles()
	if platformSlug != "" {
		for i := range profiles {
			if profiles[i].PlatformSlug == platformSlug {
				return &profiles[i]
			}
		}
	}
	for i := range profiles {
		if profiles[i].IsDefault && profiles[i].PlatformSlug == "" {
			return &profiles[i]
		}
	}
	for i := range profiles { // profiles is ORDER BY id: first global row is lowest-id
		if profiles[i].PlatformSlug == "" {
			return &profiles[i]
		}
	}
	return DefaultQualityProfile()
}

// AddQualityProfile inserts a new quality profile.
func (s *JobStore) AddQualityProfile(p *QualityProfile) (int64, error) {
	rankingJSON, _ := json.Marshal(p.SourceRanking)
	regionsJSON, _ := json.Marshal(p.RegionPriority)
	formatsJSON, _ := json.Marshal(p.FormatPreference)
	result, err := s.db.Exec(
		"INSERT INTO quality_profiles (name, source_ranking, preferred_size_min, preferred_size_max, upgrade_allowed, cutoff_source, platform_slug, is_default, region_priority, format_preference, prefer_1g1r, allow_proto, allow_demo, allow_bios) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		p.Name, string(rankingJSON), p.PreferredSizeMin, p.PreferredSizeMax, boolToInt(p.UpgradeAllowed), p.CutoffSource,
		p.PlatformSlug, boolToInt(p.IsDefault), string(regionsJSON), string(formatsJSON), boolToInt(p.Prefer1G1R),
		boolToInt(p.AllowProto), boolToInt(p.AllowDemo), boolToInt(p.AllowBIOS),
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if p.IsDefault {
		s.clearOtherDefaults(id)
	}
	return id, nil
}

// UpdateQualityProfile updates an existing quality profile.
func (s *JobStore) UpdateQualityProfile(p *QualityProfile) error {
	rankingJSON, _ := json.Marshal(p.SourceRanking)
	regionsJSON, _ := json.Marshal(p.RegionPriority)
	formatsJSON, _ := json.Marshal(p.FormatPreference)
	_, err := s.db.Exec(
		"UPDATE quality_profiles SET name = ?, source_ranking = ?, preferred_size_min = ?, preferred_size_max = ?, upgrade_allowed = ?, cutoff_source = ?, platform_slug = ?, is_default = ?, region_priority = ?, format_preference = ?, prefer_1g1r = ?, allow_proto = ?, allow_demo = ?, allow_bios = ? WHERE id = ?",
		p.Name, string(rankingJSON), p.PreferredSizeMin, p.PreferredSizeMax, boolToInt(p.UpgradeAllowed), p.CutoffSource,
		p.PlatformSlug, boolToInt(p.IsDefault), string(regionsJSON), string(formatsJSON), boolToInt(p.Prefer1G1R),
		boolToInt(p.AllowProto), boolToInt(p.AllowDemo), boolToInt(p.AllowBIOS), p.ID,
	)
	if err != nil {
		return err
	}
	if p.IsDefault {
		s.clearOtherDefaults(p.ID)
	}
	return nil
}

// clearOtherDefaults keeps IsDefault unique: at most one global fallback row.
func (s *JobStore) clearOtherDefaults(exceptID int64) {
	s.db.Exec("UPDATE quality_profiles SET is_default = 0 WHERE id != ?", exceptID)
}

// DeleteQualityProfile removes a quality profile by ID.
func (s *JobStore) DeleteQualityProfile(id int64) error {
	_, err := s.db.Exec("DELETE FROM quality_profiles WHERE id = ?", id)
	return err
}
