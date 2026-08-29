package search

import (
	"testing"

	"gamarr/internal/models"
)

func TestScoreResults_ExactMatch(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Super Mario Bros", Seeders: 50, Size: 1_000_000, PlatformSlug: "nes", SourceType: "indexer", SafetyScore: 80},
	}
	scored := ScoreResults(results, "Super Mario Bros", "nes")
	if len(scored) != 1 {
		t.Fatalf("expected 1 result, got %d", len(scored))
	}
	r := scored[0]
	if r.ScoreBreakdown == nil {
		t.Fatal("expected ScoreBreakdown to be populated")
	}
	// Exact title match should get max title score (45)
	if r.ScoreBreakdown.TitleMatch != 45 {
		t.Errorf("TitleMatch=%d, want 45", r.ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_SubstringMatch(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Super Mario Bros Deluxe Edition", Seeders: 10, Size: 1_000_000, PlatformSlug: "nes"},
	}
	scored := ScoreResults(results, "Super Mario Bros", "")
	if scored[0].ScoreBreakdown.TitleMatch != 39 {
		t.Errorf("TitleMatch=%d, want 39 (substring match)", scored[0].ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_WordOverlap(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Mario Kart Double Dash", Seeders: 10, Size: 1_000_000_000, PlatformSlug: "ngc"},
	}
	// "mario" overlaps, "bros" doesn't -> 50% of 45 = 22
	scored := ScoreResults(results, "Mario Bros", "")
	if scored[0].ScoreBreakdown.TitleMatch != 22 {
		t.Errorf("TitleMatch=%d, want 22 (50%% word overlap)", scored[0].ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_EmptyQuery(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Some Game", Seeders: 10, Size: 1_000_000_000},
	}
	scored := ScoreResults(results, "", "")
	// Empty query should give neutral title score (22)
	if scored[0].ScoreBreakdown.TitleMatch != 22 {
		t.Errorf("TitleMatch=%d, want 22 (empty query)", scored[0].ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_PlatformBonus(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game A", PlatformSlug: "switch", Seeders: 10, Size: 1_000_000_000},
		{Title: "Game B", PlatformSlug: "nes", Seeders: 10, Size: 1_000_000},
	}
	scored := ScoreResults(results, "Game", "switch")

	// Matching platform
	if scored[0].ScoreBreakdown.PlatformMatch != 15 {
		t.Errorf("matching platform: PlatformMatch=%d, want 15", scored[0].ScoreBreakdown.PlatformMatch)
	}
	// Non-matching platform
	if scored[1].ScoreBreakdown.PlatformMatch != 0 {
		t.Errorf("non-matching platform: PlatformMatch=%d, want 0", scored[1].ScoreBreakdown.PlatformMatch)
	}
}

func TestScoreResults_PlatformNeutral(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", PlatformSlug: "nes", Seeders: 10, Size: 1_000_000},
	}
	scored := ScoreResults(results, "Game", "")
	// No filter -> neutral 8
	if scored[0].ScoreBreakdown.PlatformMatch != 8 {
		t.Errorf("no filter: PlatformMatch=%d, want 8", scored[0].ScoreBreakdown.PlatformMatch)
	}
}

func TestScoreResults_PlatformAll(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", PlatformSlug: "nes", Seeders: 10, Size: 1_000_000},
	}
	scored := ScoreResults(results, "Game", "all")
	if scored[0].ScoreBreakdown.PlatformMatch != 8 {
		t.Errorf("all filter: PlatformMatch=%d, want 8", scored[0].ScoreBreakdown.PlatformMatch)
	}
}

func TestScoreResults_PCPlatformMatch(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", PlatformSlug: "pc", Seeders: 10, Size: 5_000_000_000},
		{Title: "Game", PlatformSlug: "", Seeders: 10, Size: 5_000_000_000},
	}
	scored := ScoreResults(results, "Game", "pc")
	// Both "pc" and "" should match when filtering for "pc"
	if scored[0].ScoreBreakdown.PlatformMatch != 15 {
		t.Errorf("pc slug: PlatformMatch=%d, want 15", scored[0].ScoreBreakdown.PlatformMatch)
	}
	if scored[1].ScoreBreakdown.PlatformMatch != 15 {
		t.Errorf("empty slug with pc filter: PlatformMatch=%d, want 15", scored[1].ScoreBreakdown.PlatformMatch)
	}
}

func TestScoreResults_DDLSeederScore(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 0, Size: 1_000_000_000, SourceType: "ddl"},
	}
	scored := ScoreResults(results, "Game", "")
	// Direct HTTP tops the protocol order, with no seeders involved.
	if scored[0].ScoreBreakdown.SeederScore != 20 {
		t.Errorf("DDL SeederScore=%d, want 20", scored[0].ScoreBreakdown.SeederScore)
	}
}

func TestScoreResults_NZBSeederScore(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 0, Size: 1_000_000_000, SourceType: "indexer", DownloadProtocol: "nzb"},
	}
	scored := ScoreResults(results, "Game", "")
	// nzb sits between direct HTTP and any torrent.
	if scored[0].ScoreBreakdown.SeederScore != 16 {
		t.Errorf("NZB SeederScore=%d, want 16", scored[0].ScoreBreakdown.SeederScore)
	}
}

