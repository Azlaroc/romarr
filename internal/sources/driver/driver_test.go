package driver

import (
	"context"
	"errors"
	"sort"
	"testing"
)

type fakeSource struct {
	name string
	rel  []Release
	err  error
	pan  bool
}

func (f fakeSource) Name() string { return f.name }
func (f fakeSource) Search(ctx context.Context, q Query) ([]Release, error) {
	if f.pan {
		panic("boom")
	}
	return f.rel, f.err
}
func (f fakeSource) Fetch(ctx context.Context, r Release, destDir string) (string, error) {
	return "", nil
}

func TestFanOut_MergesAndIsolatesFailures(t *testing.T) {
	sources := []SearchSource{
		fakeSource{name: "a", rel: []Release{{Source: "a", Title: "one"}, {Source: "a", Title: "two"}}},
		fakeSource{name: "b", err: errors.New("down")},
		fakeSource{name: "c", rel: []Release{{Source: "c", Title: "three"}}},
		fakeSource{name: "d", pan: true},
	}

	merged, results := FanOut(context.Background(), sources, Query{Text: "x"})

	if len(merged) != 3 {
		t.Fatalf("merged = %d, want 3", len(merged))
	}
	got := make([]string, len(merged))
	for i, r := range merged {
		got[i] = r.Title
	}
	sort.Strings(got)
	want := []string{"one", "three", "two"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged titles = %v, want %v", got, want)
		}
	}

	byName := map[string]Result{}
	for _, r := range results {
		byName[r.Source] = r
	}
	if byName["b"].Err == nil {
		t.Error("source b should carry its error")
	}
	if byName["d"].Err == nil || len(byName["d"].Releases) != 0 {
		t.Error("panicking source d should surface an error and no releases")
	}
	if byName["a"].Err != nil || len(byName["a"].Releases) != 2 {
		t.Error("healthy source a should be intact")
	}
}

func TestFanOut_Empty(t *testing.T) {
	merged, results := FanOut(context.Background(), nil, Query{})
	if merged != nil || len(results) != 0 {
		t.Fatalf("empty fan-out should be empty, got %v / %v", merged, results)
	}
}
