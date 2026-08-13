package romm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gamarr/internal/db"
)

// stubRomM is a minimal RomM impersonation: heartbeat, platforms, roms.
type stubRomM struct {
	platforms []Platform
	roms      map[int][]Rom // platform id → roms
	// failRomsForPlatform makes /api/roms 500 for one platform id.
	failRomsForPlatform atomic.Int64
}

func (s *stubRomM) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/platforms", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(s.platforms)
	})
	mux.HandleFunc("/api/roms", func(w http.ResponseWriter, r *http.Request) {
		pid, _ := strconv.Atoi(r.URL.Query().Get("platform_ids"))
		if int64(pid) == s.failRomsForPlatform.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		roms := s.roms[pid]
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": roms, "total": len(roms), "limit": 1000, "offset": 0,
		})
	})
	return mux
}

func igdb(id int64) *int64 { return &id }

func newSyncFixture(t *testing.T) (*stubRomM, *Syncer, *db.JobStore) {
	t.Helper()
	stub := &stubRomM{
		platforms: []Platform{
			{ID: 8, Slug: "genesis", FSSlug: "genesis-slash-megadrive", Name: "Sega Mega Drive/Genesis", RomCount: 1},
			{ID: 41, Slug: "psx", FSSlug: "psx", Name: "PlayStation", RomCount: 2},
		},
		roms: map[int][]Rom{
			8: {{
				ID: 201, PlatformID: 8, PlatformFSSlug: "genesis-slash-megadrive",
				FSName: "Sonic (USA).md", FSNameNoTags: "Sonic", FSNameNoExt: "Sonic (USA)",
				FSPath: "roms/genesis-slash-megadrive", FSSizeBytes: 512, Name: "Sonic the Hedgehog",
				MD5Hash: "aa", IGDBID: igdb(111),
			}},
			41: {
				{
					ID: 101, PlatformID: 41, PlatformFSSlug: "psx",
					FSName: "Castlevania (USA).chd", FSNameNoTags: "Castlevania", FSNameNoExt: "Castlevania (USA)",
					FSPath: "roms/psx", FSSizeBytes: 1024, Name: "Castlevania", IGDBID: igdb(222),
				},
				{
					ID: 102, PlatformID: 41, PlatformFSSlug: "psx",
					FSName: "Gone (USA).chd", FSNameNoTags: "Gone", FSNameNoExt: "Gone (USA)",
					FSPath: "roms/psx", FSSizeBytes: 2048, Name: "Gone", MissingFromFS: true,
				},
			},
		},
	}
	srv := httptest.NewServer(stub.handler(t))
	t.Cleanup(srv.Close)

	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	client := New(srv.URL, "romarr", "pw", WithBackoff(time.Millisecond), WithRetries(1))
	syncer := NewSyncer(client, store, SyncOptions{
		RomsRoot:  "/library/roms",
		StateFile: filepath.Join(t.TempDir(), "romm_sync.json"),
	})
	return stub, syncer, store
}

func TestSyncFullPopulatesLibrary(t *testing.T) {
	_, syncer, store := newSyncFixture(t)

	syncer.runOnce(true)

	if total := store.LibraryTotal(); total != 2 {
		t.Fatalf("library total=%d, want 2 (missing_from_fs rom excluded)", total)
	}

	// fs_slug translated to the internal slug in the DB.
	sonic := store.FindLibraryByTitle("Sonic the Hedgehog", "genesis")
	if sonic == nil {
		t.Fatal("genesis rom not stored under internal slug 'genesis'")
	}
	if sonic.FilePath != filepath.Join("/library/roms", "genesis-slash-megadrive", "Sonic (USA).md") {
		t.Errorf("file path wrong: %q", sonic.FilePath)
	}
	if sonic.FileSize != 512 || sonic.Source != "romm" || sonic.SourceID != "romm:201" {
		t.Errorf("row fields wrong: %+v", sonic)
	}

	var meta map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(sonic.Metadata), &meta); err != nil {
		t.Fatalf("metadata not JSON: %v", err)
	}
	romm := meta["romm"]
	if romm["search_key"] != "sonic (usa)" || romm["fs_slug"] != "genesis-slash-megadrive" {
		t.Errorf("romm metadata wrong: %+v", romm)
	}
	if romm["md5"] != "aa" || romm["igdb_id"] != float64(111) {
		t.Errorf("romm hash/igdb metadata wrong: %+v", romm)
	}

	status := syncer.Status()
	if status["last_error"] != "" || status["last_full"] == nil {
		t.Errorf("status after full sync: %+v", status)
	}
}

func TestSyncFailureWritesNothing(t *testing.T) {
	stub, syncer, store := newSyncFixture(t)

	// Seed a good pull first.
	syncer.runOnce(true)
	if store.LibraryTotal() != 2 {
		t.Fatalf("seed failed: %d", store.LibraryTotal())
	}

	// Second platform's rom listing now fails: the whole run must abort and
	// the last-good rows survive — even though platform 8 fetched fine.
	stub.failRomsForPlatform.Store(41)
	stub.roms[8] = nil // would delete Sonic if a partial write happened
	syncer.runOnce(true)

	if total := store.LibraryTotal(); total != 2 {
		t.Errorf("library changed on a failed pull: total=%d", total)
	}
	if syncer.Status()["last_error"] == "" {
		t.Error("failure not recorded in status")
	}
}

func TestSyncIncrementalDoesNotReconcile(t *testing.T) {
	stub, syncer, store := newSyncFixture(t)
	syncer.runOnce(true)

	// RomM stops reporting Sonic (as an incremental would: unchanged roms
	// are not returned). An incremental must NOT delete it.
	stub.roms[8] = nil
	syncer.runOnce(false)

	if store.FindLibraryByTitle("Sonic the Hedgehog", "genesis") == nil {
		t.Error("incremental sync deleted an unchanged row")
	}
}

