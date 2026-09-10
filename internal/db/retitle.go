package db

import (
	"fmt"
	"strings"
)

// The retitle plane's store half: apply and revert are here so every write
// runs inside one transaction; deciding WHAT to retitle lives in
// internal/retitle.

// RetitleChange is one row's title move, as the runner decided it.
type RetitleChange struct {
	LibraryID    int64
	PlatformSlug string
	Old          string
	New          string
}

// ApplyRetitleBatch applies title changes in ONE transaction: the row's
// title, a `$.gamarr.retitle.from` breadcrumb (the revert lever — COALESCE
// keeps the ORIGINAL title if a row is ever retitled twice, so revert goes
// all the way back), and the wishlist follow (rows matching the old title
// case-insensitively on the platform move to the new one; pins ride
// untouched — override columns are not written).
//
// followSkip lists lowered old titles whose wishlist follow the caller
// suppressed (two rows sharing an old title but diverging on the new one —
// following would let the batch's last writer win silently).
func (s *JobStore) ApplyRetitleBatch(changes []RetitleChange, followSkip map[string]bool) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range changes {
		if c.LibraryID <= 0 || c.New == "" || c.New == c.Old {
			return fmt.Errorf("retitle: malformed change %+v", c)
		}
		if _, err := tx.Exec(
			`UPDATE library_items SET title = ?, metadata = json_set(
				CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,
				'$.gamarr.retitle.from',
				COALESCE(json_extract(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END, '$.gamarr.retitle.from'), ?)
			) WHERE id = ?`,
			c.New, c.Old, c.LibraryID,
		); err != nil {
			return err
		}
		if followSkip[strings.ToLower(c.Old)] {
			continue
		}
		if _, err := tx.Exec(
			"UPDATE wishlist SET title = ? WHERE LOWER(title) = LOWER(?) AND (platform_slug = ? OR platform_slug = '')",
			c.New, c.Old, c.PlatformSlug,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RetitleBreadcrumbs returns every row carrying a `$.gamarr.retitle.from`
// breadcrumb, as the change that WOULD undo it (Old = current title,
// New = the breadcrumb). This is the revert sweep's worklist.
func (s *JobStore) RetitleBreadcrumbs() ([]RetitleChange, error) {
	rows, err := s.db.Query(
		`SELECT id, platform_slug, title,
		        json_extract(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END, '$.gamarr.retitle.from')
		   FROM library_items
		  WHERE json_extract(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END, '$.gamarr.retitle.from') IS NOT NULL
		  ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetitleChange
	for rows.Next() {
		var c RetitleChange
		if err := rows.Scan(&c.LibraryID, &c.PlatformSlug, &c.Old, &c.New); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RevertRetitles is the inverse sweep: every breadcrumbed row's title goes
// back to its recorded original, the breadcrumb is removed, and the wishlist
// follows — all in one transaction. Returns how many rows moved back.
func (s *JobStore) RevertRetitles() (int, error) {
	changes, err := s.RetitleBreadcrumbs()
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, c := range changes {
		if _, err := tx.Exec(
			"UPDATE library_items SET title = ?, metadata = json_remove(metadata, '$.gamarr.retitle') WHERE id = ?",
			c.New, c.LibraryID,
		); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(
			"UPDATE wishlist SET title = ? WHERE LOWER(title) = LOWER(?) AND (platform_slug = ? OR platform_slug = '')",
			c.New, c.Old, c.PlatformSlug,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(changes), nil
}

// LibraryItemsForRetitle streams every non-PC library row in id order —
// the retitle runner's worklist. Non-PC only: PC vault entries are display
// titles from their own scanner, not DAT-catalogued ROMs.
func (s *JobStore) LibraryItemsForRetitle() ([]LibraryItem, error) {
	rows, err := s.db.Query(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items WHERE is_pc = 0 ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryItem
	for rows.Next() {
		var item LibraryItem
		var isPC int
		if err := rows.Scan(&item.ID, &item.Title, &item.Platform, &item.PlatformSlug,
			&isPC, &item.FilePath, &item.FileSize, &item.Source, &item.SourceType,
			&item.SourceID, &item.Metadata, &item.AddedAt); err != nil {
			return nil, err
		}
		item.IsPC = isPC != 0
		out = append(out, item)
	}
	return out, rows.Err()
}
