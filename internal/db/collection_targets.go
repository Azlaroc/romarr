package db

import (
	"log/slog"
	"strings"
	"time"
)

// Collection targets are the gap list: what a platform's 1G1R set wants that
// the library does not have.
//
// They are a TABLE rather than a derivation recomputed per cycle because a gap
// needs memory. A game nothing indexes today must stop being searched every
// hour, and an operator has to be able to see that it was tried, how often, and
// what came back. The set itself stays derived; this is the work queue built
// from it.
//
// It is deliberately NOT the wishlist. The wishlist is what a person asked for
// by name; these are what a policy implies. Mixing them would drown the first
// in the second — one platform's set can imply hundreds of rows.

// Target statuses.
const (
	// TargetWanted: in the set, not owned, ready to be searched.
	TargetWanted = "wanted"
	// TargetGrabbed: a release was dispatched. The row leaves the queue while
	// the download runs, and the next sync retires it once the set is
	// satisfied — or re-opens it if the grab did not fill the gap.
	TargetGrabbed = "grabbed"
	// TargetUnavailable: searched and nothing usable came back. Still wanted —
	// it backs off rather than disappearing.
	TargetUnavailable = "unavailable"
)

// CollectionTarget is one gap.
type CollectionTarget struct {
	ID           int64  `json:"id"`
	PlatformSlug string `json:"platform_slug"`
	// SetKey is the set group's stable key, so a target survives a catalog
	// refresh that renames nothing.
	SetKey string `json:"set_key"`
	Title  string `json:"title"`
	// DumpName is the keeper's canonical DAT name — what the set actually
	// wants, kept for display and for the import-side hash gate to recognise.
	DumpName    string `json:"dump_name,omitempty"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	LastAttempt string `json:"last_attempt,omitempty"`
	LastReason  string `json:"last_reason,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func (s *JobStore) migrateCollectionTargets() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS collection_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform_slug TEXT NOT NULL,
			set_key TEXT NOT NULL,
			title TEXT NOT NULL,
			dump_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'wanted',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_attempt TEXT NOT NULL DEFAULT '',
			last_reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_collection_targets_key ON collection_targets(platform_slug, set_key)`,
		`CREATE INDEX IF NOT EXISTS idx_collection_targets_status ON collection_targets(platform_slug, status)`,
	}
	for _, ddl := range stmts {
		if _, err := s.db.Exec(ddl); err != nil {
			slog.Warn("migrate collection targets", "error", err)
		}
	}
}

// CollectionGap is one wanted entry as the sync hands it over.
type CollectionGap struct {
	SetKey   string
	Title    string
	DumpName string
}

// grabbedTimeout is how long a dispatched grab has to actually fill its gap.
//
// 🔴 A grab is not a fill. Observed on a live install the day this shipped: a
// gap was grabbed, the release imported, and the set still wanted the game —
// the dump that landed was not the one the catalog names, so the gap never
// closed while the row sat in "grabbed" forever, never retried and never
// retired. Anything still wanted this long after its grab did not get filled by
// it, whatever the download said.
const grabbedTimeout = 12 * time.Hour

// SyncCollectionTargets makes the stored gap list match the set's.
//
// New gaps are inserted; gaps that are still gaps keep their attempt history
// (re-inserting would reset the backoff and re-search a dead title every
// cycle); rows the set no longer wants are DELETED — a filled gap's record is
// the library item, and keeping a tombstone here would mean two answers to
// "what is missing". Returns added and removed counts.
func (s *JobStore) SyncCollectionTargets(platformSlug string, gaps []CollectionGap) (added, removed int) {
	if strings.TrimSpace(platformSlug) == "" {
		return 0, 0
	}
	tx, err := s.db.Begin()
	if err != nil {
		slog.Warn("sync collection targets", "error", err)
		return 0, 0
	}
	defer tx.Rollback()

	existing := map[string]bool{}
	rows, err := tx.Query(`SELECT set_key FROM collection_targets WHERE platform_slug = ?`, platformSlug)
	if err != nil {
		slog.Warn("sync collection targets", "error", err)
		return 0, 0
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			existing[k] = true
		}
	}
	rows.Close()

	want := make(map[string]bool, len(gaps))
	for _, g := range gaps {
		want[g.SetKey] = true
		if existing[g.SetKey] {
			// Keep status, attempts and backoff; refresh only what the catalog
			// can legitimately have changed.
			if _, err := tx.Exec(
				`UPDATE collection_targets SET title = ?, dump_name = ? WHERE platform_slug = ? AND set_key = ?`,
				g.Title, g.DumpName, platformSlug, g.SetKey); err != nil {
				slog.Warn("refresh collection target", "error", err)
			}
			// The game is STILL wanted, so a grab that has aged out did not
			// fill it. Re-open the row — attempts survive, so its own backoff
			// decides when it is tried again.
			if _, err := tx.Exec(
				`UPDATE collection_targets
				    SET status = ?, last_reason = ?
				  WHERE platform_slug = ? AND set_key = ? AND status = ? AND last_attempt < ?`,
				TargetWanted, "the grabbed release did not fill this gap",
				platformSlug, g.SetKey, TargetGrabbed,
				time.Now().UTC().Add(-grabbedTimeout).Format("2006-01-02 15:04:05"),
			); err != nil {
				slog.Warn("reopen collection target", "error", err)
			}
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO collection_targets (platform_slug, set_key, title, dump_name, status)
			 VALUES (?, ?, ?, ?, ?)`,
			platformSlug, g.SetKey, g.Title, g.DumpName, TargetWanted); err != nil {
			slog.Warn("insert collection target", "error", err)
			continue
		}
		added++
	}
	for key := range existing {
		if want[key] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM collection_targets WHERE platform_slug = ? AND set_key = ?`,
			platformSlug, key); err != nil {
			slog.Warn("delete collection target", "error", err)
			continue
		}
		removed++
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("sync collection targets", "error", err)
		return 0, 0
	}
	return added, removed
}

// ClearCollectionTargets drops a platform's gap list, for when collection mode
// is turned off. The rows are derived from a policy that no longer applies, so
// leaving them would leave the scheduler work nobody asked for.
func (s *JobStore) ClearCollectionTargets(platformSlug string) int {
	res, err := s.db.Exec(`DELETE FROM collection_targets WHERE platform_slug = ?`, platformSlug)
	if err != nil {
		slog.Warn("clear collection targets", "error", err)
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// CollectionTargetQuery filters a listing.
type CollectionTargetQuery struct {
	PlatformSlug string
	Status       string
	Text         string
	Limit        int
	Offset       int
}

// ListCollectionTargets returns a page plus the total matching count.
func (s *JobStore) ListCollectionTargets(q CollectionTargetQuery) ([]CollectionTarget, int) {
	where := []string{"1=1"}
	args := []interface{}{}
	if q.PlatformSlug != "" {
		where, args = append(where, "platform_slug = ?"), append(args, q.PlatformSlug)
	}
	if q.Status != "" && q.Status != "all" {
		where, args = append(where, "status = ?"), append(args, q.Status)
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		where, args = append(where, "LOWER(title) LIKE ?"), append(args, "%"+strings.ToLower(t)+"%")
	}
	clause := strings.Join(where, " AND ")

	var total int
	s.db.QueryRow(`SELECT COUNT(*) FROM collection_targets WHERE `+clause, args...).Scan(&total)

	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	rows, err := s.db.Query(
		`SELECT id, platform_slug, set_key, title, dump_name, status, attempts, last_attempt, last_reason, created_at
		   FROM collection_targets WHERE `+clause+`
		  ORDER BY platform_slug, title LIMIT ? OFFSET ?`,
		append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, total
	}
	defer rows.Close()
	var out []CollectionTarget
	for rows.Next() {
		var t CollectionTarget
		if err := rows.Scan(&t.ID, &t.PlatformSlug, &t.SetKey, &t.Title, &t.DumpName,
			&t.Status, &t.Attempts, &t.LastAttempt, &t.LastReason, &t.CreatedAt); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, total
}

// DueCollectionTargets returns a platform's targets ready to be searched now,
// oldest-attempt first so a cycle works through the queue rather than
// re-visiting the same head every hour.
//
// Backoff is exponential in the attempt count: a title no indexer carries is
// tried again tomorrow, not in an hour. now is passed in so the caller's clock
// is the one under test.
func (s *JobStore) DueCollectionTargets(platformSlug string, limit int, now time.Time) []CollectionTarget {
	rows, err := s.db.Query(
		`SELECT id, platform_slug, set_key, title, dump_name, status, attempts, last_attempt, last_reason, created_at
		   FROM collection_targets
		  WHERE platform_slug = ? AND status != ?
		  ORDER BY last_attempt ASC, title ASC`,
		platformSlug, TargetGrabbed)
	if err != nil {
		slog.Warn("due collection targets", "error", err)
		return nil
	}
	defer rows.Close()
	var out []CollectionTarget
	for rows.Next() {
		var t CollectionTarget
		if err := rows.Scan(&t.ID, &t.PlatformSlug, &t.SetKey, &t.Title, &t.DumpName,
			&t.Status, &t.Attempts, &t.LastAttempt, &t.LastReason, &t.CreatedAt); err != nil {
			continue
		}
		if !t.due(now) {
			continue
		}
		out = append(out, t)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// backoffCap bounds the retry interval. A week is long enough that a title no
// source carries costs nothing, and short enough that a newly-indexed release
// is found without an operator intervening.
const backoffCap = 7 * 24 * time.Hour

func (t CollectionTarget) due(now time.Time) bool {
	if t.Attempts == 0 || t.LastAttempt == "" {
		return true
	}
	last, err := time.Parse("2006-01-02 15:04:05", t.LastAttempt)
	if err != nil {
		return true
	}
	wait := time.Duration(1<<min(t.Attempts-1, 8)) * time.Hour
	if wait > backoffCap {
		wait = backoffCap
	}
	return !now.Before(last.UTC().Add(wait))
}

// RecordCollectionAttempt stamps an attempt's outcome on a target.
func (s *JobStore) RecordCollectionAttempt(id int64, status, reason string) {
	if _, err := s.db.Exec(
		`UPDATE collection_targets
		    SET status = ?, attempts = attempts + 1, last_attempt = ?, last_reason = ?
		  WHERE id = ?`,
		status, time.Now().UTC().Format("2006-01-02 15:04:05"), reason, id); err != nil {
		slog.Warn("record collection attempt", "error", err)
	}
}

// CollectionTargetCounts summarises the queue for a header line.
func (s *JobStore) CollectionTargetCounts() map[string]int {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM collection_targets GROUP BY status`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err == nil {
			out[status] = n
		}
	}
	return out
}

// DeleteCollectionTarget retires one row. Used when a target turns out to be
// owned mid-cycle: the next sync would drop it anyway, and leaving it until
// then would let one more cycle search for something already on disk.
func (s *JobStore) DeleteCollectionTarget(id int64) {
	if _, err := s.db.Exec(`DELETE FROM collection_targets WHERE id = ?`, id); err != nil {
		slog.Warn("delete collection target", "error", err)
	}
}
