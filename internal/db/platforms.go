package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"gamarr/internal/platform"
)

// The platform registry: one canonical vocabulary, keyed on our own slug,
// with every other name a platform answers to as a column on the row.
//
// Before this table the app carried nine separate hardcoded platform
// vocabularies — Prowlarr categories, a display-name map in the library scan,
// another in the archive.org driver, Newznab categories, the DAT lane seed,
// the RomM fs_slug alias, the CHD list and its hand-synced frontend copy —
// and nothing anchored them, so each new need minted another list. Readers
// converge here instead.
//
// Two things this table deliberately does NOT do:
//
//   - It does not absorb dat_platforms. It is already slug-keyed with its
//     own screen; the registry is the vocabulary it enumerates from, not a
//     replacement. The catalog vocabulary staying wider than the category map
//     is intentional (see internal/datsvc/validate.go) — this anchors that
//     divergence, it does not force a join.
//   - It does not replace the platform *parsers*. The extension map, the
//     title hints and the metadata-name aliases in platform.go turn a string
//     into a slug; they are parsers, not vocabulary, and they resolve into
//     these slugs. A test pins that every slug they can emit exists here.

// PlatformPatch is a sparse update: a nil field means "unchanged", the same
// shape SourcePatch uses.
type PlatformPatch struct {
	DisplayName         *string
	MediaClass          *string
	ConvertsToCHD       *bool
	AcquisitionEnabled  *bool
	CollectionMode      *bool
	DefaultProfileID    *int64
	CollectionProfileID *int64
}

