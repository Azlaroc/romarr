package archiveorg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/sources/driver"
)

const psxItem = "2024-sony-playstation-usa-hearto-1g1r-collection"

// metadataServer serves the trimmed real metadata fixture at /metadata/<item>.
func metadataServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", "metadata_psx.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	})
	return httptest.NewServer(mux)
}

func newDriver(t *testing.T, base string) *Driver {
	return New(map[string]string{"psx": psxItem}, WithBaseURL(base))
}

func TestSearch_BuriedTitle(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	rel, err := d.Search(context.Background(), driver.Query{Text: "castlevania symphony of the night", PlatformSlug: "psx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rel) != 1 {
		t.Fatalf("want 1 buried result, got %d: %+v", len(rel), rel)
	}
	got := rel[0]
	if got.Title != "Castlevania - Symphony of the Night (USA).zip" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Size != 392826195 {
		t.Errorf("size = %d, want 392826195", got.Size)
	}
	if got.MD5 != "6452e9da539aed682573794c8a794c83" {
		t.Errorf("md5 = %q", got.MD5)
	}
	if got.SourceType != "ddl" || got.Source != "archiveorg" || got.PlatformSlug != "psx" {
		t.Errorf("meta = %+v", got)
	}
	// Download URL: doubled item id, %20 spaces, %28/%29 parens (url.PathEscape
	// form — verified live to serve HTTP 206).
	wantURL := srv.URL + "/download/" + psxItem + "/" + psxItem +
		"/Sony%20-%20Playstation%20-%20USA/Castlevania%20-%20Symphony%20of%20the%20Night%20%28USA%29.zip"
	if got.DownloadURL != wantURL {
		t.Errorf("download url:\n got  %s\n want %s", got.DownloadURL, wantURL)
	}
	if got.GUID != got.DownloadURL {
		t.Errorf("guid should equal download url")
	}
}

func TestSearch_MultiDiscAllTermsMatch(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	rel, err := d.Search(context.Background(), driver.Query{Text: "final fantasy vii", PlatformSlug: "psx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// All three FF7 discs match; nothing else. (An FFVIII decoy would fail the
	// all-terms rule because "vii" != "viii".)
	if len(rel) != 3 {
		t.Fatalf("want 3 disc results, got %d: %+v", len(rel), titles(rel))
	}
	for _, r := range rel {
		if !strings.Contains(r.Title, "Final Fantasy VII (USA)") {
			t.Errorf("unexpected match: %s", r.Title)
		}
		if r.MD5 == "" || r.Size == 0 {
			t.Errorf("disc release missing hash/size: %+v", r)
		}
	}
}

func TestSearch_RegionFilter(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	// No stated region interest: the only Gran Turismo in the fixture is a
	// (Japan) dump with no English tag, so the coarse pre-filter drops it.
	rel, err := d.Search(context.Background(), driver.Query{Text: "gran turismo", PlatformSlug: "psx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rel) != 0 {
		t.Fatalf("expected Japan-only title filtered, got %v", titles(rel))
	}
}

// TestSearch_RegionFilterYieldsToTheProfile is the fix for the driver-level
// hard drop: a profile that ranks Japan could not see a Japanese dump at all,
// because the filter ran where the selector could never observe it. Under
// collection mode that is worse than a missed grab — it becomes a gap that
// can never fill, and it reads as a short catalog rather than as a filter.
func TestSearch_RegionFilterYieldsToTheProfile(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	rel, err := d.Search(context.Background(), driver.Query{
		Text: "gran turismo", PlatformSlug: "psx",
		Regions: []string{"usa", "world", "japan"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rel) == 0 {
		t.Fatal("a profile that ranks japan must be able to see the Japanese dump")
	}
	for _, r := range rel {
		if !strings.Contains(r.Title, "Japan") {
			t.Errorf("unexpected extra release %q", r.Title)
		}
	}

	// English-only interest still drops it: the filter yields to a stated
	// interest, it does not disappear.
	rel, err = d.Search(context.Background(), driver.Query{
		Text: "gran turismo", PlatformSlug: "psx",
		Regions: []string{"usa", "europe"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rel) != 0 {
		t.Errorf("english-only profile should not see %v", titles(rel))
	}
}

func TestSearch_NonRomFilesIgnored(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	// "psx" would tokenize-match the psx.dat basename, but .dat is not a rom
	// extension so it must never surface.
	rel, err := d.Search(context.Background(), driver.Query{Text: "psx", PlatformSlug: "psx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range rel {
		if strings.HasSuffix(strings.ToLower(r.Title), ".dat") ||
			strings.HasSuffix(strings.ToLower(r.Title), ".sqlite") {
			t.Fatalf("non-rom file leaked: %s", r.Title)
		}
	}
}

func TestSearch_UnmappedSlug(t *testing.T) {
	srv := metadataServer(t)
	defer srv.Close()
	d := newDriver(t, srv.URL)

	rel, err := d.Search(context.Background(), driver.Query{Text: "mario", PlatformSlug: "n64"})
	if err != nil {
		t.Fatalf("unmapped slug: %v", err)
	}
	if rel != nil {
		t.Errorf("unmapped slug: want nil, got %v", titles(rel))
	}
}

func TestSearch_EmptySlugFansAcrossItems(t *testing.T) {
	// "All platforms" (#291): an empty slug searches every mapped item and
	// tags each release with its item's platform slug — it used to return
	// nil, silently hiding every IA result behind the default UI filter.
	metaFor := func(names ...string) string {
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = `{"name":"` + n + `","source":"original","format":"ZIP","size":"1000","md5":"aa","sha1":"bb"}`
		}
		return `{"server":"s","dir":"/d","files":[` + strings.Join(parts, ",") + `]}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/item-gb", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(metaFor("Wario Land (World).zip")))
	})
	mux.HandleFunc("/metadata/item-psx", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(metaFor("Wario Kart Fake (USA).zip", "Unrelated Game (USA).zip")))
	})
	mux.HandleFunc("/metadata/item-broken", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := New(map[string]string{
		"gb":     "item-gb",
		"psx":    "item-psx",
		"broken": "item-broken",
	}, WithBaseURL(srv.URL))

	rel, err := d.Search(context.Background(), driver.Query{Text: "wario"})
	if err != nil {
		t.Fatalf("fan search: %v", err)
	}
	if len(rel) != 2 {
		t.Fatalf("want 2 fanned results, got %d: %v", len(rel), titles(rel))
	}
	// Deterministic slug order (gb < psx), per-item slug tagging.
	if rel[0].PlatformSlug != "gb" || rel[1].PlatformSlug != "psx" {
		t.Errorf("slugs = %q,%q, want gb,psx", rel[0].PlatformSlug, rel[1].PlatformSlug)
	}
	if !strings.Contains(rel[0].DownloadURL, "item-gb") || !strings.Contains(rel[1].DownloadURL, "item-psx") {
		t.Errorf("download URLs not per-item: %q / %q", rel[0].DownloadURL, rel[1].DownloadURL)
	}

	// All items failing surfaces the error (circuit breaker food).
	dBroken := New(map[string]string{"x": "item-broken"}, WithBaseURL(srv.URL))
	if _, err := dBroken.Search(context.Background(), driver.Query{Text: "wario"}); err == nil {
		t.Error("all-items-broken fan should return the error")
	}
}

func TestSearch_EmptySlugHonorsLimit(t *testing.T) {
	metaMany := `{"server":"s","dir":"/d","files":[
		{"name":"Wario A (USA).zip","source":"original","format":"ZIP","size":"1","md5":"aa","sha1":"bb"},
		{"name":"Wario B (USA).zip","source":"original","format":"ZIP","size":"1","md5":"aa","sha1":"bb"},
		{"name":"Wario C (USA).zip","source":"original","format":"ZIP","size":"1","md5":"aa","sha1":"bb"}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(metaMany))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := New(map[string]string{"a": "i1", "b": "i2"}, WithBaseURL(srv.URL))
	rel, err := d.Search(context.Background(), driver.Query{Text: "wario", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) != 4 {
		t.Errorf("fan results = %d, want overall limit 4 (3 from first item, 1 from second)", len(rel))
	}
}

func TestSearch_MetadataError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := newDriver(t, srv.URL)

	_, err := d.Search(context.Background(), driver.Query{Text: "mario", PlatformSlug: "psx"})
	if err == nil {
		t.Fatal("expected error on HTTP 500 metadata")
	}
}

// fileServer serves fixed content with Range support (via http.ServeContent) so
// the resume path is exercised.
func fileServer(t *testing.T, name string, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(content)))
	}))
}

func TestFetch_Fresh(t *testing.T) {
	content := []byte("PK\x03\x04 pretend this is a rom zip payload")
	srv := fileServer(t, "game.zip", content)
	defer srv.Close()
	d := New(nil, WithBaseURL(srv.URL))
	dest := t.TempDir()

	r := driver.Release{Title: "game.zip", Size: int64(len(content)), DownloadURL: srv.URL + "/game.zip"}
	path, err := d.Fetch(context.Background(), r, dest)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if path != filepath.Join(dest, "game.zip") {
		t.Errorf("path = %s", path)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part should be gone after rename")
	}
}

func TestFetch_ResumesFromPartial(t *testing.T) {
	content := []byte("0123456789ABCDEFGHIJ0123456789ABCDEFGHIJ")
	srv := fileServer(t, "game.zip", content)
	defer srv.Close()
	d := New(nil, WithBaseURL(srv.URL))
	dest := t.TempDir()

	// Pre-seed a partial download of the first 12 bytes.
	part := filepath.Join(dest, "game.zip.part")
	if err := os.WriteFile(part, content[:12], 0o644); err != nil {
		t.Fatal(err)
	}

	r := driver.Release{Title: "game.zip", Size: int64(len(content)), DownloadURL: srv.URL + "/game.zip"}
	path, err := d.Fetch(context.Background(), r, dest)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(content) {
		t.Fatalf("resumed content mismatch:\n got  %q\n want %q", got, content)
	}
}

func TestFetch_SizeMismatch(t *testing.T) {
	content := []byte("short")
	srv := fileServer(t, "game.zip", content)
	defer srv.Close()
	d := New(nil, WithBaseURL(srv.URL))
	dest := t.TempDir()

	r := driver.Release{Title: "game.zip", Size: 9999, DownloadURL: srv.URL + "/game.zip"}
	if _, err := d.Fetch(context.Background(), r, dest); err == nil {
		t.Fatal("expected size-mismatch error")
	}
}

func titles(rs []driver.Release) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Title
	}
	return out
}

// The #281 wrong-grab: tokenize dropped the "2" from "Spyro 2", degenerating
// the query to {spyro} and matching every Spyro game. Numeric single-char
// tokens are identity and must survive; 1-char letter fragments stay noise.
func TestTokenizeKeepsNumericSingles(t *testing.T) {
	q := tokenize("Spyro 2")
	if !q["spyro"] || !q["2"] || len(q) != 2 {
		t.Fatalf("query tokens = %v, want {spyro 2}", q)
	}
	if s := tokenize("Ripto's Rage!"); s["s"] || !s["ripto"] || !s["rage"] {
		t.Fatalf("tokens = %v, want ripto+rage without the possessive s", s)
	}
	right := tokenize("Spyro 2 - Ripto's Rage! (USA)")
	wrong := tokenize("Spyro - Year of the Dragon (USA) (Rev 1)")
	if !overlaps(q, right) {
		t.Fatalf("query %v should match %v", q, right)
	}
	if overlaps(q, wrong) {
		t.Fatalf("query %v must not match %v", q, wrong)
	}
}
