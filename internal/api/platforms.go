package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/datsvc"
	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// The platform registry: one canonical vocabulary. GET is open because every
// picker in the app needs it; edits are configuration, so they sit behind
// requireAdmin alongside the DAT assignments and size definitions.
//
// There is no POST and no DELETE. The set of platforms that exist is not an
// operator decision — a platform arrives with the shipped vocabulary, the way
// DAT authorities do. What an operator tunes is how RomArr treats one.

// mediaClasses are the shapes a platform can have. The class doubles as the
// profile-template class, which is why it is a closed set rather than free
// text: an unknown class would silently mean "no template".
var mediaClasses = map[string]bool{
	"carts": true, "discs": true, "arcade": true, "computer": true, "pc": true, "": true,
}

// platformView is a registry row plus the context a screen needs to explain
// it: which catalog lane it draws on, and what that lane covers. The counts
// come from the DAT plane rather than being recomputed here, so the platform
// page and the coverage table can never disagree.
type platformView struct {
	platform.Row
	DatAuthority string `json:"dat_authority"`
	DatCode      string `json:"dat_code"`
}

func (s *Server) platformViews() []platformView {
	lanes := map[string]db.DatPlatformRow{}
	for _, l := range s.mgr.Jobs().ListDatPlatforms() {
		lanes[l.PlatformSlug] = l
	}
	rows := platform.Rows()
	out := make([]platformView, 0, len(rows))
	for _, p := range rows {
		v := platformView{Row: p}
		if l, ok := lanes[p.Slug]; ok {
			v.DatAuthority, v.DatCode = l.Authority, l.DatCode
		}
		out = append(out, v)
	}
	return out
}

// handlePlatform handles GET /api/platforms/{slug}.
func (s *Server) handlePlatform(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	for _, v := range s.platformViews() {
		if v.Slug == slug {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "Unknown platform")
}

// handleUpdatePlatform handles PUT /api/platforms/{slug} (admin): a sparse
// {display_name?, media_class?, converts_to_chd?, acquisition_enabled?}.
// Absent fields are left alone, so a screen can save one control without
// having to round-trip the rest of the row.
func (s *Server) handleUpdatePlatform(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if err := datsvc.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		DisplayName        *string `json:"display_name"`
		MediaClass         *string `json:"media_class"`
		ConvertsToCHD      *bool   `json:"converts_to_chd"`
		AcquisitionEnabled *bool   `json:"acquisition_enabled"`
		DefaultProfileID   *int64  `json:"default_profile_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.DisplayName != nil && strings.TrimSpace(*req.DisplayName) == "" {
		writeError(w, http.StatusBadRequest, "display_name must not be empty")
		return
	}
	if req.MediaClass != nil && !mediaClasses[*req.MediaClass] {
		writeError(w, http.StatusBadRequest, "media_class must be one of carts, discs, arcade, computer, pc")
		return
	}
	// 0 is a real value: it clears the platform's default so titles added for
	// it fall through to the global one. Anything else has to name a profile
	// that exists and can actually be used — a template is cloned for new
	// platforms, never applied directly.
	if req.DefaultProfileID != nil && *req.DefaultProfileID != 0 {
		p, err := s.mgr.Jobs().GetQualityProfile(*req.DefaultProfileID)
		if err != nil || p == nil {
			writeError(w, http.StatusBadRequest, "Unknown quality profile")
			return
		}
		if p.IsTemplate {
			writeError(w, http.StatusBadRequest, "That profile is a template — it is cloned for new platforms, not assigned directly")
			return
		}
	}
	if err := s.mgr.Jobs().PatchPlatform(slug, db.PlatformPatch{
		DisplayName:        req.DisplayName,
		MediaClass:         req.MediaClass,
		ConvertsToCHD:      req.ConvertsToCHD,
		AcquisitionEnabled: req.AcquisitionEnabled,
		DefaultProfileID:   req.DefaultProfileID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "Unknown platform")
		return
	}
	s.handlePlatform(w, r)
}
