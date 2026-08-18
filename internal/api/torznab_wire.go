package api

import (
	"context"

	"gamarr/internal/models"
	"gamarr/internal/search"
)

// searchForTorznab is the SearchFunc passed to the Torznab handler. It runs
// the same 3-source fan-out as /api/search but skips the user-facing
// post-processing (blocklist filter, library-dedup, quality-profile rank,
// release-profile scoring) that downstream *arr consumers do themselves —
// they only want raw indexer-style results.
func (s *Server) searchForTorznab(ctx context.Context, query, platformSlug string) []*models.SearchResult {
	slug := platformSlug
	if slug == "all" {
		slug = ""
	}
	// Downstream *arr consumers do their own post-processing, but region
	// policy is ours: a platform whose profile ranks JP must serve JP here
	// too, or the driver silently narrows what the indexer can offer.
	prof := s.mgr.Jobs().ResolveQualityProfile(slug)
	allResults := search.FanOut(ctx, search.BuildSources(s.cfg), query, slug,
		search.Opts{Regions: prof.RegionPriority})

	// Split + filter torrent results; pass DDL through (FilterGameResults
	// targets torrent-only release artefacts like NFO/SFV/sample dirs).
	var torrentResults, ddlResults []*models.SearchResult
	for _, r := range allResults {
		if r.SourceType == "indexer" {
			torrentResults = append(torrentResults, r)
		} else {
			ddlResults = append(ddlResults, r)
		}
	}
	merged := append(search.FilterGameResults(torrentResults, query), ddlResults...)
	if merged == nil {
		return []*models.SearchResult{}
	}
	return search.ScoreResults(merged, query, platformSlug)
}
