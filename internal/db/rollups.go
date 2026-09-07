package db

import "log/slog"

// PlatformRollup is one platform's set arithmetic as of the last reconcile —
// a STAMPED DERIVATION, not a cache pretending to be truth: computed_at ships
// with every read, the set endpoint remains the live answer, and a platform
// with no row renders honest absence. Written by the collection cycle at the
// moment it has already paid for the numbers, and by the explicit per-platform
// rollup request; never by a read.
type PlatformRollup struct {
	PlatformSlug string `json:"platform_slug"`
	Owned        int    `json:"owned"`
	Covered      int    `json:"covered"`
	Gaps         int    `json:"gaps"`
	Out          int    `json:"out"`
	Uncatalogued int    `json:"uncatalogued"`
	ComputedAt   string `json:"computed_at"`
}

func (s *JobStore) migrateRollups() {
	ddl := `CREATE TABLE IF NOT EXISTS platform_rollups (
		platform_slug TEXT PRIMARY KEY,
		owned INTEGER NOT NULL DEFAULT 0,
		covered INTEGER NOT NULL DEFAULT 0,
		gaps INTEGER NOT NULL DEFAULT 0,
		out INTEGER NOT NULL DEFAULT 0,
		uncatalogued INTEGER NOT NULL DEFAULT 0,
		computed_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate rollups", "error", err)
	}
}

// SavePlatformRollup upserts one platform's numbers, restamping computed_at.
func (s *JobStore) SavePlatformRollup(r PlatformRollup) error {
	_, err := s.db.Exec(
		`INSERT INTO platform_rollups (platform_slug, owned, covered, gaps, out, uncatalogued, computed_at)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(platform_slug) DO UPDATE SET owned=excluded.owned, covered=excluded.covered,
		   gaps=excluded.gaps, out=excluded.out, uncatalogued=excluded.uncatalogued,
		   computed_at=excluded.computed_at`,
		r.PlatformSlug, r.Owned, r.Covered, r.Gaps, r.Out, r.Uncatalogued,
	)
	return err
}

// GetPlatformRollups reads every stamped rollup, keyed by slug.
func (s *JobStore) GetPlatformRollups() map[string]PlatformRollup {
	rows, err := s.db.Query(
		"SELECT platform_slug, owned, covered, gaps, out, uncatalogued, computed_at FROM platform_rollups")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]PlatformRollup{}
	for rows.Next() {
		var r PlatformRollup
		if err := rows.Scan(&r.PlatformSlug, &r.Owned, &r.Covered, &r.Gaps, &r.Out,
			&r.Uncatalogued, &r.ComputedAt); err != nil {
			continue
		}
		out[r.PlatformSlug] = r
	}
	return out
}

// LibraryPlatformTotal is the cheap half of a platform tile: live COUNT and
// SUM straight off library_items, PC lanes folded under "pc" the way the
// library filter folds them.
type LibraryPlatformTotal struct {
	Count     int
	SizeBytes int64
}

// LibraryPlatformTotals groups the whole library by effective platform slug.
func (s *JobStore) LibraryPlatformTotals() map[string]LibraryPlatformTotal {
	rows, err := s.db.Query(
		`SELECT CASE WHEN is_pc = 1 THEN 'pc' ELSE platform_slug END AS slug,
		        COUNT(*), COALESCE(SUM(file_size), 0)
		   FROM library_items GROUP BY slug`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]LibraryPlatformTotal{}
	for rows.Next() {
		var slug string
		var t LibraryPlatformTotal
		if err := rows.Scan(&slug, &t.Count, &t.SizeBytes); err != nil {
			continue
		}
		out[slug] = t
	}
	return out
}
