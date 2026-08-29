package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// The DAT backbone: the preservation communities' catalog of which dumps
// exist, stored locally so RomArr can answer "what exists for this platform
// that I don't have?" without a live third-party call.
//
// Five tables:
//   - dat_authorities: one row per publisher (No-Intro / Redump / MAME).
//     fetch_driver and fetch_base are DATA, so repointing at a mirror, a CDN
//     or a hand-uploaded file is an edit rather than a deploy, and adding a
//     fourth authority (TOSEC) is an INSERT.
//   - dat_platforms: which authority owns each platform, plus the
//     driver-specific locator that names the platform upstream. Exactly one
//     authority per platform; an absent row means the platform has no DAT
//     lane (switch is shop-native, pc has none).
//   - dat_snapshots: one pinned fetch of one platform's catalog, carrying the
//     size distribution the selector's plausibility band is derived from and
//     the diff against the snapshot it replaced.
//   - dat_games / dat_roms: the catalog itself. Split because parent/clone
//     grouping is per game while hashes are per file, and because a disc's
//     true size is the sum of its .cue and every .bin track.
//
// Nothing here is a trust signal. The parsed catalog informs candidate
// scoring and coverage only; downloads are trusted solely by the
// post-extract hash gate.
//
// This package must never import internal/dat: the parser depends on
// internal/selection, which depends on this package. Row structs are
// therefore defined locally and the service layer maps between them.

// DatAuthorityRow is one publisher of DAT catalogs.
type DatAuthorityRow struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	Kind          string `json:"kind"`         // carts | discs | arcade | computers
	FetchDriver   string `json:"fetch_driver"` // libretro | redump | upload
	FetchBase     string `json:"fetch_base"`
	Enabled       bool   `json:"enabled"`
	PinnedVersion string `json:"pinned_version,omitempty"`
	LastRefresh   string `json:"last_refresh,omitempty"`
	LastStatus    string `json:"last_status,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// DatPlatformRow assigns a platform to exactly one authority. DatCode is the
// driver-specific locator: a Redump system code ("psx"), or the libretro
// mirror's DAT name without extension ("Atari - 2600").
type DatPlatformRow struct {
	PlatformSlug string `json:"platform_slug"`
	Authority    string `json:"authority"`
	DatCode      string `json:"dat_code"`
	Enabled      bool   `json:"enabled"`
	// CloneListName is the platform's clone-list locator, a SEPARATE upstream
	// vocabulary from DatCode (see cloneListSeed for the five that disagree).
	// Empty means the set groups by parsed title alone.
	CloneListName string `json:"clonelist_name,omitempty"`
}

// DatRomRow is one file of a dumped game.
type DatRomRow struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	CRC    string `json:"crc,omitempty"`
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	Serial string `json:"serial,omitempty"`
}

// DatGameRow is one catalogued dump.
type DatGameRow struct {
	// ID is the stored row id, set when a game is read back out. It is
	// ignored on insert (the snapshot writer assigns it).
	ID        int64       `json:"id,omitempty"`
	Name      string      `json:"name"`
	BareTitle string      `json:"bare_title,omitempty"`
	Region    string      `json:"region,omitempty"`
	Languages string      `json:"languages,omitempty"`
	Revision  int         `json:"revision,omitempty"`
	CloneOf   string      `json:"clone_of,omitempty"`
	Flags     string      `json:"flags,omitempty"`
	TotalSize int64       `json:"total_size"`
	Roms      []DatRomRow `json:"roms,omitempty"`
}

// DatSnapshotMeta is everything about a fetch except the catalog itself. The
// size statistics are computed by the parser and stored so the selector's
// bands are a column read rather than a scan.
type DatSnapshotMeta struct {
	Authority    string `json:"authority"`
	PlatformSlug string `json:"platform_slug"`
	Version      string `json:"version"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	RawPath      string `json:"raw_path,omitempty"`
	// ParserVersion is the derivation version the rows were imported under.
	// Stored so an unchanged-bytes check can still notice that the PARSER
	// moved: identical bytes plus a newer parser is a stale catalog, not an
	// up-to-date one.
	ParserVersion int   `json:"parser_version,omitempty"`
	SizeMin       int64 `json:"size_min"`
	SizeMax       int64 `json:"size_max"`
	SizeP01       int64 `json:"size_p01"`
	SizeP50       int64 `json:"size_p50"`
	SizeP99       int64 `json:"size_p99"`
}

// DatSnapshotRow is a stored snapshot.
type DatSnapshotRow struct {
	ID int64 `json:"id"`
	DatSnapshotMeta
	FetchedAt   string `json:"fetched_at"`
	GameCount   int    `json:"game_count"`
	RomCount    int    `json:"rom_count"`
	DiffAdded   int    `json:"diff_added"`
	DiffRemoved int    `json:"diff_removed"`
	DiffChanged int    `json:"diff_changed"`
	Active      bool   `json:"active"`
}

