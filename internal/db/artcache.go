package db

import (
	"encoding/json"
	"log/slog"
)

// The title-art cache index: which keys have been resolved, to what, and
// when. Dumb rows only — the resolution ladder (libretro → IGDB → custom)
// lives in internal/artsvc, per the layering rule that internal/db holds no
// policy. Keys are content-derived (platform slug + sanitized name stem),
// never snapshot-scoped catalog ids.
type ArtCacheRow struct {
	Key       string // "title:<slug>:<stem>"
	Status    string // "ok" | "notfound"
	Source    string // "libretro" | "igdb" — where an ok answer came from
	RelPath   string // path under $DataDir/art/, "" for notfound
	FetchedAt string // datetime('now') at write
}

func (s *JobStore) migrateArtCache() {
	ddl := `CREATE TABLE IF NOT EXISTS art_cache (
		key TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		rel_path TEXT NOT NULL DEFAULT '',
		fetched_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate art cache", "error", err)
	}
}

// SaveArtCache upserts one resolution — a re-fetch overwrites, refreshing
// fetched_at, which is what ages a notfound out of its negative TTL.
func (s *JobStore) SaveArtCache(row ArtCacheRow) error {
	_, err := s.db.Exec(
		`INSERT INTO art_cache (key, status, source, rel_path, fetched_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET status=excluded.status, source=excluded.source,
		   rel_path=excluded.rel_path, fetched_at=excluded.fetched_at`,
		row.Key, row.Status, row.Source, row.RelPath,
	)
	return err
}

// GetArtCache reads one resolution; ok=false when the key was never asked.
func (s *JobStore) GetArtCache(key string) (ArtCacheRow, bool) {
	var row ArtCacheRow
	err := s.db.QueryRow(
		"SELECT key, status, source, rel_path, fetched_at FROM art_cache WHERE key = ?", key,
	).Scan(&row.Key, &row.Status, &row.Source, &row.RelPath, &row.FetchedAt)
	if err != nil {
		return ArtCacheRow{}, false
	}
	return row, true
}

// SaveLibraryIGDBMeta banks the metadata provider's year/genres under
// $.gamarr.igdb — the shop-native lane's only source of either. Same
// one-statement json_set discipline as SaveLibraryHashes: leaves only,
// siblings untouched.
func (s *JobStore) SaveLibraryIGDBMeta(id int64, year int, genres []string) error {
	// The whole $.gamarr.igdb object is this writer's leaf (nothing else
	// touches it), written in one piece — json_set only auto-creates the
	// last missing path level, so a two-deep leaf write could no-op on a
	// row with no $.gamarr.igdb yet.
	obj, err := json.Marshal(struct {
		Year   int      `json:"year,omitempty"`
		Genres []string `json:"genres,omitempty"`
	}{Year: year, Genres: genres})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"UPDATE library_items SET metadata = json_set("+jsonMeta+", '$.gamarr.igdb', json(?)) WHERE id = ?",
		string(obj), id,
	)
	return err
}

// CountArtCache tallies rows by status — the backfill's progress arithmetic.
func (s *JobStore) CountArtCache() (ok, notfound int) {
	s.db.QueryRow("SELECT COUNT(*) FROM art_cache WHERE status = 'ok'").Scan(&ok)
	s.db.QueryRow("SELECT COUNT(*) FROM art_cache WHERE status = 'notfound'").Scan(&notfound)
	return ok, notfound
}
