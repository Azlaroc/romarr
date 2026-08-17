// Package datsvc turns DAT authorities into stored catalogs: it fetches (or
// accepts a hand upload of) a platform's DAT, parses it, and writes a
// snapshot.
//
// It exists because of a layering constraint rather than a taste for
// indirection: internal/db can never import internal/dat (the parser reuses
// internal/selection for release-name attributes, and selection imports db),
// so the dat.Game -> db.DatGameRow mapping has to live in a third package
// that may import both.
package datsvc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Fetch driver names. They are stored in dat_authorities.fetch_driver, so
// they are data: adding TOSEC is a row plus a driver, not a schema change.
const (
	DriverLibretro = "libretro"
	DriverRedump   = "redump"
	DriverUpload   = "upload"
)

// maxDatSize caps one transport payload. The largest catalog we fetch today
// (Redump PSX) is ~10MB compressed; the cap is loose enough to absorb years
// of growth and tight enough that a redirect into something else fails fast
// instead of filling memory. A var, not a const, so the guard is testable
// without a 128MB fixture.
var maxDatSize int64 = 128 << 20

const userAgent = "RomArr/dat"

// MaxUploadBytes is the ceiling on a hand-uploaded body. The API needs it
// because requestSizeLimitMiddleware exempts multipart entirely, so the
// upload handler is the only size gate on that path.
func MaxUploadBytes() int64 { return maxDatSize }

// ErrUploadOnly is returned by the upload driver's Fetch. An authority on
// this driver has no automated source by design — No-Intro's own Dat-o-Matic
// bans scrapers by IP, and MAME ships dormant — so its catalogs arrive
// through the upload endpoint.
var ErrUploadOnly = errors.New("authority has no automated fetch; upload its DAT instead")

// Payload is one transport response: the bytes exactly as the source served
// them, plus the filename it suggested. The service hashes these bytes for
// provenance before any unwrapping, so source_sha256 identifies what the
// source actually sent.
type Payload struct {
	Filename string
	Body     []byte
}

// Fetcher pulls one platform's DAT from an authority's source. Drivers do no
// parsing: unwrapping containers, hashing, parsing and mapping all happen in
// the service, so every driver stays a URL shape plus an HTTP call.
type Fetcher interface {
	Name() string
	Fetch(ctx context.Context, base, datCode string) (Payload, error)
}

// NewFetcher returns the driver an authority's fetch_driver names.
func NewFetcher(driver string, client *http.Client) (Fetcher, error) {
	if client == nil {
		client = http.DefaultClient
	}
	switch driver {
	case DriverLibretro:
		return &libretroFetcher{http: client}, nil
	case DriverRedump:
		return &redumpFetcher{http: client}, nil
	case DriverUpload:
		return uploadFetcher{}, nil
	default:
		return nil, fmt.Errorf("unknown fetch driver %q", driver)
	}
}

// libretroFetcher reads the libretro-database mirror of the No-Intro set.
// Measured 2026-08-16: its no-intro files are complete (NES 14,132 games /
// 14,132 roms, 100% carrying md5 and size), unlike its mirrored *redump*
// files, which keep only the first track per game.
//
// dat_code is the mirror's DAT name without extension ("Atari - 2600").
type libretroFetcher struct{ http *http.Client }

func (f *libretroFetcher) Name() string { return DriverLibretro }

func (f *libretroFetcher) Fetch(ctx context.Context, base, datCode string) (Payload, error) {
	if err := ValidateDriverCode(DriverLibretro, datCode); err != nil {
		return Payload{}, err
	}
	return httpGet(ctx, f.http, joinBase(base, url.PathEscape(datCode)+".dat"))
}

// redumpFetcher reads redump.info directly, which is the only complete
// source of multi-track disc entries.
//
// dat_code is the Redump system code ("psx", "ss", "gc"); the response is a
// zip carrying one logiqx XML.
type redumpFetcher struct{ http *http.Client }

func (f *redumpFetcher) Name() string { return DriverRedump }

func (f *redumpFetcher) Fetch(ctx context.Context, base, datCode string) (Payload, error) {
	if err := ValidateDriverCode(DriverRedump, datCode); err != nil {
		return Payload{}, err
	}
	return httpGet(ctx, f.http, joinBase(base, url.PathEscape(datCode))+"/")
}

