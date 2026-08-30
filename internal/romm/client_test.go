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
