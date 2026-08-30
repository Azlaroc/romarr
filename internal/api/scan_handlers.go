package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gamarr/internal/libscan"
)

// The library scanner: reconcile the ROM tree with library_items — adopt
// rows that exist, create rows for out-of-band arrivals, report gone files.
// It deletes nothing, ever.
//
// Same shape as the hash backfill (no preview/apply split — `run` takes a
// dry_run flag), and unlike the siblings all four routes are admin: the
// results page enumerates the library tree path by path.

func (s *Server) scanRunner(w http.ResponseWriter) bool {
	if s.libscan == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "library scanner not configured",
		})
		return false
	}
	return true
}

// handleScanStatus handles GET /api/library/scan/status.
func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.libscan.Status()) // nil-receiver safe
}

// handleScanRun handles POST /api/library/scan/run.
func (s *Server) handleScanRun(w http.ResponseWriter, r *http.Request) {
	if !s.scanRunner(w) {
		return
	}
	if !requireContentTypeJSON(w, r) {
		return
	}
	var req struct {
		PlatformSlug string `json:"platform_slug"`
		// DryRun walks and measures everything and writes nothing — the way
		// to see what a first scan would do to a large library before
		// letting it do it.
		DryRun bool `json:"dry_run"`
		// Force re-measures entries whose rows already carry hashes or a
		// verdict (it still never downgrades a banked verdict to unknown).
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PlatformSlug) == "" {
		writeError(w, http.StatusBadRequest, "platform_slug required (a slug or \"all\")")
		return
	}
	scope := strings.TrimSpace(req.PlatformSlug)
	if !s.libscan.Trigger(scope, libscan.Opts{DryRun: req.DryRun, Force: req.Force}) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "already running"})
		return
	}
	message := "Library scan started"
	if req.DryRun {
		message = "Library scan dry run started"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "message": message, "scope": scope, "dry_run": req.DryRun,
	})
}

// handleScanResults handles GET /api/library/scan/results.
func (s *Server) handleScanResults(w http.ResponseWriter, r *http.Request) {
	if !s.scanRunner(w) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	rows, total := s.libscan.ResultsPage(page, pageSize)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "items": rows, "total": total,
	})
}

// handleScanStop handles POST /api/library/scan/stop.
func (s *Server) handleScanStop(w http.ResponseWriter, r *http.Request) {
	if !s.scanRunner(w) {
		return
	}
	s.libscan.Stop()
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Stopping"})
}
