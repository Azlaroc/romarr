package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gamarr/internal/prune"
)

// Declutter: the 1G1R set read in the prune direction. Preview classifies,
// apply archives, and nothing moves without a human seeing the diff — so every
// write here is admin-only, and the preview is the only thing that runs
// unattended.

func (s *Server) pruneRunner(w http.ResponseWriter) bool {
	if s.prune == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "declutter not configured",
		})
		return false
	}
	return true
}

// handlePruneStatus handles GET /api/library/prune/status.
func (s *Server) handlePruneStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.prune.Status()) // nil-receiver safe
}

// handlePrunePreview handles POST /api/library/prune/preview.
func (s *Server) handlePrunePreview(w http.ResponseWriter, r *http.Request) {
	if !s.pruneRunner(w) {
		return
	}
	if !requireContentTypeJSON(w, r) {
		return
	}
	var req struct {
		PlatformSlug string `json:"platform_slug"`
		// IncludeExcluded adds owned dumps from groups policy leaves out of
		// the set — hacks, prototypes, unlicensed. Archiving one removes a
		// game rather than a duplicate, so it is opt-in per run.
		IncludeExcluded bool `json:"include_excluded"`
		// IncludeUncataloguedDupes adds files no catalog knows whose TITLE is
		// a game the set covers and already holds. Opt-in separately because
		// the evidence is a title match, not a hash.
		IncludeUncataloguedDupes bool `json:"include_uncatalogued_duplicates"`
		// IncludeUncataloguedHacks adds files no catalog knows whose NAME
		// declares them a hack, alternate or bad dump, pirate release or fan
		// translation.
		IncludeUncataloguedHacks bool `json:"include_uncatalogued_hacks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PlatformSlug) == "" {
		writeError(w, http.StatusBadRequest, "platform_slug required (a slug or \"all\")")
		return
	}
	if !s.prune.TriggerPreview(strings.TrimSpace(req.PlatformSlug), prune.Opts{
		IncludeExcluded:          req.IncludeExcluded,
		IncludeUncataloguedDupes: req.IncludeUncataloguedDupes,
		IncludeUncataloguedHacks: req.IncludeUncataloguedHacks,
	}) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "already running"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "message": "Declutter preview started", "scope": req.PlatformSlug,
	})
}

// handlePruneResults handles GET /api/library/prune/preview/results.
func (s *Server) handlePruneResults(w http.ResponseWriter, r *http.Request) {
	if !s.pruneRunner(w) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	rows, total := s.prune.PreviewPage(page, pageSize)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "items": rows, "total": total,
	})
}

// handlePruneApply handles POST /api/library/prune/apply — archives the held
// preview's planned rows, minus {"exclude_ids":[...]}.
func (s *Server) handlePruneApply(w http.ResponseWriter, r *http.Request) {
	if !s.pruneRunner(w) {
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
	if !s.prune.TriggerApply(req.ExcludeIDs) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false, "error": "already running, or no preview held with anything to archive",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Declutter apply started"})
}

// handlePruneStop handles POST /api/library/prune/stop.
func (s *Server) handlePruneStop(w http.ResponseWriter, r *http.Request) {
	if !s.pruneRunner(w) {
		return
	}
	s.prune.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Stopping"})
}
