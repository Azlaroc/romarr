// Package archiveorg is RomArr's native Internet Archive source driver.
//
// It is the first driver written against sources/driver.SearchSource, and the
// reference for what "a native API driver, not a Torznab indexer" buys us:
// archive.org exposes each collection item's contents as a metadata JSON with
// per-file md5/sha1/size, so a single game buried inside a 1,500-file No-Intro /
// Redump collection resolves to one fetchable file with verifiable identity —
// no whole-pack download, no torrent round-trip.
//
// Ported from the RomSeerr prototype (proven end-to-end for Genesis and PSX):
//   - catalog  = GET https://archive.org/metadata/<item>  -> files[]
//   - fetch    = GET https://archive.org/download/<item>/<name>  (Range-resumable)
//
// The <name> in metadata already leads with "<item>/" for collections whose
// files sit in an item-named subfolder (e.g. the hearto PSX 1G1R set), so the
// download path legitimately repeats the identifier. We pass name verbatim;
// stripping it 404s. (Confirmed live: doubled path -> HTTP 206.)
package archiveorg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gamarr/internal/sources/driver"
)

const (
	driverName      = "archiveorg"
	defaultBaseURL  = "https://archive.org"
	defaultLimit    = 25
	metadataTTL     = time.Hour
	defaultTimeout  = 30 * time.Second
	fetchTimeout    = 30 * time.Minute
	maxMetadataSize = 32 << 20 // 32 MiB cap on a metadata document
)

// Driver resolves releases from Internet Archive collection items. It is safe
// for concurrent use.
type Driver struct {
	baseURL string
	items   map[string]string // platformSlug -> IA item identifier
	limit   int
	http    *http.Client

	store CacheStore // optional persistent tier; nil = memory-only

	mu    sync.RWMutex
	cache map[string]cacheEntry // item -> parsed files
}

type cacheEntry struct {
	files []iaFile
	at    time.Time
}

// CacheStore persists item metadata across process restarts (the second
// cache tier; the in-memory map stays first). files is the JSON-encoded
// []iaFile. Implementations must be safe for concurrent use.
type CacheStore interface {
	GetItemMetadata(item string) (files []byte, fetchedAt time.Time, ok bool)
	PutItemMetadata(item string, files []byte, fetchedAt time.Time)
}

// Option configures a Driver.
type Option func(*Driver)

// WithBaseURL overrides the archive.org base (used by tests / mirrors).
func WithBaseURL(base string) Option {
	return func(d *Driver) { d.baseURL = strings.TrimRight(base, "/") }
}

// WithHTTPClient injects a custom client (timeouts, transport).
func WithHTTPClient(c *http.Client) Option { return func(d *Driver) { d.http = c } }

// WithCache attaches a persistent metadata store consulted between the
// in-memory map and the network. Survives restarts, so a boot no longer
// costs one metadata fetch per mapped item against IA's anonymous budget.
func WithCache(store CacheStore) Option { return func(d *Driver) { d.store = store } }

// WithLimit caps releases returned per Search.
func WithLimit(n int) Option {
	return func(d *Driver) {
		if n > 0 {
			d.limit = n
		}
	}
}

// New builds a driver. items maps RomArr platform slugs to archive.org item
// identifiers (e.g. {"psx": "2024-sony-playstation-usa-hearto-1g1r-collection"}).
func New(items map[string]string, opts ...Option) *Driver {
	d := &Driver{
		baseURL: defaultBaseURL,
		items:   items,
		limit:   defaultLimit,
		http:    &http.Client{Timeout: defaultTimeout},
		cache:   make(map[string]cacheEntry),
	}
	for _, o := range opts {
		o(d)
	}
	if d.items == nil {
		d.items = map[string]string{}
	}
	return d
}

// Name implements driver.SearchSource.
func (d *Driver) Name() string { return driverName }

// iaFile is the subset of an archive.org metadata files[] entry we consume.
type iaFile struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Format string `json:"format"`
	Size   string `json:"size"` // archive.org encodes size as a string
	MD5    string `json:"md5"`
	SHA1   string `json:"sha1"`
}

type iaMetadata struct {
	Server          string   `json:"server"`
	Dir             string   `json:"dir"`
	WorkableServers []string `json:"workable_servers"`
	Files           []iaFile `json:"files"`
}

// romExtensions is the set of container/rom extensions we treat as fetchable
// game files. Everything else in an item (dats, sqlite, torrents, txt) is
// metadata noise. This is a driver-local allowlist for the archive.org scrape,
// NOT the library scanner's whitelist (that undercount bug is F8).
var romExtensions = map[string]bool{
	".zip": true, ".7z": true, ".chd": true, ".iso": true, ".cso": true,
	".bin": true, ".cue": true, ".gdi": true, ".rvz": true, ".wua": true,
	".nsp": true, ".xci": true, ".nsz": true, ".zcci": true, ".3ds": true,
	".cia": true, ".nds": true, ".gba": true, ".gb": true, ".gbc": true,
	".nes": true, ".sfc": true, ".smc": true, ".n64": true, ".z64": true,
	".md": true, ".gen": true, ".sms": true, ".gg": true, ".pce": true,
	".a26": true, ".col": true,
}

