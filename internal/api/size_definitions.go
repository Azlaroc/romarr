package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/datsvc"
	"gamarr/internal/db"
	"gamarr/internal/search"
)

// Per-platform size definitions: the band a candidate's size is judged
// against, and the reason a release was or was not rejected on size.
//
// These are configuration, so the whole surface is admin-only alongside
// /api/settings, rather than following the DAT status and coverage reads
// which are open.

// sizeDefinitionView is one platform's row as a screen needs it: the stored
// bounds plus enough context to explain them and to offer a reset.
type sizeDefinitionView struct {
	db.PlatformSizeRow
	// HasCatalog reports whether a reset has anything to re-derive from.
	// Without it a UI cannot tell "reset restores the catalog's numbers" from
	// "reset clears this platform's limits entirely".
	HasCatalog bool `json:"has_catalog"`
}

// handleSizeDefinitions lists every platform that either has a definition or
// has a catalog lane. The union matters: the DAT vocabulary is deliberately
// wider than the download-side platform list, so enumerating from the latter
// would hide the small-cartridge platforms this feature exists for.
func (s *Server) handleSizeDefinitions(w http.ResponseWriter, r *http.Request) {
	jobs := s.mgr.Jobs()

	views := map[string]*sizeDefinitionView{}
	for _, row := range jobs.ListPlatformSizes() {
		views[row.PlatformSlug] = &sizeDefinitionView{PlatformSizeRow: row}
	}
	for _, p := range jobs.ListDatPlatforms() {
		if _, ok := views[p.PlatformSlug]; !ok {
			// No definition yet — surfaced anyway, with zeroes, so a platform
			// whose catalog says nothing is visibly unbounded rather than
			// missing from the screen.
			views[p.PlatformSlug] = &sizeDefinitionView{
				PlatformSizeRow: db.PlatformSizeRow{PlatformSlug: p.PlatformSlug},
			}
		}
	}
	for slug, v := range views {
		_, v.HasCatalog = jobs.ActiveDatSnapshot(slug)
	}

	out := make([]sizeDefinitionView, 0, len(views))
	for _, v := range views {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlatformSlug < out[j].PlatformSlug })
	writeJSON(w, http.StatusOK, map[string]interface{}{"definitions": out})
}

// handleUpdateSizeDefinition stores an operator's own bounds for a platform.
// They are enforced exactly as given — a typed bound is a statement about the
// files themselves, not about a catalog, so nothing widens it afterwards.
func (s *Server) handleUpdateSizeDefinition(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if err := datsvc.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		MinSize *int64 `json:"min_size"`
		MaxSize *int64 `json:"max_size"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.MinSize == nil && req.MaxSize == nil {
		writeError(w, http.StatusBadRequest, "min_size or max_size required")
		return
	}

	jobs := s.mgr.Jobs()
	row := db.PlatformSizeRow{PlatformSlug: slug}
	if existing, ok := jobs.GetPlatformSize(slug); ok {
		row = existing
	}
	// Omitting an end leaves it alone rather than zeroing it, so a caller
	// setting only a floor does not silently remove the ceiling.
	if req.MinSize != nil {
		row.MinSize = *req.MinSize
	}
	if req.MaxSize != nil {
		row.MaxSize = *req.MaxSize
	}
	row.Source = db.SizeSourceManual

	if err := jobs.SetPlatformSize(row); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	search.RefreshBands()
	jobs.LogActivity("size_definition_updated", platformLabel(slug),
		describeBand(row.MinSize, row.MaxSize), "", nil)

	stored, _ := jobs.GetPlatformSize(slug)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "definition": stored})
}

// handleResetSizeDefinition returns a platform to its catalog-derived band,
// or to no limits at all when nothing has been imported for it.
//
// There is no DELETE here: retiring an operator's numbers is a reset to the
// authority's, which is a different thing from removing the platform, and
// the DAT endpoints already establish reset-not-delete as the shape.
func (s *Server) handleResetSizeDefinition(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if err := datsvc.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobs := s.mgr.Jobs()
	snap, ok := jobs.ActiveDatSnapshot(slug)
	if !ok {
		if err := jobs.DeletePlatformSize(slug); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		search.RefreshBands()
		jobs.LogActivity("size_definition_updated", platformLabel(slug),
			"reset to no limits (no catalog for this platform)", "", nil)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "definition": db.PlatformSizeRow{PlatformSlug: slug},
		})
		return
	}

	def := datsvc.DeriveSizeDefinition(snap)
	if def.MinSize == 0 && def.MaxSize == 0 {
		if err := jobs.DeletePlatformSize(slug); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if err := jobs.SetPlatformSize(def); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	search.RefreshBands()
	jobs.LogActivity("size_definition_updated", platformLabel(slug),
		"reset to the catalog band: "+describeBand(def.MinSize, def.MaxSize), "", nil)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "definition": def})
}

func platformLabel(slug string) string { return "Size definition (" + slug + ")" }

// describeBand renders a band the way the rest of the surface talks about
// one: zero is "unlimited", never "0 bytes".
func describeBand(min, max int64) string {
	lo, hi := "no floor", "no ceiling"
	if min > 0 {
		lo = "floor " + search.HumanSize(min)
	}
	if max > 0 {
		hi = "ceiling " + search.HumanSize(max)
	}
	return lo + ", " + hi
}