func (s *JobStore) migratePlatforms() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platforms (
			slug TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			igdb_slug TEXT NOT NULL DEFAULT '',
			igdb_id INTEGER NOT NULL DEFAULT 0,
			romm_fs_slug TEXT NOT NULL DEFAULT '',
			prowlarr_categories TEXT NOT NULL DEFAULT '[]',
			torznab_category TEXT NOT NULL DEFAULT '',
			media_class TEXT NOT NULL DEFAULT '',
			converts_to_chd INTEGER NOT NULL DEFAULT 0,
			rename_frozen INTEGER NOT NULL DEFAULT 0,
			acquisition_enabled INTEGER NOT NULL DEFAULT 1,
			is_system INTEGER NOT NULL DEFAULT 0,
			default_profile_id INTEGER NOT NULL DEFAULT 0,
			collection_mode INTEGER NOT NULL DEFAULT 0,
			collection_profile_id INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		// The IGDB identity is unique where it is claimed at all. Rows without
		// one (a directory that is not a platform) are exempt rather than
		// colliding on the empty string.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_platforms_igdb ON platforms(igdb_slug) WHERE igdb_slug != ''`,
	}
	for _, ddl := range stmts {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate platforms", "error", err)
		}
	}
	// CREATE TABLE IF NOT EXISTS is a no-op on an existing install, so a new
	// column arrives by ALTER or not at all. Off by default: a platform
	// nobody has opted in must not start acquiring a whole catalog.
	if !s.columnExists("platforms", "rename_frozen") {
		if _, err := s.db.Exec(`ALTER TABLE platforms ADD COLUMN rename_frozen INTEGER NOT NULL DEFAULT 0`); err != nil {
			slog.Warn("migrate platforms rename_frozen", "error", err)
		} else if _, err := s.db.Exec(`UPDATE platforms SET rename_frozen = 1 WHERE slug IN
			('switch','switch2','wiiu','pc','ps3','ps4','ps5','xbox360','xboxone','arcade')`); err != nil {
			// Seeded at column birth, once ever — a backfill, not a
			// virgin-table guard, or every existing install stays unfrozen.
			slog.Warn("seed rename_frozen", "error", err)
		}
	}
	if !s.columnExists("platforms", "collection_mode") {
		if _, err := s.db.Exec(`ALTER TABLE platforms ADD COLUMN collection_mode INTEGER NOT NULL DEFAULT 0`); err != nil {
			slog.Warn("migrate platforms collection_mode", "error", err)
		}
	}
	// No backfill: 0 resolves to the built-in Standard collection profile,
	// which reproduces the pre-profile policy exactly (the fold-in migration
	// in collection_profiles.go re-points the platforms that were living a
	// non-default quality-profile tuple).
	if !s.columnExists("platforms", "collection_profile_id") {
		if _, err := s.db.Exec(`ALTER TABLE platforms ADD COLUMN collection_profile_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			slog.Warn("migrate platforms collection_profile_id", "error", err)
		}
	}
	s.seedPlatformDefaults()
}

// platformSeed is the shipped vocabulary. Display names are the ones the app
// already showed, so nothing renames under an operator; the IGDB identity,
// the RomM fs_slug and the media class were lifted from RomM's
// /api/platforms/supported (which is IGDB-backed) at authoring time and
// committed, because a fresh install and CI must be able to say what a
// platform is without a RomM to ask.
//
// Values worth not re-deriving from memory: psx is IGDB "ps", tg16 is
// "turbografx16--1", ps4 is "ps4--1", genesis is "genesis-slash-megadrive"
// (and that last one is also its on-disk directory).
//
// acquisition_enabled is derived on insert rather than repeated 37 times:
// every real platform ships acquirable, every system directory does not.
var platformSeed = []platform.Row{
	{Slug: "pc", DisplayName: "PC", IGDBSlug: "win", IGDBID: 6,
		RommFSSlug: "pc", ProwlarrCategories: []int{4000, 100010}, TorznabCategory: "4070", MediaClass: "pc", RenameFrozen: true},
	{Slug: "ps2", DisplayName: "PS2", IGDBSlug: "ps2", IGDBID: 8,
		RommFSSlug: "ps2", ProwlarrCategories: []int{100011}, TorznabCategory: "1090", MediaClass: "discs", ConvertsToCHD: true},
	{Slug: "psp", DisplayName: "PSP", IGDBSlug: "psp", IGDBID: 38,
		RommFSSlug: "psp", ProwlarrCategories: []int{100012}, TorznabCategory: "1020", MediaClass: "discs", ConvertsToCHD: true},
	{Slug: "xbox", DisplayName: "Xbox", IGDBSlug: "xbox", IGDBID: 11,
		RommFSSlug: "xbox", ProwlarrCategories: []int{100013}, TorznabCategory: "1040", MediaClass: "discs"},
	{Slug: "xbox360", DisplayName: "Xbox 360", IGDBSlug: "xbox360", IGDBID: 12,
		RommFSSlug: "xbox360", ProwlarrCategories: []int{100014}, TorznabCategory: "1050", MediaClass: "discs", RenameFrozen: true},
	{Slug: "psx", DisplayName: "PS1", IGDBSlug: "ps", IGDBID: 7,
		RommFSSlug: "psx", ProwlarrCategories: []int{100015}, TorznabCategory: "1090", MediaClass: "discs", ConvertsToCHD: true},
	{Slug: "dc", DisplayName: "Dreamcast", IGDBSlug: "dc", IGDBID: 23,
		RommFSSlug: "dc", ProwlarrCategories: []int{100016}, TorznabCategory: "1090", MediaClass: "discs", ConvertsToCHD: true},
	{Slug: "ps3", DisplayName: "PS3", IGDBSlug: "ps3", IGDBID: 9,
		RommFSSlug: "ps3", ProwlarrCategories: []int{100043}, TorznabCategory: "1080", MediaClass: "discs", RenameFrozen: true},
	{Slug: "wii", DisplayName: "Wii", IGDBSlug: "wii", IGDBID: 5,
		RommFSSlug: "wii", ProwlarrCategories: []int{100044}, TorznabCategory: "1030", MediaClass: "discs"},
	{Slug: "nds", DisplayName: "DS", IGDBSlug: "nds", IGDBID: 20,
		RommFSSlug: "nds", ProwlarrCategories: []int{100045}, TorznabCategory: "1010", MediaClass: "carts"},
	{Slug: "ngc", DisplayName: "GameCube", IGDBSlug: "ngc", IGDBID: 21,
		RommFSSlug: "ngc", ProwlarrCategories: []int{100046}, TorznabCategory: "1090", MediaClass: "discs"},
	{Slug: "3ds", DisplayName: "3DS", IGDBSlug: "3ds", IGDBID: 37,
		RommFSSlug: "3ds", ProwlarrCategories: []int{100072}, TorznabCategory: "1010", MediaClass: "carts"},
	{Slug: "ps4", DisplayName: "PS4", IGDBSlug: "ps4--1", IGDBID: 48,
		RommFSSlug: "ps4", ProwlarrCategories: []int{100077}, TorznabCategory: "1090", MediaClass: "discs", RenameFrozen: true},
	{Slug: "switch", DisplayName: "Switch", IGDBSlug: "switch", IGDBID: 130,
		RommFSSlug: "switch", ProwlarrCategories: []int{4050, 100082}, TorznabCategory: "1090", MediaClass: "carts", RenameFrozen: true},
	{Slug: "n64", DisplayName: "Nintendo 64", IGDBSlug: "n64", IGDBID: 4,
		RommFSSlug: "n64", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "snes", DisplayName: "SNES", IGDBSlug: "snes", IGDBID: 19,
		RommFSSlug: "snes", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "nes", DisplayName: "NES", IGDBSlug: "nes", IGDBID: 18,
		RommFSSlug: "nes", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "gb", DisplayName: "Game Boy", IGDBSlug: "gb", IGDBID: 33,
		RommFSSlug: "gb", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "gba", DisplayName: "Game Boy Advance", IGDBSlug: "gba", IGDBID: 24,
		RommFSSlug: "gba", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "genesis", DisplayName: "Sega Genesis", IGDBSlug: "genesis-slash-megadrive", IGDBID: 29,
		RommFSSlug: "genesis-slash-megadrive", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "saturn", DisplayName: "Sega Saturn", IGDBSlug: "saturn", IGDBID: 32,
		RommFSSlug: "saturn", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "discs"},
	{Slug: "wiiu", DisplayName: "Wii U", IGDBSlug: "wiiu", IGDBID: 41,
		RommFSSlug: "wiiu", ProwlarrCategories: nil, TorznabCategory: "1030", MediaClass: "discs", RenameFrozen: true},
	{Slug: "psvita", DisplayName: "PS Vita", IGDBSlug: "psvita", IGDBID: 46,
		RommFSSlug: "psvita", ProwlarrCategories: nil, TorznabCategory: "1020", MediaClass: "carts"},
	{Slug: "gbc", DisplayName: "Game Boy Color", IGDBSlug: "gbc", IGDBID: 22,
		RommFSSlug: "gbc", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "atari2600", DisplayName: "Atari 2600", IGDBSlug: "atari2600", IGDBID: 59,
		RommFSSlug: "atari2600", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "atari7800", DisplayName: "Atari 7800", IGDBSlug: "atari7800", IGDBID: 60,
		RommFSSlug: "atari7800", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "sms", DisplayName: "Sega Master System", IGDBSlug: "sms", IGDBID: 64,
		RommFSSlug: "sms", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "gamegear", DisplayName: "Sega Game Gear", IGDBSlug: "gamegear", IGDBID: 35,
		RommFSSlug: "gamegear", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "sega32", DisplayName: "Sega 32X", IGDBSlug: "sega32", IGDBID: 30,
		RommFSSlug: "sega32", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "tg16", DisplayName: "TurboGrafx-16", IGDBSlug: "turbografx16--1", IGDBID: 86,
		RommFSSlug: "tg16", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "lynx", DisplayName: "Atari Lynx", IGDBSlug: "lynx", IGDBID: 61,
		RommFSSlug: "lynx", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "virtualboy", DisplayName: "Virtual Boy", IGDBSlug: "virtualboy", IGDBID: 87,
		RommFSSlug: "virtualboy", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "wonderswan-color", DisplayName: "WonderSwan Color", IGDBSlug: "wonderswan-color", IGDBID: 123,
		RommFSSlug: "wonderswan-color", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "neo-geo-pocket-color", DisplayName: "Neo Geo Pocket Color", IGDBSlug: "neo-geo-pocket-color", IGDBID: 120,
		RommFSSlug: "neo-geo-pocket-color", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "colecovision", DisplayName: "ColecoVision", IGDBSlug: "colecovision", IGDBID: 68,
		RommFSSlug: "colecovision", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "carts"},
	{Slug: "apple-iigs", DisplayName: "Apple IIGS", IGDBSlug: "apple-iigs", IGDBID: 115,
		RommFSSlug: "apple-iigs", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "computer"},
	{Slug: "arcade", DisplayName: "Arcade", IGDBSlug: "arcade", IGDBID: 52,
		RommFSSlug: "arcade", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "arcade", RenameFrozen: true},
	{Slug: "forwarders", DisplayName: "Forwarders", IGDBSlug: "", IGDBID: 0,
		RommFSSlug: "forwarders", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "", IsSystem: true},
	{Slug: "other", DisplayName: "Other", IGDBSlug: "", IGDBID: 0,
		RommFSSlug: "other", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "", IsSystem: true},
	{Slug: "supporting_files", DisplayName: "Supporting Files", IGDBSlug: "", IGDBID: 0,
		RommFSSlug: "supporting_files", ProwlarrCategories: nil, TorznabCategory: "1090", MediaClass: "", IsSystem: true},
}

// seedPlatformDefaults populates the shipped vocabulary exactly once, on a
// table that has never been written — so an existing install keeps whatever
// its operator has edited, and a fresh one matches what ships.
func (s *JobStore) seedPlatformDefaults() {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM platforms").Scan(&count); err != nil || count > 0 {
		return
	}
	for _, p := range platformSeed {
		cats, _ := json.Marshal(p.ProwlarrCategories)
		if p.ProwlarrCategories == nil {
			cats = []byte("[]")
		}
		if _, err := s.db.Exec(`INSERT INTO platforms
			(slug, display_name, igdb_slug, igdb_id, romm_fs_slug, prowlarr_categories,
			 torznab_category, media_class, converts_to_chd, rename_frozen, acquisition_enabled, is_system)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.Slug, p.DisplayName, p.IGDBSlug, p.IGDBID, p.RommFSSlug, string(cats),
			p.TorznabCategory, p.MediaClass, boolInt(p.ConvertsToCHD), boolInt(p.RenameFrozen),
			boolInt(!p.IsSystem), boolInt(p.IsSystem)); err != nil {
			slog.Warn("seed platform", "slug", p.Slug, "error", err)
		}
	}
	slog.Info("seeded platform registry", "platforms", len(platformSeed))
}

