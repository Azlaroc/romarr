package search

import (
	"strings"

	"gamarr/internal/models"
)

// ScoreResults applies scoring to all results and returns them (modifies in place).
func ScoreResults(results []*models.SearchResult, query string, platformFilter string) []*models.SearchResult {
	for _, r := range results {
		sb := scoreResult(r, query, platformFilter)
		r.Score = sb.Total
		r.ScoreBreakdown = &models.ScoreBreakdown{
			TitleMatch:    sb.TitleMatch,
			PlatformMatch: sb.PlatformMatch,
			SeederScore:   sb.SeederScore,
			SafetyScore:   sb.SafetyScore,
			Total:         sb.Total,
			Confidence:    sb.Confidence,
		}
	}
	return results
}

// The tiers sum to a 100-point scale: Title 45 + Platform 15 + Seeders 20 +
// Safety 20. Size is deliberately not a tier (blaster#349) — its 15 points
// were redistributed rather than deleted, because SCHEDULER_MIN_SCORE
// (default 70) and the 70/40 confidence bands are calibrated against a
// 100-point maximum; shrinking the scale would silently tighten every
// operator threshold.
type scoreBreakdown struct {
	TitleMatch    int
	PlatformMatch int
	SeederScore   int
	SafetyScore   int
	Total         int
	Confidence    string
}

func scoreResult(r *models.SearchResult, query, platformFilter string) scoreBreakdown {
	var sb scoreBreakdown

	sb.TitleMatch = scoreTitleMatch(r.Title, query)
	sb.PlatformMatch = scorePlatformMatch(r.PlatformSlug, platformFilter)
	sb.SeederScore = scoreSeederCount(r.Seeders, r.SourceType, r.DownloadProtocol)
	sb.SafetyScore = scoreSafety(r.SafetyScore)

	sb.Total = sb.TitleMatch + sb.PlatformMatch + sb.SeederScore + sb.SafetyScore
	if sb.Total > 100 {
		sb.Total = 100
	}
	if sb.Total < 0 {
		sb.Total = 0
	}

	switch {
	case sb.Total >= 70:
		sb.Confidence = "high"
	case sb.Total >= 40:
		sb.Confidence = "medium"
	default:
		sb.Confidence = "low"
	}

	return sb
}

// scoreTitleMatch scores title similarity (0-45).
func scoreTitleMatch(title, query string) int {
	if query == "" {
		return 22
	}
	tLower := strings.ToLower(title)
	qLower := strings.ToLower(query)

	// Exact match
	if tLower == qLower {
		return 45
	}

	// Full query is a substring
	if strings.Contains(tLower, qLower) {
		return 39
	}

	// Word overlap scoring
	qWords := extractWords(query)
	tWords := extractWords(title)
	if len(qWords) == 0 {
		return 22
	}

	overlap := 0
	for w := range qWords {
		if tWords[w] {
			overlap++
		}
	}

	ratio := float64(overlap) / float64(len(qWords))
	return int(ratio * 45)
}

// scorePlatformMatch scores platform match (0-15).
func scorePlatformMatch(resultSlug, filterSlug string) int {
	if filterSlug == "" || filterSlug == "all" {
		return 8 // neutral when no filter
	}
	if resultSlug == filterSlug {
		return 15
	}
	// PC platform has multiple slugs
	if filterSlug == "pc" && (resultSlug == "pc" || resultSlug == "") {
		return 15
	}
	return 0
}

// scoreSeederCount scores availability confidence (0-20), and is the one place
// protocol preference reaches ranking today.
//
// Order is the locked default: direct-HTTP > nzb > torrent. A direct source
// either serves the file or it does not; a torrent's availability is a function
// of who happens to be seeding it this minute, so even a well-seeded torrent
// ranks below a direct source rather than above it. Seeders still grade
// torrents against each other.
//
// This is a scoring signal, not a filter — a torrent still wins when it is the
// best or only candidate. Per-source priority as first-class data (a stored
// per-source rank, and protocol as its own tier in the rank key) belongs to the
// profiles/sources work, not to this function.
func scoreSeederCount(seeders int, sourceType, downloadProtocol string) int {
	if sourceType == "ddl" {
		return 20
	}
	if downloadProtocol == "nzb" {
		return 16
	}
	switch {
	case seeders >= 50:
		return 10
	case seeders >= 20:
		return 8
	case seeders >= 10:
		return 6
	case seeders >= 5:
		return 4
	case seeders >= 2:
		return 2
	default:
		return 0
	}
}

// scoreSafety maps the existing 0-100 SafetyScore to 0-20 range.
func scoreSafety(safetyScore int) int {
	if safetyScore <= 0 {
		return 0
	}
	// Scale 0-100 to 0-20
	scaled := safetyScore * 20 / 100
	if scaled > 20 {
		scaled = 20
	}
	return scaled
}
