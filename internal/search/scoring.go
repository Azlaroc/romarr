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
			SizeScore:     sb.SizeScore,
			SafetyScore:   sb.SafetyScore,
			Total:         sb.Total,
			Confidence:    sb.Confidence,
		}
	}
	return results
}

type scoreBreakdown struct {
	TitleMatch    int
	PlatformMatch int
	SeederScore   int
	SizeScore     int
	SafetyScore   int
	Total         int
	Confidence    string
}

func scoreResult(r *models.SearchResult, query, platformFilter string) scoreBreakdown {
	var sb scoreBreakdown

	sb.TitleMatch = scoreTitleMatch(r.Title, query)
	sb.PlatformMatch = scorePlatformMatch(r.PlatformSlug, platformFilter)
	sb.SeederScore = scoreSeederCount(r.Seeders, r.SourceType, r.DownloadProtocol)
	sb.SizeScore = scoreSizeRange(r.Size, r.PlatformSlug)
	sb.SafetyScore = scoreSafety(r.SafetyScore)

	sb.Total = sb.TitleMatch + sb.PlatformMatch + sb.SeederScore + sb.SizeScore + sb.SafetyScore
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

// scoreTitleMatch scores title similarity (0-40).
func scoreTitleMatch(title, query string) int {
	if query == "" {
		return 20
	}
	tLower := strings.ToLower(title)
	qLower := strings.ToLower(query)

	// Exact match
	if tLower == qLower {
		return 40
	}

	// Full query is a substring
	if strings.Contains(tLower, qLower) {
		return 35
	}

	// Word overlap scoring
	qWords := extractWords(query)
	tWords := extractWords(title)
	if len(qWords) == 0 {
		return 20
	}

	overlap := 0
	for w := range qWords {
		if tWords[w] {
			overlap++
		}
	}

	ratio := float64(overlap) / float64(len(qWords))
	return int(ratio * 40)
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

// scoreSeederCount scores by seeder count (0-15). DDL gets flat 10.
func scoreSeederCount(seeders int, sourceType, downloadProtocol string) int {
	if sourceType == "ddl" || downloadProtocol == "nzb" {
		return 10
	}
	switch {
	case seeders >= 50:
		return 15
	case seeders >= 20:
		return 12
	case seeders >= 10:
		return 10
	case seeders >= 5:
		return 7
	case seeders >= 2:
		return 4
	default:
		return 0
	}
}

// scoreSizeRange scores by whether size is reasonable for the platform (0-15).
// It reads the same resolver the ranking filter uses; scoring off a second
// copy of the table is how a platform ended up scored against one band and
// filtered against another.
func scoreSizeRange(size int64, platformSlug string) int {
	if size == 0 {
		return 7 // unknown, neutral
	}

	minSize, maxSize := PlatformSizeRange(platformSlug)
	if minSize == 0 && maxSize == 0 {
		return 7 // no definition for this platform: no opinion either way
	}

	within := func(lo, hi int64) bool {
		return (lo <= 0 || size >= lo) && (hi <= 0 || size <= hi)
	}
	if within(minSize, maxSize) {
		return 15 // ideal range
	}
	// Slightly outside range
	if within(minSize/2, maxSize*2) {
		return 10
	}
	// Suspiciously tiny or huge
	return 2
}

// scoreSafety maps the existing 0-100 SafetyScore to 0-15 range.
func scoreSafety(safetyScore int) int {
	if safetyScore <= 0 {
		return 0
	}
	// Scale 0-100 to 0-15
	scaled := safetyScore * 15 / 100
	if scaled > 15 {
		scaled = 15
	}
	return scaled
}
