package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/datsvc"
	"gamarr/internal/db"
)

// DAT catalog endpoints. Configuration and writes are admin-only, matching
// /api/source-registry and /api/settings; the read-only status and coverage
// views are not, matching GET /api/library/normalize/status.

// datService returns the catalog service, or writes 503 when the server was
// built without one (the normalize tests build a router with nils).
func (s *Server) datService(w http.ResponseWriter) (*datsvc.Service, bool) {
	if s.dat == nil {
		writeError(w, http.StatusServiceUnavailable, "DAT catalog service unavailable")
		return nil, false
	}
	return s.dat, true
}

// handleDatAuthorities returns the authorities and their platform
// assignments together: they are edited as one screen and read as one.
func (s *Server) handleDatAuthorities(w http.ResponseWriter, r *http.Request) {
	jobs := s.mgr.Jobs()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authorities": jobs.ListDatAuthorities(),
		"platforms":   jobs.ListDatPlatforms(),
	})
}

// handleUpdateDatAuthority patches one authority. fetch_driver and fetch_base
// are data on purpose: repointing at a mirror, a CDN or a self-hosted clone
// is an edit rather than a deploy.
func (s *Server) handleUpdateDatAuthority(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	jobs := s.mgr.Jobs()
	current, err := jobs.GetDatAuthority(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var req struct {
		Enabled       *bool   `json:"enabled"`
		FetchDriver   *string `json:"fetch_driver"`
		FetchBase     *string `json:"fetch_base"`
		PinnedVersion *string `json:"pinned_version"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}

	driver := current.FetchDriver
	if req.FetchDriver != nil {
		driver = strings.TrimSpace(*req.FetchDriver)
		if err := datsvc.ValidateDriver(driver); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	base := current.FetchBase
	if req.FetchBase != nil {
		base = strings.TrimSpace(*req.FetchBase)
	}
	if err := datsvc.ValidateFetchBase(driver, base); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	patch := db.DatAuthorityPatch{Enabled: req.Enabled, PinnedVersion: req.PinnedVersion}
	if req.FetchDriver != nil {
		patch.FetchDriver = &driver
	}
	if req.FetchBase != nil {
		patch.FetchBase = &base
	}
	if err := jobs.UpdateDatAuthority(name, patch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	updated, _ := jobs.GetDatAuthority(name)
	resp := map[string]interface{}{"success": true, "authority": updated}
	// A driver change can invalidate every assignment underneath it — a
	// Redump code is not a libretro DAT name. Report rather than block: the
	// operator may be mid-migration, and a silent break would surface much
	// later as a failed refresh.
	if warnings := invalidAssignments(jobs, updated); len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, http.StatusOK, resp)
}

func invalidAssignments(jobs *db.JobStore, auth db.DatAuthorityRow) []string {
	var out []string
	for _, p := range jobs.ListDatPlatforms() {
		if p.Authority != auth.Name {
			continue
		}
		if err := datsvc.ValidateDriverCode(auth.FetchDriver, p.DatCode); err != nil {
			out = append(out, p.PlatformSlug+": "+err.Error())
		}
	}
	return out
}

// handleUpdateDatPlatform assigns a platform to an authority. The pair is
// validated here because the store deliberately does not: a code belonging
// to another driver would otherwise fail only at fetch time.
func (s *Server) handleUpdateDatPlatform(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if err := datsvc.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Authority string `json:"authority"`
		DatCode   string `json:"dat_code"`
		Enabled   *bool  `json:"enabled"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	jobs := s.mgr.Jobs()
	auth, err := jobs.GetDatAuthority(strings.TrimSpace(req.Authority))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	code := strings.TrimSpace(req.DatCode)
	if err := datsvc.ValidateDriverCode(auth.FetchDriver, code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := db.DatPlatformRow{PlatformSlug: slug, Authority: auth.Name, DatCode: code, Enabled: enabled}
	if err := jobs.SetDatPlatform(row); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "platform": row})
}

// handleDatRefresh starts a refresh of every platform assigned to an
// authority. Sequential with a politeness gap: eleven files in a burst is
// what earns an egress ban.
func (s *Server) handleDatRefresh(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.datService(w)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if _, err := s.mgr.Jobs().GetDatAuthority(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	started, err := svc.RefreshAuthority(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !started {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "A DAT refresh is already running",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"message":   "DAT refresh started",
		"authority": name,
	})
}

// handleDatUpload imports a hand-supplied catalog.
//
// The transport is multipart/form-data and cannot be anything else:
// requestSizeLimitMiddleware caps every non-multipart body at 1MB and
// pre-rejects on Content-Length, while a daily pack is tens of megabytes.
// That exemption also makes this handler the only size gate on the path.
func (s *Server) handleDatUpload(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.datService(w)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		writeError(w, http.StatusUnsupportedMediaType,
			"DAT uploads must be multipart/form-data with a file part; raw bodies are capped at 1MB")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Malformed multipart body")
		return
	}
	var body []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Malformed multipart body")
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		limit := datsvc.MaxUploadBytes()
		body, err = io.ReadAll(io.LimitReader(part, limit+1))
		part.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "Could not read the uploaded file")
			return
		}
		if int64(len(body)) > limit {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("DAT upload exceeds %d bytes", limit))
			return
		}
		break
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "No file part in the upload")
		return
	}

	res, err := svc.ImportUpload(name, r.URL.Query().Get("platform"), body)
	if errors.Is(err, datsvc.ErrBusy) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"skipped": res.Skipped,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"authority": res.Authority,
		"imported":  res.Imported,
		"skipped":   res.Skipped,
	})
}

// handleDatStatus reports the current run for the UI poller.
func (s *Server) handleDatStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.dat.Status())
}

// handleDatCoverage reports owned-versus-known per platform.
//
// The two counts are independent: coverage v1 does not match owned files
// against catalog entries. The rendered summary ships with the row so no
// client can invent a completion percentage out of them.
func (s *Server) handleDatCoverage(w http.ResponseWriter, r *http.Request) {
	rows := s.mgr.Jobs().DatCoverage()
	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]interface{}{
			"platform_slug":    row.PlatformSlug,
			"authority":        row.Authority,
			"owned":            row.Owned,
			"known":            row.Known,
			"summary":          fmt.Sprintf("%d owned · %d known", row.Owned, row.Known),
			"snapshot_version": row.SnapshotVersion,
			"last_refresh":     row.LastRefresh,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"coverage": out,
		"note":     "Owned and known are independent counts. Coverage does not match owned files to catalog entries.",
	})
}
