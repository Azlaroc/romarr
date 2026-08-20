package db

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
)

// The library's identity contract: which hashes a row carries and what each
// one means. Full rationale in docs/library-identity.md.
//
// Two namespaces, and the difference is not cosmetic:
//
//   - $.romm.{crc,md5,sha1} — RomM's, rewritten wholesale on every sync.
//     Never write here; mergeRommMetadata would clobber it.
//   - $.gamarr.* — ours, preserved across syncs.
//
// Inside ours:
//
//	$.gamarr.{crc,md5,sha1}  the ROM's own bytes (an archive's INNER file).
//	                         The file's identity: stable forever, derived
//	                         from nothing but the bytes.
//	$.gamarr.unh.*           the same bytes minus a container header, present
//	                         only when one was recognised. What the catalog
//	                         can be asked about on headered platforms.
//	$.gamarr.release.*       the source's PUBLISHED hash of the file it
//	                         served — a different object entirely (an
//	                         archive's outer bytes), useful only for "have I
//	                         downloaded this exact release before?".
//	$.gamarr.hash_skipped    why this row can never carry a hash.
//
// One key, one meaning. Release hashes used to live at $.gamarr.{md5,sha1};
// migrateLibraryReleaseHashes moves the handful of rows that predate the
// split so nothing has to guess which sense a value is in.

// UnheaderedHashes are a ROM's hashes with its container header removed.
type UnheaderedHashes struct {
	CRC    string `json:"crc,omitempty"`
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	Header string `json:"header,omitempty"` // the header kind that was stripped
}

// LibraryHashes is one row's computed identity, ready to persist.
type LibraryHashes struct {
	CRC      string
	MD5      string
	SHA1     string
	Unh      *UnheaderedHashes
	HashedAt string // RFC3339; filled in by the writer when empty
}

// Hash-skip reasons that are permanent facts about the entry rather than
// today's weather. A row marked with one stops being enumerated for hashing,
// so `pending` can reach zero instead of parking a remainder that reads as a
// stuck job. Transient trouble — a missing file, a full disk, an unreadable
// mount — is deliberately NOT marked: it is worth retrying.
const (
	HashSkipDirectory = "directory"
	HashSkipMultiFile = "multi-file-archive"
	HashSkipRar       = "rar-unsupported"
)

// jsonMeta guards every metadata read: json_extract raises on a malformed
// blob and would error the whole statement, not just the bad row.
const jsonMeta = "CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END"

// SaveLibraryHashes writes a row's computed identity under $.gamarr.
//
// One statement, not read-modify-write: a backfill sweep runs for minutes
// beside a live RomM sync, and json_set patches the leaves it names while
// leaving every sibling ($.romm, $.gamarr.set, $.gamarr.catalog) untouched.
// It also creates the $.gamarr object when the row has none.
func (s *JobStore) SaveLibraryHashes(id int64, h LibraryHashes) error {
	sets, args, err := hashSetClause(h)
	if err != nil {
		return err
	}
	args = append(args, id)
	_, err = s.db.Exec(
		"UPDATE library_items SET metadata = json_set("+jsonMeta+", "+strings.Join(sets, ", ")+") WHERE id = ?", args...)
	return err
}

// SaveLibraryHashesBySourceID is the same write keyed on source_id, for the
// import path, which knows the source_id it just wrote and not the row id.
func (s *JobStore) SaveLibraryHashesBySourceID(sourceID string, h LibraryHashes) error {
	if sourceID == "" {
		return nil
	}
	sets, args, err := hashSetClause(h)
	if err != nil {
		return err
	}
	args = append(args, sourceID)
	_, err = s.db.Exec(
		"UPDATE library_items SET metadata = json_set("+jsonMeta+", "+strings.Join(sets, ", ")+") WHERE source_id = ?", args...)
	return err
}

// hashSetClause builds the json_set path/value pairs.
//
// 🔴 Pairs, never a map: json_set takes path and value positionally, so a
// map's iteration order would pair the placeholders with the wrong values —
// silently, and only when more than one hash family is present.
func hashSetClause(h LibraryHashes) ([]string, []interface{}, error) {
	stamp := h.HashedAt
	if stamp == "" {
		stamp = time.Now().UTC().Format(time.RFC3339)
	}
	pairs := []struct{ path, val string }{
		{"$.gamarr.crc", lowerTrim(h.CRC)},
		{"$.gamarr.md5", lowerTrim(h.MD5)},
		{"$.gamarr.sha1", lowerTrim(h.SHA1)},
		{"$.gamarr.hashed_at", stamp},
	}
	var sets []string
	var args []interface{}
	for _, p := range pairs {
		if p.val == "" {
			continue
		}
		sets = append(sets, "?, ?")
		args = append(args, p.path, p.val)
	}
	if h.Unh != nil {
		blob, err := json.Marshal(UnheaderedHashes{
			CRC:    lowerTrim(h.Unh.CRC),
			MD5:    lowerTrim(h.Unh.MD5),
			SHA1:   lowerTrim(h.Unh.SHA1),
			Header: h.Unh.Header,
		})
		if err != nil {
			return nil, nil, err
		}
		sets = append(sets, "?, json(?)")
		args = append(args, "$.gamarr.unh", string(blob))
	}
	return sets, args, nil
}

