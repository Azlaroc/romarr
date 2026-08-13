package search

import (
	"context"
	"log/slog"
	"sync"

	"gamarr/internal/config"
	"gamarr/internal/models"
	"gamarr/internal/sources"
)

// Source is the search-plane contract: anything RomArr can ask "what releases
// match this query?" A Prowlarr indexer, the Vimm scrape client, and the native
// archive.org driver all reduce to this one shape. Concrete drivers still live
// as free functions in this package; the adapters below wrap them so the four
// fan-out call sites collapse into a single FanOut over a registered slice.
//
// ctx is accepted for future cancellation/deadline propagation; today's
// adapters ignore it because each underlying driver manages its own request
// timeout (preserving existing behaviour).
type Source interface {
	Name() string
	Search(ctx context.Context, query, platformSlug string) []*models.SearchResult
}

// FanOut runs Search across every source concurrently and returns the merged
// results in deterministic source order (the order of srcs). A source that
// panics is isolated — logged, contributes nothing — and never aborts the
// others; a source that simply finds nothing contributes nothing. No dedup,
// filtering, or scoring happens here: callers apply their own post-processing.
func FanOut(ctx context.Context, srcs []Source, query, platformSlug string) []*models.SearchResult {
	parts := make([][]*models.SearchResult, len(srcs))
	var wg sync.WaitGroup
	wg.Add(len(srcs))
	for i, src := range srcs {
		go func(i int, src Source) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					slog.Error("search source panicked", "source", src.Name(), "panic", p)
					parts[i] = nil
				}
			}()
			parts[i] = src.Search(ctx, query, platformSlug)
		}(i, src)
	}
	wg.Wait()

	var merged []*models.SearchResult
	for _, p := range parts {
		merged = append(merged, p...)
	}
	return merged
}

// prowlarrSource adapts the Prowlarr indexer client to the Source contract.
type prowlarrSource struct{ cfg *config.Config }

func (prowlarrSource) Name() string { return "prowlarr" }
func (s prowlarrSource) Search(_ context.Context, query, platformSlug string) []*models.SearchResult {
	return SearchProwlarr(s.cfg, query, platformSlug)
}

// vimmSource adapts the Vimm's Lair scrape client to the Source contract.
type vimmSource struct{ reg *sources.Registry }

func (vimmSource) Name() string { return "vimm" }
func (s vimmSource) Search(_ context.Context, query, platformSlug string) []*models.SearchResult {
	return SearchVimm(s.reg, query, platformSlug)
}

// archiveOrgSource adapts the native Internet Archive driver to the Source
// contract. It is inert until a platform is opted into the registry.
type archiveOrgSource struct{ reg *sources.Registry }

func (archiveOrgSource) Name() string { return "archiveorg" }
func (s archiveOrgSource) Search(_ context.Context, query, platformSlug string) []*models.SearchResult {
	return SearchArchiveOrg(s.reg, query, platformSlug)
}

// BuildSources returns the ordered set of sources RomArr fans a query across:
// archive.org first (native, hash-carrying), then the Prowlarr indexer, then
// Vimm. The downstream split/score is order-independent, so this governs only
// iteration order. Myrient is intentionally absent — it shut down 2026-03-31.
func BuildSources(cfg *config.Config) []Source {
	return []Source{
		archiveOrgSource{cfg.Sources},
		prowlarrSource{cfg},
		vimmSource{cfg.Sources},
	}
}
