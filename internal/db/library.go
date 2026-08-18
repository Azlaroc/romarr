package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// LibraryItem represents a game/ROM in the library.
type LibraryItem struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Platform     string `json:"platform"`
	PlatformSlug string `json:"platform_slug"`
	IsPC         bool   `json:"is_pc"`
	FilePath     string `json:"file_path"`
	FileSize     int64  `json:"file_size"`
	Source       string `json:"source"`      // "torrent", "ddl", "scan"
	SourceType   string `json:"source_type"` // "prowlarr", "myrient", "vimm", "manual"
	SourceID     string `json:"source_id"`   // dedup key (hash, url, etc.)
	Metadata     string `json:"metadata"`    // JSON blob
	AddedAt      string `json:"added_at"`
}

// WishlistItem represents a game on the wishlist.
type WishlistItem struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Platform     string `json:"platform"`
	PlatformSlug string `json:"platform_slug"`
	AddedAt      string `json:"added_at"`
}

// ActivityEntry represents an activity log entry.
type ActivityEntry struct {
	ID            int64  `json:"id"`
	EventType     string `json:"event_type"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	LibraryItemID *int64 `json:"library_item_id,omitempty"`
	JobID         string `json:"job_id,omitempty"`
	Timestamp     string `json:"timestamp"`
}

// LibraryPage is a paginated library result.
type LibraryPage struct {
	Items      []LibraryItem `json:"items"`
	Total      int           `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

func (s *JobStore) migrateExtra() {
	s.migrateRequests()
	s.migrateNotifications()
	s.migrateWebhooks()
	s.migrateHistory()
	s.migrateQualityProfiles()
	s.migrateBlocklist()
	s.migrateReleaseProfiles()
	s.migrateTags()
	s.migrateSettings()
	s.migrateSourceRegistry()
	s.migrateDDLSources()
	s.migrateIAItemMetadata()
	s.migrateDat()
	s.migratePlatformSizes()
	s.migratePlatforms()

	tables := []string{
		`CREATE TABLE IF NOT EXISTS library_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			platform TEXT NOT NULL DEFAULT '',
			platform_slug TEXT NOT NULL DEFAULT '',
			is_pc INTEGER NOT NULL DEFAULT 0,
			file_path TEXT NOT NULL DEFAULT '',
			file_size INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			added_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS wishlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			platform TEXT NOT NULL DEFAULT '',
			platform_slug TEXT NOT NULL DEFAULT '',
			added_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS activity_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			library_item_id INTEGER,
			job_id TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_library_platform ON library_items(platform_slug)`,
		`CREATE INDEX IF NOT EXISTS idx_library_source_id ON library_items(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_library_file_path ON library_items(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_timestamp ON activity_log(timestamp)`,
	}
	for _, ddl := range tables {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate extra table", "error", err)
		}
	}
}

// DB returns the underlying sql.DB for direct use.
func (s *JobStore) DB() *sql.DB {
	return s.db
}

// ── Library Items ──────────────────────────────────────────────────────────────

// AddLibraryItem inserts a new library item.
func (s *JobStore) AddLibraryItem(item *LibraryItem) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO library_items (title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Title, item.Platform, item.PlatformSlug, boolToInt(item.IsPC),
		item.FilePath, item.FileSize, item.Source, item.SourceType, item.SourceID, item.Metadata,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetLibraryPage returns a paginated library.
func (s *JobStore) GetLibraryPage(page, pageSize int, query, platformSlug string) LibraryPage {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}

	where, args := buildLibraryWhere(query, platformSlug)

	var total int
	row := s.db.QueryRow("SELECT COUNT(*) FROM library_items "+where, args...)
	row.Scan(&total)

	totalPages := (total + pageSize - 1) / pageSize
	offset := (page - 1) * pageSize

	rows, err := s.db.Query(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items "+
			where+" ORDER BY added_at DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...,
	)
	if err != nil {
		return LibraryPage{Page: page, PageSize: pageSize}
	}
	defer rows.Close()

	var items []LibraryItem
	for rows.Next() {
		var item LibraryItem
		var isPC int
		rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt)
		item.IsPC = isPC != 0
		items = append(items, item)
	}
	if items == nil {
		items = []LibraryItem{}
	}

	return LibraryPage{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}
}

// GetLibraryItem returns a single library item by ID.
func (s *JobStore) GetLibraryItem(id int64) (*LibraryItem, error) {
	row := s.db.QueryRow(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE id = ?",
		id,
	)
	var item LibraryItem
	var isPC int
	err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt)
	if err != nil {
		return nil, err
	}
	item.IsPC = isPC != 0
	return &item, nil
}

