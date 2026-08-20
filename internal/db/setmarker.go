package db

import (
	"encoding/json"
	"sort"
)

// SetMarker mirrors metadata $.gamarr.set on a disc-set library row: the
// durable record of the set's composition and repair state. Member job blobs
// are the only other evidence of a degraded finalize and the 7-day job cleanup
// purges them, so anything the repair pass needs later must live here.
type SetMarker struct {
	ID             string `json:"id"`                         // disc_set_id (source_id minus the "set:" prefix)
	Total          int    `json:"total"`                      // declared disc count the set is complete at
	Have           []int  `json:"have"`                       // imported disc indices, ascending
	Degraded       bool   `json:"degraded"`                   // finalized with fewer discs than declared
	RepairAttempts int    `json:"repair_attempts"`            // repair cycles spent on this set
	Exhausted      bool   `json:"repair_exhausted,omitempty"` // repair gave up; needs manual intervention
	DegradedAt     string `json:"degraded_at,omitempty"`      // RFC3339, first degraded finalize
	RepairedAt     string `json:"repaired_at,omitempty"`      // RFC3339, complete finalize after a degrade
}

// ParseSetMarker reads $.gamarr.set out of a metadata blob; ok=false when the
// blob is malformed or carries no set record.
func ParseSetMarker(metadata string) (SetMarker, bool) {
	var envelope struct {
		Gamarr struct {
			Set *SetMarker `json:"set"`
		} `json:"gamarr"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil || envelope.Gamarr.Set == nil {
		return SetMarker{}, false
	}
	return *envelope.Gamarr.Set, true
}

// SaveSetMarker merges mk into the row's metadata at $.gamarr.set, preserving
// every other key (RomM identity under $.romm, our hashes under $.gamarr — see
// docs/library-identity.md).
// The row's metadata is re-read here rather than taken from the caller's
// snapshot so a concurrent merge (e.g. the RomM sync) isn't clobbered.
func (s *JobStore) SaveSetMarker(libraryID int64, mk SetMarker) error {
	item, err := s.GetLibraryItem(libraryID)
	if err != nil {
		return err
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &meta); err != nil || meta == nil {
		meta = map[string]interface{}{}
	}
	gamarr, _ := meta["gamarr"].(map[string]interface{})
	if gamarr == nil {
		gamarr = map[string]interface{}{}
	}
	sort.Ints(mk.Have)
	gamarr["set"] = mk
	meta["gamarr"] = gamarr
	out, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return s.UpdateLibraryItemMetadata(libraryID, string(out))
}

// ListDegradedSets returns the disc-set library rows flagged degraded, oldest
// first. Metadata is json_valid-guarded like FindLibraryByHash: one malformed
// blob must not error the whole query. SQLite's json_extract yields integer 1
// for JSON true.
func (s *JobStore) ListDegradedSets() []LibraryItem {
	rows, err := s.db.Query(
		"SELECT id, title, platform, platform_slug, is_pc, file_path, file_size, source, source_type, source_id, metadata, added_at FROM library_items " +
			"WHERE source_id LIKE 'set:%' AND json_extract(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END, '$.gamarr.set.degraded') = 1 " +
			"ORDER BY added_at, id")
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
