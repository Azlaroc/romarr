package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleNormalizeStatus handles GET /api/library/normalize/status.
func (s *Server) handleNormalizeStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.renamer.Status()) // nil-receiver safe
}

// handleNormalizePreview handles POST /api/library/normalize/preview —
// starts an async bulk-rename preview over {"platform_slug":"gb"} or "all".
func (s *Server) handleNormalizePreview(w http.ResponseWriter, r *http.Request) {
	if s.renamer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "renamer not configured",
		})
		return
	}
	if !requireContentTypeJSON(w, r) {
		return
	}
	var req struct {
		PlatformSlug string `json:"platform_slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlatformSlug == "" {
		writeError(w, http.StatusBadRequest, "platform_slug required (a slug or \"all\")")
		return
	}
	if !s.renamer.TriggerPreview(req.PlatformSlug) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "already running",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Rename preview started",
		"scope":   req.PlatformSlug,
	})
}

// handleNormalizeResults handles GET /api/library/normalize/preview/results.
func (s *Server) handleNormalizeResults(w http.ResponseWriter, r *http.Request) {
	if s.renamer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "renamer not configured",
		})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize > 500 {
		pageSize = 500
	}
	rows, total := s.renamer.PreviewPage(page, pageSize)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"items":   rows,
		"total":   total,
	})
}

// handleNormalizeApply handles POST /api/library/normalize/apply — applies
// the held preview's planned renames, minus {"exclude_ids":[...]}.
func (s *Server) handleNormalizeApply(w http.ResponseWriter, r *http.Request) {
	if s.renamer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "renamer not configured",
		})
		return
	}
	var req struct {
		ExcludeIDs []int64 `json:"exclude_ids"`
	}
	if r.ContentLength != 0 {
		if !requireContentTypeJSON(w, r) {
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
	}
	if !s.renamer.TriggerApply(req.ExcludeIDs) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "already running, or no preview held",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Rename apply started",
	})
}

// handleNormalizeStop handles POST /api/library/normalize/stop.
func (s *Server) handleNormalizeStop(w http.ResponseWriter, r *http.Request) {
	if s.renamer == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "renamer not configured",
		})
		return
	}
	s.renamer.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}
