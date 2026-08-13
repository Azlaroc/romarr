package selection

import (
	"sort"

	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/search"
)

// Pipeline is the one shared post-FanOut preparation stage. Every consumer of
// merged search results (/api/search, the scheduler's searchFn, request
// search) runs the same sequence: split, torrent-gate, blocklist, release
// profiles, parse, score, tier-sort. The torznab endpoint deliberately stays
// on the raw legacy path — downstream *arrs do their own selection.
//
// The DB seams are injected so the package stays pure and testable.
type Pipeline struct {
	// Blocklisted reports whether a download URL / infohash is blocklisted
	// (db.JobStore.IsBlocklisted). Nil disables the check.
	Blocklisted func(downloadURL, infoHash string) bool
	// ReleaseProfiles returns the preferred-word score adjustment and whether
	// a must-not-contain term excludes the title
	// (db.JobStore.ApplyReleaseProfiles). Nil disables.
	ReleaseProfiles func(title string) (adjust int, exclude bool)
}

// Prepare filters, annotates, scores and tier-sorts merged fan-out results
// against a quality profile. It preserves the legacy torrent gates
// (FilterGameResults) and blocklist/exclude drops, then:
//
//   - parses every title into SearchResult.Attrs,
//   - computes the base quality score (search.ScoreResults) and folds the
//     release-profile word adjustment into ScoreBreakdown.ProfileAdjust with
//     the total re-clamped — Score == ScoreBreakdown.Total always (the
//     legacy pipeline overwrote the adjustment and let the old source boost
//     push Score past the breakdown),
//   - sorts by the profile's tiered rank (see rankKey), best first.
//
// platformFilter may be "" or "all" for cross-platform searches; blanking is
// handled here so every caller gets it.
func (pl *Pipeline) Prepare(results []*models.SearchResult, query, platformFilter string, prof *db.QualityProfile) []*models.SearchResult {
	if prof == nil {
		prof = db.DefaultQualityProfile()
	}
	if platformFilter == "all" {
		platformFilter = ""
	}

	// Legacy torrent gates; DDL results pass through as before.
	var torrents, ddl []*models.SearchResult
	for _, r := range results {
		if r.SourceType == "indexer" {
			torrents = append(torrents, r)
		} else {
			ddl = append(ddl, r)
		}
	}
	kept := append(search.FilterGameResults(torrents, query), ddl...)

	out := make([]*models.SearchResult, 0, len(kept))
	for _, r := range kept {
		if pl.Blocklisted != nil && pl.Blocklisted(r.DownloadURL, r.InfoHash) {
			continue
		}
		adjust := 0
		if pl.ReleaseProfiles != nil {
			var exclude bool
			adjust, exclude = pl.ReleaseProfiles(r.Title)
			if exclude {
				continue
			}
		}
		attrs := Parse(r.Title)
		r.Attrs = &attrs

		search.ScoreResults([]*models.SearchResult{r}, query, platformFilter)
		if adjust != 0 && r.ScoreBreakdown != nil {
			r.ScoreBreakdown.ProfileAdjust = adjust
			total := r.ScoreBreakdown.Total + adjust
			if total > 100 {
				total = 100
			}
			if total < 0 {
				total = 0
			}
			r.ScoreBreakdown.Total = total
			r.Score = total
		}
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return buildRankKey(out[i], prof).less(buildRankKey(out[j], prof))
	})
	return out
}
