package api

import (
	"net/http"
)

// handleLibrarySyncStatus handles GET /api/library/sync/status.
func (s *Server) handleLibrarySyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.rommSync == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, s.rommSync.Status())
}

// handleLibrarySync handles POST /api/library/sync — triggers a RomM library
// sync. ?full=true forces a full reconcile.
func (s *Server) handleLibrarySync(w http.ResponseWriter, r *http.Request) {
	if s.rommSync == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "RomM sync not configured",
		})
		return
	}
	full := r.URL.Query().Get("full") == "true"
	if !s.rommSync.TriggerSync(full) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "Sync already running",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "RomM library sync started",
		"full":    full,
	})
}