// MarkLibraryHashSkipped records why a row can never be hashed. Idempotent.
func (s *JobStore) MarkLibraryHashSkipped(id int64, reason string) error {
	if reason == "" {
		return nil
	}
	_, err := s.db.Exec(
		"UPDATE library_items SET metadata = json_set("+jsonMeta+", '$.gamarr.hash_skipped', ?) WHERE id = ?",
		reason, id)
	return err
}

// hashlessPredicate matches rows carrying no hash in either namespace and no
// permanent skip marker. NULLIF folds an empty string into NULL so a row that
// was written with a blank hash reads as hashless, and the json_valid guard
// makes a malformed blob count as NEEDING a hash rather than erroring the
// query — the row is a candidate precisely because nothing can be read off it.
const hashlessPredicate = "json_extract(" + jsonMeta + ", '$.gamarr.hash_skipped') IS NULL" +
	" AND NULLIF(json_extract(" + jsonMeta + ", '$.romm.md5'), '') IS NULL" +
	" AND NULLIF(json_extract(" + jsonMeta + ", '$.romm.sha1'), '') IS NULL" +
	" AND NULLIF(json_extract(" + jsonMeta + ", '$.gamarr.md5'), '') IS NULL" +
	" AND NULLIF(json_extract(" + jsonMeta + ", '$.gamarr.sha1'), '') IS NULL"

// ListLibraryItemsNeedingHash returns the rows a hash backfill should visit:
// non-PC rows with no hash in either namespace, one platform or all when slug
// is "". Ordered platform then path so batch progress reads coherently.
//
// The filter is in SQL rather than in the caller so a re-run costs O(rows
// still missing a hash) instead of O(library) — and so the runner's "total"
// means "work to do" rather than "rows scanned".
//
// force ignores every exclusion and returns the whole scope, for re-hashing
// after a derivation change (a new header rule, say).
func (s *JobStore) ListLibraryItemsNeedingHash(platformSlug string, force bool) []LibraryItem {
	query := "SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at" +
		" FROM library_items WHERE is_pc = 0"
	var args []interface{}
	if platformSlug != "" {
		query += " AND platform_slug = ?"
		args = append(args, platformSlug)
	}
	if !force {
		query += " AND " + hashlessPredicate
	}
	query += " ORDER BY platform_slug, file_path"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		slog.Warn("list library items needing hash", "error", err)
		return nil
	}
	defer rows.Close()
	return scanLibraryItems(rows)
}

// CountLibraryItemsNeedingHash reports how many rows per platform still need
// a hash. It is the number the UI shows before anything runs, so an operator
// can see the size of the job without starting it.
func (s *JobStore) CountLibraryItemsNeedingHash() map[string]int {
	rows, err := s.db.Query(
		"SELECT platform_slug, COUNT(*) FROM library_items WHERE is_pc = 0 AND " + hashlessPredicate +
			" GROUP BY platform_slug")
	if err != nil {
		slog.Warn("count library items needing hash", "error", err)
		return nil
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			continue
		}
		out[slug] = n
	}
	return out
}

// migrateLibraryReleaseHashes moves pre-split release hashes from
// $.gamarr.{md5,sha1} to $.gamarr.release.*.
//
// Those keys now mean "the ROM's own bytes"; before the split they held the
// source's published hash of the file it served, which for an archive is a
// different object. Rather than carry a discriminator forever, the handful of
// rows that predate the split are moved once. Guarded on the absence of
// $.gamarr.hashed_at, which only the new writer sets, so a second run is a
// no-op and a backfilled row is never touched.
func (s *JobStore) migrateLibraryReleaseHashes() {
	res, err := s.db.Exec(
		`UPDATE library_items
		    SET metadata = json_remove(
		          json_set(` + jsonMeta + `,
		            '$.gamarr.release',
		            json_object(
		              'md5',  json_extract(` + jsonMeta + `, '$.gamarr.md5'),
		              'sha1', json_extract(` + jsonMeta + `, '$.gamarr.sha1'))),
		          '$.gamarr.md5', '$.gamarr.sha1')
		  WHERE json_extract(` + jsonMeta + `, '$.gamarr.hashed_at') IS NULL
		    AND (json_extract(` + jsonMeta + `, '$.gamarr.md5') IS NOT NULL
		      OR json_extract(` + jsonMeta + `, '$.gamarr.sha1') IS NOT NULL)`)
	if err != nil {
		slog.Warn("migrate library release hashes", "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("moved pre-split release hashes to $.gamarr.release", "rows", n)
	}
}

func lowerTrim(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// scanLibraryItems drains a rows cursor selecting the full library column
// list, in order. Shared so a new lister cannot transcribe the Scan args
// slightly differently from the existing ones.
func scanLibraryItems(rows *sql.Rows) []LibraryItem {
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

// hashFamilies are the metadata paths a hash can live at, strongest sense
// first. Order is load-bearing only in that it must stay stable: callers
// build parallel clause and column lists from it.
var hashFamilies = []string{"romm", "gamarr", "gamarr.unh", "gamarr.release"}
