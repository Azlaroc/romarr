package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// The clone-list plane: the upstream opinion about which differently-NAMED
// dumps are the same game.
//
// The catalog cannot answer that. Both authorities ship STANDARD DATs — every
// one of the 93,814 games imported on 2026-08-19 carries an empty clone_of —
// so parent/clone grouping has no data behind it and the 1G1R set is built
// from parsed titles plus these lists.
//
// Two tables, deliberately flat: one row per (platform, group, search term),
// because that is the shape both the overlay lookup and the UI want, and a
// clone list is small (Game Boy is the biggest at 278 groups / 565 titles).

// CloneListRow is one platform's stored list.
type CloneListRow struct {
	PlatformSlug string `json:"platform_slug"`
	ListName     string `json:"list_name"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	// LastUpdated is the list's OWN stamp, not ours: it is what tells an
	// operator whether upstream has moved.
	LastUpdated string `json:"last_updated,omitempty"`
	FetchedAt   string `json:"fetched_at,omitempty"`
	GroupCount  int    `json:"group_count"`
	TitleCount  int    `json:"title_count"`
}

// CloneGroupRow is one title belonging to one group.
type CloneGroupRow struct {
	PlatformSlug string `json:"platform_slug"`
	GroupName    string `json:"group_name"`
	Categories   string `json:"categories,omitempty"` // comma-joined
	SearchTerm   string `json:"search_term"`
	Priority     int    `json:"priority"`
}

func (s *JobStore) migrateCloneLists() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS clone_lists (
			platform_slug TEXT PRIMARY KEY,
			list_name TEXT NOT NULL DEFAULT '',
			source_sha256 TEXT NOT NULL DEFAULT '',
			last_updated TEXT NOT NULL DEFAULT '',
			fetched_at TEXT NOT NULL DEFAULT (datetime('now')),
			group_count INTEGER NOT NULL DEFAULT 0,
			title_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS clone_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform_slug TEXT NOT NULL,
			group_name TEXT NOT NULL,
			categories TEXT NOT NULL DEFAULT '',
			search_term TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clone_groups_platform ON clone_groups(platform_slug)`,
	}
	for _, ddl := range stmts {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate clone list table", "error", err)
		}
	}
	if !s.columnExists("dat_platforms", "clonelist_name") {
		if _, err := s.db.Exec(`ALTER TABLE dat_platforms ADD COLUMN clonelist_name TEXT NOT NULL DEFAULT ''`); err != nil {
			slog.Warn("migrate dat_platforms clonelist_name", "error", err)
		}
	}
	s.seedCloneListNames()
}

// cloneListNameFor returns a platform's shipped clone-list locator. The
// locators live in datPlatformSeed beside the DAT codes so one row carries
// everything shipped about a platform's catalog lane.
func cloneListNameFor(slug string) (string, bool) {
	for _, p := range datPlatformSeed {
		if p.PlatformSlug == slug && p.CloneListName != "" {
			return p.CloneListName, true
		}
	}
	return "", false
}

// seedCloneListNames fills the locator on rows that have none.
//
// 🔴 Deliberately NOT virgin-table-guarded, unlike seedDatDefaults. Every
// existing install already has its dat_platforms rows, so a virgin-only seed
// would leave the column empty forever and the whole plane dead on exactly the
// installs that have catalogs to reconcile. An operator's own value is
// preserved: only an empty locator is filled.
func (s *JobStore) seedCloneListNames() {
	for _, p := range datPlatformSeed {
		s.seedCloneListName(p.PlatformSlug)
	}
}

// seedCloneListName fills one platform's locator when it has none.
func (s *JobStore) seedCloneListName(slug string) {
	name, ok := cloneListNameFor(slug)
	if !ok {
		return
	}
	if _, err := s.db.Exec(
		`UPDATE dat_platforms SET clonelist_name = ? WHERE platform_slug = ? AND clonelist_name = ''`,
		name, slug,
	); err != nil {
		slog.Warn("seed clone list name", "platform", slug, "error", err)
	}
}