func TestScoreResults_SeederTiers(t *testing.T) {
	tests := []struct {
		name    string
		seeders int
		want    int
	}{
		{"zero seeders", 0, 0},
		{"1 seeder", 1, 0},
		{"2 seeders", 2, 2},
		{"5 seeders", 5, 4},
		{"10 seeders", 10, 6},
		{"20 seeders", 20, 8},
		{"50 seeders", 50, 10},
		{"100 seeders", 100, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []*models.SearchResult{
				{Title: "Game", Seeders: tt.seeders, Size: 1_000_000_000, SourceType: "indexer"},
			}
			scored := ScoreResults(results, "Game", "")
			if scored[0].ScoreBreakdown.SeederScore != tt.want {
				t.Errorf("SeederScore=%d, want %d", scored[0].ScoreBreakdown.SeederScore, tt.want)
			}
		})
	}
}

// The locked protocol order is direct-HTTP > nzb > torrent. Asserted as an
// ordering rather than three magic numbers so retuning the tiers cannot quietly
// invert the preference — which is exactly how a well-seeded torrent came to
// outrank every direct source.
func TestScoreResults_ProtocolPreferenceOrdering(t *testing.T) {
	ddl := []*models.SearchResult{{Title: "Game", Seeders: 0, Size: 1_000_000_000, SourceType: "ddl"}}
	nzb := []*models.SearchResult{{Title: "Game", Seeders: 0, Size: 1_000_000_000, SourceType: "indexer", DownloadProtocol: "nzb"}}
	// A torrent as healthy as torrents get: it must still lose to both.
	torrent := []*models.SearchResult{{Title: "Game", Seeders: 5000, Size: 1_000_000_000, SourceType: "indexer"}}

	d := ScoreResults(ddl, "Game", "")[0].ScoreBreakdown.SeederScore
	n := ScoreResults(nzb, "Game", "")[0].ScoreBreakdown.SeederScore
	tr := ScoreResults(torrent, "Game", "")[0].ScoreBreakdown.SeederScore

	if !(d > n && n > tr) {
		t.Errorf("protocol order broken: ddl=%d nzb=%d torrent(5000 seeders)=%d, want ddl > nzb > torrent", d, n, tr)
	}
}

