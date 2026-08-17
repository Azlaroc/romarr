package db

import (
	"fmt"
	"log/slog"
	"strings"
)

// Per-platform size definitions: the plausibility band a candidate's size is
// judged against, one row per platform.
//
// This is the arr "quality definitions" table, keyed by platform rather than
// by quality tier. It replaced a hardcoded 27-entry map whose slug vocabulary
// never matched the app's canonical slugs, so the platforms it claimed to
// cover silently fell through to a generic band that rejected every genuine
// cartridge dump as implausibly small. The governing rule that came out of
// that incident: RomArr may reject on size, but never on a number nobody can
// see. Hence a table an operator can read and edit, not a constant.
//
// Two conventions carry the design:
//
//   - Zero means unlimited, per end. A floor of 0 imposes no floor; a ceiling
//     of 0 imposes no ceiling. That is how "we have no data for this platform,
//     so we decline to measure it" is expressed, and it is why an absent row
//     is equivalent to a row of zeroes. Platforms with no DAT lane (arcade,
//     apple-iigs, switch, wiiu, pc) simply have no row.
//
//   - The stored numbers are the numbers that enforce. DAT catalogs measure
//     uncompressed inner content while candidates are archives, so a floor
//     needs headroom — but that headroom is applied once, here, when the row
//     is derived. Nothing widens or narrows these values at the point of use.
//     What a screen displays is exactly what rejects.
//
// Deliberately not folded into dat_platforms, the only other slug-keyed
// table: its primary key exists only for platforms assigned to an authority,
// whereas a definition has to be settable for a platform that has no DAT lane
// and never will.
const (
	// SizeSourceCatalog marks a row derived from a DAT snapshot. Rows like
	// this are recomputed on every import.
	SizeSourceCatalog = "catalog"
	// SizeSourceManual marks a row an operator set by hand. A refresh must
	// never overwrite it — that is what makes the table an override surface
	// rather than a cache.
	SizeSourceManual = "manual"
)

// PlatformSizeRow is one platform's size definition. MinSize and MaxSize are
// bytes; zero means "no limit on this end" rather than "zero bytes".
type PlatformSizeRow struct {
	PlatformSlug    string `json:"platform_slug"`
	MinSize         int64  `json:"min_size"`
	MaxSize         int64  `json:"max_size"`
	Source          string `json:"source"`
	SnapshotVersion string `json:"snapshot_version,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

func (s *JobStore) migratePlatformSizes() {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS platform_size_definitions (
			platform_slug TEXT PRIMARY KEY,
			min_size INTEGER NOT NULL DEFAULT 0,
			max_size INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			snapshot_version TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, ddl := range tables {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate platform size table", "error", err)
		}
	}
}

// ListPlatformSizes returns every stored definition, stably ordered.
func (s *JobStore) ListPlatformSizes() []PlatformSizeRow {
	rows, err := s.db.Query(`SELECT platform_slug, min_size, max_size, source, snapshot_version, updated_at
		FROM platform_size_definitions ORDER BY platform_slug`)
	if err != nil {
		slog.Warn("list platform sizes", "error", err)
		return nil
	}
	defer rows.Close()
	var out []PlatformSizeRow
	for rows.Next() {
		var p PlatformSizeRow
		if err := rows.Scan(&p.PlatformSlug, &p.MinSize, &p.MaxSize, &p.Source, &p.SnapshotVersion, &p.UpdatedAt); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// GetPlatformSize returns one platform's definition. The bool reports whether
// a row exists at all, which callers must not conflate with an all-zero row:
// both mean "no limits", but only a stored row survives a catalog refresh
// when it is manual.
func (s *JobStore) GetPlatformSize(slug string) (PlatformSizeRow, bool) {
	var p PlatformSizeRow
	err := s.db.QueryRow(`SELECT platform_slug, min_size, max_size, source, snapshot_version, updated_at
		FROM platform_size_definitions WHERE platform_slug = ?`, slug).
		Scan(&p.PlatformSlug, &p.MinSize, &p.MaxSize, &p.Source, &p.SnapshotVersion, &p.UpdatedAt)
	if err != nil {
		return PlatformSizeRow{}, false
	}
	return p, true
}

// SetPlatformSize writes a definition, replacing whatever was there.
// Negative bounds are rejected rather than clamped: zero already means
// "unlimited", so a negative value is a caller bug and silently coercing it
// would hide the bug behind a band that quietly stops enforcing.
func (s *JobStore) SetPlatformSize(p PlatformSizeRow) error {
	slug := strings.TrimSpace(p.PlatformSlug)
	if slug == "" {
		return fmt.Errorf("platform slug required")
	}
	if p.MinSize < 0 || p.MaxSize < 0 {
		return fmt.Errorf("size bounds cannot be negative")
	}
	if p.MinSize > 0 && p.MaxSize > 0 && p.MinSize > p.MaxSize {
		return fmt.Errorf("minimum size %d exceeds maximum %d", p.MinSize, p.MaxSize)
	}
	if p.Source == SizeSourceManual {
		// Provenance is cleared structurally rather than left to callers: the
		// numbers are no longer the catalog's, and a stale version string
		// would render on screen as a claim the row cannot support.
		p.SnapshotVersion = ""
	}
	_, err := s.db.Exec(`INSERT INTO platform_size_definitions
		(platform_slug, min_size, max_size, source, snapshot_version, updated_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(platform_slug) DO UPDATE SET min_size = excluded.min_size,
			max_size = excluded.max_size, source = excluded.source,
			snapshot_version = excluded.snapshot_version, updated_at = excluded.updated_at`,
		slug, p.MinSize, p.MaxSize, p.Source, p.SnapshotVersion)
	return err
}

// DeletePlatformSize removes a definition, returning the platform to "no
// limits". This is the reset path for a platform with no active snapshot to
// re-derive from — the row goes away rather than lingering as zeroes, so the
// absence of an opinion is visible as an absence.
func (s *JobStore) DeletePlatformSize(slug string) error {
	_, err := s.db.Exec(`DELETE FROM platform_size_definitions WHERE platform_slug = ?`, slug)
	return err
}

// PlatformSizeBands returns every definition as a {min, max} pair keyed by
// slug, for the selector to arm itself with in one read. Zero entries are
// preserved rather than filtered: a row of zeroes and an absent row mean the
// same thing to a consumer, and dropping them here would only make the two
// shapes differ for no benefit.
func (s *JobStore) PlatformSizeBands() map[string][2]int64 {
	out := map[string][2]int64{}
	for _, p := range s.ListPlatformSizes() {
		out[p.PlatformSlug] = [2]int64{p.MinSize, p.MaxSize}
	}
	return out
}
