package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ── The dump override ("That!") ────────────────────────────────────────────────
//
// Setting an override records the exact catalogued dump this title's next
// grab must BE, on the title's wishlist row (created when the title had
// none — an owned title's override is an upgrade request). Stored as
// name + the keeper roms' hashes; a catalog row id would be snapshot-scoped
// and die on every refresh. The selector enforces it: unmet means wait,
// and the decision log names the pin either way — an override must never
// be silently identical to the default.

func (s *Server) handleSetOverride(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid library id")
		return
	}
	item, err := s.mgr.Jobs().GetLibraryItem(id)
	if err != nil {
		writeError(w, 404, "Library item not found")
		return
	}
	var req struct {
		DumpName string   `json:"dump_name"`
		Hashes   []string `json:"hashes"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.DumpName = strings.TrimSpace(req.DumpName)
	if req.DumpName == "" {
		writeError(w, 400, "dump_name required")
		return
	}
	wishID, err := s.mgr.Jobs().UpsertWishlistOverride(
		item.Title, item.Platform, item.PlatformSlug, req.DumpName, req.Hashes)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.mgr.Jobs().LogActivity("dump_override", item.Title,
		"Pinned dump: "+req.DumpName, "", &item.ID)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "wishlist_id": wishID, "dump_name": req.DumpName,
	})
}

func (s *Server) handleClearOverride(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid library id")
		return
	}
	item, err := s.mgr.Jobs().GetLibraryItem(id)
	if err != nil {
		writeError(w, 404, "Library item not found")
		return
	}
	if !s.mgr.Jobs().ClearWishlistOverride(item.Title, item.PlatformSlug) {
		writeError(w, 404, "No override set for this title")
		return
	}
	s.mgr.Jobs().LogActivity("dump_override", item.Title,
		"Pin cleared — policy picks again", "", &item.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