// TestScoreResults_TierScale pins the 100-point composition — Title 45 +
// Platform 15 + Seeders 20 + Safety 20 — so a retune can't silently shrink
// the scale out from under SCHEDULER_MIN_SCORE and the confidence bands
// (blaster#349: size's 15 points were redistributed, not deleted, for
// exactly that reason).
func TestScoreResults_TierScale(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 0, Size: 12345, PlatformSlug: "nes", SourceType: "ddl", SafetyScore: 100},
	}
	scored := ScoreResults(results, "Game", "nes")
	b := scored[0].ScoreBreakdown
	if b.TitleMatch != 45 || b.PlatformMatch != 15 || b.SeederScore != 20 || b.SafetyScore != 20 {
		t.Errorf("tier maxima = title %d / platform %d / seeders %d / safety %d, want 45/15/20/20",
			b.TitleMatch, b.PlatformMatch, b.SeederScore, b.SafetyScore)
	}
	if scored[0].Score != 100 {
		t.Errorf("perfect result Total=%d, want exactly 100 — the tiers no longer sum to the scale", scored[0].Score)
	}
}

func TestScoreResults_SafetyScore(t *testing.T) {
	tests := []struct {
		name  string
		score int
		want  int
	}{
		{"zero", 0, 0},
		{"negative", -5, 0},
		{"50", 50, 10},
		{"100", 100, 20},
		{"75", 75, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []*models.SearchResult{
				{Title: "Game", Seeders: 10, Size: 1_000_000_000, SafetyScore: tt.score},
			}
			scored := ScoreResults(results, "Game", "")
			if scored[0].ScoreBreakdown.SafetyScore != tt.want {
				t.Errorf("SafetyScore=%d, want %d", scored[0].ScoreBreakdown.SafetyScore, tt.want)
			}
		})
	}
}

func TestScoreResults_Confidence(t *testing.T) {
	tests := []struct {
		name   string
		result *models.SearchResult
		query  string
		want   string
	}{
		{
			"high confidence - exact match with good seeders",
			&models.SearchResult{Title: "Zelda", Seeders: 50, Size: 5_000_000_000, PlatformSlug: "switch", SafetyScore: 80},
			"Zelda",
			"high",
		},
		{
			"low confidence - no match zero seeders",
			&models.SearchResult{Title: "Totally Different", Seeders: 0, Size: 500, PlatformSlug: "nes", SafetyScore: 0},
			"Mario",
			"low",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []*models.SearchResult{tt.result}
			scored := ScoreResults(results, tt.query, tt.result.PlatformSlug)
			if scored[0].ScoreBreakdown.Confidence != tt.want {
				t.Errorf("Confidence=%q (total=%d), want %q", scored[0].ScoreBreakdown.Confidence, scored[0].Score, tt.want)
			}
		})
	}
}

func TestScoreResults_TotalClamped(t *testing.T) {
	// A perfect result with all max scores
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 100, Size: 5_000_000_000, PlatformSlug: "pc", SourceType: "indexer", SafetyScore: 100},
	}
	scored := ScoreResults(results, "Game", "pc")
	if scored[0].Score > 100 {
		t.Errorf("Total=%d, should be clamped to 100", scored[0].Score)
	}
	if scored[0].Score < 0 {
		t.Errorf("Total=%d, should not be negative", scored[0].Score)
	}
}

func TestScoreResults_EmptyResults(t *testing.T) {
	scored := ScoreResults(nil, "query", "pc")
	if scored != nil {
		t.Errorf("expected nil for nil input, got %v", scored)
	}

	scored = ScoreResults([]*models.SearchResult{}, "query", "pc")
	if len(scored) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(scored))
	}
}

// TestScoreResults_SizeNeverScores pins blaster#349's other half: size is not
// a scoring input either. Two identical releases differing only in size must
// score identically — a 3KB cart and a 500MB disc are the same candidate to
// the scorer, because the DAT and the trust gate own byte-level judgment.
func TestScoreResults_SizeNeverScores(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 10, Size: 500_000_000, PlatformSlug: "nes"},
		{Title: "Game", Seeders: 10, Size: 2_987, PlatformSlug: "nes"},
		{Title: "Game", Seeders: 10, Size: 0, PlatformSlug: "nes"},
	}
	scored := ScoreResults(results, "Game", "")
	for i, r := range scored[1:] {
		if r.Score != scored[0].Score {
			t.Errorf("result %d: score %d != result 0's %d — size is influencing the score", i+1, r.Score, scored[0].Score)
		}
	}
}
