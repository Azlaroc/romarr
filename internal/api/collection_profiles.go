package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/db"
)

// Collection-profile CRUD: the named "what does this platform collect" slice
// of the DAT (region order, language preference, category gates). The quality
// profiles keep the release-side concerns; this vocabulary has one owner.

// validateCollectionProfile normalizes list tokens and rejects unknown
// vocabulary. Returns a human-readable problem or "".
func validateCollectionProfile(p *db.CollectionProfile) string {
	if strings.TrimSpace(p.Name) == "" {
		return "Name is required"
	}
	for i, tok := range p.RegionPriority {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if !validRegionTokens[tok] {
			return "Unknown region token: " + p.RegionPriority[i]
		}
		p.RegionPriority[i] = tok
	}
	// Exclusion categories are clone-list vocabulary ("Applications",
	// "Educational") — an open set upstream owns, so they are trimmed, not
	// enumerated.
	for i, cat := range p.ExcludeCategories {
		cat = strings.TrimSpace(cat)
		if cat == "" {
			return "Empty exclusion category"
		}
		p.ExcludeCategories[i] = cat
	}
	return ""
}

func (s *Server) handleGetCollectionProfiles(w http.ResponseWriter, r *http.Request) {
	profiles := s.mgr.Jobs().GetCollectionProfiles()
	if profiles == nil {
		profiles = []*db.CollectionProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"profiles": profiles})
}

func (s *Server) handleGetCollectionProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile id")
		return
	}
	p, err := s.mgr.Jobs().GetCollectionProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Unknown collection profile")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCreateCollectionProfile(w http.ResponseWriter, r *http.Request) {
	var p db.CollectionProfile
	if !decodeJSONBody(w, r, &p) {
		return
	}
	if problem := validateCollectionProfile(&p); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	id, err := s.mgr.Jobs().AddCollectionProfile(&p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.ID = id
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateCollectionProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile id")
		return
	}
	var p db.CollectionProfile
	if !decodeJSONBody(w, r, &p) {
		return
	}
	p.ID = id
	if problem := validateCollectionProfile(&p); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	if err := s.mgr.Jobs().UpdateCollectionProfile(&p); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, &p)
}

func (s *Server) handleDeleteCollectionProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile id")
		return
	}
	if err := s.mgr.Jobs().DeleteCollectionProfile(id); err != nil {
		// In-use and not-found both surface as the message; the referenced
		// case is the one an operator hits from the UI.
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
