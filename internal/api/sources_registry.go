package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/db"
	"gamarr/internal/sources"
)

// The built-in source registry (vimm, archiveorg) is DB-backed and editable
// at runtime. A write hydrates a FRESH *sources.Registry and installs it via
// cfg.SetSourcesRegistry — the one intentional registry-pointer swap, which
// also (deliberately) rotates the IA driver memo.

func sourceRegistryRow(name string, reg *sources.Registry) map[string]interface{} {
	switch name {
	case "vimm":
		return map[string]interface{}{
			"name": "vimm", "label": "Vimm's Lair", "builtin": true,
			"enabled":  reg.Vimm.IsEnabled(),
			"base_url": reg.Vimm.BaseURL,
			"mapping":  reg.Vimm.PlatformSystems,
			"active":   reg.VimmActive(),
		}
	case "archiveorg":
		return map[string]interface{}{
			"name": "archiveorg", "label": "Internet Archive", "builtin": true,
			"enabled":  reg.ArchiveOrg.IsEnabled(),
			"base_url": reg.ArchiveOrg.BaseURL,
			"mapping":  reg.ArchiveOrg.Items,
			"active":   reg.ArchiveOrgActive(),
		}
	}
	return nil
}

// handleSourceRegistry handles GET /api/source-registry (admin).
func (s *Server) handleSourceRegistry(w http.ResponseWriter, r *http.Request) {
	reg := s.cfg.SourcesRegistry()
	writeJSON(w, 200, map[string]interface{}{"sources": []map[string]interface{}{
		sourceRegistryRow("vimm", reg),
		sourceRegistryRow("archiveorg", reg),
	}})
}

// handleUpdateSourceRegistry handles PUT /api/source-registry/{name}
// (admin): sparse {enabled?, base_url?, mapping?}. The registry is a closed
// set — unknown names 404, and there is no POST or DELETE.
func (s *Server) handleUpdateSourceRegistry(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name != "vimm" && name != "archiveorg" {
		writeError(w, 404, "Unknown source")
		return
	}
	var req struct {
		Enabled *bool              `json:"enabled"`
		BaseURL *string            `json:"base_url"`
		Mapping *map[string]string `json:"mapping"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.BaseURL != nil && *req.BaseURL != "" {
		u, err := url.Parse(*req.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeError(w, 400, "base_url must be an http(s) URL")
			return
		}
	}
	patch := db.SourcePatch{Enabled: req.Enabled, BaseURL: req.BaseURL}
	if req.Mapping != nil {
		m := make(map[string]string, len(*req.Mapping))
		for k, v := range *req.Mapping {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "" || v == "" {
				writeError(w, 400, "mapping keys and values must be non-empty")
				return
			}
			m[k] = v
		}
		patch.Mapping = m
	}
	if err := s.mgr.Jobs().UpdateSourceSpec(name, patch); err != nil {
		writeError(w, 404, "Unknown source")
		return
	}

	// The one intentional pointer swap: hydrate fresh, install, respond.
	s.cfg.SetSourcesRegistry(s.mgr.Jobs().HydrateSourceRegistry())
	writeJSON(w, 200, sourceRegistryRow(name, s.cfg.SourcesRegistry()))
}
