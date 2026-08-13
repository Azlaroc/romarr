package romm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testUser = "romarr"
	testPass = "secret"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// checkAuth enforces Basic auth on the stub like RomM does.
func checkAuth(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	if !ok || user != testUser || pass != testPass {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func newTestClient(base string, opts ...Option) *Client {
	opts = append([]Option{WithBackoff(time.Millisecond)}, opts...)
	return New(base, testUser, testPass, opts...)
}

func TestListPlatforms(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !checkAuth(t, w, r) {
			return
		}
		if r.URL.Path != "/api/platforms" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(fixture(t, "platforms.json"))
	}))
	defer srv.Close()

	platforms, err := newTestClient(srv.URL).ListPlatforms(context.Background())
	if err != nil {
		t.Fatalf("ListPlatforms: %v", err)
	}
	if len(platforms) != 3 {
		t.Fatalf("expected 3 platforms, got %d", len(platforms))
	}
	genesis := platforms[0]
	if genesis.Slug != "genesis" || genesis.FSSlug != "genesis-slash-megadrive" {
		t.Errorf("genesis platform decoded wrong: %+v", genesis)
	}
	if genesis.RomCount != 949 || genesis.ID != 8 {
		t.Errorf("genesis counts decoded wrong: %+v", genesis)
	}
	if requests.Load() != 1 {
		t.Errorf("expected 1 request, got %d", requests.Load())
	}
}

func TestListRomsPagination(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !checkAuth(t, w, r) {
			return
		}
		if r.URL.Path != "/api/roms" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		for key, want := range map[string]string{
			"platform_ids":       "41",
			"limit":              "3",
			"order_by":           "id",
			"order_dir":          "asc",
			"with_char_index":    "false",
			"with_filter_values": "false",
			"with_rom_id_index":  "false",
		} {
			if got := q.Get(key); got != want {
				t.Errorf("query %s = %q, want %q", key, got, want)
			}
		}
		if q.Has("updated_after") {
			t.Errorf("unexpected updated_after on a full pull: %q", q.Get("updated_after"))
		}
		switch q.Get("offset") {
		case "0":
			w.Write(fixture(t, "roms_page1.json"))
		case "3":
			w.Write(fixture(t, "roms_page2.json"))
		default:
			t.Errorf("unexpected offset %q", q.Get("offset"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	roms, err := newTestClient(srv.URL, WithPageSize(3)).ListRoms(context.Background(), 41, time.Time{})
	if err != nil {
		t.Fatalf("ListRoms: %v", err)
	}
	if len(roms) != 5 {
		t.Fatalf("expected 5 roms across pages, got %d", len(roms))
	}
	if requests.Load() != 2 {
		t.Errorf("expected 2 page requests, got %d", requests.Load())
	}

	first := roms[0]
	if first.ID != 101 || first.PlatformFSSlug != "psx" || first.Name != "Castlevania: Symphony of the Night" {
		t.Errorf("first rom decoded wrong: %+v", first)
	}
	if first.IGDBID == nil || *first.IGDBID != 1078 {
		t.Errorf("first rom igdb_id decoded wrong: %+v", first.IGDBID)
	}
	if roms[1].IGDBID != nil {
		t.Errorf("null igdb_id should decode to nil, got %v", roms[1].IGDBID)
	}
	if !roms[4].MissingFromFS {
		t.Errorf("missing_from_fs not decoded on last rom: %+v", roms[4])
	}
}

func TestListRomsUpdatedAfter(t *testing.T) {
	after := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(t, w, r) {
			return
		}
		if got := r.URL.Query().Get("updated_after"); got != "2026-08-01T12:00:00Z" {
			t.Errorf("updated_after = %q", got)
		}
		w.Write([]byte(`{"items": [], "total": 0, "limit": 1000, "offset": 0}`))
	}))
	defer srv.Close()

	roms, err := newTestClient(srv.URL).ListRoms(context.Background(), 41, after)
	if err != nil {
		t.Fatalf("ListRoms: %v", err)
	}
	if len(roms) != 0 {
		t.Fatalf("expected empty result, got %d", len(roms))
	}
}

func TestAuthFailureDoesNotRetry(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ListPlatforms(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnauthorized {
		t.Fatalf("expected HTTPError 401, got %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("401 must not retry; saw %d requests", requests.Load())
	}
}

func TestRetryOn5xx(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !checkAuth(t, w, r) {
			return
		}
		w.Write(fixture(t, "platforms.json"))
	}))
	defer srv.Close()

	platforms, err := newTestClient(srv.URL).ListPlatforms(context.Background())
	if err != nil {
		t.Fatalf("expected success on third attempt, got %v", err)
	}
	if len(platforms) != 3 {
		t.Fatalf("expected 3 platforms, got %d", len(platforms))
	}
	if requests.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", requests.Load())
	}
}

func TestRetriesExhausted(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ListPlatforms(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadGateway {
		t.Fatalf("expected HTTPError 502, got %v", err)
	}
	if requests.Load() != defaultRetries {
		t.Errorf("expected %d attempts, got %d", defaultRetries, requests.Load())
	}
}

func TestTestConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(t, w, r) {
			return
		}
		w.Write(fixture(t, "platforms.json"))
	}))
	defer srv.Close()

	count, err := newTestClient(srv.URL).TestConnection(context.Background())
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 platforms, got %d", count)
	}
}

func TestHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/heartbeat" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).Heartbeat(context.Background()); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // would retry, but ctx is dead
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newTestClient(srv.URL).ListPlatforms(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
