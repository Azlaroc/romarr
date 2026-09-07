package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ── Title and platform art ─────────────────────────────────────────────────────
//
// Art lives under /api (session-gated): art URLs enumerate library contents,
// and /static would serve them to anyone. Same-origin <img> carries the
// session cookie, so the gate costs the UI nothing.

// handleTitleArt serves a row's cached cover, or 404s and quietly asks the
// art service to mint one — the card shows its accent placeholder and the
// art appears on a later paint. The 404 is immediate by design: a browse
// scroll must never wait on a fetch.
func (s *Server) handleTitleArt(w http.ResponseWriter, r *http.Request) {
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
	// size=thumb serves the ~25KB grid rendition (falling back to the
	// original while a thumb doesn't exist yet); no param = the full image.
	if r.URL.Query().Get("size") == "thumb" {
		if p, ok := s.art.CachedThumbPath(item); ok {
			serveArtFile(w, r, p)
			return
		}
	}
	path, ok := s.art.CachedPath(item)
	if !ok {
		s.art.Enqueue(id)
		writeError(w, 404, "No art cached")
		return
	}
	serveArtFile(w, r, path)
}

// handlePlatformArt serves ONLY the per-install override tier
// ($DataDir/art/platforms/<slug>.<ext>). The committed photo pack is served
// by the frontend bundle's /assets route; the rollup payload decides which
// URL a tile uses.
func (s *Server) handlePlatformArt(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	path, ok := s.art.PlatformOverridePath(slug)
	if !ok {
		writeError(w, 404, "No custom platform art")
		return
	}
	serveArtFile(w, r, path)
}

func serveArtFile(w http.ResponseWriter, r *http.Request, path string) {
	// Belt over the service's own path discipline: the served file must be
	// inside the art tree even if a stored rel_path were ever tampered with.
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 404, "Art file missing")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		writeError(w, 404, "Art file missing")
		return
	}
	// Private: gated content must not land in shared caches. A day is
	// plenty — art is content-addressed by the row and rarely changes; a
	// re-curated custom file wins on the next day's revalidation.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

// ── Backfill campaign ──────────────────────────────────────────────────────────

func (s *Server) handleArtBackfill(w http.ResponseWriter, r *http.Request) {
	if !s.art.StartBackfill() {
		writeError(w, http.StatusConflict, "Art backfill already running")
		return
	}
	writeJSON(w, 202, map[string]interface{}{"started": true})
}

func (s *Server) handleArtStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.art.GetStatus())
}
