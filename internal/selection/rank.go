package selection

import (
	"fmt"
	"strings"

	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/search"
)

// Rejection records why a candidate was filtered out of selection —
// surfaced in shadow logs and Decision.Rejected so a pick is explainable.
type Rejection struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// filterCandidate applies the profile's hard filters to one result. It
// returns "" to keep the candidate or the rejection reason. Ranking never
// sees a rejected candidate: a filter is a statement that the release is
// unusable under this profile, not merely worse.
func filterCandidate(r *models.SearchResult, prof *db.QualityProfile) string {
	a := r.Attrs
	if a == nil {
		return "" // unparsed: nothing to filter on
	}
	if a.BadDump {
		return "bad dump [b]"
	}
	if a.IsBIOS && !prof.AllowBIOS {
		return "BIOS release (profile disallows)"
	}
	if a.IsProto && !prof.AllowProto {
		return "proto/beta/sample release (profile disallows)"
	}
	if a.IsDemo && !prof.AllowDemo {
		return "demo release (profile disallows)"
	}
	// Region: a region-tagged release must intersect the profile's priority
	// list. An empty priority list means no region filtering at all (the
	// seeded PC profile); region-less releases always pass and rank last in
	// the region tier instead.
	if len(prof.RegionPriority) > 0 && len(a.Regions) > 0 && regionRank(a.Regions, prof.RegionPriority) == len(prof.RegionPriority) {
		return "region not in profile priority (" + strings.Join(a.Regions, ",") + ")"
	}
	// Size sanity: reject implausibly small (placeholder) and implausibly
	// large payloads. Profile bounds win when set; 0 falls back to the
	// built-in per-platform band. Size 0 (unreported) passes the filter and
	// loses the hash/verifiability tier instead.
	if r.Size > 0 {
		minSize, maxSize := search.PlatformSizeRange(r.PlatformSlug)
		if prof.PreferredSizeMin > 0 {
			minSize = prof.PreferredSizeMin
		}
		if prof.PreferredSizeMax > 0 {
			maxSize = prof.PreferredSizeMax
		}
		if r.Size < minSize/2 {
			return fmt.Sprintf("implausibly small (%d bytes)", r.Size)
		}
		if r.Size > 2*maxSize {
			return fmt.Sprintf("implausibly large (%d bytes)", r.Size)
		}
	}
	return ""
}

// rankKey is the lexicographic comparison key: lower is better at every
// position, compared left to right. The tier order is the selection policy:
// verifiability outranks everything (a hash-carrying release feeds the
// destructive-convert BEFORE gate; a hashless one cannot), then region,
// format, 1G1R quality, source trust, and only then the fuzzy quality score.
type rankKey struct {
	noHash      int // 0 = has MD5/SHA1
	regionRank  int // index into profile RegionPriority; len = region-less/unmatched
	formatRank  int // index into profile FormatPreference; len = unknown format
	notVerified int // 0 = [!] verified dump (only when Prefer1G1R)
	negRevision int // -Revision: later revision first (only when Prefer1G1R)
	sourceRank  int // index into profile SourceRanking; len = unranked
	negScore    int // -Score: higher quality score first
}

func buildRankKey(r *models.SearchResult, prof *db.QualityProfile) rankKey {
	k := rankKey{negScore: -r.Score}
	if r.MD5 == "" && r.SHA1 == "" {
		k.noHash = 1
	}
	a := r.Attrs
	if len(prof.RegionPriority) > 0 {
		if a != nil && len(a.Regions) > 0 {
			k.regionRank = regionRank(a.Regions, prof.RegionPriority)
		} else {
			k.regionRank = len(prof.RegionPriority) // region-less: after last priority
		}
	}
	if len(prof.FormatPreference) > 0 {
		k.formatRank = len(prof.FormatPreference)
		if a != nil && a.FormatHint != "" {
			for i, f := range prof.FormatPreference {
				if f == a.FormatHint {
					k.formatRank = i
					break
				}
			}
		}
	}
	if prof.Prefer1G1R && a != nil {
		if !a.VerifiedDump {
			k.notVerified = 1
		}
		k.negRevision = -a.Revision
	}
	k.sourceRank = sourceRank(r.Indexer, prof.SourceRanking)
	return k
}

// less compares two rank keys lexicographically.
func (k rankKey) less(o rankKey) bool {
	if k.noHash != o.noHash {
		return k.noHash < o.noHash
	}
	if k.regionRank != o.regionRank {
		return k.regionRank < o.regionRank
	}
	if k.formatRank != o.formatRank {
		return k.formatRank < o.formatRank
	}
	if k.notVerified != o.notVerified {
		return k.notVerified < o.notVerified
	}
	if k.negRevision != o.negRevision {
		return k.negRevision < o.negRevision
	}
	if k.sourceRank != o.sourceRank {
		return k.sourceRank < o.sourceRank
	}
	return k.negScore < o.negScore
}

// regionRank returns the best (lowest) index any of the release's regions
// holds in the profile's priority list, or len(priority) when none match —
// a dual-region "(USA, Europe)" dump takes the USA rank.
func regionRank(regions, priority []string) int {
	best := len(priority)
	for _, reg := range regions {
		for i, p := range priority {
			if i < best && strings.EqualFold(reg, p) {
				best = i
			}
		}
	}
	return best
}

// sourceRank matches the result's indexer display name against the profile's
// SourceRanking entries by case-folded substring ("Vimm" matches
// "Vimm's Lair"), returning the entry index or len(ranking) for unranked.
func sourceRank(indexer string, ranking []string) int {
	li := strings.ToLower(indexer)
	for i, entry := range ranking {
		e := strings.ToLower(strings.TrimSpace(entry))
		if e != "" && strings.Contains(li, e) {
			return i
		}
	}
	return len(ranking)
}
