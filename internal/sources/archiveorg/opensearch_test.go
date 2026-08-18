package archiveorg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gamarr/internal/sources/driver"
)

// iaStub is an archive.org stand-in with both endpoints the driver now uses:
// the item search and the per-item metadata. It counts search calls, because
// how OFTEN the driver queries is a correctness property here, not a detail.
type iaStub struct {
	srv         *httptest.Server
	searchCalls atomic.Int64
}

func newIAStub(t *testing.T, found []string, items map[string][]string) *iaStub {
	t.Helper()
	s := &iaStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		s.searchCalls.Add(1)
		docs := make([]string, 0, len(found))
		for _, id := range found {
			docs = append(docs, `{"identifier":"`+id+`"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":{"numFound":` + itoa(len(found)) + `,"docs":[` + strings.Join(docs, ",") + `]}}`))
	})
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		item := strings.TrimPrefix(r.URL.Path, "/metadata/")
		names, ok := items[item]
		if !ok {
			http.NotFound(w, r)
			return
		}
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = `{"name":"` + n + `","source":"original","format":"ZIP","size":"1000","md5":"aa","sha1":"bb"}`
		}
		w.Write([]byte(`{"server":"s","dir":"/d","files":[` + strings.Join(parts, ",") + `]}`))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The pin gate: a platform with no mapped collection used to return nothing,
// which made a registry edit the prerequisite for finding anything on a new
// platform. It is now searched openly.
func TestSearch_UnmappedPlatformSearchesOpenly(t *testing.T) {
	stub := newIAStub(t, []string{"someones-n64-uploads"}, map[string][]string{
		"someones-n64-uploads": {"Super Mario 64 (USA).z64", "readme.txt"},
	})
	d := New(map[string]string{"psx": psxItem}, WithBaseURL(stub.srv.URL), WithSearchInterval(0))

	rel, err := d.Search(context.Background(), driver.Query{
		Text: "super mario 64", PlatformSlug: "n64", PlatformName: "Nintendo 64",
	})
	if err != nil {
		t.Fatalf("open search: %v", err)
	}
	if len(rel) != 1 || !strings.Contains(rel[0].Title, "Super Mario 64") {
		t.Fatalf("releases = %v, want the open-search hit", titles(rel))
	}
	if rel[0].PlatformSlug != "n64" {
		t.Errorf("platform slug = %q, want the requested platform", rel[0].PlatformSlug)
	}
	if rel[0].MD5 == "" || rel[0].DownloadURL == "" {
		t.Errorf("open-search release lost its identity: %+v", rel[0])
	}
}

