package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// ── The platform shelf ─────────────────────────────────────────────────────────
//
// GET /api/library/platforms is Level 1 of the browse: one tile per platform
// the operator actually inhabits (owned rows, or collection mode on). Two
// kinds of numbers ride each tile, with different freshness by design:
//
//   - owned count + size on disk: live SQL, always current.
//   - set quadrants (owned/covered/gaps/out + uncatalogued): the STAMPED
//     rollup the collection cycle wrote, computed_at included. Deriving a
//     whole set per tile per landing would be the ×40 reconcile this
//     endpoint exists to avoid; the set page stays the live answer.

type platformTile struct {
	Slug           string             `json:"slug"`
	DisplayName    string             `json:"display_name"`
	AccentColor    string             `json:"accent_color"`
	ArtURL         string             `json:"art_url"`
	ArtSource      string             `json:"art_source"` // "custom" | "asset" | "none"
	MediaClass     string             `json:"media_class"`
	CollectionMode bool               `json:"collection_mode"`
	Owned          int                `json:"owned"`
	SizeBytes      int64              `json:"size_bytes"`
	SetCounts      *db.PlatformRollup `json:"set_counts"` // null = never rolled up: honest absence
}

func (s *Server) handleLibraryPlatforms(w http.ResponseWriter, r *http.Request) {
	totals := s.mgr.Jobs().LibraryPlatformTotals()
	rollups := s.mgr.Jobs().GetPlatformRollups()

	seen := map[string]bool{}
	tiles := []platformTile{}
	var totalOwned int
	var totalBytes int64

	for _, row := range platform.Rows() {
		if row.IsSystem {
			continue
		}
		t := totals[row.Slug]
		if t.Count == 0 && !row.CollectionMode {
			continue // the shelf shows what is inhabited, not the whole registry
		}
		seen[row.Slug] = true
		tiles = append(tiles, s.tileFor(row, t, rollups))
		totalOwned += t.Count
		totalBytes += t.SizeBytes
	}
	// Library-only slugs the registry does not know: still shelved, with
	// empty vocabulary — hiding rows because the registry lags would make
	// the shelf lie about what is on disk.
	for slug, t := range totals {
		if seen[slug] || t.Count == 0 {
			continue
		}
		tiles = append(tiles, s.tileFor(platform.Row{Slug: slug, DisplayName: slug}, t, rollups))
		totalOwned += t.Count
		totalBytes += t.SizeBytes
	}

	writeJSON(w, 200, map[string]interface{}{
		"platforms": tiles,
		"totals": map[string]interface{}{
			"owned":      totalOwned,
			"size_bytes": totalBytes,
		},
	})
}

func (s *Server) tileFor(row platform.Row, t db.LibraryPlatformTotal, rollups map[string]db.PlatformRollup) platformTile {
	tile := platformTile{
		Slug:           row.Slug,
		DisplayName:    row.DisplayName,
		AccentColor:    row.AccentColor,
		ArtSource:      "none",
		MediaClass:     row.MediaClass,
		CollectionMode: row.CollectionMode,
		Owned:          t.Count,
		SizeBytes:      t.SizeBytes,
	}
	if _, ok := s.art.PlatformOverridePath(row.Slug); ok {
		tile.ArtURL, tile.ArtSource = "/api/art/platform/"+row.Slug, "custom"
	} else if name, ok := strings.CutPrefix(row.ArtRef, "asset:"); ok && name != "" {
		tile.ArtURL, tile.ArtSource = "/assets/platforms/"+name, "asset"
	}
	if ru, ok := rollups[row.Slug]; ok {
		ruCopy := ru
		tile.SetCounts = &ruCopy
	}
	return tile
}

func (s *Server) handleWriteRollup(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if _, ok := platform.Lookup(slug); !ok {
		writeError(w, 404, "Unknown platform")
		return
	}
	res := s.coll.WriteRollup(slug)
	writeJSON(w, 200, map[string]interface{}{
		"platform":     slug,
		"counts":       res.Counts,
		"uncatalogued": res.Uncatalogued,
	})
}
