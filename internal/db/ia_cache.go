package db

import (
	"log/slog"
	"time"
)

// The IA driver's per-item metadata cache (~1MB JSON per big collection
// item, a handful of rows) persists here so restarts don't refetch every
// mapped item against archive.org's anonymous rate budget. *JobStore
// satisfies archiveorg.CacheStore structurally.

func (s *JobStore) migrateIAItemMetadata() {
	ddl := `CREATE TABLE IF NOT EXISTS ia_item_metadata (
		item TEXT PRIMARY KEY,
		files TEXT NOT NULL,
		fetched_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate ia_item_metadata", "error", err)
	}
}

// GetItemMetadata returns the cached JSON listing and its fetch time.
func (s *JobStore) GetItemMetadata(item string) ([]byte, time.Time, bool) {
	var files, fetched string
	err := s.db.QueryRow("SELECT files, fetched_at FROM ia_item_metadata WHERE item = ?", item).
		Scan(&files, &fetched)
	if err != nil {
		return nil, time.Time{}, false
	}
	at, err := time.Parse("2006-01-02 15:04:05", fetched)
	if err != nil {
		return nil, time.Time{}, false
	}
	return []byte(files), at, true
}

// PutItemMetadata upserts an item's JSON listing.
func (s *JobStore) PutItemMetadata(item string, files []byte, fetchedAt time.Time) {
	_, err := s.db.Exec(`INSERT INTO ia_item_metadata (item, files, fetched_at) VALUES (?, ?, ?)
		ON CONFLICT(item) DO UPDATE SET files = excluded.files, fetched_at = excluded.fetched_at`,
		item, string(files), fetchedAt.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		slog.Warn("persist ia metadata", "item", item, "error", err)
	}
}
