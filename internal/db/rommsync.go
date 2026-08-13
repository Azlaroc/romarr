package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// RommSyncItem is one RomM catalog entry mapped to a library row by the
// caller. Metadata must be a JSON object whose "romm" key carries the RomM
// identity blob; on update only that key is written so foreign metadata
// (e.g. a RAWG enrich) survives.
type RommSyncItem struct {
	Item          LibraryItem
	MissingFromFS bool
}

// SyncRommItems applies one RomM catalog pull to library_items in a single
// transaction. Per item:
//   - missing_from_fs → delete any existing row with that source_id
//   - existing source_id → update in place (metadata merged, not replaced)
//   - a row with the same file_path from another source → skip ("adopt"):
//     the import row keeps the file's identity and history
//   - otherwise → insert
//
// When fullReconcile is set, rows with source='romm' whose source_id was not
// part of this pull are deleted (RomM hard-deleted them), and legacy
// fs-scanner ROM rows (source='scan' AND is_pc=0) are purged — the vault
// scanner's rows are is_pc=1 and untouched.
func (s *JobStore) SyncRommItems(items []RommSyncItem, fullReconcile bool) (added, updated, removed int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("begin romm sync tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if fullReconcile {
		// Retire the legacy fs-scanner's ROM rows BEFORE the upsert loop:
		// left in place they would satisfy the path-collision check below and
		// swallow the very roms this sync is supposed to own (the first
		// cutover sync did exactly that). Vault rows are is_pc=1 and kept.
		res, perr := tx.Exec("DELETE FROM library_items WHERE source = 'scan' AND is_pc = 0")
		if perr != nil {
			err = perr
			return 0, 0, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			removed += int(n)
		}
	}

	seen := make(map[string]struct{}, len(items))
	for i := range items {
		it := &items[i].Item
		if it.SourceID == "" {
			continue
		}
		seen[it.SourceID] = struct{}{}

		if items[i].MissingFromFS {
			res, derr := tx.Exec("DELETE FROM library_items WHERE source_id = ? AND source = 'romm'", it.SourceID)
			if derr != nil {
				err = derr
				return 0, 0, 0, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				removed += int(n)
			}
			continue
		}

		var existingID int64
		var existingMeta string
		serr := tx.QueryRow("SELECT id, metadata FROM library_items WHERE source_id = ?", it.SourceID).
			Scan(&existingID, &existingMeta)
		switch serr {
		case nil:
			merged := mergeRommMetadata(existingMeta, it.Metadata)
			_, err = tx.Exec(
				`UPDATE library_items SET title = ?, platform = ?, platform_slug = ?, file_path = ?, file_size = ?, metadata = ? WHERE id = ?`,
				it.Title, it.Platform, it.PlatformSlug, it.FilePath, it.FileSize, merged, existingID,
			)
			if err != nil {
				return 0, 0, 0, err
			}
			updated++
		case sql.ErrNoRows:
			// Adopt: an import row (torrent/ddl/nzb/manual) already tracks
			// this file — do not create a duplicate ownership row. Legacy
			// scan rows deliberately do NOT count as owners: they are the
			// rows this sync replaces, so displace them instead (covers
			// incremental runs before the first full reconcile).
			var pathOwners int
			if err = tx.QueryRow(
				"SELECT COUNT(*) FROM library_items WHERE file_path = ? AND source NOT IN ('romm', 'scan')", it.FilePath,
			).Scan(&pathOwners); err != nil {
				return 0, 0, 0, err
			}
			if pathOwners > 0 {
				continue
			}
			res, derr := tx.Exec("DELETE FROM library_items WHERE file_path = ? AND source = 'scan' AND is_pc = 0", it.FilePath)
			if derr != nil {
				err = derr
				return 0, 0, 0, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				removed += int(n)
			}
			_, err = tx.Exec(
				`INSERT INTO library_items (title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata)
				 VALUES (?, ?, ?, 0, ?, ?, 'romm', 'romm', ?, ?)`,
				it.Title, it.Platform, it.PlatformSlug, it.FilePath, it.FileSize, it.SourceID, it.Metadata,
			)
			if err != nil {
				return 0, 0, 0, err
			}
			added++
		default:
			err = serr
			return 0, 0, 0, err
		}
	}

	if fullReconcile {
		// Remove romm rows RomM no longer reports (hard-deleted upstream).
		rows, qerr := tx.Query("SELECT source_id FROM library_items WHERE source = 'romm'")
		if qerr != nil {
			err = qerr
			return 0, 0, 0, err
		}
		var stale []string
		for rows.Next() {
			var sid string
			if rows.Scan(&sid) == nil {
				if _, ok := seen[sid]; !ok {
					stale = append(stale, sid)
				}
			}
		}
		rows.Close()
		for _, sid := range stale {
			if _, err = tx.Exec("DELETE FROM library_items WHERE source_id = ? AND source = 'romm'", sid); err != nil {
				return 0, 0, 0, err
			}
			removed++
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, 0, 0, fmt.Errorf("commit romm sync tx: %w", err)
	}
	return added, updated, removed, nil
}

// mergeRommMetadata rewrites only the "romm" key of an existing metadata
// blob, preserving any sibling keys (e.g. a RAWG enrich result). Both inputs
// are JSON objects; on any parse failure the fresh blob wins.
func mergeRommMetadata(existing, fresh string) string {
	var cur map[string]json.RawMessage
	if json.Unmarshal([]byte(existing), &cur) != nil || cur == nil {
		return fresh
	}
	var incoming map[string]json.RawMessage
	if json.Unmarshal([]byte(fresh), &incoming) != nil {
		return fresh
	}
	rommBlob, ok := incoming["romm"]
	if !ok {
		return fresh
	}
	cur["romm"] = rommBlob
	out, err := json.Marshal(cur)
	if err != nil {
		return fresh
	}
	return string(out)
}

// LibraryHasFilePath reports whether any library row tracks this exact path.
func (s *JobStore) LibraryHasFilePath(path string) bool {
	if path == "" {
		return false
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM library_items WHERE file_path = ?", path).Scan(&count)
	return count > 0
}

// LibraryPlatform is one distinct non-PC platform present in the library.
type LibraryPlatform struct {
	Slug string
	Name string
}

// LibraryPlatforms returns the distinct non-PC platforms present in the
// library, for merging into the platform filter list.
func (s *JobStore) LibraryPlatforms() []LibraryPlatform {
	rows, err := s.db.Query(
		"SELECT platform_slug, MAX(platform) FROM library_items WHERE is_pc = 0 AND platform_slug != '' GROUP BY platform_slug ORDER BY platform_slug",
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []LibraryPlatform
	for rows.Next() {
		var p LibraryPlatform
		if rows.Scan(&p.Slug, &p.Name) == nil {
			out = append(out, p)
		}
	}
	return out
}
