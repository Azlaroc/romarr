// Package driver defines the source-plane contract for RomArr.
//
// A SearchSource is anything RomArr can ask "what releases match this query?"
// and "fetch this release to disk." Torrent indexers (via Prowlarr), scrape
// sites (Vimm), and native API clients (archive.org) all reduce to this same
// two-method shape. Today the concrete drivers still live as free functions in
// internal/search; the archive.org driver (internal/sources/archiveorg) is the
// first one written natively against this interface. The follow-up F2 work
// hoists the remaining drivers behind it so the four fan-out call sites collapse
// into a single FanOut over a registered slice.
//
// The key property archive.org forces into the contract that Torznab never
// modelled: a Release is a single *file*, carrying its own md5/sha1/size. That
// is what lets the selector (F4) and the normalize/convert hand-off (F5) work
// on verifiable per-file identity instead of a whole-pack blob.
package driver

import (
	"context"
	"sync"
)

// Release is one fetchable artefact from a source. For archive.org it is a
// single file inside a collection item; for a torrent indexer it is one
// torrent/NZB. The hash fields are populated when the source exposes them
// (archive.org always does) and flow downstream to F5 for verification.
type Release struct {
	Source       string // driver name that produced this release ("archiveorg", "prowlarr", ...)
	Title        string // human title (usually the file/torrent name)
	PlatformSlug string // RomArr platform slug this release was resolved for
	Size         int64  // exact byte size, 0 if unknown
	MD5          string // hex, lowercase; "" if the source doesn't expose it
	SHA1         string // hex, lowercase; "" if the source doesn't expose it

	// Locator. Exactly one fulfilment path is meaningful per SourceType:
	//   ddl     -> DownloadURL is an HTTP(S) GET (archive.org, Vimm, Myrient)
	//   torrent -> DownloadURL/MagnetURL/InfoHash hand off to the torrent client
	SourceType  string // "ddl" or "torrent"
	DownloadURL string
	MagnetURL   string
	InfoHash    string

	// GUID is a stable identity for dedup/blocklist. For archive.org it is the
	// per-file download URL (globally unique).
	GUID string
}

// Query is what a caller asks a SearchSource to resolve.
type Query struct {
	Text         string // free-text title, e.g. "castlevania symphony of the night"
	PlatformSlug string // "" means "any platform the source can serve"
	// PlatformName is the platform's display name ("Game Boy Color"), from
	// the caller's platform registry. A driver that searches an open corpus
	// uses it to steer relevance. Optional: drivers must work without it, and
	// no driver may hold its own copy of the platform vocabulary.
	PlatformName string
	Limit        int // 0 means the driver's own default cap

	// Regions is the caller's ordered region interest, from the quality
	// profile that will judge the results (lowercased tokens: "usa", "japan").
	// A driver may use it to skip work it knows the caller will reject, but
	// it must never DROP a region the caller asked for: a driver-level filter
	// the profile cannot see turns a wanted dump into a gap that looks like a
	// short catalog. Empty means "no stated interest" — apply defaults.
	Regions []string
}

// SearchSource is the source-plane contract. Implementations MUST be safe for
// concurrent use by multiple goroutines (FanOut relies on it).
type SearchSource interface {
	// Name is the stable driver identifier, e.g. "archiveorg".
	Name() string

	// Search resolves releases matching q. It returns an error only for a
	// genuine source failure (network/HTTP/parse); "no matches" is (nil, nil)
	// so a healthy-but-empty source never trips a circuit breaker.
	Search(ctx context.Context, q Query) ([]Release, error)

	// Fetch downloads r into destDir and returns the absolute path of the
	// written file. Implementations should be resumable where the transport
	// allows (archive.org honours HTTP Range).
	Fetch(ctx context.Context, r Release, destDir string) (localPath string, err error)
}

// Result pairs a source with the outcome of its Search, so FanOut can report
// per-source failures without discarding the successful ones.
type Result struct {
	Source   string
	Releases []Release
	Err      error
}

// FanOut runs Search across every source concurrently and returns the merged
// releases plus a per-source result set. A source that errors or panics
// contributes an entry to results with Err set and zero releases; it never
// aborts the others. This is the shape the four current fan-out call sites
// (api.go, requests.go, torznab_wire.go, cmd/gamarr/main.go) converge on once
// Prowlarr/Vimm are hoisted behind SearchSource.
func FanOut(ctx context.Context, sources []SearchSource, q Query) (merged []Release, results []Result) {
	results = make([]Result, len(sources))
	var wg sync.WaitGroup
	wg.Add(len(sources))
	for i, src := range sources {
		go func(i int, src SearchSource) {
			defer wg.Done()
			res := Result{Source: src.Name()}
			defer func() {
				if p := recover(); p != nil {
					res.Err = &panicError{src.Name(), p}
					res.Releases = nil
				}
				results[i] = res
			}()
			rel, err := src.Search(ctx, q)
			res.Releases, res.Err = rel, err
		}(i, src)
	}
	wg.Wait()
	for _, r := range results {
		merged = append(merged, r.Releases...)
	}
	return merged, results
}

type panicError struct {
	source string
	val    any
}

func (e *panicError) Error() string {
	return "driver " + e.source + " panicked during Search"
}
