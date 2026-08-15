package api

import (
	"fmt"
	"net/http"
	"strings"

	"gamarr/internal/models"
)

// hashOwnedConflict enforces the by-hash duplicate gate on a download
// request. When the release's hashes prove a byte-identical copy is already
// in the library and the request doesn't set force, it writes a 409
// (code "duplicate_hash") carrying the owned item's identity and returns
// true — the caller must stop before dispatching or mutating anything.
// Title-only duplicate detection stays a warning; this blocks only proven
// byte-identical re-downloads.
func (s *Server) hashOwnedConflict(w http.ResponseWriter, req *models.DownloadRequest) bool {
	if req.Force {
		return false
	}
	owned := s.mgr.Jobs().FindLibraryByHash(req.MD5, req.SHA1)
	if owned == nil {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]interface{}{
		"success": false,
		"code":    "duplicate_hash",
		"error": fmt.Sprintf("Already in library (identical file): %s (%s). Send force to download anyway.",
			owned.Title, owned.Platform),
		"owned": map[string]interface{}{
			"id":            owned.ID,
			"title":         owned.Title,
			"platform":      owned.Platform,
			"platform_slug": owned.PlatformSlug,
		},
	})
	return true
}

// handleCheckLibrary handles GET /api/library/check?title=X&platform=Y.
// Returns whether a game exists in the library.
func (s *Server) handleCheckLibrary(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))

	if title == "" {
		writeError(w, http.StatusBadRequest, "title parameter is required")
		return
	}

	item := s.mgr.Jobs().FindLibraryByTitle(title, platform)
	if item != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"exists": true,
			"item":   item,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"exists": false,
		"item":   nil,
	})
}
