package db

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"gamarr/internal/sources"
)

// The source registry lives in two tables:
//   - source_registry: the two built-in driver specs (vimm, archiveorg) —
//     a closed set matching the sources.Registry struct; rows are updated,
//     never added or deleted.
//   - ddl_sources: user-managed custom DDL rows with stable integer ids
//     (replacing the positional-index ddl_sources.json store).
//
// The env-resolved registry (GAMARR_SOURCES_PATH/URL → embedded defaults +
// env URL overrides) seeds source_registry exactly once, on first boot with
// an empty table; after that the DB is authoritative.

func (s *JobStore) migrateSourceRegistry() {
	ddl := `CREATE TABLE IF NOT EXISTS source_registry (
		name TEXT PRIMARY KEY,
		enabled INTEGER NOT NULL DEFAULT 1,
		base_url TEXT NOT NULL DEFAULT '',
		mapping TEXT NOT NULL DEFAULT '{}',
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate source_registry", "error", err)
	}
}

func (s *JobStore) migrateDDLSources() {
	ddl := `CREATE TABLE IF NOT EXISTS ddl_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate ddl_sources", "error", err)
	}
}

func (s *JobStore) persistSourceSpec(name string, enabled bool, baseURL string, mapping map[string]string) error {
	mj, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	if mapping == nil {
		mj = []byte("{}")
	}
	en := 0
	if enabled {
		en = 1
	}
	_, err = s.db.Exec(`INSERT INTO source_registry (name, enabled, base_url, mapping, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(name) DO UPDATE SET enabled = excluded.enabled, base_url = excluded.base_url,
			mapping = excluded.mapping, updated_at = excluded.updated_at`, name, en, baseURL, string(mj))
	return err
}

// LoadSourceRegistry seeds the table from the env-resolved registry when it
// is empty (first boot, or a restored pre-registry backup) and returns the
// hydrated registry. The returned pointer is fresh: callers install it once
// and rebuild it only on a write.
func (s *JobStore) LoadSourceRegistry(seed *sources.Registry) *sources.Registry {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM source_registry").Scan(&count)
	if count == 0 && seed != nil {
		if err := s.persistSourceSpec("vimm", seed.Vimm.IsEnabled(), seed.Vimm.BaseURL, seed.Vimm.PlatformSystems); err != nil {
			slog.Warn("seed source_registry vimm", "error", err)
		}
		if err := s.persistSourceSpec("archiveorg", seed.ArchiveOrg.IsEnabled(), seed.ArchiveOrg.BaseURL, seed.ArchiveOrg.Items); err != nil {
			slog.Warn("seed source_registry archiveorg", "error", err)
		}
		slog.Info("seeded source registry from environment-resolved config")
	}
	return s.HydrateSourceRegistry()
}

// HydrateSourceRegistry builds a fresh *sources.Registry from the DB rows.
// Missing rows keep zero-value specs (inactive by the Active predicates).
func (s *JobStore) HydrateSourceRegistry() *sources.Registry {
	reg := &sources.Registry{Version: 1}
	rows, err := s.db.Query("SELECT name, enabled, base_url, mapping FROM source_registry")
	if err != nil {
		slog.Warn("hydrate source_registry", "error", err)
		return reg
	}
	defer rows.Close()
	for rows.Next() {
		var name, baseURL, mappingJSON string
		var enabled int
		if err := rows.Scan(&name, &enabled, &baseURL, &mappingJSON); err != nil {
			continue
		}
		en := enabled != 0
		mapping := map[string]string{}
		if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
			slog.Warn("source_registry mapping unreadable", "source", name, "error", err)
		}
		switch name {
		case "vimm":
			reg.Vimm = sources.VimmSpec{BaseURL: baseURL, Enabled: &en, PlatformSystems: mapping}
		case "archiveorg":
			reg.ArchiveOrg = sources.ArchiveOrgSpec{BaseURL: baseURL, Enabled: &en, Items: mapping}
		}
	}
	return reg
}

// SourcePatch is a sparse update to one registry spec; nil fields are
// unchanged.
type SourcePatch struct {
	Enabled *bool
	BaseURL *string
	Mapping map[string]string
}

// UpdateSourceSpec applies a sparse patch to a named built-in spec. Unknown
// names error (the registry is a closed set).
func (s *JobStore) UpdateSourceSpec(name string, p SourcePatch) error {
	var enabled int
	var baseURL, mappingJSON string
	err := s.db.QueryRow("SELECT enabled, base_url, mapping FROM source_registry WHERE name = ?", name).
		Scan(&enabled, &baseURL, &mappingJSON)
	if err != nil {
		return fmt.Errorf("unknown source %q", name)
	}
	en := enabled != 0
	if p.Enabled != nil {
		en = *p.Enabled
	}
	if p.BaseURL != nil {
		baseURL = *p.BaseURL
	}
	mapping := map[string]string{}
	json.Unmarshal([]byte(mappingJSON), &mapping)
	if p.Mapping != nil {
		mapping = p.Mapping
	}
	return s.persistSourceSpec(name, en, baseURL, mapping)
}

// DDLSourceRow is a user-managed custom DDL source.
type DDLSourceRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// ListDDLSources returns the custom DDL rows, oldest first.
func (s *JobStore) ListDDLSources() []DDLSourceRow {
	rows, err := s.db.Query("SELECT id, name, url, enabled, created_at FROM ddl_sources ORDER BY id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DDLSourceRow
	for rows.Next() {
		var r DDLSourceRow
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.URL, &enabled, &r.CreatedAt); err != nil {
			continue
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out
}

// AddDDLSource inserts a custom DDL row and returns its id.
func (s *JobStore) AddDDLSource(name, url string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO ddl_sources (name, url) VALUES (?, ?)", name, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteDDLSource removes a custom DDL row by id; false when absent.
func (s *JobStore) DeleteDDLSource(id int64) (bool, error) {
	res, err := s.db.Exec("DELETE FROM ddl_sources WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CountDDLSources reports the number of custom DDL rows (import guard).
func (s *JobStore) CountDDLSources() int {
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM ddl_sources").Scan(&n)
	return n
}
