package metadata

import "context"

// Game is one title as a metadata authority describes it: the identity and
// the art, not the release. A Game is something you can WANT; a release is
// something you can fetch. Keeping them apart is why the catalog plane
// (dumps, hashes) and this plane (what a game IS) do not have to agree about
// anything except which platform a title belongs to.
type Game struct {
	// ProviderID is the authority's own id, kept so a later lookup can be
	// exact rather than another text search.
	ProviderID int64  `json:"provider_id"`
	Name       string `json:"name"`
	Slug       string `json:"slug,omitempty"`
	Summary    string `json:"summary,omitempty"`
	// CoverURL is an https URL to box art. Empty when the authority has none
	// — a missing cover is normal and must render as a placeholder, never as
	// an error.
	CoverURL string `json:"cover_url,omitempty"`
	// ReleaseYear is 0 when unknown.
	ReleaseYear int `json:"release_year,omitempty"`
	// Platforms are RomArr platform slugs, resolved through the platform
	// registry — the authority's own platform names never leave this package.
	Platforms []string `json:"platforms,omitempty"`
	// UnmappedPlatforms are the authority's platform names that no registry
	// row claims. Surfaced rather than dropped: they are how you find out the
	// registry is missing a platform you care about.
	UnmappedPlatforms []string `json:"unmapped_platforms,omitempty"`
}

// Provider is a public metadata authority: it answers "what games are there?"
// and "what does this one look like?".
//
// It is deliberately NOT the library. RomArr asks a public authority about
// games it does not own, the way Radarr asks TMDB rather than asking Plex.
type Provider interface {
	// Name is the stable provider id, e.g. "igdb".
	Name() string
	// Configured reports whether credentials are present. An unconfigured
	// provider is an honest empty state, not an error.
	Configured() bool
	// Search resolves a free-text query to candidate games, best match first.
	Search(ctx context.Context, query string, limit int) ([]Game, error)
}
