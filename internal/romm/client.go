// Package romm is a read-only client for the RomM REST API
// (https://github.com/rommapp/romm). RomM is the system of record for ROM
// ownership: it scans the same library tree Gamarr organizes into and keeps
// every file matched against IGDB. The client exposes just enough surface to
// mirror that catalog — platforms and paginated ROM listings — plus a
// connection test.
//
// RomM serializes expensive queries internally, so issuing them concurrently
// is slower than sequential access. All methods are synchronous and callers
// are expected to run at most one catalog pull at a time.
package romm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout  = 60 * time.Second
	defaultPageSize = 1000
	defaultRetries  = 3
	// maxResponseSize caps a single page. RomM inlines full metadata blobs
	// (~5KB per ROM), so a 1000-item page runs several megabytes.
	maxResponseSize = 64 << 20
)

// defaultBackoff holds the waits between retry attempts.
var defaultBackoff = []time.Duration{2 * time.Second, 8 * time.Second}

// HTTPError is a non-2xx response from RomM.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("RomM %s: HTTP %d", e.Path, e.Status)
}

// Platform is the subset of RomM's PlatformSchema the sync consumes.
type Platform struct {
	ID       int    `json:"id"`
	Slug     string `json:"slug"`
	FSSlug   string `json:"fs_slug"`
	Name     string `json:"name"`
	RomCount int    `json:"rom_count"`
}

// Rom is the subset of RomM's SimpleRomSchema the sync consumes.
type Rom struct {
	ID             int64  `json:"id"`
	PlatformID     int    `json:"platform_id"`
	PlatformSlug   string `json:"platform_slug"`
	PlatformFSSlug string `json:"platform_fs_slug"`
	FSName         string `json:"fs_name"`
	FSNameNoTags   string `json:"fs_name_no_tags"`
	FSNameNoExt    string `json:"fs_name_no_ext"`
	FSPath         string `json:"fs_path"`
	FSSizeBytes    int64  `json:"fs_size_bytes"`
	Name           string `json:"name"`
	CRCHash        string `json:"crc_hash"`
	MD5Hash        string `json:"md5_hash"`
	SHA1Hash       string `json:"sha1_hash"`
	IGDBID         *int64 `json:"igdb_id"`
	MissingFromFS  bool   `json:"missing_from_fs"`
}

type romsPage struct {
	Items  []Rom `json:"items"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// Client talks to one RomM instance over HTTP Basic auth.
type Client struct {
	baseURL  string
	user     string
	pass     string
	http     *http.Client
	pageSize int
	retries  int
	backoff  []time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects a custom client (timeouts, transport).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithPageSize overrides the /api/roms page size (RomM caps it at 10000).
func WithPageSize(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.pageSize = n
		}
	}
}

// WithRetries overrides the total attempt count per request.
func WithRetries(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.retries = n
		}
	}
}

// WithBackoff overrides the waits between retry attempts (used by tests).
func WithBackoff(waits ...time.Duration) Option {
	return func(c *Client) { c.backoff = waits }
}

// New builds a RomM client. baseURL is the server root (e.g. http://romm:8080);
// user/pass are a RomM account with read access to the library.
func New(baseURL, user, pass string, opts ...Option) *Client {
	c := &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		pass:     pass,
		http:     &http.Client{Timeout: defaultTimeout},
		pageSize: defaultPageSize,
		retries:  defaultRetries,
		backoff:  defaultBackoff,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ListPlatforms returns every platform RomM knows about.
func (c *Client) ListPlatforms(ctx context.Context) ([]Platform, error) {
	body, err := c.get(ctx, "/api/platforms")
	if err != nil {
		return nil, err
	}
	var platforms []Platform
	if err := json.Unmarshal(body, &platforms); err != nil {
		return nil, fmt.Errorf("decode platforms: %w", err)
	}
	return platforms, nil
}

// ListRoms returns all ROMs on one platform, transparently walking RomM's
// limit/offset pagination. A zero updatedAfter fetches everything; otherwise
// only ROMs RomM touched after that instant are returned.
func (c *Client) ListRoms(ctx context.Context, platformID int, updatedAfter time.Time) ([]Rom, error) {
	var roms []Rom
	offset := 0
	for {
		q := url.Values{}
		q.Set("platform_ids", strconv.Itoa(platformID))
		q.Set("limit", strconv.Itoa(c.pageSize))
		q.Set("offset", strconv.Itoa(offset))
		q.Set("order_by", "id")
		q.Set("order_dir", "asc")
		// These index blobs default to true and bloat every page; the sync
		// never reads them.
		q.Set("with_char_index", "false")
		q.Set("with_filter_values", "false")
		q.Set("with_rom_id_index", "false")
		if !updatedAfter.IsZero() {
			q.Set("updated_after", updatedAfter.UTC().Format(time.RFC3339))
		}

		body, err := c.get(ctx, "/api/roms?"+q.Encode())
		if err != nil {
			return nil, err
		}
		var page romsPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode roms page: %w", err)
		}
		roms = append(roms, page.Items...)
		if len(page.Items) == 0 {
			break
		}
		offset += len(page.Items)
		if offset >= page.Total {
			break
		}
	}
	return roms, nil
}

// TestConnection verifies RomM is reachable and the credentials resolve, and
// returns the number of platforms it reports. The heartbeat endpoint is
// unauthenticated, so this deliberately uses an authenticated read instead.
func (c *Client) TestConnection(ctx context.Context) (int, error) {
	platforms, err := c.ListPlatforms(ctx)
	if err != nil {
		return 0, err
	}
	return len(platforms), nil
}

// Heartbeat pings RomM's unauthenticated health endpoint.
func (c *Client) Heartbeat(ctx context.Context) error {
	_, err := c.get(ctx, "/api/heartbeat")
	return err
}

// get performs one GET with auth, bounded retries and a response-size cap.
// Retries cover network errors and 5xx responses; 4xx returns immediately
// (retrying bad credentials just re-fails and can trip account lockouts).
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < c.retries; attempt++ {
		if attempt > 0 {
			wait := c.backoff[min(attempt-1, len(c.backoff)-1)]
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		body, retryable, err := c.getOnce(ctx, path)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) getOnce(ctx context.Context, path string) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		httpErr := &HTTPError{Status: resp.StatusCode, Path: strippedPath(path)}
		return nil, resp.StatusCode >= 500, httpErr
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, true, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseSize {
		return nil, false, fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	return body, false, nil
}

// strippedPath drops the query string so HTTPError stays terse in logs.
func strippedPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
