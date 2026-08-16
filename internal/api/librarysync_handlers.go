package api

import (
	"net/http"

	"gamarr/internal/romm"
)

// syncer returns the live RomM syncer via the supervisor — the instance is
// rebuilt on runtime settings changes, so handlers read it per request
// rather than holding a boot-time pointer.
func (s *Server) syncer() *romm.Syncer {
	if s.sup == nil {
		return nil
	}
	return s.sup.RomMSync()
}

// handleLibrarySyncStatus handles GET /api/library/sync/status.
func (s *Server) handleLibrarySyncStatus(w http.ResponseWriter, r *http.Request) {
	sy := s.syncer()
	if sy == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, sy.Status())
}

// handleLibrarySync handles POST /api/library/sync — triggers a RomM library
// sync. ?full=true forces a full reconcile.
func (s *Server) handleLibrarySync(w http.ResponseWriter, r *http.Request) {
	sy := s.syncer()
	if sy == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "RomM sync not configured",
		})
		return
	}
	full := r.URL.Query().Get("full") == "true"
	if !sy.TriggerSync(full) {
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
