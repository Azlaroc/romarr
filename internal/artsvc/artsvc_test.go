package artsvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/metadata"
)

func TestSanitizeThumbName(t *testing.T) {
	cases := map[string]string{
		"Dragon Warrior (USA)":                "Dragon Warrior (USA)",
		"R-Type II (Japan)":                   "R-Type II (Japan)",
		"Q*bert (USA)":                        "Q_bert (USA)",
		"Fun & Games (USA)":                   "Fun _ Games (USA)",
		"WWF War Zone / Attitude":             "WWF War Zone _ Attitude",
		"Where: Deep? <Trouble> \\ A|B \"C\"": "Where_ Deep_ _Trouble_ _ A_B _C_",
		"Tick`ed":                             "Tick_ed",
	}
	for in, want := range cases {
		if got := SanitizeThumbName(in); got != want {
			t.Errorf("SanitizeThumbName(%q)=%q, want %q", in, got, want)
		}
	}
}

type fakeProvider struct {
	games []metadata.Game
	calls int
}

func (f *fakeProvider) Name() string     { return "fake" }
func (f *fakeProvider) Configured() bool { return true }
func (f *fakeProvider) Search(_ context.Context, _ string, _, _ int) ([]metadata.Game, error) {
	f.calls++
	return f.games, nil
}

func newTestService(t *testing.T, meta metadata.Provider, rawBase string) (*Service, *db.JobStore) {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{DataDir: t.TempDir()}
	// Built by hand, not New(): no worker goroutine, no politeness delay —
	// the tests drive resolveOne directly.
	s := &Service{
		cfg: cfg, store: store, meta: meta,
		client:  &http.Client{},
		rawBase: rawBase,
		queued:  map[int64]bool{},
		ch:      make(chan int64, queueDepth),
		stop:    make(chan struct{}),
	}
	return s, store
}

func addArtItem(t *testing.T, store *db.JobStore, title, slug, path string) *db.LibraryItem {
	t.Helper()
	id, err := store.AddLibraryItem(&db.LibraryItem{
		Title: title, Platform: slug, PlatformSlug: slug,
		FilePath: path, FileSize: 1, Source: "scan", SourceType: "manual",
		SourceID: "manual:" + path, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem: %v", err)
	}
	return item
}

// fakeImg is not decodable — enough for the fetch path, and a deliberate
// proof that a broken thumb never blocks a mint.
var fakeImg = []byte("\x89PNG\r\n\x1a\n fake body")

func TestResolveLadderLibretroHit(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if strings.Contains(r.URL.Path, "Named_Boxarts") {
			w.Write(fakeImg)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s, store := newTestService(t, &fakeProvider{}, srv.URL)
	// gb has a curated thumb repo; the basename stem keys the fetch.
	item := addArtItem(t, store, "Alleyway", "gb", "/roms/gb/Alleyway (World).zip")

	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	if !strings.Contains(gotPath, "Nintendo_-_Game_Boy/master/Named_Boxarts/Alleyway%20%28World%29.png") &&
		!strings.Contains(gotPath, "Nintendo_-_Game_Boy/master/Named_Boxarts/Alleyway (World).png") {
		t.Fatalf("fetched path=%q, want the curated repo + sanitized stem", gotPath)
	}
	p, ok := s.CachedPath(item)
	if !ok {
		t.Fatal("no cached file after a libretro hit")
	}
	if filepath.Base(p) != "Alleyway (World).png" {
		t.Fatalf("cached as %q", filepath.Base(p))
	}
	row, ok := store.GetArtCache("title:gb:Alleyway (World)")
	if !ok || row.Status != "ok" || row.Source != "libretro" {
		t.Fatalf("cache row=%+v", row)
	}
}

func TestResolveLadderIGDBFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cover") {
			w.Write(fakeImg)
			return
		}
		http.NotFound(w, r) // every libretro ask misses
	}))
	defer srv.Close()

	meta := &fakeProvider{games: []metadata.Game{
		{Name: "Wrong Platform", CoverURL: srv.URL + "/cover-wrong.jpg", Platforms: []string{"psx"}},
		{Name: "Right", CoverURL: srv.URL + "/cover-right.jpg", ReleaseYear: 1998, Genres: []string{"Platform"}, Platforms: []string{"switch"}},
	}}
	s, store := newTestService(t, meta, srv.URL)
	// switch has no thumb repo — the ladder must go straight to the provider.
	item := addArtItem(t, store, "Some Shop Title", "switch", "/roms/switch/Some Shop Title.nsp")

	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	if meta.calls != 1 {
		t.Fatalf("provider calls=%d", meta.calls)
	}
	if _, ok := s.CachedPath(item); !ok {
		t.Fatal("no cached file after provider fallback")
	}
	row, _ := store.GetArtCache(s.cacheKey(item))
	if row.Source != "igdb" {
		t.Fatalf("cache row=%+v", row)
	}
	// Year/genres banked for the detail header (the shop lane's only source).
	got, err := store.GetLibraryItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Metadata, `"year":1998`) || !strings.Contains(got.Metadata, `"Platform"`) {
		t.Fatalf("igdb meta not banked: %s", got.Metadata)
	}
}

func TestResolveLadderNegativeCache(t *testing.T) {
	misses := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		misses++
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s, store := newTestService(t, &fakeProvider{}, srv.URL) // provider returns no games
	item := addArtItem(t, store, "Nothing Anywhere", "gb", "/roms/gb/Nothing Anywhere.zip")

	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	row, ok := store.GetArtCache(s.cacheKey(item))
	if !ok || row.Status != "notfound" {
		t.Fatalf("cache row=%+v", row)
	}
	// A fresh negative answer suppresses the enqueue entirely.
	s.Enqueue(item.ID)
	if len(s.ch) != 0 {
		t.Fatal("enqueue ignored a fresh notfound")
	}
	// An aged one does not.
	store.DB().Exec("UPDATE art_cache SET fetched_at = datetime('now', '-8 days') WHERE key = ?", s.cacheKey(item))
	s.Enqueue(item.ID)
	if len(s.ch) != 1 {
		t.Fatal("aged notfound did not re-enqueue")
	}
}

func TestCustomDropInWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("network touched despite a custom file on disk")
	}))
	defer srv.Close()

	s, store := newTestService(t, &fakeProvider{}, srv.URL)
	item := addArtItem(t, store, "Handmade", "gb", "/roms/gb/Handmade (World).zip")

	dir := s.titleDir("gb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Handmade (World).jpg"), fakeImg, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CachedPath(item); !ok {
		t.Fatal("custom drop-in not found")
	}
	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
}

func TestBackfillSingleFlight(t *testing.T) {
	s, _ := newTestService(t, &fakeProvider{}, "http://127.0.0.1:0")
	if !s.StartBackfill() {
		t.Fatal("first start refused")
	}
	if s.StartBackfill() {
		t.Fatal("second start allowed while running")
	}
}