// UpdateLibraryItemMetadata updates the metadata JSON blob for a library item.
func (s *JobStore) UpdateLibraryItemMetadata(id int64, metadata string) error {
	_, err := s.db.Exec("UPDATE library_items SET metadata = ? WHERE id = ?", metadata, id)
	return err
}

// DeleteLibraryItem deletes a library item by ID.
func (s *JobStore) DeleteLibraryItem(id int64) error {
	_, err := s.db.Exec("DELETE FROM library_items WHERE id = ?", id)
	return err
}

// LibraryHasSourceID checks if a source_id already exists in the library.
func (s *JobStore) LibraryHasSourceID(sourceID string) bool {
	if sourceID == "" {
		return false
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM library_items WHERE source_id = ?", sourceID).Scan(&count)
	return count > 0
}

// LibraryStats returns counts by platform.
func (s *JobStore) LibraryStats() map[string]int {
	stats := make(map[string]int)
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(platform_slug,''), CASE WHEN is_pc THEN 'pc' ELSE 'unknown' END) as p, COUNT(*) FROM library_items GROUP BY p`)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var c int
		rows.Scan(&p, &c)
		stats[p] = c
	}
	return stats
}

// LibraryTotal returns the total number of library items.
func (s *JobStore) LibraryTotal() int {
	var total int
	s.db.QueryRow("SELECT COUNT(*) FROM library_items").Scan(&total)
	return total
}

// ScanLibraryDir scans a directory and adds new items to the library.
func (s *JobStore) ScanLibraryDir(dir, platform, platformSlug string, isPC bool) int {
	// Implemented in download/manager.go or called from main
	return 0
}

func buildLibraryWhere(query, platformSlug string) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	if query != "" {
		conditions = append(conditions, "title LIKE ?")
		args = append(args, "%"+query+"%")
	}
	if platformSlug != "" && platformSlug != "all" {
		if platformSlug == "pc" {
			conditions = append(conditions, "is_pc = 1")
		} else {
			conditions = append(conditions, "platform_slug = ?")
			args = append(args, platformSlug)
		}
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

// ── Wishlist ───────────────────────────────────────────────────────────────────

// AddWishlistItem adds an item to the wishlist.
func (s *JobStore) AddWishlistItem(title, platform, platformSlug string) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO wishlist (title, platform, platform_slug) VALUES (?, ?, ?)",
		title, platform, platformSlug,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetWishlist returns all wishlist items.
func (s *JobStore) GetWishlist() []WishlistItem {
	rows, err := s.db.Query("SELECT id, title, platform, platform_slug, added_at FROM wishlist ORDER BY added_at DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var items []WishlistItem
	for rows.Next() {
		var item WishlistItem
		rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug, &item.AddedAt)
		items = append(items, item)
	}
	return items
}

// DeleteWishlistItem removes a wishlist item.
func (s *JobStore) DeleteWishlistItem(id int64) error {
	_, err := s.db.Exec("DELETE FROM wishlist WHERE id = ?", id)
	return err
}

// SchedulerDownloadTitle returns the wishlist title that drove jobID's grab —
// the scheduler_download activity row logged at dispatch — or "" when the job
// was not a scheduler grab (manual downloads, request searches).
func (s *JobStore) SchedulerDownloadTitle(jobID string) string {
	if jobID == "" {
		return ""
	}
	var title string
	err := s.db.QueryRow(
		"SELECT title FROM activity_log WHERE event_type = 'scheduler_download' AND job_id = ? ORDER BY id DESC LIMIT 1",
		jobID,
	).Scan(&title)
	if err != nil {
		return ""
	}
	return title
}

// DeleteWishlistByTitle removes wishlist rows matching title case-insensitively
// on the given platform (rows with a blank platform_slug match any platform;
// a blank platformSlug argument matches every row with the title). Reports how
// many rows were removed.
func (s *JobStore) DeleteWishlistByTitle(title, platformSlug string) int {
	if title == "" {
		return 0
	}
	res, err := s.db.Exec(
		"DELETE FROM wishlist WHERE LOWER(title) = LOWER(?) AND (platform_slug = ? OR platform_slug = '' OR ? = '')",
		title, platformSlug, platformSlug,
	)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// ── Activity Log ───────────────────────────────────────────────────────────────

// LogActivity writes an activity log entry.
// ActivityCount returns the total number of activity log entries.
func (s *JobStore) ActivityCount() int {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM activity_log").Scan(&count)
	return count
}

func (s *JobStore) LogActivity(eventType, title, detail, jobID string, libraryItemID *int64) {
	_, err := s.db.Exec(
		"INSERT INTO activity_log (event_type, title, detail, library_item_id, job_id) VALUES (?, ?, ?, ?, ?)",
		eventType, title, detail, libraryItemID, jobID,
	)
	if err != nil {
		slog.Warn("failed to log activity", "error", err)
	}
}

// GetActivity returns recent activity with pagination.
func (s *JobStore) GetActivity(page, pageSize int) ([]ActivityEntry, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	var total int
	s.db.QueryRow("SELECT COUNT(*) FROM activity_log").Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := s.db.Query(
		"SELECT id, event_type, title, detail, library_item_id, job_id, timestamp FROM activity_log ORDER BY timestamp DESC LIMIT ? OFFSET ?",
		pageSize, offset,
	)
	if err != nil {
		return nil, total
	}
	defer rows.Close()

	var entries []ActivityEntry
	for rows.Next() {
		var e ActivityEntry
		var libID sql.NullInt64
		rows.Scan(&e.ID, &e.EventType, &e.Title, &e.Detail, &libID, &e.JobID, &e.Timestamp)
		if libID.Valid {
			e.LibraryItemID = &libID.Int64
		}
		entries = append(entries, e)
	}
	return entries, total
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// ClearScanEntries removes all library items added by directory scanning.
// This is called before a rescan to ensure accuracy.
func (s *JobStore) ClearScanEntries() {
	result, _ := s.db.Exec("DELETE FROM library_items WHERE source = 'scan'")
	if n, _ := result.RowsAffected(); n > 0 {
		slog.Info("cleared scan entries for rescan", "count", n)
	}
}

// ClearVaultScanEntries removes only the PC-vault scan rows, leaving ROM scan
// rows alone. Used when the RomM sync owns the ROM side of the library and
// the fs scanner only walks the vault.
func (s *JobStore) ClearVaultScanEntries() {
	result, _ := s.db.Exec("DELETE FROM library_items WHERE source = 'scan' AND is_pc = 1")
	if n, _ := result.RowsAffected(); n > 0 {
		slog.Info("cleared vault scan entries for rescan", "count", n)
	}
}

// FindLibraryByTitle checks if a game with a matching title+platform exists in the library.
// Uses case-insensitive LIKE matching. Returns nil if not found.
func (s *JobStore) FindLibraryByTitle(title, platformSlug string) *LibraryItem {
	if title == "" {
		return nil
	}

	var query string
	var args []interface{}

	normalizedTitle := strings.ToLower(strings.TrimSpace(title))

	if platformSlug != "" && platformSlug != "all" {
		if platformSlug == "pc" {
			query = "SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE LOWER(title) = ? AND is_pc = 1 LIMIT 1"
			args = []interface{}{normalizedTitle}
		} else {
			query = "SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE LOWER(title) = ? AND platform_slug = ? LIMIT 1"
			args = []interface{}{normalizedTitle, platformSlug}
		}
	} else {
		query = "SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE LOWER(title) = ? LIMIT 1"
		args = []interface{}{normalizedTitle}
	}

	row := s.db.QueryRow(query, args...)
	var item LibraryItem
	var isPC int
	err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt)
	if err != nil {
		// Fallback: match on the RomM filesystem name (release names carry the
		// on-disk name, not the IGDB display title the row is titled with).
		if platformSlug != "" && platformSlug != "all" && platformSlug != "pc" {
			return s.findLibraryBySearchKey(NormalizeTitleKey(title), platformSlug)
		}
		return nil
	}
	item.IsPC = isPC != 0
	return &item
}

// findLibraryBySearchKey matches against the pre-lowered fs-name key the RomM
// sync stashes at metadata $.romm.search_key.
func (s *JobStore) findLibraryBySearchKey(key, platformSlug string) *LibraryItem {
	if key == "" {
		return nil
	}
	row := s.db.QueryRow(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE json_extract(COALESCE(NULLIF(metadata, ''), '{}'), '$.romm.search_key') = ? AND platform_slug = ? LIMIT 1",
		key, platformSlug,
	)
	var item LibraryItem
	var isPC int
	if err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
		return nil
	}
	item.IsPC = isPC != 0
	return &item
}

// FindLibraryByHash finds a library row whose stored hash identity matches
// either given hash (hex, any case). Two hash families are checked: $.romm
// (RomM's DAT-style content hashes) and $.gamarr (the source release's file
// hashes persisted at import) — an archived release's file hash and its inner
// rom's content hash never agree, so all four paths are matched independently.
// Global across platforms: byte identity is platform-independent. Returns nil
// when both inputs are empty; an empty input never matches a row whose stored
// hash is absent. Metadata is json_valid-guarded: one malformed blob would
// otherwise error the whole query (json_extract raises on bad JSON).
func (s *JobStore) FindLibraryByHash(md5, sha1 string) *LibraryItem {
	md5 = strings.ToLower(strings.TrimSpace(md5))
	sha1 = strings.ToLower(strings.TrimSpace(sha1))
	var conds []string
	var args []interface{}
	for _, h := range []struct{ name, val string }{{"md5", md5}, {"sha1", sha1}} {
		if h.val == "" {
			continue
		}
		for _, fam := range []string{"romm", "gamarr"} {
			conds = append(conds,
				"LOWER(json_extract(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END, '$."+fam+"."+h.name+"')) = ?")
			args = append(args, h.val)
		}
	}
	if len(conds) == 0 {
		return nil
	}
	row := s.db.QueryRow(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE "+
			strings.Join(conds, " OR ")+" LIMIT 1", args...,
	)
	var item LibraryItem
	var isPC int
	if err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
		return nil
	}
	item.IsPC = isPC != 0
	return &item
}

// pathSourceIDPrefixes are the source_id schemes derived from the file path;
// a path change must rewrite these keys or later imports/scans at the new
// path mint duplicate rows. RomM's "romm:<id>" identity is path-free.
var pathSourceIDPrefixes = []string{"ddl:", "torrent:", "nzb:", "manual:", "scan:"}

// UpdateLibraryItemPath updates a row's file_path after an on-disk rename
// and, in the same transaction, rewrites a path-derived source_id whose
// remainder equals the old path. Must run BEFORE any RomM rescan so the
// sync's adopt-by-path check merges instead of duplicating.
func (s *JobStore) UpdateLibraryItemPath(id int64, newPath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin path update tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	var oldPath, sourceID string
	if err = tx.QueryRow("SELECT file_path, source_id FROM library_items WHERE id = ?", id).
		Scan(&oldPath, &sourceID); err != nil {
		return err
	}
	newSourceID := sourceID
	for _, p := range pathSourceIDPrefixes {
		if sourceID == p+oldPath {
			newSourceID = p + newPath
			break
		}
	}
	if _, err = tx.Exec("UPDATE library_items SET file_path = ?, source_id = ? WHERE id = ?",
		newPath, newSourceID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// FindLibraryBySourceID returns the row carrying exactly this source_id, or
// nil when none does (idx_library_source_id backed). LibraryHasSourceID is
// the bool-only variant; this one hands back the row itself.
func (s *JobStore) FindLibraryBySourceID(sourceID string) *LibraryItem {
	if sourceID == "" {
		return nil
	}
	row := s.db.QueryRow(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE source_id = ? LIMIT 1",
		sourceID,
	)
	var item LibraryItem
	var isPC int
	if err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
		return nil
	}
	item.IsPC = isPC != 0
	return &item
}

// UpdateLibraryItemFileSize refreshes a row's file_size — the re-finalize
// path for disc sets, where the source_id dedupe keeps TrackInLibrary from
// rewriting the row after new members land.
func (s *JobStore) UpdateLibraryItemFileSize(id, size int64) error {
	_, err := s.db.Exec("UPDATE library_items SET file_size = ? WHERE id = ?", size, id)
	return err
}

// GetLibraryItemByFilePath returns the row tracking exactly this path, or
// nil without error when none does (idx_library_file_path backed).
func (s *JobStore) GetLibraryItemByFilePath(path string) *LibraryItem {
	if path == "" {
		return nil
	}
	row := s.db.QueryRow(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE file_path = ? LIMIT 1",
		path,
	)
	var item LibraryItem
	var isPC int
	if err := row.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
		&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
		&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
		return nil
	}
	item.IsPC = isPC != 0
	return &item
}

// ListLibraryItemsForRename returns every non-PC library row in scope for a
// bulk-rename pass — one platform slug, or all when slug is "". Ordered by
// platform then path so batch progress reads coherently.
func (s *JobStore) ListLibraryItemsForRename(platformSlug string) []LibraryItem {
	query := "SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE is_pc = 0"
	var args []interface{}
	if platformSlug != "" {
		query += " AND platform_slug = ?"
		args = append(args, platformSlug)
	}
	query += " ORDER BY platform_slug, file_path"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []LibraryItem
	for rows.Next() {
		var item LibraryItem
		var isPC int
		if err := rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
			continue
		}
		item.IsPC = isPC != 0
		out = append(out, item)
	}
	return out
}

// LibraryHashIndex returns every stored library hash keyed "md5:<hex>" /
// "sha1:<hex>" (lowercased), mapped to its row. Both hash families are
// indexed independently — $.romm (DAT-style content hashes) and $.gamarr
// (release-file hashes persisted at import) — since an archived release's
// file hash and its inner rom's content hash never agree. One full-table
// query, json_valid-guarded like FindLibraryByHash; callers snapshot it once
// per scheduler cycle.
func (s *JobStore) LibraryHashIndex() map[string]*LibraryItem {
	const m = "CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END"
	rows, err := s.db.Query(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at, " +
			"LOWER(json_extract(" + m + ", '$.romm.md5')), LOWER(json_extract(" + m + ", '$.gamarr.md5')), " +
			"LOWER(json_extract(" + m + ", '$.romm.sha1')), LOWER(json_extract(" + m + ", '$.gamarr.sha1')) " +
			"FROM library_items",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	idx := map[string]*LibraryItem{}
	for rows.Next() {
		var item LibraryItem
		var isPC int
		var rm, gm, rs, gs sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt, &rm, &gm, &rs, &gs); err != nil {
			continue
		}
		item.IsPC = isPC != 0
		for _, h := range []struct {
			prefix string
			v      sql.NullString
		}{{"md5:", rm}, {"md5:", gm}, {"sha1:", rs}, {"sha1:", gs}} {
			if h.v.Valid && h.v.String != "" {
				if _, dup := idx[h.prefix+h.v.String]; !dup {
					idx[h.prefix+h.v.String] = &item
				}
			}
		}
	}
	return idx
}

// NormalizeTitleKey lowercases, trims and strips one trailing file extension
// from a title, matching how the RomM sync builds search keys from fs names.
func NormalizeTitleKey(title string) string {
	key := strings.ToLower(strings.TrimSpace(title))
	if dot := strings.LastIndexByte(key, '.'); dot > 0 && dot < len(key)-1 {
		ext := key[dot+1:]
		if len(ext) <= 4 && !strings.ContainsAny(ext, " ()[]") {
			key = key[:dot]
		}
	}
	return strings.TrimSpace(key)
}

// GetAllLibraryTitles returns a map of normalized "title|platform_slug" to LibraryItem for bulk lookups.
// RomM-synced rows are additionally keyed by their fs-name search key.
func (s *JobStore) GetAllLibraryTitles() map[string]*LibraryItem {
	rows, err := s.db.Query("SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at, json_extract(COALESCE(NULLIF(metadata, ''), '{}'), '$.romm.search_key') FROM library_items")
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make(map[string]*LibraryItem)
	for rows.Next() {
		var item LibraryItem
		var isPC int
		var searchKey sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt, &searchKey); err != nil {
			continue
		}
		item.IsPC = isPC != 0
		key := strings.ToLower(strings.TrimSpace(item.Title)) + "|" + item.PlatformSlug
		cp := item
		result[key] = &cp
		// Second key on the RomM fs-name so release-name lookups hit too.
		if searchKey.Valid && searchKey.String != "" {
			result[searchKey.String+"|"+item.PlatformSlug] = &cp
		}
	}
	return result
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FormatSize formats bytes into a human-readable string.
func FormatSize(size int64) string {
	if size == 0 {
		return "?"
	}
	s := float64(size)
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if s < 1024 {
			return fmt.Sprintf("%.1f %s", s, unit)
		}
		s /= 1024
	}
	return fmt.Sprintf("%.1f TB", s)
}

// RecentLibraryItems returns the most recently added items.
func (s *JobStore) RecentLibraryItems(limit int) []LibraryItem {
	rows, err := s.db.Query(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items ORDER BY added_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var items []LibraryItem
	for rows.Next() {
		var item LibraryItem
		var isPC int
		rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt)
		item.IsPC = isPC != 0
		items = append(items, item)
	}
	return items
}

// init extra tables during New()
func init() {
	// We'll call migrateExtra from New after the initial migrate
	_ = time.Now // avoid unused import
}
