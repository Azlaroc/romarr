package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Retitle: the one-shot sweep that moves library titles to their
// catalog-canonical form (full DAT game name, else file-name stem). Same
// discipline as prune: preview classifies, apply changes exactly the held
// preview, nothing moves without a human seeing the diff — every route here
// is admin-only, and revert undoes the whole sweep from the per-row
// breadcrumbs.

// handleRetitleStatus handles GET /api/retitle/status.
func (s *Server) handleRetitleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.retitle.Status()) // nil-receiver safe
}

// handleRetitlePreview handles POST /api/retitle/preview.
func (s *Server) handleRetitlePreview(w http.ResponseWriter, r *http.Request) {
	if !s.retitle.TriggerPreview() {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "already running"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Retitle preview started"})
}

// handleRetitleResults handles GET /api/retitle/preview/results.
func (s *Server) handleRetitleResults(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	rows, total := s.retitle.PreviewPage(page, pageSize)
	writeJSON(w, http.StatusOK, map[string]interface{}{"rows": rows, "total": total})
}

// handleRetitleApply handles POST /api/retitle/apply.
func (s *Server) handleRetitleApply(w http.ResponseWriter, r *http.Request) {
	if !requireContentTypeJSON(w, r) {
		return
	}
	var req struct {
		ExcludeIDs []int64 `json:"exclude_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !s.retitle.TriggerApply(req.ExcludeIDs) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false, "error": "no finished preview to apply (run a preview first)",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Retitle apply started"})
}

// handleRetitleStop handles POST /api/retitle/stop.
func (s *Server) handleRetitleStop(w http.ResponseWriter, r *http.Request) {
	s.retitle.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleRetitleRevert handles POST /api/retitle/revert — the inverse sweep,
// driven by the `$.gamarr.retitle.from` breadcrumbs alone.
func (s *Server) handleRetitleRevert(w http.ResponseWriter, r *http.Request) {
	n, err := s.retitle.Revert()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "reverted": n})
}
