package search

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/platform"
)

// A ps2 release on a general indexer carries the standard Newznab category
// (or a sibling console category plus a platform-naming title) — never the
// registry's tracker-specific customs. The filter must keep both shapes,
// label them as the requested platform, and still drop unrelated releases.
// The customs-only filter silently emptied every Prowlarr search for ps2.
func TestSearchProwlarr_StandardCatsAndTitleRescue(t *testing.T) {
	platform.SetRegistry(platform.StaticRegistry{
		{Slug: "ps2", DisplayName: "PS2", ProwlarrCategories: []int{100011}, TorznabCategory: "1090"},
	})
	t.Cleanup(func() { platform.SetRegistry(nil) })

	items := []map[string]interface{}{
		{ // standard Console/Other tag — kept via the standard category
			"title":      "Grand Theft Auto - Vice City (USA) DVD",
			"size":       float64(4_000_000_000),
			"categories": []interface{}{map[string]interface{}{"id": float64(1090)}},
		},
		{ // misfiled under Console/PS3, but the title names ps2 — rescued
			"title":      "Grand Theft Auto Vice City v1 02 USA PS2-PS4 -PSN",
			"size":       float64(2_900_000_000),
			"categories": []interface{}{map[string]interface{}{"id": float64(1080)}},
		},
		{ // unrelated: PC category, nothing in the title names ps2 — dropped
			"title":      "Grand Theft Auto Vice City Definitive Edition Update",
			"size":       float64(9_000_000_000),
			"categories": []interface{}{map[string]interface{}{"id": float64(4020)}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(items)
	}))
	defer srv.Close()

	cfg := &config.Config{
		ProwlarrURL:          srv.URL,
		ProwlarrAPIKey:       "key",
		ProwlarrGameIndexers: []int{1},
	}
	results := SearchProwlarr(cfg, "grand theft auto vice city", "ps2")
	if len(results) != 2 {
		got := make([]string, 0, len(results))
		for _, r := range results {
			got = append(got, r.Title)
		}
		t.Fatalf("expected 2 results, got %d: %v", len(results), got)
	}
	for _, r := range results {
		if r.PlatformSlug != "ps2" {
			t.Errorf("%q: platform_slug=%q, want ps2", r.Title, r.PlatformSlug)
		}
		if r.Platform != "PS2" {
			t.Errorf("%q: platform=%q, want PS2", r.Title, r.Platform)
		}
	}
}
