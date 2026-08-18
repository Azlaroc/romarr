// Package sources holds the runtime registry of source-driver endpoints
// (base URLs and per-platform path/system mappings).
//
// Resolved at startup from, in order:
//
//  1. GAMARR_SOURCES_PATH — local JSON file (takes precedence)
//  2. GAMARR_SOURCES_URL  — HTTP(S) URL to a JSON file
//  3. embedded defaults    — fallback if neither is set or both fail to load
//
// Legacy per-source env vars (VIMM_URL, ARCHIVEORG_URL) still take precedence
// over the registry value when set, so existing deployments need no
// migration.
package sources

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed defaults.json
var defaultsJSON []byte

// Registry is the in-memory representation of the source-driver registry.
type Registry struct {
	Version    int            `json:"version"`
	Vimm       VimmSpec       `json:"vimm"`
	ArchiveOrg ArchiveOrgSpec `json:"archiveorg"`
}

// ArchiveOrgSpec carries the configurable bits of the native Internet Archive
// driver: the API base and a platform-slug -> collection-item map.
//
// Items is a set of PREFERRED collections, searched before the open corpus —
// curated No-Intro/Redump sets are better-named and one cached read answers a
// whole platform. It is not a gate: a platform absent from the map is searched
// openly. The embedded defaults ship empty, which now means "search openly
// everywhere" rather than "do nothing". See internal/sources/archiveorg and
// docs/source-plane.md for proven items.
type ArchiveOrgSpec struct {
	BaseURL string `json:"base_url"`
	// Enabled gates the driver. A pointer so absence means enabled —
	// registry files written before the field existed keep working.
	Enabled *bool `json:"enabled,omitempty"`
	// OpenSearch gates searching archive.org itself for platforms with no
	// pinned collection. Absence means enabled. Turn it off to go back to
	// pinned-collections-only — the one lever that matters if archive.org
	// ever rate-limits us harder than the driver's own pacing handles.
	OpenSearch *bool             `json:"open_search,omitempty"`
	Items      map[string]string `json:"items"`
}

// OpenSearchEnabled reports the open-search flag, defaulting to true.
func (s ArchiveOrgSpec) OpenSearchEnabled() bool { return s.OpenSearch == nil || *s.OpenSearch }

// IsEnabled reports the spec's enable flag, defaulting to true when unset.
func (s ArchiveOrgSpec) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// VimmSpec carries the configurable bits of the Vimm direct-download driver.
type VimmSpec struct {
	BaseURL string `json:"base_url"`
	// Enabled gates the driver. A pointer so absence means enabled —
	// registry files written before the field existed keep working.
	Enabled         *bool             `json:"enabled,omitempty"`
	PlatformSystems map[string]string `json:"platform_systems"`
}

// IsEnabled reports the spec's enable flag, defaulting to true when unset.
func (s VimmSpec) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// VimmActive reports whether the Vimm driver can produce results at all:
// enabled AND at least one platform mapped. An empty platform_systems map
// used to fall through to an unfiltered site search that returned
// cross-platform hits tagged with the requested slug — the reason disabling
// Vimm needed a dead base_url workaround.
func (r *Registry) VimmActive() bool {
	return r != nil && r.Vimm.IsEnabled() && len(r.Vimm.PlatformSystems) > 0
}

// ArchiveOrgActive reports whether the Internet Archive driver can produce
// results. Unlike Vimm it does NOT require a mapped platform: the driver
// searches archive.org itself when a platform has no pinned collection, so
// "enabled" is the whole condition. Requiring a mapping was what made a
// registry edit a prerequisite for finding anything on a new platform.
func (r *Registry) ArchiveOrgActive() bool {
	return r != nil && r.ArchiveOrg.IsEnabled()
}

// Default returns the embedded fallback registry.
// Used when no external source is configured or when fetching fails.
func Default() (*Registry, error) {
	var r Registry
	if err := json.Unmarshal(defaultsJSON, &r); err != nil {
		return nil, fmt.Errorf("decode embedded sources registry: %w", err)
	}
	return &r, nil
}
