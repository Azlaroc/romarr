package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// igdbStub stands in for Twitch's token endpoint and IGDB's API, counting
// token issuance because how OFTEN we authenticate is a property worth
// pinning: a token is good for two months, so one per search would be a bug.
type igdbStub struct {
	srv        *httptest.Server
	tokenCalls atomic.Int64
	unauthOnce atomic.Bool
	lastBody   atomic.Value // string
}

const igdbGamesJSON = `[
  {"id": 1074, "name": "Chrono Trigger", "slug": "chrono-trigger",
   "summary": "A time-travelling RPG.", "first_release_date": 794448000,
   "cover": {"url": "//images.igdb.com/igdb/image/upload/t_thumb/co2h5j.jpg"},
   "platforms": [{"id": 19, "name": "Super Nintendo Entertainment System", "slug": "snes"},
                 {"id": 7, "name": "PlayStation", "slug": "ps"},
                 {"id": 99, "name": "Some Platform We Have No Lane For", "slug": "weird"}]}
]`

func newIGDBStub(t *testing.T) *igdbStub {
	t.Helper()
	s := &igdbStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-` + itoa(s.tokenCalls.Load()) + `","expires_in":5000000,"token_type":"bearer"}`))
	})
	mux.HandleFunc("/games", func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		s.lastBody.Store(body)
		if s.unauthOnce.CompareAndSwap(true, false) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Client-ID") == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(igdbGamesJSON))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func readAll(r *http.Request) (string, error) {
	defer r.Body.Close()
	b := make([]byte, 4096)
	n, _ := r.Body.Read(b)
	return string(b[:n]), nil
}

func itoa(n int64) string {
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

func newStubProvider(t *testing.T, s *igdbStub) *IGDB {
	t.Helper()
	return NewIGDB("client", "secret", WithIGDBBase(s.srv.URL, s.srv.URL))
}

func TestIGDBSearch(t *testing.T) {
	stub := newIGDBStub(t)
	p := newStubProvider(t, stub)

	games, err := p.Search(context.Background(), "chrono trigger", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("games = %d, want 1", len(games))
	}
	g := games[0]
	if g.Name != "Chrono Trigger" || g.ProviderID != 1074 {
		t.Errorf("game = %+v", g)
	}
	if g.ReleaseYear != 1995 {
		t.Errorf("release year = %d, want 1995", g.ReleaseYear)
	}
	// A thumbnail is a 90px square; the grid needs the big cover, over https.
	if g.CoverURL != "https://images.igdb.com/igdb/image/upload/t_cover_big/co2h5j.jpg" {
		t.Errorf("cover = %q", g.CoverURL)
	}
	// Platform identities resolve through the registry — by id and by slug.
	if len(g.Platforms) != 2 || g.Platforms[0] != "snes" || g.Platforms[1] != "psx" {
		t.Errorf("platforms = %v, want our slugs", g.Platforms)
	}
	// A platform we have no lane for is surfaced, not silently dropped:
	// that is how a missing registry row becomes visible.
	if len(g.UnmappedPlatforms) != 1 {
		t.Errorf("unmapped = %v, want the one platform with no row", g.UnmappedPlatforms)
	}
	if body, _ := stub.lastBody.Load().(string); !strings.Contains(body, `search "chrono trigger";`) {
		t.Errorf("request body = %q, want an APIcalypse search", body)
	}
}

func TestIGDBTokenIsCachedAcrossSearches(t *testing.T) {
	stub := newIGDBStub(t)
	p := newStubProvider(t, stub)
	for i := 0; i < 3; i++ {
		if _, err := p.Search(context.Background(), "chrono", 5); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	if n := stub.tokenCalls.Load(); n != 1 {
		t.Errorf("token calls = %d, want 1 — a token is good for two months", n)
	}
}

// A rejected token must not stick: if it did, a rotated secret would leave
// the provider failing every search until a restart.
func TestIGDBUnauthorizedDropsTheCachedToken(t *testing.T) {
	stub := newIGDBStub(t)
	p := newStubProvider(t, stub)

	if _, err := p.Search(context.Background(), "chrono", 5); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	stub.unauthOnce.Store(true)
	if _, err := p.Search(context.Background(), "chrono", 5); err == nil {
		t.Fatal("a 401 must surface as an error")
	}
	if _, err := p.Search(context.Background(), "chrono", 5); err != nil {
		t.Fatalf("the next search must re-authenticate: %v", err)
	}
	if n := stub.tokenCalls.Load(); n != 2 {
		t.Errorf("token calls = %d, want 2 (one warm-up, one after the 401)", n)
	}
}

func TestIGDBUnconfigured(t *testing.T) {
	p := NewIGDB("", "")
	if p.Configured() {
		t.Fatal("no credentials must report unconfigured")
	}
	if _, err := p.Search(context.Background(), "anything", 5); err == nil {
		t.Error("an unconfigured provider must not pretend to search")
	}
}

func TestIGDBEmptyQueryDoesNotCallOut(t *testing.T) {
	stub := newIGDBStub(t)
	p := newStubProvider(t, stub)
	games, err := p.Search(context.Background(), "   ", 5)
	if err != nil || games != nil {
		t.Fatalf("games = %v, err = %v — an empty query is not a request", games, err)
	}
	if stub.tokenCalls.Load() != 0 {
		t.Error("an empty query must not even authenticate")
	}
}
