package supervise

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/datsvc"
	"gamarr/internal/db"
	"gamarr/internal/romm"
	"gamarr/internal/rommconnect"
)

func rommStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/heartbeat":
			w.Write([]byte(`{}`))
		case "/api/platforms":
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newStore(t *testing.T) *db.JobStore {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestApplyRomMSyncEnableDisable(t *testing.T) {
	stub := rommStub(t)
	store := newStore(t)
	cfg := &config.Config{RomMURL: stub.URL, RomMAPIUser: "u", RomMAPIPass: "p"}
	// Row-backed toggle so Apply sees runtime state (env default is false).
	cfg.AttachSettings(store)

	builds := 0
	sup := New(cfg, nil, nil, Builders{
		RomMSync: func() *romm.Syncer {
			builds++
			return romm.NewSyncer(romm.New(stub.URL, "u", "p"), store, romm.SyncOptions{
				RomsRoot:  t.TempDir(),
				StateFile: filepath.Join(t.TempDir(), "romm_sync.json"),
			})
		},
	}, nil)

	sup.StartAll() // sync disabled → no instance
	if sup.RomMSync() != nil {
		t.Fatal("syncer should not exist while disabled")
	}

	store.SetSetting("romm_sync_enabled", "true")
	sup.Apply([]string{"romm_sync_enabled"})
	if sup.RomMSync() == nil {
		t.Fatal("syncer missing after enable")
	}
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}

	// Interval change rebuilds the instance.
	store.SetSetting("romm_sync_interval_seconds", "3600")
	sup.Apply([]string{"romm_sync_interval_seconds"})
	if sup.RomMSync() == nil || builds != 2 {
		t.Fatalf("after interval change: syncer=%v builds=%d, want live and 2", sup.RomMSync() != nil, builds)
	}

	store.SetSetting("romm_sync_enabled", "false")
	sup.Apply([]string{"romm_sync_enabled"})
	if sup.RomMSync() != nil {
		t.Fatal("syncer should be gone after disable")
	}
	sup.StopAll()
}

func TestApplyConnectSwapsImportNotify(t *testing.T) {
	stub := rommStub(t)
	store := newStore(t)
	cfg := &config.Config{RomMURL: stub.URL, RomMAPIUser: "u", RomMAPIPass: "p"}
	cfg.AttachSettings(store)

	var mu sync.Mutex
	var current func(string)
	setNotify := func(fn func(fsSlug string)) {
		mu.Lock()
		defer mu.Unlock()
		current = fn
	}
	getNotify := func() func(string) {
		mu.Lock()
		defer mu.Unlock()
		return current
	}

	sup := New(cfg, nil, nil, Builders{
		Connect: func() (*rommconnect.Notifier, func(string)) {
			n := rommconnect.NewNotifier(rommconnect.New(stub.URL, "u", "p"), rommconnect.NotifierOptions{})
			return n, n.Enqueue
		},
	}, setNotify)

	sup.StartAll() // connect disabled by default → nothing attached
	if getNotify() != nil {
		t.Fatal("notify attached while disabled")
	}

	store.SetSetting("romm_connect_enabled", "true")
	sup.Apply([]string{"romm_connect_enabled"})
	if getNotify() == nil {
		t.Fatal("notify not attached after enable")
	}

	store.SetSetting("romm_connect_enabled", "false")
	sup.Apply([]string{"romm_connect_enabled"})
	if getNotify() != nil {
		t.Fatal("notify still attached after disable")
	}

	// Enable → disable → enable must construct fresh notifiers (a reused one
	// would panic on second Start).
	store.SetSetting("romm_connect_enabled", "true")
	sup.Apply([]string{"romm_connect_enabled"})
	if getNotify() == nil {
		t.Fatal("re-enable failed")
	}
	sup.StopAll()
	if getNotify() != nil {
		t.Fatal("StopAll should detach notify")
	}
}

// The DAT cadence is the one loop that ships off, so the settings flip is
// the only thing that ever arms it — and it must arm without a restart.
func TestApplyDatCadenceEnableDisable(t *testing.T) {
	store := newStore(t)
	cfg := &config.Config{}
	cfg.AttachSettings(store)
	svc := datsvc.New(cfg, store)

	sup := New(cfg, nil, svc, Builders{}, nil)
	sup.StartAll()
	if svc.LoopRunning() {
		t.Fatal("cadence armed with the toggle off; a fresh deploy must fetch nothing")
	}

	store.SetSetting("dat_auto_refresh_enabled", "true")
	sup.Apply([]string{"dat_auto_refresh_enabled"})
	if !svc.LoopRunning() {
		t.Fatal("cadence did not arm when the toggle went on")
	}

	// Re-arm in place on an interval change: the service pointer the API
	// holds must stay the same instance.
	store.SetSetting("dat_refresh_interval_days", "7")
	sup.Apply([]string{"dat_refresh_interval_days"})
	if !svc.LoopRunning() {
		t.Fatal("cadence lost after an interval change")
	}

	store.SetSetting("dat_auto_refresh_enabled", "false")
	sup.Apply([]string{"dat_auto_refresh_enabled"})
	if svc.LoopRunning() {
		t.Fatal("cadence survived the toggle going off")
	}

	// And back on again — the StopLoop path must not have left the stop
	// channel closed.
	store.SetSetting("dat_auto_refresh_enabled", "true")
	sup.Apply([]string{"dat_auto_refresh_enabled"})
	if !svc.LoopRunning() {
		t.Fatal("cadence could not re-arm after a disable")
	}
	sup.StopAll()
}
