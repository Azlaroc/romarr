package archiveorg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeStore is a map-backed CacheStore.
type fakeStore struct {
	mu   sync.Mutex
	rows map[string]struct {
		files []byte
		at    time.Time
	}
	puts int
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]struct {
		files []byte
		at    time.Time
	}{}}
}

func (f *fakeStore) GetItemMetadata(item string) ([]byte, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[item]
	return r.files, r.at, ok
}

func (f *fakeStore) PutItemMetadata(item string, files []byte, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[item] = struct {
		files []byte
		at    time.Time
	}{files, at}
	f.puts++
}

func (f *fakeStore) seed(t *testing.T, item string, files []iaFile, at time.Time) {
	t.Helper()
	raw, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.rows[item] = struct {
		files []byte
		at    time.Time
	}{raw, at}
	f.mu.Unlock()
}

func metaServer(t *testing.T, hits *int, status int, files []iaFile) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"files": files})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListingWarmStoreSkipsNetwork(t *testing.T) {
	hits := 0
	srv := metaServer(t, &hits, 200, nil)
	store := newFakeStore()
	store.seed(t, "itemA", []iaFile{{Name: "Game (USA).zip", MD5: "aa"}}, time.Now())

	d := New(map[string]string{"gb": "itemA"}, WithBaseURL(srv.URL), WithCache(store))
	files, err := d.listing(context.Background(), "itemA")
	if err != nil || len(files) != 1 || files[0].Name != "Game (USA).zip" {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if hits != 0 {
		t.Fatalf("network hit despite warm store: %d", hits)
	}
}

func TestListingStaleStoreRefetchesAndPersists(t *testing.T) {
	hits := 0
	fresh := []iaFile{{Name: "Fresh (USA).zip"}}
	srv := metaServer(t, &hits, 200, fresh)
	store := newFakeStore()
	store.seed(t, "itemA", []iaFile{{Name: "Old.zip"}}, time.Now().Add(-2*time.Hour))

	d := New(map[string]string{"gb": "itemA"}, WithBaseURL(srv.URL), WithCache(store))
	files, err := d.listing(context.Background(), "itemA")
	if err != nil || len(files) != 1 || files[0].Name != "Fresh (USA).zip" {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if hits != 1 || store.puts != 1 {
		t.Fatalf("hits=%d puts=%d, want 1/1", hits, store.puts)
	}
}

func TestListingServesStaleOnFetchError(t *testing.T) {
	hits := 0
	srv := metaServer(t, &hits, 503, nil)
	store := newFakeStore()
	store.seed(t, "itemA", []iaFile{{Name: "Stale (USA).zip"}}, time.Now().Add(-3*time.Hour))

	d := New(map[string]string{"gb": "itemA"}, WithBaseURL(srv.URL), WithCache(store))
	files, err := d.listing(context.Background(), "itemA")
	if err != nil || len(files) != 1 || files[0].Name != "Stale (USA).zip" {
		t.Fatalf("stale-if-error failed: files=%v err=%v", files, err)
	}
	if hits != 1 {
		t.Fatalf("expected one attempted fetch, got %d", hits)
	}
}

func TestListingCorruptStoreRowIsMiss(t *testing.T) {
	hits := 0
	fresh := []iaFile{{Name: "Fresh (USA).zip"}}
	srv := metaServer(t, &hits, 200, fresh)
	store := newFakeStore()
	store.mu.Lock()
	store.rows["itemA"] = struct {
		files []byte
		at    time.Time
	}{[]byte("{{{"), time.Now()}
	store.mu.Unlock()

	d := New(map[string]string{"gb": "itemA"}, WithBaseURL(srv.URL), WithCache(store))
	files, err := d.listing(context.Background(), "itemA")
	if err != nil || len(files) != 1 || files[0].Name != "Fresh (USA).zip" {
		t.Fatalf("corrupt row not treated as miss: files=%v err=%v", files, err)
	}
	if hits != 1 || store.puts != 1 {
		t.Fatalf("hits=%d puts=%d, want refetch+overwrite", hits, store.puts)
	}
}