// CloneListPlatform is a platform that has a clone list to fetch.
type CloneListPlatform struct {
	PlatformSlug string `json:"platform_slug"`
	Name         string `json:"clonelist_name"`
}

// ListCloneListPlatforms returns the platforms carrying a clone-list locator,
// in slug order.
func (s *JobStore) ListCloneListPlatforms() []CloneListPlatform {
	rows, err := s.db.Query(
		`SELECT platform_slug, clonelist_name FROM dat_platforms
		  WHERE clonelist_name != '' AND enabled = 1 ORDER BY platform_slug`)
	if err != nil {
		slog.Warn("list clone list platforms", "error", err)
		return nil
	}
	defer rows.Close()
	var out []CloneListPlatform
	for rows.Next() {
		var p CloneListPlatform
		if err := rows.Scan(&p.PlatformSlug, &p.Name); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SetCloneListName repoints one platform at a different list, or clears it.
func (s *JobStore) SetCloneListName(slug, name string) error {
	res, err := s.db.Exec(`UPDATE dat_platforms SET clonelist_name = ? WHERE platform_slug = ?`,
		strings.TrimSpace(name), slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("platform %q has no DAT assignment", slug)
	}
	return nil
}

// ReplaceCloneList swaps one platform's stored list for a freshly parsed one.
// Whole-list replacement rather than a diff: the list is the upstream's
// current opinion, and a partial merge would keep groups upstream deleted.
func (s *JobStore) ReplaceCloneList(meta CloneListRow, groups []CloneGroupRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM clone_groups WHERE platform_slug = ?`, meta.PlatformSlug); err != nil {
		return err
	}
	ins, err := tx.Prepare(`INSERT INTO clone_groups
		(platform_slug, group_name, categories, search_term, priority) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	names := map[string]struct{}{}
	for _, g := range groups {
		if _, err := ins.Exec(meta.PlatformSlug, g.GroupName, g.Categories, g.SearchTerm, g.Priority); err != nil {
			return err
		}
		names[g.GroupName] = struct{}{}
	}
	meta.GroupCount, meta.TitleCount = len(names), len(groups)
	if meta.FetchedAt == "" {
		meta.FetchedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	if _, err := tx.Exec(
		`INSERT INTO clone_lists (platform_slug, list_name, source_sha256, last_updated, fetched_at, group_count, title_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(platform_slug) DO UPDATE SET
		   list_name = excluded.list_name, source_sha256 = excluded.source_sha256,
		   last_updated = excluded.last_updated, fetched_at = excluded.fetched_at,
		   group_count = excluded.group_count, title_count = excluded.title_count`,
		meta.PlatformSlug, meta.ListName, meta.SourceSHA256, meta.LastUpdated,
		meta.FetchedAt, meta.GroupCount, meta.TitleCount,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// GetCloneList returns one platform's stored list metadata.
func (s *JobStore) GetCloneList(slug string) (CloneListRow, bool) {
	var r CloneListRow
	err := s.db.QueryRow(
		`SELECT platform_slug, list_name, source_sha256, last_updated, fetched_at, group_count, title_count
		   FROM clone_lists WHERE platform_slug = ?`, slug).
		Scan(&r.PlatformSlug, &r.ListName, &r.SourceSHA256, &r.LastUpdated, &r.FetchedAt, &r.GroupCount, &r.TitleCount)
	if err != nil {
		return CloneListRow{}, false
	}
	return r, true
}

// ListCloneLists returns every stored list, slug order.
func (s *JobStore) ListCloneLists() []CloneListRow {
	rows, err := s.db.Query(
		`SELECT platform_slug, list_name, source_sha256, last_updated, fetched_at, group_count, title_count
		   FROM clone_lists ORDER BY platform_slug`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CloneListRow
	for rows.Next() {
		var r CloneListRow
		if err := rows.Scan(&r.PlatformSlug, &r.ListName, &r.SourceSHA256, &r.LastUpdated,
			&r.FetchedAt, &r.GroupCount, &r.TitleCount); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ListCloneGroups returns one platform's clone-list rows.
func (s *JobStore) ListCloneGroups(slug string) []CloneGroupRow {
	rows, err := s.db.Query(
		`SELECT platform_slug, group_name, categories, search_term, priority
		   FROM clone_groups WHERE platform_slug = ? ORDER BY group_name, priority, search_term`, slug)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []CloneGroupRow
	for rows.Next() {
		var g CloneGroupRow
		if err := rows.Scan(&g.PlatformSlug, &g.GroupName, &g.Categories, &g.SearchTerm, &g.Priority); err != nil {
			continue
		}
		out = append(out, g)
	}
	return out
}

// DatSetMember is one catalogued dump with its files attached — the shape the
// set engine reads. Loaded in ONE query per platform: psx is 10,970 games and
// 60,435 roms, so a per-game roms call would be sixty thousand round trips.
type DatSetMember struct {
	GameID    int64
	Name      string
	BareTitle string
	Region    string
	Languages string
	Revision  int
	Flags     string
	TotalSize int64
	Roms      []DatRomRow
}

// DatSetMembers returns every dump in a platform's ACTIVE snapshot.
func (s *JobStore) DatSetMembers(slug string) []DatSetMember {
	if slug == "" {
		return nil
	}
	rows, err := s.db.Query(
		`SELECT g.id, g.name, g.bare_title, g.region, g.languages, g.revision, g.flags, g.total_size,
		        r.name, r.size, r.crc, r.md5, r.sha1
		   FROM dat_games g
		   JOIN dat_snapshots s ON s.id = g.snapshot_id AND s.active = 1
		   LEFT JOIN dat_roms r ON r.game_id = g.id
		  WHERE g.platform_slug = ?
		  ORDER BY g.id, r.name`, slug)
	if err != nil {
		slog.Warn("load set members", "platform", slug, "error", err)
		return nil
	}
	defer rows.Close()

	var out []DatSetMember
	var cur *DatSetMember
	for rows.Next() {
		var (
			g                     DatSetMember
			rn, rcrc, rmd5, rsha1 sql.NullString
			rsize                 sql.NullInt64
		)
		if err := rows.Scan(&g.GameID, &g.Name, &g.BareTitle, &g.Region, &g.Languages,
			&g.Revision, &g.Flags, &g.TotalSize, &rn, &rsize, &rcrc, &rmd5, &rsha1); err != nil {
			continue
		}
		if cur == nil || cur.GameID != g.GameID {
			out = append(out, g)
			cur = &out[len(out)-1]
		}
		if rn.Valid && rn.String != "" {
			cur.Roms = append(cur.Roms, DatRomRow{
				Name: rn.String, Size: rsize.Int64,
				CRC: rcrc.String, MD5: rmd5.String, SHA1: rsha1.String,
			})
		}
	}
	return out
}

// LibraryNameIndex maps a library file's name forms to its row: the file's
// base name and its stem, both lowered. This is the middle ownership tier —
// weaker than a hash, stronger than a parsed title — and it is what makes a
// renamed-to-canonical library match its catalogue entry exactly.
func (s *JobStore) LibraryNameIndex(platformSlug string) map[string]*LibraryItem {
	q := `SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items`
	args := []interface{}{}
	if platformSlug != "" {
		q += ` WHERE platform_slug = ?`
		args = append(args, platformSlug)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	idx := map[string]*LibraryItem{}
	for rows.Next() {
		var item LibraryItem
		var isPC int
		if err := rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
			continue
		}
		item.IsPC = isPC != 0
		cp := item
		base := item.FilePath
		if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
			base = base[i+1:]
		}
		for _, k := range nameKeys(base) {
			if _, dup := idx[k]; !dup {
				idx[k] = &cp
			}
		}
	}
	return idx
}

// nameKeys are the lowered forms a file name is matched under: the name as
// stored, and the name with one trailing archive extension removed (the
// library keeps .zip/.7z wrappers around a canonically-named rom).
func nameKeys(base string) []string {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil
	}
	keys := []string{strings.ToLower(base)}
	if i := strings.LastIndex(base, "."); i > 0 {
		keys = append(keys, strings.ToLower(base[:i]))
	}
	sort.Strings(keys)
	return keys
}
