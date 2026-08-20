package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gamarr/internal/hashfill"
)

// The hash backfill: give library rows that carry no hash the hash of their
// ROM's bytes, so ownership can be decided by proof rather than by a guess at
// the title.
//
// 🔴 This plane is deliberately shaped UNLIKE its two siblings. Rename and
// declutter are preview-then-apply because they move files and a human must
// see the diff first. This one writes a JSON field on rows that have none, so
// there is nothing to approve — and a preview phase would pay the whole cost
// of the run (the hashing) only to discard the answer. `run` with
// {"dry_run":true} is the preview: same pass, no write, same paged results.
//
// Same auth rule as the siblings: the status read is open, every write is
// admin-only.

func (s *Server) hashRunner(w http.ResponseWriter) bool {
	if s.hashfill == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "hash backfill not configured",
		})
		return false
	}
	return true
}

// handleHashStatus handles GET /api/library/hash/status.
func (s *Server) handleHashStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hashfill.Status()) // nil-receiver safe
}

// handleHashRun handles POST /api/library/hash/run.
func (s *Server) handleHashRun(w http.ResponseWriter, r *http.Request) {
	if !s.hashRunner(w) {
		return
	}
	if !requireContentTypeJSON(w, r) {
		return
	}
	var req struct {
		PlatformSlug string `json:"platform_slug"`
		// DryRun hashes everything and writes nothing — the way to check a
		// derivation (a container-header rule, say) against real files
		// before committing thousands of rows to it.
		DryRun bool `json:"dry_run"`
		// Force re-visits rows that already carry a hash or a permanent skip
		// marker, for re-hashing after that derivation changes.
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PlatformSlug) == "" {
		writeError(w, http.StatusBadRequest, "platform_slug required (a slug or \"all\")")
		return
	}
	scope := strings.TrimSpace(req.PlatformSlug)
	if !s.hashfill.Trigger(scope, hashfill.Opts{DryRun: req.DryRun, Force: req.Force}) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "already running"})
		return
	}
	message := "Hash backfill started"
	if req.DryRun {
		message = "Hash backfill dry run started"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "message": message, "scope": scope, "dry_run": req.DryRun,
	})
}

// handleHashResults handles GET /api/library/hash/results.
func (s *Server) handleHashResults(w http.ResponseWriter, r *http.Request) {
	if !s.hashRunner(w) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	rows, total := s.hashfill.ResultsPage(page, pageSize)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "items": rows, "total": total,
	})
}

// handleHashStop handles POST /api/library/hash/stop.
func (s *Server) handleHashStop(w http.ResponseWriter, r *http.Request) {
	if !s.hashRunner(w) {
		return
	}
	s.hashfill.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Stopping"})
}