// Non-English region filter, ported from the Myrient driver: drop clearly
// non-English regional dumps unless they also carry an English tag.
//
// This is a COARSE PRE-FILTER, applied only when the caller has stated no
// interest in any of these regions. A profile that ranks "japan" must be able
// to see Japanese dumps — dropping them here, where the selector cannot see
// it, turns a wanted release into a gap that reads as a short catalog rather
// than as a filter.
var (
	nonEnglishRe = regexp.MustCompile(`\((?:Japan|Korea|China|Taiwan|France|Germany|Spain|Italy|Netherlands|Sweden|Brazil)\)`)
	englishRe    = regexp.MustCompile(`\((?:USA|World|Europe|UK|Australia|Canada)\)|\(En`)
	wordSplitRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

// Search implements driver.SearchSource. With a platform slug it searches
// that platform's mapped item; an unmapped slug yields (nil, nil). An empty
// slug ("All platforms") fans across every mapped item in deterministic slug
// order — affordable now that the driver is long-lived and its item metadata
// cache holds between queries, where it used to mean N ~1MB refetches per
// query (the reason the empty slug was previously rejected).
func (d *Driver) Search(ctx context.Context, q driver.Query) ([]driver.Release, error) {
	qWords := tokenize(q.Text)
	if len(qWords) == 0 {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 || limit > d.limit {
		limit = d.limit
	}

	// The coarse non-English drop applies only when the caller has named no
	// interest in any of those regions.
	keepAllRegions := wantsNonEnglish(q.Regions)

	if q.PlatformSlug != "" {
		item, ok := d.items[q.PlatformSlug]
		if !ok || item == "" {
			return nil, nil
		}
		return d.searchItem(ctx, item, q.PlatformSlug, qWords, limit, keepAllRegions)
	}

	slugs := make([]string, 0, len(d.items))
	for s := range d.items {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	var out []driver.Release
	var firstErr error
	for _, slug := range slugs {
		if len(out) >= limit {
			break
		}
		item := d.items[slug]
		if item == "" {
			continue
		}
		part, err := d.searchItem(ctx, item, slug, qWords, limit-len(out), keepAllRegions)
		if err != nil {
			// One broken item must not blank the whole fan; surface the
			// error only when nothing at all could be searched.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, part...)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// searchItem matches one item's file listing against the tokenized query,
// tagging releases with the item's platform slug.
// nonEnglishRegions are the region tokens the coarse filter would drop. A
// caller naming any of them disables the filter entirely rather than trying
// to drop "the other" non-English regions: region policy belongs to the
// profile, and a half-applied filter is harder to reason about than none.
var nonEnglishRegions = map[string]bool{
	"japan": true, "korea": true, "china": true, "taiwan": true, "france": true,
	"germany": true, "spain": true, "italy": true, "netherlands": true,
	"sweden": true, "brazil": true, "asia": true,
}

// wantsNonEnglish reports whether the caller's region priority names a region
// the coarse filter would otherwise drop.
func wantsNonEnglish(regions []string) bool {
	for _, r := range regions {
		if nonEnglishRegions[strings.ToLower(strings.TrimSpace(r))] {
			return true
		}
	}
	return false
}

func (d *Driver) searchItem(ctx context.Context, item, platformSlug string, qWords map[string]bool, limit int, keepAllRegions bool) ([]driver.Release, error) {
	files, err := d.listing(ctx, item)
	if err != nil {
		return nil, err
	}

	var out []driver.Release
	for _, f := range files {
		base := filepath.Base(f.Name)
		if !romExtensions[strings.ToLower(filepath.Ext(base))] {
			continue
		}
		if !keepAllRegions && nonEnglishRe.MatchString(base) && !englishRe.MatchString(base) {
			continue
		}
		fWords := tokenize(strings.TrimSuffix(base, filepath.Ext(base)))
		if !overlaps(qWords, fWords) {
			continue
		}
		size, _ := strconv.ParseInt(f.Size, 10, 64)
		dl := d.downloadURL(item, f.Name)
		out = append(out, driver.Release{
			Source:       driverName,
			Title:        base,
			PlatformSlug: platformSlug,
			Size:         size,
			MD5:          strings.ToLower(f.MD5),
			SHA1:         strings.ToLower(f.SHA1),
			SourceType:   "ddl",
			DownloadURL:  dl,
			GUID:         dl,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// listing fetches an item's file list through three tiers: the in-memory
// map (within TTL), the persistent store (within TTL — warms the map), then
// the network (writes both). On a network failure a stale store copy is
// served instead of failing the search (stale-if-error: an IA outage or 429
// degrades results rather than blanking them).
func (d *Driver) listing(ctx context.Context, item string) ([]iaFile, error) {
	d.mu.RLock()
	if e, ok := d.cache[item]; ok && time.Since(e.at) < metadataTTL {
		d.mu.RUnlock()
		return e.files, nil
	}
	d.mu.RUnlock()

	var stale []iaFile
	if d.store != nil {
		if raw, at, ok := d.store.GetItemMetadata(item); ok {
			var files []iaFile
			if err := json.Unmarshal(raw, &files); err == nil {
				if time.Since(at) < metadataTTL {
					d.mu.Lock()
					d.cache[item] = cacheEntry{files: files, at: at}
					d.mu.Unlock()
					return files, nil
				}
				stale = files
			}
			// Corrupt rows are treated as a miss and overwritten on fetch.
		}
	}

	files, err := d.fetchListing(ctx, item)
	if err != nil {
		if stale != nil {
			slog.Warn("archiveorg metadata fetch failed; serving stale cached listing", "item", item, "error", err)
			return stale, nil
		}
		return nil, err
	}

	now := time.Now()
	d.mu.Lock()
	d.cache[item] = cacheEntry{files: files, at: now}
	d.mu.Unlock()
	if d.store != nil {
		if raw, err := json.Marshal(files); err == nil {
			d.store.PutItemMetadata(item, raw, now)
		}
	}
	return files, nil
}

func (d *Driver) fetchListing(ctx context.Context, item string) ([]iaFile, error) {
	u := d.baseURL + "/metadata/" + url.PathEscape(item)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RomArr/archiveorg")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("archiveorg metadata %s: %w", item, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archiveorg metadata %s: HTTP %d", item, resp.StatusCode)
	}
	var md iaMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadataSize)).Decode(&md); err != nil {
		return nil, fmt.Errorf("archiveorg metadata %s: decode: %w", item, err)
	}
	return md.Files, nil
}

// downloadURL builds the per-file endpoint. name is passed verbatim (it may
// already lead with "<item>/"; archive.org's rule is /download/<item>/<name>).
func (d *Driver) downloadURL(item, name string) string {
	return d.baseURL + "/download/" + pathEscape(item) + "/" + pathEscape(name)
}

// pathEscape percent-encodes each path segment while preserving separators.
func pathEscape(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

// Fetch implements driver.SearchSource. It streams r.DownloadURL into destDir,
// resuming from any existing .part file via HTTP Range, then renames into place.
// The final size is validated against r.Size when known.
func (d *Driver) Fetch(ctx context.Context, r driver.Release, destDir string) (string, error) {
	if r.DownloadURL == "" {
		return "", fmt.Errorf("archiveorg fetch: release has no download URL")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	name := r.Title
	if name == "" {
		name = filepath.Base(r.DownloadURL)
	}
	final := filepath.Join(destDir, filepath.Base(name))
	part := final + ".part"

	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}

	client := d.http
	if client.Timeout != 0 && client.Timeout < fetchTimeout {
		// Use a longer per-fetch deadline than the metadata client without
		// mutating the shared client.
		c := *client
		c.Timeout = fetchTimeout
		client = &c
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "RomArr/archiveorg")
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var flags int
	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range (or none sent): (re)write from scratch.
		flags = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
		have = 0
	case http.StatusPartialContent:
		flags = os.O_CREATE | os.O_APPEND | os.O_WRONLY
	case http.StatusRequestedRangeNotSatisfiable:
		// Already have the whole thing; finalise.
		resp.Body.Close()
		return final, os.Rename(part, final)
	default:
		return "", fmt.Errorf("archiveorg fetch %s: HTTP %d", name, resp.StatusCode)
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr // .part is kept for a later resume
	}
	if closeErr != nil {
		return "", closeErr
	}

	if r.Size > 0 {
		if got := have + written; got != r.Size {
			return "", fmt.Errorf("archiveorg fetch %s: size mismatch got %d want %d", name, got, r.Size)
		}
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return final, nil
}

// tokenize lowercases and splits into alnum words. Single-character words
// are kept only when numeric: the "2" in "Spyro 2" is identity and dropping
// it made the query match every Spyro game (the #281 wrong-grab); the
// possessive "s" in "Ripto's" stays noise.
func tokenize(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordSplitRe.Split(strings.ToLower(s), -1) {
		if len(w) > 1 || (len(w) == 1 && w[0] >= '0' && w[0] <= '9') {
			out[w] = true
		}
	}
	return out
}

// overlaps reports whether the filename covers the query: every query word must
// appear in the filename (all-terms match). This keeps "final fantasy vii" from
// matching "final fantasy viii" only by the shared "final"/"fantasy".
func overlaps(query, name map[string]bool) bool {
	if len(query) == 0 {
		return false
	}
	for w := range query {
		if !name[w] {
			return false
		}
	}
	return true
}
