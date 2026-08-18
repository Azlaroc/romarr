package search

import (
	"context"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/models"
	"gamarr/internal/sources"
)

// testRegistry returns the embedded default registry for tests.
func testRegistry(t *testing.T) *sources.Registry {
	t.Helper()
	r, err := sources.Default()
	if err != nil {
		t.Fatalf("load default registry: %v", err)
	}
	return r
}

// stubSource is a Source that returns a fixed result set (or panics), for
// exercising FanOut without any network.
type stubSource struct {
	name    string
	results []*models.SearchResult
	boom    bool
}

func (s stubSource) Name() string { return s.name }
func (s stubSource) Search(_ context.Context, _, _ string, _ Opts) []*models.SearchResult {
	if s.boom {
		panic("stub source boom")
	}
	return s.results
}

func TestFanOut_MergesInSourceOrder(t *testing.T) {
	a := stubSource{name: "a", results: []*models.SearchResult{{Title: "A1"}, {Title: "A2"}}}
	b := stubSource{name: "b", results: []*models.SearchResult{{Title: "B1"}}}

	got := FanOut(context.Background(), []Source{a, b}, "q", "", Opts{})
	if len(got) != 3 {
		t.Fatalf("expected 3 merged results, got %d", len(got))
	}
	// Merge order is deterministic: the order of the sources slice.
	want := []string{"A1", "A2", "B1"}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("result[%d].Title = %q, want %q", i, got[i].Title, w)
		}
	}
}

func TestFanOut_IsolatesPanic(t *testing.T) {
	bad := stubSource{name: "bad", boom: true}
	good := stubSource{name: "good", results: []*models.SearchResult{{Title: "G"}}}

	got := FanOut(context.Background(), []Source{bad, good}, "q", "", Opts{})
	if len(got) != 1 || got[0].Title != "G" {
		t.Fatalf("panicking source should be isolated; got %+v", got)
	}
}

func TestFanOut_Empty(t *testing.T) {
	if got := FanOut(context.Background(), nil, "q", "", Opts{}); len(got) != 0 {
		t.Errorf("expected no results from empty source set, got %d", len(got))
	}
}

func TestBuildSources_OrderAndMembership(t *testing.T) {
	cfg := &config.Config{Sources: testRegistry(t)}
	srcs := BuildSources(cfg)

	got := make([]string, len(srcs))
	for i, s := range srcs {
		got[i] = s.Name()
	}
	// archive.org first (native, hash-carrying), then the Prowlarr indexer,
	// then Vimm. Myrient is intentionally absent.
	want := []string{"archiveorg", "prowlarr", "vimm"}
	if len(got) != len(want) {
		t.Fatalf("BuildSources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BuildSources[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