// uploadFetcher marks an authority as hand-fed. It is a first-class driver,
// not a fallback: uploading works for every authority, and this driver simply
// says no automated source exists for this one.
type uploadFetcher struct{}

func (uploadFetcher) Name() string { return DriverUpload }

func (uploadFetcher) Fetch(context.Context, string, string) (Payload, error) {
	return Payload{}, ErrUploadOnly
}

// joinBase appends a path segment to a configured base, tolerating a base
// with or without its trailing slash (operators edit these by hand).
func joinBase(base, segment string) string {
	return strings.TrimRight(base, "/") + "/" + segment
}

func httpGet(ctx context.Context, client *http.Client, rawURL string) (Payload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Payload{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Payload{}, fmt.Errorf("dat fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Payload{}, fmt.Errorf("dat fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	// One byte past the cap distinguishes "exactly at the limit" from
	// "truncated", so an oversized response is an error rather than a
	// silently half-read catalog.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDatSize+1))
	if err != nil {
		return Payload{}, fmt.Errorf("dat fetch %s: %w", rawURL, err)
	}
	if int64(len(body)) > maxDatSize {
		return Payload{}, fmt.Errorf("dat fetch %s: payload exceeds %d bytes", rawURL, maxDatSize)
	}
	return Payload{Filename: responseFilename(resp, rawURL), Body: body}, nil
}

// responseFilename prefers the name the source declares (Redump's
// Content-Disposition carries the version and game count) and falls back to
// the URL's last segment.
func responseFilename(resp *http.Response, rawURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := strings.TrimSpace(params["filename"]); name != "" {
				return path.Base(name)
			}
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		if name := path.Base(strings.TrimRight(u.Path, "/")); name != "" && name != "." && name != "/" {
			return name
		}
	}
	return ""
}

// zipEntry is one DAT member of a zip container.
type zipEntry struct {
	Name string
	Body []byte
}

// isZip reports whether raw is a zip container. Sniffing the magic bytes
// beats trusting a filename: uploads arrive named anything, and Redump's
// URL has no extension at all.
func isZip(raw []byte) bool { return bytes.HasPrefix(raw, []byte("PK\x03\x04")) }

// zipDats extracts every DAT-looking member of a zip, in archive order.
//
// A member stored with any compression method other than Store or Deflate is
// a hard error. Some mirrors have started emitting zstd-compressed zips,
// which unzip and 7-Zip fail on *silently* — a truncated catalog would poison
// both the coverage counts and the selector's size bands, so this fails loud.
func zipDats(raw []byte) ([]zipEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("dat zip: %w", err)
	}
	var out []zipEntry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !looksLikeDat(f.Name) {
			continue
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("dat zip: member %q uses unsupported compression method %d", f.Name, f.Method)
		}
		if f.UncompressedSize64 > uint64(maxDatSize) {
			return nil, fmt.Errorf("dat zip: member %q exceeds %d bytes", f.Name, maxDatSize)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("dat zip: open %q: %w", f.Name, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxDatSize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("dat zip: read %q: %w", f.Name, err)
		}
		if int64(len(body)) > maxDatSize {
			return nil, fmt.Errorf("dat zip: member %q exceeds %d bytes", f.Name, maxDatSize)
		}
		out = append(out, zipEntry{Name: path.Base(f.Name), Body: body})
	}
	if len(out) == 0 {
		return nil, errors.New("dat zip: no .dat or .xml member found")
	}
	return out, nil
}

// looksLikeDat filters a pack's members down to catalogs: No-Intro's daily
// zip also carries readmes and licence files.
func looksLikeDat(name string) bool {
	lower := strings.ToLower(path.Base(name))
	return strings.HasSuffix(lower, ".dat") || strings.HasSuffix(lower, ".xml")
}

// unwrap returns the single catalog inside a payload: the bytes verbatim when
// the source served a bare DAT, or the zip's only DAT member. A container
// holding several catalogs is ambiguous for a single-platform import, so it
// is rejected here and handled by the pack fan-out instead.
func unwrap(raw []byte) ([]byte, error) {
	if !isZip(raw) {
		return raw, nil
	}
	entries, err := zipDats(raw)
	if err != nil {
		return nil, err
	}
	if len(entries) > 1 {
		return nil, fmt.Errorf("dat zip: %d catalogs in one archive; upload it without a platform to fan out", len(entries))
	}
	return entries[0].Body, nil
}
