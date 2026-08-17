package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/romm"
	"gamarr/internal/supervise"
)

func TestLibrarySyncUnconfigured(t *testing.T) {
	env := newTestEnv(t, nil) // no RomM syncer wired

	rr := env.do("POST", "/api/library/sync", "")
	wantStatus(t, rr, 200)
	m := decodeMap(t, rr)
	if m["success"] != false || m["error"] != "RomM sync not configured" {
		t.Errorf("got %v", m)
	}

	rr = env.do("GET", "/api/library/sync/status", "")
	wantStatus(t, rr, 200)
	if m := decodeMap(t, rr); m["enabled"] != false {
		t.Errorf("status got %v", m)
	}
}

func TestLibrarySyncTrigger(t *testing.T) {
	// Minimal healthy RomM stub so the triggered background run terminates.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/heartbeat":
			w.Write([]byte(`{}`))
		case "/api/platforms":
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer stub.Close()

	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	syncer := romm.NewSyncer(romm.New(stub.URL, "u", "p"), store, romm.SyncOptions{
		RomsRoot:  t.TempDir(),
		StateFile: filepath.Join(t.TempDir(), "romm_sync.json"),
	})
	cfg := &config.Config{RomMURL: stub.URL, RomMAPIUser: "u", RomMAPIPass: "p", RomMSyncEnabled: true}
	sup := supervise.New(cfg, nil, nil, supervise.Builders{
		RomMSync: func() *romm.Syncer { return syncer },
	}, nil)
	sup.StartAll()
	t.Cleanup(sup.StopAll)
	s := &Server{cfg: cfg, sup: sup}

	// Syncer.Start fires an immediate sync; wait for it to settle so the
	// explicit trigger below isn't rejected as already-running.
	for i := 0; i < 100; i++ {
		if st := syncer.Status(); st["running"] != true {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	rr := httptest.NewRecorder()
	s.handleLibrarySync(rr, httptest.NewRequest("POST", "/api/library/sync?full=true", nil))
	m := decodeMap(t, rr)
	if m["success"] != true || m["full"] != true {
		t.Errorf("trigger got %v", m)
	}

	rr = httptest.NewRecorder()
	s.handleLibrarySyncStatus(rr, httptest.NewRequest("GET", "/api/library/sync/status", nil))
	if m := decodeMap(t, rr); m["enabled"] != true {
		t.Errorf("status got %v", m)
	}
}
