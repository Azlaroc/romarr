package archiveorg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"gamarr/internal/sources/driver"
)

// Open search — finding items instead of reading a pinned one.
//
// The driver was built to read collection items an operator had mapped in the
// registry, and an unmapped platform returned nothing at all. That made the
// registry a prerequisite: a game on a platform nobody had pinned an item for
// could not be found, no matter how many copies archive.org holds.
//
// Now a pinned item is a *preference*, not a gate. It is searched first —
// curated No-Intro/Redump collections are better-named and cheaper to read
// than anything a query turns up — and open search fills in behind it.
//
// The cost model is what shapes this code. Reading a pinned item is one
// metadata fetch that caches for an hour; open search is a query plus one
// metadata fetch per candidate item. Anonymous archive.org allows on the order
// of a request a second, and RomArr runs whole-wishlist cycles, so: candidates
// are capped, every query result is cached (including the empty ones, which
// are the expensive mistake to repeat), and calls are spaced by a minimum
// interval.
const (
	searchTTL         = time.Hour
	defaultCandidates = 5
	defaultInterval   = time.Second
	maxSearchSize     = 4 << 20
)

type searchEntry struct {
	items []string
	at    time.Time
}

// openSearchState is the driver's open-search half: the query cache and the
// pacer. Kept separate from the item-metadata cache because they expire and
// grow on completely different terms.
type openSearchState struct {
	mu       sync.Mutex
	cache    map[string]searchEntry
	lastCall time.Time
}

// WithCandidates caps how many items one open search will read metadata for.
func WithCandidates(n int) Option {
	return func(d *Driver) {
		if n > 0 {
			d.candidates = n
		}
	}
}

// WithSearchInterval sets the minimum spacing between open-search queries.
// Zero disables pacing (loopback stubs in tests).
func WithSearchInterval(v time.Duration) Option {
	return func(d *Driver) { d.interval = v }
}

// findItems resolves a query to candidate item identifiers, cached and paced.
// A query that matches nothing caches the empty answer: an unfindable title on
// a wishlist would otherwise pay for a live query on every cycle, forever.
func (d *Driver) findItems(ctx context.Context, text, platformName string) ([]string, error) {
	q := buildQuery(text, platformName)
	if q == "" {
		return nil, nil
	}
	key := q

	d.open.mu.Lock()
	if e, ok := d.open.cache[key]; ok && time.Since(e.at) < searchTTL {
		items := e.items
		d.open.mu.Unlock()
		return items, nil
	}
	// Pace while holding the lock: concurrent searches queue behind each
	// other rather than all firing at once and tripping the rate limit
	// together.
	if d.interval > 0 {
		if wait := d.interval - time.Since(d.open.lastCall); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				d.open.mu.Unlock()
				return nil, ctx.Err()
			}
		}
		d.open.lastCall = time.Now()
	}
	d.open.mu.Unlock()

	items, err := d.queryItems(ctx, q)
	if err != nil {
		return nil, err
	}

	d.open.mu.Lock()
	if d.open.cache == nil {
		d.open.cache = map[string]searchEntry{}
	}
	d.open.cache[key] = searchEntry{items: items, at: time.Now()}
	d.open.mu.Unlock()
	return items, nil
}

// buildQuery turns a title (and the platform's display name, when the caller
// knows it) into an archive.org query. mediatype:software is the whole
// software corpus — collections, single-game uploads, preservation dumps —
// which is the population a ROM lives in.
func buildQuery(text, platformName string) string {
	words := queryWords(text)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("(")
	for i, w := range words {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString(w)
	}
	b.WriteString(")")
	if name := strings.TrimSpace(platformName); name != "" {
		// A soft hint, not a filter: items rarely name the platform in a
		// field, but they very often name it in the title or description, and
		// relevance ranking is what we are steering.
		b.WriteString(" AND (")
		b.WriteString(quotePhrase(name))
		b.WriteString(" OR mediatype:software)")
	} else {
		b.WriteString(" AND mediatype:software")
	}
	return b.String()
}

// queryWords keeps the alphanumeric tokens Lucene will accept unquoted.
func queryWords(text string) []string {
	var out []string
	for _, w := range wordSplitRe.Split(strings.ToLower(text), -1) {
		if len(w) < 2 {
			continue
		}
		out = append(out, w)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func quotePhrase(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, "") + `"`
}

type advancedSearchResponse struct {
	Response struct {
		NumFound int `json:"numFound"`
		Docs     []struct {
			Identifier string `json:"identifier"`
		} `json:"docs"`
	} `json:"response"`
}

func (d *Driver) queryItems(ctx context.Context, q string) ([]string, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Add("fl[]", "identifier")
	params.Set("rows", fmt.Sprint(d.candidates))
	params.Set("page", "1")
	params.Set("output", "json")
	params.Add("sort[]", "downloads desc")

	u := d.baseURL + "/advancedsearch.php?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RomArr/archiveorg")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("archiveorg search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archiveorg search: HTTP %d", resp.StatusCode)
	}
	var out advancedSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSearchSize)).Decode(&out); err != nil {
		return nil, fmt.Errorf("archiveorg search: decode: %w", err)
	}
	items := make([]string, 0, len(out.Response.Docs))
	for _, doc := range out.Response.Docs {
		if doc.Identifier != "" {
			items = append(items, doc.Identifier)
		}
	}
	return items, nil
}

// searchOpen runs open search and matches inside whatever items it finds,
// skipping any item already read on the pinned path.
func (d *Driver) searchOpen(ctx context.Context, q queryContext, seen map[string]bool, limit int) []driver.Release {
	items, err := d.findItems(ctx, q.text, q.platformName)
	if err != nil {
		// Open search is the widening step: its failure must degrade the
		// result set, never blank the pinned hits already collected.
		slog.Warn("archiveorg open search failed", "error", err)
		return nil
	}
	sort.Strings(items)
	var out []driver.Release
	for _, item := range items {
		if len(out) >= limit {
			break
		}
		if seen[item] {
			continue
		}
		part, err := d.searchItem(ctx, item, q.platformSlug, q.words, limit-len(out), q.keepAllRegions)
		if err != nil {
			slog.Warn("archiveorg open search item unreadable", "item", item, "error", err)
			continue
		}
		out = append(out, part...)
	}
	return out
}

// queryContext is the per-Search state both halves need.
type queryContext struct {
	text           string
	platformSlug   string
	platformName   string
	words          map[string]bool
	keepAllRegions bool
}