// A mapped collection is where we look first, and when it answers we stop:
// widening every search would multiply archive.org traffic across a wishlist
// cycle for worse-named copies of a curated set.
func TestSearch_PinnedHitSkipsOpenSearch(t *testing.T) {
	stub := newIAStub(t, []string{"open-extra"}, map[string][]string{
		"pinned-gb":  {"Tetris (World) (Rev 1).zip"},
		"open-extra": {"Tetris (Japan).zip"},
	})
	d := New(map[string]string{"gb": "pinned-gb"}, WithBaseURL(stub.srv.URL), WithSearchInterval(0))

	rel, err := d.Search(context.Background(), driver.Query{Text: "tetris", PlatformSlug: "gb"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := titles(rel); len(got) != 1 || !strings.Contains(got[0], "World") {
		t.Fatalf("releases = %v, want just the pinned hit", got)
	}
	if n := stub.searchCalls.Load(); n != 0 {
		t.Errorf("search calls = %d, want 0 — the preferred collection answered", n)
	}
}

// The pinned item missing the title is exactly when the open corpus earns its
// cost.
func TestSearch_PinnedMissWidens(t *testing.T) {
	stub := newIAStub(t, []string{"open-extra"}, map[string][]string{
		"pinned-gb":  {"Tetris (World) (Rev 1).zip"},
		"open-extra": {"Wario Land (World).zip"},
	})
	d := New(map[string]string{"gb": "pinned-gb"}, WithBaseURL(stub.srv.URL), WithSearchInterval(0))

	rel, err := d.Search(context.Background(), driver.Query{Text: "wario land", PlatformSlug: "gb"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := titles(rel); len(got) != 1 || !strings.Contains(got[0], "Wario") {
		t.Fatalf("releases = %v, want the open-search hit", got)
	}
	if n := stub.searchCalls.Load(); n != 1 {
		t.Errorf("search calls = %d, want 1", n)
	}
}

// The same query on every wishlist cycle must not be the same query against
// archive.org: anonymous budget is about a request a second and cycles are
// hundreds of rows. An empty answer is cached too — an unfindable title is
// exactly the one that would otherwise re-query forever.
func TestSearch_OpenSearchCachesQueries(t *testing.T) {
	t.Run("hits are cached", func(t *testing.T) {
		stub := newIAStub(t, []string{"open-extra"}, map[string][]string{
			"open-extra": {"Tetris (Japan).zip"},
		})
		d := New(nil, WithBaseURL(stub.srv.URL), WithSearchInterval(0))
		for i := 0; i < 3; i++ {
			if _, err := d.Search(context.Background(), driver.Query{Text: "tetris", PlatformSlug: "gb"}); err != nil {
				t.Fatalf("search %d: %v", i, err)
			}
		}
		if n := stub.searchCalls.Load(); n != 1 {
			t.Errorf("search calls = %d, want 1 (later searches served from cache)", n)
		}
	})

	t.Run("misses are cached", func(t *testing.T) {
		stub := newIAStub(t, nil, nil)
		d := New(nil, WithBaseURL(stub.srv.URL), WithSearchInterval(0))
		for i := 0; i < 3; i++ {
			rel, err := d.Search(context.Background(), driver.Query{Text: "nothing here at all", PlatformSlug: "gb"})
			if err != nil {
				t.Fatalf("search %d: %v", i, err)
			}
			if len(rel) != 0 {
				t.Fatalf("releases = %v, want none", titles(rel))
			}
		}
		if n := stub.searchCalls.Load(); n != 1 {
			t.Errorf("search calls = %d, want 1 (an empty answer is still an answer)", n)
		}
	})
}

// Open search is the widening step. If it fails, the caller should still get
// what the mapped collection already produced.
func TestSearch_OpenSearchFailureKeepsPinnedHits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	mux.HandleFunc("/metadata/pinned-gb", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"server":"s","dir":"/d","files":[{"name":"Tetris (World).zip","source":"original","format":"ZIP","size":"1000","md5":"aa","sha1":"bb"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := New(map[string]string{"gb": "pinned-gb"}, WithBaseURL(srv.URL), WithSearchInterval(0))
	rel, err := d.Search(context.Background(), driver.Query{Text: "tetris", PlatformSlug: "gb"})
	if err != nil {
		t.Fatalf("a failed widening must not fail the search: %v", err)
	}
	if len(rel) != 1 {
		t.Fatalf("releases = %v, want the pinned hit", titles(rel))
	}
}

func TestBuildQuery(t *testing.T) {
	q := buildQuery("Super Mario 64", "Nintendo 64")
	if !strings.Contains(q, "super") || !strings.Contains(q, "mario") {
		t.Errorf("query = %q, want the title's words", q)
	}
	if !strings.Contains(q, `"Nintendo 64"`) {
		t.Errorf("query = %q, want the platform name as a phrase", q)
	}
	if buildQuery("", "Game Boy") != "" {
		t.Error("a query with no usable words must not reach archive.org")
	}
	if got := buildQuery("Tetris", ""); !strings.Contains(got, "mediatype:software") {
		t.Errorf("query = %q, want the software corpus when no platform is named", got)
	}
}
