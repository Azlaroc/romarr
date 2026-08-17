package search

import (
	"sync"
	"testing"
)

type fakeBandStore struct {
	mu    sync.Mutex
	bands map[string][2]int64
	reads int
}

func (f *fakeBandStore) PlatformSizeBands() map[string][2]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	out := make(map[string][2]int64, len(f.bands))
	for k, v := range f.bands {
		out[k] = v
	}
	return out
}

func (f *fakeBandStore) set(slug string, lo, hi int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bands[slug] = [2]int64{lo, hi}
}

// armBands installs a definitions table for one test. The resolver is
// process-global, so every caller must undo it or the next test inherits it.
func armBands(t *testing.T, bands map[string][2]int64) *fakeBandStore {
	t.Helper()
	store := &fakeBandStore{bands: bands}
	SetBandStore(store)
	t.Cleanup(func() { SetBandStore(nil) })
	return store
}

func TestPlatformSizeRangeUnknownPlatformHasNoLimits(t *testing.T) {
	armBands(t, map[string][2]int64{"psx": {50e6, 1e9}})

	// A platform we have no definition for gets no floor and no ceiling.
	// Inventing a floor here is what made every unlisted platform unusable:
	// a generic minimum rejected genuine cartridge dumps as "too small".
	lo, hi := PlatformSizeRange("arcade")
	if lo != 0 || hi != 0 {
		t.Errorf("unknown platform band = [%d %d], want [0 0]", lo, hi)
	}
}

func TestPlatformSizeRangeWithNoStore(t *testing.T) {
	SetBandStore(nil)
	t.Cleanup(func() { SetBandStore(nil) })

	if lo, hi := PlatformSizeRange("psx"); lo != 0 || hi != 0 {
		t.Errorf("band with no store = [%d %d], want [0 0]", lo, hi)
	}
}

func TestPlatformSizeRangeReadsDefinitions(t *testing.T) {
	armBands(t, map[string][2]int64{
		"psx":       {4734000, 1499371948},
		"atari2600": {256, 65536},
		"ngc":       {0, 2919956480}, // flat catalog: ceiling only
	})

	if lo, hi := PlatformSizeRange("psx"); lo != 4734000 || hi != 1499371948 {
		t.Errorf("psx = [%d %d]", lo, hi)
	}
	if lo, hi := PlatformSizeRange("atari2600"); lo != 256 || hi != 65536 {
		t.Errorf("atari2600 = [%d %d]", lo, hi)
	}
	// A flat catalog stores a ceiling with no floor; the zero must survive
	// the round trip rather than being read as "unset, use a default".
	if lo, hi := PlatformSizeRange("ngc"); lo != 0 || hi != 2919956480 {
		t.Errorf("ngc = [%d %d], want [0 2919956480]", lo, hi)
	}
}

func TestPlatformSizeRangeIsCaseInsensitive(t *testing.T) {
	armBands(t, map[string][2]int64{"psx": {50e6, 1e9}})

	// Slugs arrive from indexers in whatever case the source used. The two
	// call paths previously disagreed here — the score lowercased and the
	// filter did not — so the same candidate could be scored against the psx
	// band and filtered against nothing.
	for _, slug := range []string{"psx", "PSX", "Psx"} {
		if lo, hi := PlatformSizeRange(slug); lo != 50e6 || hi != 1e9 {
			t.Errorf("%q = [%d %d], want the psx band", slug, lo, hi)
		}
	}
}

func TestBandsMemoizedUntilRefreshed(t *testing.T) {
	store := armBands(t, map[string][2]int64{"psx": {50e6, 1e9}})

	for i := 0; i < 25; i++ {
		PlatformSizeRange("psx")
		PlatformSizeRange("nes")
	}
	if store.reads != 1 {
		t.Errorf("table read %d times, want 1 — a scheduler cycle would hammer the DB", store.reads)
	}

	// A definition changed underneath us: without invalidation the process
	// would keep judging against the superseded table until restart.
	store.set("psx", 1, 2)
	if lo, _ := PlatformSizeRange("psx"); lo != 50e6 {
		t.Fatalf("memo did not hold: got floor %d", lo)
	}
	RefreshBands()
	if lo, hi := PlatformSizeRange("psx"); lo != 1 || hi != 2 {
		t.Errorf("after refresh = [%d %d], want the new definition", lo, hi)
	}
	if store.reads != 2 {
		t.Errorf("reads = %d, want exactly one re-read after refresh", store.reads)
	}
}

func TestEmptyTableDoesNotRequeryPerLookup(t *testing.T) {
	store := armBands(t, map[string][2]int64{})

	for i := 0; i < 25; i++ {
		PlatformSizeRange("psx")
	}
	// A fresh install has no definitions at all. Treating "empty" as "not
	// loaded yet" would turn the hot path into a query per candidate.
	if store.reads != 1 {
		t.Errorf("empty table read %d times, want 1", store.reads)
	}
}

// TestSizeBandSingleResolver is the regression guard for the defect this
// design replaced: the size score read its own copy of the band table,
// bypassing the exported resolver and carrying a second hardcoded fallback,
// so fixing the filter left the score still wrong. Any reintroduced second
// lookup makes this fail.
func TestSizeBandSingleResolver(t *testing.T) {
	armBands(t, map[string][2]int64{"psx": {1000, 5000}})

	sizes := []int64{1, 499, 500, 999, 1000, 3000, 5000, 5001, 10000, 10001, 99999}
	for _, slug := range []string{"psx", "PSX"} {
		lo, hi := PlatformSizeRange(slug)
		if lo != 1000 || hi != 5000 {
			t.Fatalf("%q resolver = [%d %d]", slug, lo, hi)
		}
		for _, size := range sizes {
			want := 2
			switch {
			case size >= lo && size <= hi:
				want = 15
			case size >= lo/2 && size <= hi*2:
				want = 10
			}
			if got := scoreSizeRange(size, slug); got != want {
				t.Errorf("scoreSizeRange(%d, %q) = %d, want %d — score and resolver disagree",
					size, slug, got, want)
			}
		}
	}
}

func TestScoreSizeRangeNoOpinionWithoutDefinition(t *testing.T) {
	armBands(t, map[string][2]int64{"psx": {1000, 5000}})

	// No definition means no opinion, scored the same as an unreported size —
	// not "suspiciously huge", which is what a generic default would say
	// about a perfectly normal arcade rom.
	if got := scoreSizeRange(3000, "arcade"); got != 7 {
		t.Errorf("undefined platform SizeScore=%d, want 7 (neutral)", got)
	}
	if got := scoreSizeRange(0, "psx"); got != 7 {
		t.Errorf("unreported size SizeScore=%d, want 7 (neutral)", got)
	}
}