func TestSyncExcludesPlatforms(t *testing.T) {
	stub, _, _ := newSyncFixture(t)
	srv := httptest.NewServer(stub.handler(t))
	t.Cleanup(srv.Close)

	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	client := New(srv.URL, "romarr", "pw", WithBackoff(time.Millisecond))
	syncer := NewSyncer(client, store, SyncOptions{
		RomsRoot:         "/library/roms",
		ExcludePlatforms: []string{"psx"},
		StateFile:        filepath.Join(t.TempDir(), "romm_sync.json"),
	})
	syncer.runOnce(true)

	if store.FindLibraryByTitle("Castlevania", "psx") != nil {
		t.Error("excluded platform was synced")
	}
	if store.FindLibraryByTitle("Sonic the Hedgehog", "genesis") == nil {
		t.Error("non-excluded platform missing")
	}
}

func TestTriggerSyncGuardsConcurrency(t *testing.T) {
	_, syncer, _ := newSyncFixture(t)
	syncer.running.Store(true) // simulate an in-flight run
	if syncer.TriggerSync(false) {
		t.Error("TriggerSync must refuse while a run is in flight")
	}
	syncer.running.Store(false)
}

func TestLocalPath(t *testing.T) {
	cases := []struct {
		fsPath, fsSlug, fsName, want string
	}{
		{"roms/psx", "psx", "Game.chd", "/r/psx/Game.chd"},
		{"roms/psx/discs", "psx", "Game.chd", "/r/psx/discs/Game.chd"},
		{"library/roms/psx", "psx", "Game (USA)", "/r/psx/Game (USA)"},
		{"weird/layout", "psx", "Game.chd", "/r/psx/Game.chd"}, // slug absent → default
	}
	for _, c := range cases {
		r := &Rom{FSPath: c.fsPath, PlatformFSSlug: c.fsSlug, FSName: c.fsName}
		if got := LocalPath("/r", r); got != filepath.FromSlash(c.want) {
			t.Errorf("LocalPath(%q, slug=%q) = %q, want %q", c.fsPath, c.fsSlug, got, c.want)
		}
	}
}

func TestSyncStatePersistsAcrossSyncers(t *testing.T) {
	stub, syncer, _ := newSyncFixture(t)
	syncer.runOnce(true)

	// A new Syncer over the same state file starts from the saved state, so
	// the next scheduled run is incremental rather than full.
	store2, err := db.New(filepath.Join(t.TempDir(), "gamarr2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store2.Close() })
	srv := httptest.NewServer(stub.handler(t))
	t.Cleanup(srv.Close)

	s2 := NewSyncer(New(srv.URL, "u", "p"), store2, SyncOptions{
		RomsRoot:  "/library/roms",
		StateFile: syncer.stateFN,
	})
	if s2.state.LastFull.IsZero() {
		t.Error("state not reloaded from disk")
	}
}

func TestMapRomFallbacks(t *testing.T) {
	_, syncer, _ := newSyncFixture(t)
	r := &Rom{ID: 1, PlatformFSSlug: "tg16", FSName: "Bonk.pce", FSNameNoExt: "Bonk", FSPath: "roms/tg16"}
	p := &Platform{ID: 39, FSSlug: "tg16"} // no display name from RomM

	item := syncer.mapRom(r, p)
	if item.Item.Title != "Bonk" {
		t.Errorf("title fallback: %q", item.Item.Title)
	}
	if item.Item.Platform != "TG16" {
		t.Errorf("platform name fallback: %q", item.Item.Platform)
	}
	if item.Item.SourceID != "romm:1" {
		t.Errorf("source id: %q", item.Item.SourceID)
	}
}

func TestSyncStatusUnconfigured(t *testing.T) {
	var s *Syncer
	if got := s.Status(); got["enabled"] != false {
		t.Errorf("nil syncer status: %+v", got)
	}
	if s.TriggerSync(false) {
		t.Error("nil syncer must not trigger")
	}
	s.Start() // must not panic
	s.Stop()
}

func TestSyncRommOwnedRowsReplaceLegacyScan(t *testing.T) {
	_, syncer, store := newSyncFixture(t)
	// Legacy fs-scanner rows: one ROM (purged by reconcile), one vault (kept).
	store.AddLibraryItem(&db.LibraryItem{Title: "Old", Source: "scan", IsPC: false, PlatformSlug: "psx", Metadata: "{}"})
	store.AddLibraryItem(&db.LibraryItem{Title: "Vault", Source: "scan", IsPC: true, Metadata: "{}"})

	syncer.runOnce(true)

	if store.FindLibraryByTitle("Old", "psx") != nil {
		t.Error("legacy ROM scan row survived the cutover reconcile")
	}
	if store.FindLibraryByTitle("Vault", "pc") == nil {
		t.Error("vault scan row was purged")
	}
	if total := store.LibraryTotal(); total != 3 { // 2 romm + 1 vault
		t.Errorf("total=%d, want 3", total)
	}
}

func TestSyncSourceIDFormat(t *testing.T) {
	// Guards the "romm:<id>" contract other code may grep for.
	r := &Rom{ID: 42, PlatformFSSlug: "nes", FSName: "x.nes", FSPath: "roms/nes"}
	p := &Platform{ID: 1, FSSlug: "nes", Name: "NES"}
	_, syncer, _ := newSyncFixture(t)
	if got := syncer.mapRom(r, p).Item.SourceID; got != fmt.Sprintf("romm:%d", 42) {
		t.Errorf("source id format drifted: %q", got)
	}
}