// DatCoverageRow reports owned-vs-known for one platform. Owned counts
// library rows; Known counts catalogued dumps. v1 deliberately does not match
// owned files against catalog entries, so these are two independent counts —
// present them as "N owned / M known", never as a completion percentage.
type DatCoverageRow struct {
	PlatformSlug    string `json:"platform_slug"`
	Authority       string `json:"authority,omitempty"`
	Owned           int    `json:"owned"`
	Known           int    `json:"known"`
	SnapshotVersion string `json:"snapshot_version,omitempty"`
	LastRefresh     string `json:"last_refresh,omitempty"`
}

// snapshotRetention is how many snapshots per platform survive a refresh: the
// active one plus its predecessor, so a bad advance can be rolled back
// without keeping every historical catalog's rows.
const snapshotRetention = 2

func (s *JobStore) migrateDat() {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS dat_authorities (
			name TEXT PRIMARY KEY,
			label TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			fetch_driver TEXT NOT NULL DEFAULT 'upload',
			fetch_base TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			pinned_version TEXT NOT NULL DEFAULT '',
			last_refresh TEXT NOT NULL DEFAULT '',
			last_status TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS dat_platforms (
			platform_slug TEXT PRIMARY KEY,
			authority TEXT NOT NULL,
			dat_code TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS dat_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			authority TEXT NOT NULL,
			platform_slug TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			source_sha256 TEXT NOT NULL DEFAULT '',
			raw_path TEXT NOT NULL DEFAULT '',
			fetched_at TEXT NOT NULL DEFAULT (datetime('now')),
			game_count INTEGER NOT NULL DEFAULT 0,
			rom_count INTEGER NOT NULL DEFAULT 0,
			size_min INTEGER NOT NULL DEFAULT 0,
			size_max INTEGER NOT NULL DEFAULT 0,
			size_p01 INTEGER NOT NULL DEFAULT 0,
			size_p50 INTEGER NOT NULL DEFAULT 0,
			size_p99 INTEGER NOT NULL DEFAULT 0,
			diff_added INTEGER NOT NULL DEFAULT 0,
			diff_removed INTEGER NOT NULL DEFAULT 0,
			diff_changed INTEGER NOT NULL DEFAULT 0,
			active INTEGER NOT NULL DEFAULT 0,
			parser_version INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS dat_games (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_id INTEGER NOT NULL,
			platform_slug TEXT NOT NULL,
			name TEXT NOT NULL,
			bare_title TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			languages TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 0,
			clone_of TEXT NOT NULL DEFAULT '',
			flags TEXT NOT NULL DEFAULT '',
			total_size INTEGER NOT NULL DEFAULT 0,
			rom_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS dat_roms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			game_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			crc TEXT NOT NULL DEFAULT '',
			md5 TEXT NOT NULL DEFAULT '',
			sha1 TEXT NOT NULL DEFAULT '',
			serial TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_games_snapshot ON dat_games(snapshot_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_games_platform_title ON dat_games(platform_slug, bare_title)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_roms_game ON dat_roms(game_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_roms_md5 ON dat_roms(md5)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_roms_sha1 ON dat_roms(sha1)`,
		`CREATE INDEX IF NOT EXISTS idx_dat_roms_crc ON dat_roms(crc)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_dat_snapshot_active ON dat_snapshots(platform_slug) WHERE active = 1`,
	}
	for _, ddl := range tables {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate dat table", "error", err)
		}
	}
	// CREATE TABLE IF NOT EXISTS is a no-op on an existing install. Defaulting
	// to 0 is deliberate: every snapshot imported before this column existed
	// predates the current parser, so each one re-imports on its next refresh.
	if !s.columnExists("dat_snapshots", "parser_version") {
		if _, err := s.db.Exec(`ALTER TABLE dat_snapshots ADD COLUMN parser_version INTEGER NOT NULL DEFAULT 0`); err != nil {
			slog.Warn("migrate dat_snapshots parser_version", "error", err)
		}
	}
	s.seedDatDefaults()
}

// datAuthoritySeed ships the three authorities. MAME is dormant: arcade is
// not a RomArr platform, so it carries no assignments and stays disabled
// until someone lights that lane up.
var datAuthoritySeed = []DatAuthorityRow{
	{
		Name: "no-intro", Label: "No-Intro", Kind: "carts",
		// No-Intro's own Dat-o-Matic is deliberately NOT automated: it bans
		// scrapers by IP, and every project in this space (datoso, Hasheous,
		// libretro-dats) keeps it human-in-the-loop. The libretro mirror
		// republishes the same data with full sizes and hashes; uploading a
		// daily pack by hand is the first-class alternative.
		FetchDriver: "libretro",
		FetchBase:   "https://raw.githubusercontent.com/libretro/libretro-database/master/metadat/no-intro/",
		Enabled:     true,
	},
	{
		Name: "redump", Label: "Redump", Kind: "discs",
		// redump.info, not redump.org: the .org host serves no TLS on 443 and
		// lags behind. Do not "fix" this to .org.
		FetchDriver: "redump",
		FetchBase:   "https://redump.info/datfile/",
		Enabled:     true,
	},
	{
		Name: "mame", Label: "MAME", Kind: "arcade",
		FetchDriver: "upload", FetchBase: "", Enabled: false,
	},
}

// datPlatformSeed assigns each platform we can currently catalogue. Cart
// platforms carry the libretro mirror's DAT name (without extension); disc
// platforms carry the Redump system code. Platforms with no DAT lane are
// absent by design: switch is shop-native, pc has no authority.
var datPlatformSeed = []DatPlatformRow{
	// Fields: slug, authority, dat_code, enabled, clonelist_name.
	// 🔴 dat_code and clonelist_name are DIFFERENT upstream vocabularies and
	// five pairs disagree — verified against both listings on 2026-08-19, not
	// remembered. Two are exact reversals of each other:
	//   "NEC - PC Engine - TurboGrafx 16" / "...TurboGrafx-16"      (hyphen)
	//   "SNK - Neo Geo Pocket Color"     / "SNK - NeoGeo Pocket Color" (spacing)
	// TestDatPlatformSeedVocabularies pins all five.
	// No-Intro — cartridges and handhelds.
	{"nes", "no-intro", "Nintendo - Nintendo Entertainment System", true, "Nintendo - Nintendo Entertainment System (No-Intro)"},
	{"snes", "no-intro", "Nintendo - Super Nintendo Entertainment System", true, "Nintendo - Super Nintendo Entertainment System (No-Intro)"},
	{"gb", "no-intro", "Nintendo - Game Boy", true, "Nintendo - Game Boy (No-Intro)"},
	{"gbc", "no-intro", "Nintendo - Game Boy Color", true, "Nintendo - Game Boy Color (No-Intro)"},
	{"gba", "no-intro", "Nintendo - Game Boy Advance", true, "Nintendo - Game Boy Advance (No-Intro)"},
	{"n64", "no-intro", "Nintendo - Nintendo 64", true, "Nintendo - Nintendo 64 (No-Intro)"},
	{"nds", "no-intro", "Nintendo - Nintendo DS", true, "Nintendo - Nintendo DS (No-Intro)"},
	{"3ds", "no-intro", "Nintendo - Nintendo 3DS", true, "Nintendo - Nintendo 3DS (No-Intro)"},
	{"genesis", "no-intro", "Sega - Mega Drive - Genesis", true, "Sega - Mega Drive - Genesis (No-Intro)"},
	{"atari2600", "no-intro", "Atari - 2600", true, "Atari - Atari 2600 (No-Intro)"},
	{"atari7800", "no-intro", "Atari - 7800", true, "Atari - Atari 7800 (No-Intro)"},
	{"sms", "no-intro", "Sega - Master System - Mark III", true, "Sega - Master System - Mark III (No-Intro)"},
	{"gamegear", "no-intro", "Sega - Game Gear", true, "Sega - Game Gear (No-Intro)"},
	{"sega32", "no-intro", "Sega - 32X", true, "Sega - 32X (No-Intro)"},
	{"tg16", "no-intro", "NEC - PC Engine - TurboGrafx 16", true, "NEC - PC Engine - TurboGrafx-16 (No-Intro)"},
	{"lynx", "no-intro", "Atari - Lynx", true, "Atari - Atari Lynx (No-Intro)"},
	{"virtualboy", "no-intro", "Nintendo - Virtual Boy", true, "Nintendo - Virtual Boy (No-Intro)"},
	{"wonderswan-color", "no-intro", "Bandai - WonderSwan Color", true, "Bandai - WonderSwan Color (No-Intro)"},
	{"neo-geo-pocket-color", "no-intro", "SNK - Neo Geo Pocket Color", true, "SNK - NeoGeo Pocket Color (No-Intro)"},
	{"colecovision", "no-intro", "Coleco - ColecoVision", true, "Coleco - ColecoVision (No-Intro)"},
	// Redump — optical media.
	{"psx", "redump", "psx", true, "Sony - PlayStation (Redump)"},
	{"ps2", "redump", "ps2", true, "Sony - PlayStation 2 (Redump)"},
	{"ps3", "redump", "ps3", true, "Sony - PlayStation 3 (Redump)"},
	{"psp", "redump", "psp", true, "Sony - PlayStation Portable (Redump)"},
	{"dc", "redump", "dc", true, "Sega - Dreamcast (Redump)"},
	{"saturn", "redump", "ss", true, "Sega - Saturn (Redump)"},
	{"ngc", "redump", "gc", true, "Nintendo - GameCube (Redump)"},
	{"wii", "redump", "wii", true, "Nintendo - Wii (Redump)"},
	{"xbox", "redump", "xbox", true, "Microsoft - Xbox (Redump)"},
	{"xbox360", "redump", "xbox360", true, "Microsoft - Xbox 360 (Redump)"},
}

// seedDatDefaults populates the shipped data pack exactly once, on a table
// that has never been written. After that the DB is authoritative and an
// operator's edits survive upgrades.
func (s *JobStore) seedDatDefaults() {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM dat_authorities").Scan(&count)
	if count > 0 {
		return
	}
	for _, a := range datAuthoritySeed {
		if _, err := s.db.Exec(`INSERT INTO dat_authorities
			(name, label, kind, fetch_driver, fetch_base, enabled)
			VALUES (?, ?, ?, ?, ?, ?)`,
			a.Name, a.Label, a.Kind, a.FetchDriver, a.FetchBase, boolInt(a.Enabled)); err != nil {
			slog.Warn("seed dat authority", "name", a.Name, "error", err)
		}
	}
	for _, p := range datPlatformSeed {
		if _, err := s.db.Exec(`INSERT INTO dat_platforms
			(platform_slug, authority, dat_code, enabled) VALUES (?, ?, ?, ?)`,
			p.PlatformSlug, p.Authority, p.DatCode, boolInt(p.Enabled)); err != nil {
			slog.Warn("seed dat platform", "slug", p.PlatformSlug, "error", err)
		}
	}
	slog.Info("seeded DAT authorities and platform assignments",
		"authorities", len(datAuthoritySeed), "platforms", len(datPlatformSeed))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListDatAuthorities returns every authority row, stably ordered.
func (s *JobStore) ListDatAuthorities() []DatAuthorityRow {
	rows, err := s.db.Query(`SELECT name, label, kind, fetch_driver, fetch_base, enabled,
		pinned_version, last_refresh, last_status, last_error FROM dat_authorities ORDER BY name`)
	if err != nil {
		slog.Warn("list dat authorities", "error", err)
		return nil
	}
	defer rows.Close()
	var out []DatAuthorityRow
	for rows.Next() {
		var a DatAuthorityRow
		var en int
		if err := rows.Scan(&a.Name, &a.Label, &a.Kind, &a.FetchDriver, &a.FetchBase, &en,
			&a.PinnedVersion, &a.LastRefresh, &a.LastStatus, &a.LastError); err != nil {
			continue
		}
		a.Enabled = en != 0
		out = append(out, a)
	}
	return out
}

// GetDatAuthority returns one authority; unknown names error.
func (s *JobStore) GetDatAuthority(name string) (DatAuthorityRow, error) {
	var a DatAuthorityRow
	var en int
	err := s.db.QueryRow(`SELECT name, label, kind, fetch_driver, fetch_base, enabled,
		pinned_version, last_refresh, last_status, last_error FROM dat_authorities WHERE name = ?`, name).
		Scan(&a.Name, &a.Label, &a.Kind, &a.FetchDriver, &a.FetchBase, &en,
			&a.PinnedVersion, &a.LastRefresh, &a.LastStatus, &a.LastError)
	if err != nil {
		return DatAuthorityRow{}, fmt.Errorf("unknown DAT authority %q", name)
	}
	a.Enabled = en != 0
	return a, nil
}

// DatAuthorityPatch is a sparse update; nil fields are left unchanged.
type DatAuthorityPatch struct {
	Enabled       *bool
	FetchDriver   *string
	FetchBase     *string
	PinnedVersion *string
}

// UpdateDatAuthority applies a sparse patch. Swapping an authority's transport
// — mirror, CDN, self-hosted clone, hand upload — happens here rather than in
// a deploy.
func (s *JobStore) UpdateDatAuthority(name string, p DatAuthorityPatch) error {
	cur, err := s.GetDatAuthority(name)
	if err != nil {
		return err
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.FetchDriver != nil {
		cur.FetchDriver = *p.FetchDriver
	}
	if p.FetchBase != nil {
		cur.FetchBase = *p.FetchBase
	}
	if p.PinnedVersion != nil {
		cur.PinnedVersion = *p.PinnedVersion
	}
	_, err = s.db.Exec(`UPDATE dat_authorities SET enabled = ?, fetch_driver = ?, fetch_base = ?,
		pinned_version = ?, updated_at = datetime('now') WHERE name = ?`,
		boolInt(cur.Enabled), cur.FetchDriver, cur.FetchBase, cur.PinnedVersion, name)
	return err
}

// SetDatRefreshResult records the outcome of a refresh attempt. A failure
// leaves the active snapshot in place — stale data beats no data.
func (s *JobStore) SetDatRefreshResult(name, status, errMsg string, at time.Time) {
	_, err := s.db.Exec(`UPDATE dat_authorities SET last_refresh = ?, last_status = ?, last_error = ?,
		updated_at = datetime('now') WHERE name = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), status, errMsg, name)
	if err != nil {
		slog.Warn("record dat refresh result", "authority", name, "error", err)
	}
}

// ListDatPlatforms returns every platform assignment, stably ordered.
func (s *JobStore) ListDatPlatforms() []DatPlatformRow {
	rows, err := s.db.Query(`SELECT platform_slug, authority, dat_code, enabled, clonelist_name
		FROM dat_platforms ORDER BY platform_slug`)
	if err != nil {
		slog.Warn("list dat platforms", "error", err)
		return nil
	}
	defer rows.Close()
	var out []DatPlatformRow
	for rows.Next() {
		var p DatPlatformRow
		var en int
		if err := rows.Scan(&p.PlatformSlug, &p.Authority, &p.DatCode, &en, &p.CloneListName); err != nil {
			continue
		}
		p.Enabled = en != 0
		out = append(out, p)
	}
	return out
}

// SetDatPlatform assigns a platform to an authority with its driver-specific
// locator. Both move together: a code that belongs to a different driver
// would fail only at fetch time, so callers validate the pair before saving.
func (s *JobStore) SetDatPlatform(p DatPlatformRow) error {
	if strings.TrimSpace(p.PlatformSlug) == "" {
		return fmt.Errorf("platform slug required")
	}
	if _, err := s.GetDatAuthority(p.Authority); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT INTO dat_platforms (platform_slug, authority, dat_code, enabled, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(platform_slug) DO UPDATE SET authority = excluded.authority,
			dat_code = excluded.dat_code, enabled = excluded.enabled, updated_at = excluded.updated_at`,
		p.PlatformSlug, p.Authority, p.DatCode, boolInt(p.Enabled)); err != nil {
		return err
	}
	// The clone-list locator is deliberately NOT in the upsert's column list,
	// so editing an assignment never clears it. An explicit value is applied;
	// otherwise the shipped locator fills an empty one — which is what makes a
	// lane lit up at runtime (nine of them were, on 2026-08-17, with no code
	// change) arrive with its clone list rather than silently without.
	if name := strings.TrimSpace(p.CloneListName); name != "" {
		return s.SetCloneListName(p.PlatformSlug, name)
	}
	s.seedCloneListName(p.PlatformSlug)
	return nil
}

// InsertDatSnapshot stores a freshly parsed catalog, makes it the active
// snapshot for its platform, records what changed against the snapshot it
// replaces, and prunes history beyond snapshotRetention. The whole write is
// one transaction: a partially imported catalog would silently corrupt both
// the coverage counts and the selector's size bands.
func (s *JobStore) InsertDatSnapshot(meta DatSnapshotMeta, games []DatGameRow) (DatSnapshotRow, error) {
	if strings.TrimSpace(meta.PlatformSlug) == "" || strings.TrimSpace(meta.Authority) == "" {
		return DatSnapshotRow{}, fmt.Errorf("snapshot needs an authority and a platform")
	}
	prev := s.datGameSignatures(meta.PlatformSlug)
	added, removed, changed := diffCatalog(prev, games)

	tx, err := s.db.Begin()
	if err != nil {
		return DatSnapshotRow{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE dat_snapshots SET active = 0 WHERE platform_slug = ?`, meta.PlatformSlug); err != nil {
		return DatSnapshotRow{}, err
	}
	romTotal := 0
	for _, g := range games {
		romTotal += len(g.Roms)
	}
	res, err := tx.Exec(`INSERT INTO dat_snapshots
		(authority, platform_slug, version, source_sha256, raw_path, fetched_at,
		 game_count, rom_count, size_min, size_max, size_p01, size_p50, size_p99,
		 diff_added, diff_removed, diff_changed, active, parser_version)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		meta.Authority, meta.PlatformSlug, meta.Version, meta.SourceSHA256, meta.RawPath,
		len(games), romTotal, meta.SizeMin, meta.SizeMax, meta.SizeP01, meta.SizeP50, meta.SizeP99,
		added, removed, changed, meta.ParserVersion)
	if err != nil {
		return DatSnapshotRow{}, err
	}
	snapID, err := res.LastInsertId()
	if err != nil {
		return DatSnapshotRow{}, err
	}

	insGame, err := tx.Prepare(`INSERT INTO dat_games
		(snapshot_id, platform_slug, name, bare_title, region, languages, revision, clone_of, flags, total_size, rom_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return DatSnapshotRow{}, err
	}
	defer insGame.Close()
	insRom, err := tx.Prepare(`INSERT INTO dat_roms (game_id, name, size, crc, md5, sha1, serial)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return DatSnapshotRow{}, err
	}
	defer insRom.Close()

	for _, g := range games {
		gr, err := insGame.Exec(snapID, meta.PlatformSlug, g.Name, g.BareTitle, g.Region,
			g.Languages, g.Revision, g.CloneOf, g.Flags, g.TotalSize, len(g.Roms))
		if err != nil {
			return DatSnapshotRow{}, err
		}
		gid, err := gr.LastInsertId()
		if err != nil {
			return DatSnapshotRow{}, err
		}
		for _, r := range g.Roms {
			if _, err := insRom.Exec(gid, r.Name, r.Size, r.CRC, r.MD5, r.SHA1, r.Serial); err != nil {
				return DatSnapshotRow{}, err
			}
		}
	}

	if err := pruneSnapshots(tx, meta.PlatformSlug); err != nil {
		return DatSnapshotRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return DatSnapshotRow{}, err
	}

	out := DatSnapshotRow{
		ID: snapID, DatSnapshotMeta: meta,
		GameCount: len(games), RomCount: romTotal,
		DiffAdded: added, DiffRemoved: removed, DiffChanged: changed, Active: true,
	}
	return out, nil
}

// datGameSignatures fingerprints the currently active catalog so a refresh can
// report what actually moved. The signature folds size and hashes together, so
// a re-dump of the same title counts as changed rather than unchanged.
func (s *JobStore) datGameSignatures(slug string) map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT g.name, g.total_size, COALESCE(GROUP_CONCAT(r.sha1), '')
		FROM dat_games g
		JOIN dat_snapshots s ON s.id = g.snapshot_id AND s.active = 1
		LEFT JOIN dat_roms r ON r.game_id = g.id
		WHERE g.platform_slug = ?
		GROUP BY g.id`, slug)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name, sha string
		var size int64
		if err := rows.Scan(&name, &size, &sha); err != nil {
			continue
		}
		out[name] = catalogSignature(size, strings.Split(sha, ","))
	}
	return out
}

// catalogSignature folds a game's size and hash set into one comparable
// string. The hashes are sorted so the signature does not depend on rom
// ordering, which SQLite's GROUP_CONCAT does not guarantee.
func catalogSignature(size int64, hashes []string) string {
	clean := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if h = strings.TrimSpace(h); h != "" {
			clean = append(clean, h)
		}
	}
	sort.Strings(clean)
	return fmt.Sprintf("%d|%s", size, strings.Join(clean, ","))
}

func diffCatalog(prev map[string]string, games []DatGameRow) (added, removed, changed int) {
	seen := make(map[string]struct{}, len(games))
	for _, g := range games {
		hashes := make([]string, 0, len(g.Roms))
		for _, r := range g.Roms {
			hashes = append(hashes, r.SHA1)
		}
		sig := catalogSignature(g.TotalSize, hashes)
		seen[g.Name] = struct{}{}
		old, ok := prev[g.Name]
		switch {
		case !ok:
			added++
		case old != sig:
			changed++
		}
	}
	for name := range prev {
		if _, ok := seen[name]; !ok {
			removed++
		}
	}
	return added, removed, changed
}

// pruneSnapshots drops everything older than the retained window, including
// the catalog rows those snapshots own (no FK cascade is configured).
func pruneSnapshots(tx *sql.Tx, slug string) error {
	rows, err := tx.Query(`SELECT id FROM dat_snapshots WHERE platform_slug = ?
		ORDER BY id DESC LIMIT -1 OFFSET ?`, slug, snapshotRetention)
	if err != nil {
		return err
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.Exec(`DELETE FROM dat_roms WHERE game_id IN (SELECT id FROM dat_games WHERE snapshot_id = ?)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dat_games WHERE snapshot_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dat_snapshots WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ActiveDatSnapshot returns the live snapshot for a platform, if any.
func (s *JobStore) ActiveDatSnapshot(slug string) (DatSnapshotRow, bool) {
	var r DatSnapshotRow
	var active int
	err := s.db.QueryRow(`SELECT id, authority, platform_slug, version, source_sha256, raw_path,
		fetched_at, game_count, rom_count, size_min, size_max, size_p01, size_p50, size_p99,
		diff_added, diff_removed, diff_changed, active, parser_version
		FROM dat_snapshots WHERE platform_slug = ? AND active = 1`, slug).
		Scan(&r.ID, &r.Authority, &r.PlatformSlug, &r.Version, &r.SourceSHA256, &r.RawPath,
			&r.FetchedAt, &r.GameCount, &r.RomCount, &r.SizeMin, &r.SizeMax, &r.SizeP01, &r.SizeP50, &r.SizeP99,
			&r.DiffAdded, &r.DiffRemoved, &r.DiffChanged, &active, &r.ParserVersion)
	if err != nil {
		return DatSnapshotRow{}, false
	}
	r.Active = active != 0
	return r, true
}

// DatCoverage reports owned-vs-known per platform, covering every platform
// that either has a library or has a catalog.
func (s *JobStore) DatCoverage() []DatCoverageRow {
	owned := s.LibraryStats()
	byslug := map[string]*DatCoverageRow{}
	for slug, n := range owned {
		byslug[slug] = &DatCoverageRow{PlatformSlug: slug, Owned: n}
	}
	rows, err := s.db.Query(`SELECT platform_slug, authority, version, fetched_at, game_count
		FROM dat_snapshots WHERE active = 1`)
	if err != nil {
		slog.Warn("dat coverage", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var slug, authority, version, fetched string
			var known int
			if err := rows.Scan(&slug, &authority, &version, &fetched, &known); err != nil {
				continue
			}
			r, ok := byslug[slug]
			if !ok {
				r = &DatCoverageRow{PlatformSlug: slug}
				byslug[slug] = r
			}
			r.Authority, r.Known, r.SnapshotVersion, r.LastRefresh = authority, known, version, fetched
		}
	}
	out := make([]DatCoverageRow, 0, len(byslug))
	for _, r := range byslug {
		out = append(out, *r)
	}
	sortCoverage(out)
	return out
}

func sortCoverage(rows []DatCoverageRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].PlatformSlug < rows[j].PlatformSlug })
}

// ── Reading the catalog ────────────────────────────────────────────────────
//
// The DAT plane was write-only until now: refresh imported catalogs and
// coverage counted them, but nothing could ask "what dumps exist for this
// platform?" or "is this file one of them?". Those two questions are what the
// catalog is FOR — the browse door and the trust gate — so they get real
// queries against the active snapshot rather than a scan.

// DatGameQuery filters a catalog browse.
type DatGameQuery struct {
	PlatformSlug string
	Text         string // matches bare_title or name, case-insensitively
	Limit        int
	Offset       int
}

// BrowseDatGames lists catalogued dumps for a platform's ACTIVE snapshot,
// newest catalog only, ordered by name. The second return is the total
// matching count (for pagination), not the page length.
func (s *JobStore) BrowseDatGames(q DatGameQuery) ([]DatGameRow, int) {
	if q.PlatformSlug == "" {
		return nil, 0
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	where := "g.platform_slug = ? AND s.active = 1"
	args := []interface{}{q.PlatformSlug}
	if text := strings.TrimSpace(q.Text); text != "" {
		where += " AND (LOWER(g.bare_title) LIKE ? OR LOWER(g.name) LIKE ?)"
		like := "%" + strings.ToLower(text) + "%"
		args = append(args, like, like)
	}

	var total int
	s.db.QueryRow(
		"SELECT COUNT(*) FROM dat_games g JOIN dat_snapshots s ON s.id = g.snapshot_id WHERE "+where,
		args...,
	).Scan(&total)

	rows, err := s.db.Query(
		`SELECT g.id, g.name, g.bare_title, g.region, g.languages, g.revision, g.clone_of,
		        g.flags, g.total_size, g.rom_count
		   FROM dat_games g JOIN dat_snapshots s ON s.id = g.snapshot_id
		  WHERE `+where+` ORDER BY g.name LIMIT ? OFFSET ?`,
		append(args, q.Limit, q.Offset)...,
	)
	if err != nil {
		return nil, total
	}
	defer rows.Close()
	var out []DatGameRow
	for rows.Next() {
		var g DatGameRow
		var romCount int
		if err := rows.Scan(&g.ID, &g.Name, &g.BareTitle, &g.Region, &g.Languages,
			&g.Revision, &g.CloneOf, &g.Flags, &g.TotalSize, &romCount); err != nil {
			continue
		}
		out = append(out, g)
	}
	return out, total
}

// DatGameRoms returns the files of one catalogued dump. A disc is several
// rows (cue plus every track), which is why this is a separate call rather
// than a column on the game.
func (s *JobStore) DatGameRoms(gameID int64) []DatRomRow {
	rows, err := s.db.Query(
		"SELECT name, size, crc, md5, sha1, serial FROM dat_roms WHERE game_id = ? ORDER BY name", gameID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DatRomRow
	for rows.Next() {
		var r DatRomRow
		if err := rows.Scan(&r.Name, &r.Size, &r.CRC, &r.MD5, &r.SHA1, &r.Serial); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Catalog verdicts for one file measured against the active snapshot.
const (
	// CatalogVerified: a catalogued dump has this file's hash.
	CatalogVerified = "verified"
	// CatalogMismatch: the catalog knows a file by this name for this
	// platform and its hash is NOT this one. The catalog disagrees.
	CatalogMismatch = "mismatch"
	// CatalogUnknown: nothing in the catalog matches by hash or by name.
	// Not a failure — hacks, homebrew, translations and dumps newer than the
	// snapshot all land here, and on some platforms they outnumber the
	// catalogued ones several times over.
	CatalogUnknown = "unknown"
)

// DatVerdict is the catalog's answer about one file.
type DatVerdict struct {
	Status   string `json:"status"`
	GameName string `json:"game_name,omitempty"`
	RomName  string `json:"rom_name,omitempty"`
	// Expected is the hash the catalog holds for the name that matched, set
	// only on a mismatch — it is the evidence for the rejection.
	Expected string `json:"expected,omitempty"`
	Got      string `json:"got,omitempty"`
}

// MatchDatRom measures one extracted file against the platform's active
// catalog. Hashes are compared lowercase; pass whichever the caller computed
// (crc32 is No-Intro's canonical, md5/sha1 are Redump's and both carry it).
//
// Order matters: a hash hit is the strongest possible answer and is checked
// first, so a correctly-dumped-but-renamed file is verified rather than
// accused. Only when no hash matches does the name lookup decide between
// "the catalog disagrees" and "the catalog has never heard of this".
func (s *JobStore) MatchDatRom(platformSlug, fileName, crc, md5, sha1 string) DatVerdict {
	if platformSlug == "" {
		return DatVerdict{Status: CatalogUnknown}
	}
	crc, md5, sha1 = strings.ToLower(crc), strings.ToLower(md5), strings.ToLower(sha1)
	if crc == "" && md5 == "" && sha1 == "" {
		// Nothing to compare. A file we could not hash is not evidence of
		// anything — accusing it because its NAME is catalogued would reject
		// good imports on an unreadable-file error.
		return DatVerdict{Status: CatalogUnknown}
	}

	// Built from a slice, not a map: the clauses and their arguments are two
	// halves of one list, and iterating a map would pair them by luck.
	var hashClauses []string
	args := []interface{}{platformSlug}
	for _, h := range []struct{ col, val string }{{"crc", crc}, {"md5", md5}, {"sha1", sha1}} {
		if h.val != "" {
			hashClauses = append(hashClauses, "(r."+h.col+" = ? AND r."+h.col+" != '')")
			args = append(args, h.val)
		}
	}
	if len(hashClauses) > 0 {
		var gameName, romName string
		err := s.db.QueryRow(
			`SELECT g.name, r.name FROM dat_roms r
			   JOIN dat_games g ON g.id = r.game_id
			   JOIN dat_snapshots s ON s.id = g.snapshot_id
			  WHERE g.platform_slug = ? AND s.active = 1 AND (`+strings.Join(hashClauses, " OR ")+`) LIMIT 1`,
			args...,
		).Scan(&gameName, &romName)
		if err == nil {
			return DatVerdict{Status: CatalogVerified, GameName: gameName, RomName: romName}
		}
	}

	if fileName == "" {
		return DatVerdict{Status: CatalogUnknown}
	}
	var gameName, romName, wantCRC, wantMD5, wantSHA1 string
	err := s.db.QueryRow(
		`SELECT g.name, r.name, r.crc, r.md5, r.sha1 FROM dat_roms r
		   JOIN dat_games g ON g.id = r.game_id
		   JOIN dat_snapshots s ON s.id = g.snapshot_id
		  WHERE g.platform_slug = ? AND s.active = 1 AND LOWER(r.name) = ? LIMIT 1`,
		platformSlug, strings.ToLower(fileName),
	).Scan(&gameName, &romName, &wantCRC, &wantMD5, &wantSHA1)
	if err != nil {
		return DatVerdict{Status: CatalogUnknown}
	}
	// Same name, and no hash of ours matched it: the catalog disagrees about
	// what this file should contain.
	expected, got := wantCRC, crc
	if expected == "" {
		expected, got = wantMD5, md5
	}
	if expected == "" {
		expected, got = wantSHA1, sha1
	}
	return DatVerdict{
		Status: CatalogMismatch, GameName: gameName, RomName: romName,
		Expected: expected, Got: got,
	}
}

// DatRomMatch is one active-snapshot hash hit, for naming. Where MatchDatRom
// answers "is this the real dump?" with a single verdict, this returns EVERY
// row the hashes land on so a caller deciding a FILENAME can see a tie
// (header twins, compilation extractions) instead of inheriting whichever
// row SQLite returns first.
type DatRomMatch struct {
	GameID   int64
	GameName string
	RomName  string // dat_roms.name — the canonical file name, extension included
}

// LookupDatRomsByHash returns every rom in the platform's active snapshot
// whose crc/md5/sha1 equals one of the given values, in deterministic
// (game name, rom name) order so ties resolve the same way every run.
// Nil when the slug is empty or no hash is given.
func (s *JobStore) LookupDatRomsByHash(platformSlug, crc, md5, sha1 string) []DatRomMatch {
	if platformSlug == "" {
		return nil
	}
	crc, md5, sha1 = strings.ToLower(crc), strings.ToLower(md5), strings.ToLower(sha1)
	// Built from a slice, not a map: the clauses and their arguments are two
	// halves of one list, and iterating a map would pair them by luck.
	var hashClauses []string
	args := []interface{}{platformSlug}
	for _, h := range []struct{ col, val string }{{"crc", crc}, {"md5", md5}, {"sha1", sha1}} {
		if h.val != "" {
			hashClauses = append(hashClauses, "(r."+h.col+" = ? AND r."+h.col+" != '')")
			args = append(args, h.val)
		}
	}
	if len(hashClauses) == 0 {
		return nil
	}
	rows, err := s.db.Query(
		`SELECT g.id, g.name, r.name FROM dat_roms r
		   JOIN dat_games g ON g.id = r.game_id
		   JOIN dat_snapshots s ON s.id = g.snapshot_id
		  WHERE g.platform_slug = ? AND s.active = 1 AND (`+strings.Join(hashClauses, " OR ")+`)
		  ORDER BY g.name, r.name`,
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DatRomMatch
	for rows.Next() {
		var m DatRomMatch
		if err := rows.Scan(&m.GameID, &m.GameName, &m.RomName); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// SetLibraryCatalogStatus records a catalog verdict on the library row a
// source produced. Kept separate from AddLibraryItem so the verdict can be
// written after the import without threading a new argument through every
// TrackInLibrary caller.
func (s *JobStore) SetLibraryCatalogStatus(sourceID, status string) {
	if sourceID == "" || status == "" {
		return
	}
	_, err := s.db.Exec(
		`UPDATE library_items
		    SET metadata = json_set(
		          CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,
		          '$.gamarr.catalog', ?)
		  WHERE source_id = ?`, status, sourceID)
	if err != nil {
		slog.Warn("record catalog status", "error", err)
	}
}

// SetSnapshotParserVersion rewrites a snapshot's derivation stamp. Tests use it
// to age a catalog; nothing in the app does, because the stamp is written by
// the import that produced the rows.
func (s *JobStore) SetSnapshotParserVersion(id int64, version int) error {
	_, err := s.db.Exec(`UPDATE dat_snapshots SET parser_version = ? WHERE id = ?`, version, id)
	return err
}
