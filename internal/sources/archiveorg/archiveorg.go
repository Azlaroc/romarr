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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

	mu    sync.RWMutex
	cache map[string]cacheEntry // item -> parsed files
}

type cacheEntry struct {
	files []iaFile
	at    time.Time
}

// Option configures a Driver.
type Option func(*Driver)

// WithBaseURL overrides the archive.org base (used by tests / mirrors).
func WithBaseURL(base string) Option {
	return func(d *Driver) { d.baseURL = strings.TrimRight(base, "/") }
}

// WithHTTPClient injects a custom client (timeouts, transport).
func WithHTTPClient(c *http.Client) Option { return func(d *Driver) { d.http = c } }

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
var (
	nonEnglishRe = regexp.MustCompile(`\((?:Japan|Korea|China|Taiwan|France|Germany|Spain|Italy|Netherlands|Sweden|Brazil)\)`)
	englishRe    = regexp.MustCompile(`\((?:USA|World|Europe|UK|Australia|Canada)\)|\(En`)
	wordSplitRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

// Search implements driver.SearchSource. It requires a platform slug (searching
// every configured item per query is prohibitively slow — same stance as the
// Myrient driver); an unmapped or empty slug yields (nil, nil).
func (d *Driver) Search(ctx context.Context, q driver.Query) ([]driver.Release, error) {
	if q.PlatformSlug == "" {
		return nil, nil
	}
	item, ok := d.items[q.PlatformSlug]
	if !ok || item == "" {
		return nil, nil
	}
	qWords := tokenize(q.Text)
	if len(qWords) == 0 {
		return nil, nil
	}

	files, err := d.listing(ctx, item)
	if err != nil {
		return nil, err
	}

	limit := q.Limit
	if limit <= 0 || limit > d.limit {
		limit = d.limit
	}

	var out []driver.Release
	for _, f := range files {
		base := filepath.Base(f.Name)
		if !romExtensions[strings.ToLower(filepath.Ext(base))] {
			continue
		}
		if nonEnglishRe.MatchString(base) && !englishRe.MatchString(base) {
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
			PlatformSlug: q.PlatformSlug,
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

// listing fetches and caches an item's file list.
func (d *Driver) listing(ctx context.Context, item string) ([]iaFile, error) {
	d.mu.RLock()
	if e, ok := d.cache[item]; ok && time.Since(e.at) < metadataTTL {
		d.mu.RUnlock()
		return e.files, nil
	}
	d.mu.RUnlock()

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

	d.mu.Lock()
	d.cache[item] = cacheEntry{files: md.Files, at: time.Now()}
	d.mu.Unlock()
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
