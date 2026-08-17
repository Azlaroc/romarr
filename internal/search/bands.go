package search

import (
	"strings"
	"sync"
)

// BandStore supplies the per-platform size definitions that candidate sizes
// are judged against. The interface is declared here, in the leaf package, so
// internal/search never imports internal/db — *db.JobStore satisfies it
// structurally, the same arrangement archiveorg.CacheStore uses.
//
// Each pair is {min, max} in bytes and zero means "no limit on that end", so
// a platform absent from the map and a platform whose row is all zeroes mean
// the same thing: we have no opinion about sizes there.
type BandStore interface {
	PlatformSizeBands() map[string][2]int64
}

const (
	bandMin = 0
	bandMax = 1
)

// The table is memoized rather than queried per lookup: a scheduler cycle
// asks for a band once per candidate per path, which would be thousands of
// SELECTs against a table that changes a few times a day. It is not snapshot
// at boot either — a definition edited or re-derived at runtime has to take
// effect without a restart, since a process quietly judging against a
// superseded table is precisely the invisible-behaviour failure this whole
// design exists to end. Hence: lazy load, explicit invalidation.
var (
	bandMu     sync.RWMutex
	bandSource BandStore
	bandCache  map[string][2]int64
	bandLoaded bool
)

// SetBandStore attaches the definitions store. Called once at boot. A nil
// store — every unit test, and any binary that does not wire one — means no
// platform has limits, which is the safe direction to fail: the band exists
// to catch nonsense, not to be the thing that blocks a real release.
func SetBandStore(s BandStore) {
	bandMu.Lock()
	defer bandMu.Unlock()
	bandSource = s
	bandCache = nil
	bandLoaded = false
}

// RefreshBands drops the memo so the next lookup re-reads the table. Called
// after a catalog import rewrites the derived rows and after an operator
// edits one.
func RefreshBands() {
	bandMu.Lock()
	defer bandMu.Unlock()
	bandCache = nil
	bandLoaded = false
}

func loadedBands() map[string][2]int64 {
	bandMu.RLock()
	if bandLoaded {
		defer bandMu.RUnlock()
		return bandCache
	}
	bandMu.RUnlock()

	bandMu.Lock()
	defer bandMu.Unlock()
	if bandLoaded {
		return bandCache
	}
	if bandSource != nil {
		bandCache = bandSource.PlatformSizeBands()
	}
	// Marked loaded even when the table is empty or there is no store, so a
	// fresh install does not re-query on every candidate. An import or an
	// edit invalidates it.
	bandLoaded = true
	return bandCache
}

// PlatformSizeRange returns the [min, max] size band in bytes for a platform
// slug, where zero on either end means that end is unbounded. An unknown
// platform yields {0, 0} — no floor and no ceiling — because declining to
// judge a platform we have no data for is the honest answer, and a confident
// wrong floor is what made unlisted platforms unusable before.
//
// This is the single resolver: both the ranking filter and the size score
// read it, so the two can never disagree about what a platform's band is.
func PlatformSizeRange(slug string) (int64, int64) {
	b := loadedBands()[strings.ToLower(slug)]
	return b[bandMin], b[bandMax]
}
