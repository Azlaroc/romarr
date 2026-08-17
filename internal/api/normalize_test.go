package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/download"
	"gamarr/internal/qbit"
	"gamarr/internal/renamer"
	"gamarr/internal/sources"
)

// newRenamerEnv builds a testEnv whose router carries a live renamer over a
// real temp roms tree with a fake rom-converto.
func newRenamerEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	romsRoot := t.TempDir()
	fake := filepath.Join(t.TempDir(), "rom-converto")
	script := `#!/bin/sh
for last in "$@"; do :; done
c=$(cat "$last")
case "$c" in
  MATCH:*)
    new=${c#MATCH:}
    new=${new%%:*}
    mv "$last" "$(dirname "$last")/$new"
    ;;
  *) : ;;
esac
exit 0
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Sources:            sources.Load("", ""),
		DataDir:            t.TempDir(),
		QBCategory:         "games",
		GamesRomsPath:      romsRoot,
		ConvertoBin:        fake,
		ConvertoTimeoutSec: 30,
	}
	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := download.New(cfg, store, qbit.New(cfg.QBURL, cfg.QBUser, cfg.QBPass))
	ren := renamer.New(cfg, store, nil)
	router := NewRouter(cfg, mgr, nil, nil, nil, ren, nil)
	return &testEnv{t: t, cfg: cfg, jobs: store, mgr: mgr, router: router}, romsRoot
}

func TestNormalizeEndpoints(t *testing.T) {
	env, romsRoot := newRenamerEnv(t)

	// Seed one renameable rom.
	dir := filepath.Join(romsRoot, "gb")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "Old (U).gb")
	os.WriteFile(p, []byte("MATCH:New (USA).gb:x"), 0o644)
	env.jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Old (U)", PlatformSlug: "gb", FilePath: p,
		Source: "ddl", SourceType: "ddl", SourceID: "ddl:" + p, Metadata: "{}",
	})

	rr := env.do("GET", "/api/library/normalize/status", "")
	wantStatus(t, rr, http.StatusOK)
	if resp := decodeMap(t, rr); resp["phase"] != "idle" {
		t.Errorf("status = %v", resp)
	}

	// Content-type enforcement (env.do defaults to JSON, so force a bad one).
	rr = env.do("POST", "/api/library/normalize/preview", `{"platform_slug":"gb"}`,
		withHeader("Content-Type", "text/plain"))
	wantStatus(t, rr, 415)

	rr = env.do("POST", "/api/library/normalize/preview", `{"platform_slug":"gb"}`,
		withHeader("Content-Type", "application/json"))
	wantStatus(t, rr, http.StatusOK)
	if resp := decodeMap(t, rr); resp["success"] != true {
		t.Fatalf("preview trigger = %v", resp)
	}

	// Poll until the run finishes.
	deadline := 200
	for ; deadline > 0; deadline-- {
		rr = env.do("GET", "/api/library/normalize/status", "")
		if decodeMap(t, rr)["running"] == false {
			break
		}
		// Throttled: an unthrottled poll storm can trip the router's own
		// per-IP API rate limit before the background run finishes.
		time.Sleep(5 * time.Millisecond)
	}
	if deadline == 0 {
		t.Fatal("preview never finished")
	}

	rr = env.do("GET", "/api/library/normalize/preview/results?page=1&page_size=50", "")
	wantStatus(t, rr, http.StatusOK)
	resp := decodeMap(t, rr)
	items, _ := resp["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("results = %v", resp)
	}
	row, _ := items[0].(map[string]interface{})
	if row["status"] != "rename" || row["new_name"] != "New (USA).gb" {
		t.Errorf("row = %v", row)
	}

	rr = env.do("POST", "/api/library/normalize/apply", `{"exclude_ids":[]}`,
		withHeader("Content-Type", "application/json"))
	wantStatus(t, rr, http.StatusOK)
	if resp := decodeMap(t, rr); resp["success"] != true {
		t.Fatalf("apply trigger = %v", resp)
	}
	for i := 0; i < 200; i++ {
		rr = env.do("GET", "/api/library/normalize/status", "")
		if decodeMap(t, rr)["running"] == false {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "New (USA).gb")); err != nil {
		t.Errorf("apply did not rename on disk: %v", err)
	}

	// Apply again with nothing held → refused shape.
	rr = env.do("POST", "/api/library/normalize/apply", "")
	if resp := decodeMap(t, rr); resp["success"] != false {
		t.Errorf("empty re-apply = %v", resp)
	}

	// Stop is always accepted.
	rr = env.do("POST", "/api/library/normalize/stop", "")
	wantStatus(t, rr, http.StatusOK)
}

func TestNormalizeNilRunner(t *testing.T) {
	env := newTestEnv(t, nil) // router built with ren = nil
	rr := env.do("GET", "/api/library/normalize/status", "")
	wantStatus(t, rr, http.StatusOK)
	if resp := decodeMap(t, rr); resp["enabled"] != false {
		t.Errorf("nil status = %v", resp)
	}
	rr = env.do("POST", "/api/library/normalize/preview", `{"platform_slug":"gb"}`,
		withHeader("Content-Type", "application/json"))
	if resp := decodeMap(t, rr); resp["success"] != false {
		t.Errorf("nil preview = %v", resp)
	}
}