func scanPlatformRow(rows *sql.Rows) (platform.Row, error) {
	var p platform.Row
	var cats string
	var chd, frozen, acq, sys, coll int
	err := rows.Scan(&p.Slug, &p.DisplayName, &p.IGDBSlug, &p.IGDBID, &p.RommFSSlug, &cats,
		&p.TorznabCategory, &p.MediaClass, &chd, &frozen, &acq, &sys, &p.DefaultProfileID, &coll,
		&p.CollectionProfileID, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.ConvertsToCHD, p.AcquisitionEnabled, p.IsSystem = chd == 1, acq == 1, sys == 1
	p.RenameFrozen = frozen == 1
	p.CollectionMode = coll == 1
	if cats != "" {
		_ = json.Unmarshal([]byte(cats), &p.ProwlarrCategories)
	}
	return p, nil
}

const platformCols = `slug, display_name, igdb_slug, igdb_id, romm_fs_slug, prowlarr_categories,
	torznab_category, media_class, converts_to_chd, rename_frozen, acquisition_enabled, is_system,
	default_profile_id, collection_mode, collection_profile_id, updated_at`

// PlatformRows implements platform.Registry.
func (s *JobStore) PlatformRows() []platform.Row {
	rows, err := s.db.Query(`SELECT ` + platformCols + ` FROM platforms ORDER BY slug`)
	if err != nil {
		slog.Warn("list platforms", "error", err)
		return nil
	}
	defer rows.Close()
	var out []platform.Row
	for rows.Next() {
		p, err := scanPlatformRow(rows)
		if err != nil {
			slog.Warn("scan platform", "error", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// GetPlatformRow returns one platform.
func (s *JobStore) GetPlatformRow(slug string) (platform.Row, bool) {
	rows, err := s.db.Query(`SELECT `+platformCols+` FROM platforms WHERE slug = ?`, slug)
	if err != nil {
		return platform.Row{}, false
	}
	defer rows.Close()
	if !rows.Next() {
		return platform.Row{}, false
	}
	p, err := scanPlatformRow(rows)
	return p, err == nil
}

// PatchPlatform applies a sparse update and drops the registry memo so the
// change is live without a restart.
func (s *JobStore) PatchPlatform(slug string, p PlatformPatch) error {
	if _, ok := s.GetPlatformRow(slug); !ok {
		return fmt.Errorf("unknown platform %q", slug)
	}
	var sets []string
	var args []interface{}
	if p.DisplayName != nil {
		sets, args = append(sets, "display_name = ?"), append(args, strings.TrimSpace(*p.DisplayName))
	}
	if p.MediaClass != nil {
		sets, args = append(sets, "media_class = ?"), append(args, *p.MediaClass)
	}
	if p.ConvertsToCHD != nil {
		sets, args = append(sets, "converts_to_chd = ?"), append(args, boolInt(*p.ConvertsToCHD))
	}
	if p.AcquisitionEnabled != nil {
		sets, args = append(sets, "acquisition_enabled = ?"), append(args, boolInt(*p.AcquisitionEnabled))
	}
	if p.CollectionMode != nil {
		sets, args = append(sets, "collection_mode = ?"), append(args, boolInt(*p.CollectionMode))
	}
	if p.DefaultProfileID != nil {
		sets, args = append(sets, "default_profile_id = ?"), append(args, *p.DefaultProfileID)
	}
	if p.CollectionProfileID != nil {
		sets, args = append(sets, "collection_profile_id = ?"), append(args, *p.CollectionProfileID)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = datetime('now')")
	args = append(args, slug)
	if _, err := s.db.Exec(`UPDATE platforms SET `+strings.Join(sets, ", ")+` WHERE slug = ?`, args...); err != nil {
		return err
	}
	platform.RefreshRegistry()
	return nil
}

// BackfillLibraryPlatformNames repairs display names the old lookups wrote.
// The archive.org driver stored the literal string "Unknown" on every import
// for a platform its category map had never carried, and those rows are read
// straight back out by the library filter — so a new read path alone would
// leave the wrong name on disk forever. Runs at boot, touches only rows that
// are demonstrably wrong, and reports how many it fixed.
func (s *JobStore) BackfillLibraryPlatformNames() int {
	rows, err := s.db.Query(`SELECT DISTINCT platform_slug FROM library_items
		WHERE platform_slug != '' AND (platform = '' OR platform = 'Unknown')`)
	if err != nil {
		slog.Warn("backfill platform names", "error", err)
		return 0
	}
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err == nil {
			slugs = append(slugs, slug)
		}
	}
	rows.Close()

	fixed := 0
	for _, slug := range slugs {
		row, ok := s.GetPlatformRow(slug)
		if !ok || row.DisplayName == "" {
			continue
		}
		res, err := s.db.Exec(`UPDATE library_items SET platform = ?
			WHERE platform_slug = ? AND (platform = '' OR platform = 'Unknown')`, row.DisplayName, slug)
		if err != nil {
			slog.Warn("backfill platform name", "slug", slug, "error", err)
			continue
		}
		n, _ := res.RowsAffected()
		fixed += int(n)
	}
	if fixed > 0 {
		slog.Info("repaired library platform names", "rows", fixed, "platforms", len(slugs))
	}
	return fixed
}
