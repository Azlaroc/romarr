package datsvc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// zipWith builds a zip in memory. method is the stored compression method:
// zip.Deflate for the real thing, or an unregistered method to prove the
// reader refuses what it cannot decode.
func zipWith(t *testing.T, method uint16, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if method != zip.Store && method != zip.Deflate {
		zw.RegisterCompressor(method, func(w io.Writer) (io.WriteCloser, error) {
			return nopCloser{w}, nil
		})
	}
	for name, body := range members {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatalf("zip header %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func TestLibretroFetchBuildsMirrorURL(t *testing.T) {
	body := fixture(t, "nointro_sample.dat")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write(body)
	}))
	defer srv.Close()

	f, err := NewFetcher(DriverLibretro, srv.Client())
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	// The seeded code is a DAT name with spaces; the driver appends the
	// extension the stored code deliberately omits.
	p, err := f.Fetch(context.Background(), srv.URL+"/metadat/no-intro/", "Atari - 2600")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/metadat/no-intro/Atari - 2600.dat" {
		t.Fatalf("fetched %q, want the mirror's DAT-name path", gotPath)
	}
	if !bytes.Equal(p.Body, body) {
		t.Fatalf("payload differs from what the source served")
	}
	if p.Filename != "Atari - 2600.dat" {
		t.Fatalf("filename %q, want the URL's last segment", p.Filename)
	}
}

func TestLibretroFetchToleratesBaseWithoutTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write(fixture(t, "nointro_sample.dat"))
	}))
	defer srv.Close()

	f, _ := NewFetcher(DriverLibretro, srv.Client())
	if _, err := f.Fetch(context.Background(), srv.URL+"/mirror", "Atari - 2600"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/mirror/Atari - 2600.dat" {
		t.Fatalf("fetched %q; operators edit fetch_base by hand, so a missing slash must still join", gotPath)
	}
}

func TestRedumpFetchBuildsDatfileURL(t *testing.T) {
	inner := fixture(t, "redump_sample.dat")
	archive := zipWith(t, zip.Deflate, map[string][]byte{
		"Sony - PlayStation - Datfile (10970) (2026-08-16 18-16-58).dat": inner,
	})
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Disposition", `attachment; filename="Sony - PlayStation - Datfile (10970) (2026-08-16 18-16-58).zip"`)
		w.Write(archive)
	}))
	defer srv.Close()

	f, err := NewFetcher(DriverRedump, srv.Client())
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	p, err := f.Fetch(context.Background(), srv.URL+"/datfile/", "psx")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/datfile/psx/" {
		t.Fatalf("fetched %q, want the trailing-slash datfile path", gotPath)
	}
	// Redump's Content-Disposition carries the version and game count, which
	// is better provenance than the URL's bare system code.
	if !strings.HasPrefix(p.Filename, "Sony - PlayStation - Datfile (10970)") {
		t.Fatalf("filename %q, want the declared Content-Disposition name", p.Filename)
	}
	got, err := unwrap(p.Body)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, inner) {
		t.Fatalf("unwrapped bytes differ from the zipped catalog")
	}
}

func TestFetchNon200IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f, _ := NewFetcher(DriverRedump, srv.Client())
	_, err := f.Fetch(context.Background(), srv.URL+"/datfile/", "psx")
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err = %v, want an HTTP 503 failure", err)
	}
}

func TestFetchRejectsOversizedPayload(t *testing.T) {
	orig := maxDatSize
	maxDatSize = 64
	defer func() { maxDatSize = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 4096))
	}))
	defer srv.Close()

	f, _ := NewFetcher(DriverLibretro, srv.Client())
	_, err := f.Fetch(context.Background(), srv.URL+"/", "Atari - 2600")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want the size guard to reject rather than truncate", err)
	}
}

func TestUploadDriverRefusesFetch(t *testing.T) {
	f, err := NewFetcher(DriverUpload, nil)
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "", ""); !errors.Is(err, ErrUploadOnly) {
		t.Fatalf("err = %v, want ErrUploadOnly", err)
	}
}

func TestNewFetcherRejectsUnknownDriver(t *testing.T) {
	if _, err := NewFetcher("datomatic", nil); err == nil {
		t.Fatal("unknown driver must not resolve to a fetcher")
	}
}

func TestUnwrapPassesBareDatThrough(t *testing.T) {
	raw := fixture(t, "nointro_sample.dat")
	got, err := unwrap(raw)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("a bare DAT must survive unwrap byte-for-byte")
	}
}

func TestUnwrapRejectsMultiCatalogZip(t *testing.T) {
	archive := zipWith(t, zip.Deflate, map[string][]byte{
		"Atari - 2600 (20260801-063015).dat": fixture(t, "nointro_sample.dat"),
		"Atari - 7800 (20260801-063015).dat": fixture(t, "nointro_sample.dat"),
	})
	if _, err := unwrap(archive); err == nil {
		t.Fatal("a pack is ambiguous for a single-platform import and must be refused")
	}
}

// The zstd-zip lesson: unzip and 7-Zip fail silently on method 93, so a
// truncated catalog would poison coverage counts and the selector's size
// bands. Anything we cannot decode must fail loudly.
func TestZipDatsRejectsUnsupportedCompression(t *testing.T) {
	const methodZstd = 93
	archive := zipWith(t, methodZstd, map[string][]byte{
		"Atari - 2600.dat": fixture(t, "nointro_sample.dat"),
	})
	_, err := zipDats(archive)
	if err == nil || !strings.Contains(err.Error(), "compression method") {
		t.Fatalf("err = %v, want a loud unsupported-compression failure", err)
	}
}

func TestZipDatsSkipsNonCatalogMembers(t *testing.T) {
	archive := zipWith(t, zip.Deflate, map[string][]byte{
		"readme.txt":       []byte("No-Intro daily pack"),
		"Atari - 2600.dat": fixture(t, "nointro_sample.dat"),
	})
	entries, err := zipDats(archive)
	if err != nil {
		t.Fatalf("zipDats: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "Atari - 2600.dat" {
		t.Fatalf("entries = %+v, want only the catalog member", entries)
	}
}

func TestZipDatsRejectsArchiveWithoutCatalogs(t *testing.T) {
	archive := zipWith(t, zip.Deflate, map[string][]byte{"readme.txt": []byte("nothing here")})
	if _, err := zipDats(archive); err == nil {
		t.Fatal("an archive with no DAT member must be an error, not an empty import")
	}
}
