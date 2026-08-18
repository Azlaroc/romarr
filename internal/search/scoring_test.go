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
	// Exact title match should get max title score (40)
	if r.ScoreBreakdown.TitleMatch != 40 {
		t.Errorf("TitleMatch=%d, want 40", r.ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_SubstringMatch(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Super Mario Bros Deluxe Edition", Seeders: 10, Size: 1_000_000, PlatformSlug: "nes"},
	}
	scored := ScoreResults(results, "Super Mario Bros", "")
	if scored[0].ScoreBreakdown.TitleMatch != 35 {
		t.Errorf("TitleMatch=%d, want 35 (substring match)", scored[0].ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_WordOverlap(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Mario Kart Double Dash", Seeders: 10, Size: 1_000_000_000, PlatformSlug: "ngc"},
	}
	// "mario" overlaps, "bros" doesn't -> 50% of 40 = 20
	scored := ScoreResults(results, "Mario Bros", "")
	if scored[0].ScoreBreakdown.TitleMatch != 20 {
		t.Errorf("TitleMatch=%d, want 20 (50%% word overlap)", scored[0].ScoreBreakdown.TitleMatch)
	}
}

func TestScoreResults_EmptyQuery(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Some Game", Seeders: 10, Size: 1_000_000_000},
	}
	scored := ScoreResults(results, "", "")
	// Empty query should give neutral title score (20)
	if scored[0].ScoreBreakdown.TitleMatch != 20 {
		t.Errorf("TitleMatch=%d, want 20 (empty query)", scored[0].ScoreBreakdown.TitleMatch)
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
	if scored[0].ScoreBreakdown.SeederScore != 15 {
		t.Errorf("DDL SeederScore=%d, want 15", scored[0].ScoreBreakdown.SeederScore)
	}
}

func TestScoreResults_NZBSeederScore(t *testing.T) {
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 0, Size: 1_000_000_000, SourceType: "indexer", DownloadProtocol: "nzb"},
	}
	scored := ScoreResults(results, "Game", "")
	// nzb sits between direct HTTP and any torrent.
	if scored[0].ScoreBreakdown.SeederScore != 12 {
		t.Errorf("NZB SeederScore=%d, want 12", scored[0].ScoreBreakdown.SeederScore)
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

func TestScoreResults_SizeInRange(t *testing.T) {
	armBands(t, map[string][2]int64{"nes": {10e3, 5e6}})
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 10, Size: 1_000_000, PlatformSlug: "nes"},   // 1MB in range
		{Title: "Game", Seeders: 10, Size: 100_000_000, PlatformSlug: "nes"}, // 100MB way out of range
		{Title: "Game", Seeders: 10, Size: 0, PlatformSlug: "nes"},           // unknown
	}
	scored := ScoreResults(results, "Game", "")
	if scored[0].ScoreBreakdown.SizeScore != 15 {
		t.Errorf("in-range SizeScore=%d, want 15", scored[0].ScoreBreakdown.SizeScore)
	}
	if scored[1].ScoreBreakdown.SizeScore != 2 {
		t.Errorf("out-of-range SizeScore=%d, want 2", scored[1].ScoreBreakdown.SizeScore)
	}
	if scored[2].ScoreBreakdown.SizeScore != 7 {
		t.Errorf("unknown SizeScore=%d, want 7", scored[2].ScoreBreakdown.SizeScore)
	}
}

func TestScoreResults_SizeSlightlyOutOfRange(t *testing.T) {
	// NES ceiling is 5MB, slightly over = 5MB to 10MB
	armBands(t, map[string][2]int64{"nes": {10e3, 5e6}})
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 10, Size: 7_000_000, PlatformSlug: "nes"}, // 7MB: > 5MB but < 10MB
	}
	scored := ScoreResults(results, "Game", "")
	if scored[0].ScoreBreakdown.SizeScore != 10 {
		t.Errorf("slightly-out SizeScore=%d, want 10", scored[0].ScoreBreakdown.SizeScore)
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
		{"50", 50, 7},
		{"100", 100, 15},
		{"75", 75, 11},
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

func TestScoreResults_UnknownPlatformSize(t *testing.T) {
	armBands(t, map[string][2]int64{"nes": {10e3, 5e6}})

	// A platform with no definition scores neutral rather than being measured
	// against a generic band. The generic band was never neutral: its floor
	// rejected small-cartridge platforms outright and its scoring pushed them
	// below the grab threshold, which is how unlisted platforms broke.
	results := []*models.SearchResult{
		{Title: "Game", Seeders: 10, Size: 500_000_000, PlatformSlug: "unknownplatform"},
		{Title: "Game", Seeders: 10, Size: 2_987, PlatformSlug: "unknownplatform"},
	}
	scored := ScoreResults(results, "Game", "")
	for i, r := range scored {
		if r.ScoreBreakdown.SizeScore != 7 {
			t.Errorf("result %d: undefined-platform SizeScore=%d, want 7 (neutral)", i, r.ScoreBreakdown.SizeScore)
		}
	}
}
