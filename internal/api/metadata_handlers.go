package api

import (
	"net/http"
	"strconv"
	"strings"

	"gamarr/internal/metadata"
)

// Metadata provider endpoints.
//
// RomArr asks a public authority about games it does not own, the way Radarr
// asks TMDB rather than asking Plex. The library — RomM included — is never
// the source of truth for a game RomArr has not acquired.

// handleMetadataProviders reports what the metadata plane is wired to. It is
// the settings screen's honest empty state: a provider with no credentials
// says so instead of failing a search later.
func (s *Server) handleMetadataProviders(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]interface{}{}
	if s.meta != nil {
		providers = append(providers, map[string]interface{}{
			"name":            s.meta.Name(),
			"label":           "IGDB",
			"configured":      s.meta.Configured(),
			"role":            "primary",
			"credentials_env": []string{"IGDB_CLIENT_ID", "IGDB_CLIENT_SECRET"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": providers})
}

// handleMetadataSearch is the art-forward door: a title, in, and games with
// covers out. It answers about games, not releases — what comes back is
// something you can WANT, and the release search happens after you pick one.
func (s *Server) handleMetadataSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"games": []metadata.Game{}})
		return
	}
	if s.meta == nil || !s.meta.Configured() {
		writeError(w, http.StatusServiceUnavailable,
			"No metadata provider is configured. Set IGDB_CLIENT_ID and IGDB_CLIENT_SECRET.")
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	games, err := s.meta.Search(r.Context(), query, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Metadata search failed: "+err.Error())
		return
	}
	if games == nil {
		games = []metadata.Game{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider": s.meta.Name(),
		"games":    games,
	})
}
