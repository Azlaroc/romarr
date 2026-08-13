package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/db"
)

// validRegionTokens is the release-name parser's region vocabulary; profile
// region_priority entries must come from it (stored lowered).
var validRegionTokens = map[string]bool{
	"usa": true, "world": true, "europe": true, "japan": true,
	"uk": true, "australia": true, "canada": true, "korea": true,
	"china": true, "taiwan": true, "france": true, "germany": true,
	"spain": true, "italy": true, "netherlands": true, "sweden": true,
	"brazil": true, "asia": true,
}

// validFormatTokens mirrors the parser's FormatHint vocabulary.
var validFormatTokens = map[string]bool{
	"chd": true, "cue": true, "iso": true, "gdi": true,
	"zip": true, "7z": true, "raw": true,
}

// validateQualityProfile normalizes list tokens to lowercase and rejects
// unknown vocabulary. Returns a human-readable problem or "".
func validateQualityProfile(p *db.QualityProfile) string {
	if p.Name == "" {
		return "Name is required"
	}
	for i, tok := range p.RegionPriority {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if !validRegionTokens[tok] {
			return "Unknown region token: " + p.RegionPriority[i]
		}
		p.RegionPriority[i] = tok
	}
	for i, tok := range p.FormatPreference {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if !validFormatTokens[tok] {
			return "Unknown format token: " + p.FormatPreference[i]
		}
		p.FormatPreference[i] = tok
	}
	p.PlatformSlug = strings.ToLower(strings.TrimSpace(p.PlatformSlug))
	return ""
}

// platformSlugTaken reports whether another profile already claims the slug
// (one override per platform; the idx_qp_platform unique index backstops
// races, this gives the friendly 409).
func (s *Server) platformSlugTaken(slug string, exceptID int64) bool {
	if slug == "" {
		return false
	}
	for _, p := range s.mgr.Jobs().GetQualityProfiles() {
		if p.PlatformSlug == slug && p.ID != exceptID {
			return true
		}
	}
	return false
}

// handleGetQualityProfiles handles GET /api/quality-profiles.
func (s *Server) handleGetQualityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles := s.mgr.Jobs().GetQualityProfiles()
	if profiles == nil {
		profiles = []db.QualityProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"profiles": profiles,
	})
}

// handleCreateQualityProfile handles POST /api/quality-profiles.
func (s *Server) handleCreateQualityProfile(w http.ResponseWriter, r *http.Request) {
	var p db.QualityProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if msg := validateQualityProfile(&p); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if s.platformSlugTaken(p.PlatformSlug, 0) {
		writeError(w, http.StatusConflict, "A profile for platform '"+p.PlatformSlug+"' already exists")
		return
	}
	if p.SourceRanking == nil {
		p.SourceRanking = []string{}
	}
	if p.RegionPriority == nil {
		p.RegionPriority = []string{}
	}
	if p.FormatPreference == nil {
		p.FormatPreference = []string{}
	}
	id, err := s.mgr.Jobs().AddQualityProfile(&p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"success": true, "id": id})
}

// handleUpdateQualityProfile handles PUT /api/quality-profiles/{id}.
// Full-body replace: omitted fields are zeroed, matching the pre-F4 contract.
func (s *Server) handleUpdateQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile ID")
		return
	}
	var p db.QualityProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	p.ID = id
	if msg := validateQualityProfile(&p); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if s.platformSlugTaken(p.PlatformSlug, id) {
		writeError(w, http.StatusConflict, "A profile for platform '"+p.PlatformSlug+"' already exists")
		return
	}
	if p.SourceRanking == nil {
		p.SourceRanking = []string{}
	}
	if p.RegionPriority == nil {
		p.RegionPriority = []string{}
	}
	if p.FormatPreference == nil {
		p.FormatPreference = []string{}
	}
	if err := s.mgr.Jobs().UpdateQualityProfile(&p); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update profile")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// handleDeleteQualityProfile handles DELETE /api/quality-profiles/{id}.
func (s *Server) handleDeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid profile ID")
		return
	}
	if err := s.mgr.Jobs().DeleteQualityProfile(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete profile")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}
