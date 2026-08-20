package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/db"
)

const hashStatusPath = "/api/library/hash/status"

// hashEnv gives the router a real roms tree holding the three shapes the
// backfill has to tell apart: a headered NES ROM, a plain one, and a
// directory that can never have a single-ROM identity.
func hashEnv(t *testing.T) (*testEnv, map[string]int64) {
	t.Helper()
	roms := t.TempDir()
	env := newTestEnv(t, func(cfg *config.Config) { cfg.GamesRomsPath = roms })

	ines := append([]byte{'N', 'E', 'S', 0x1a, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		[]byte("cartridge payload")...)
	ids := map[string]int64{}
	add := func(slug, title, name string, body []byte) {
		t.Helper()
		path := filepath.Join(roms, slug, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if body == nil {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := env.jobs.AddLibraryItem(&db.LibraryItem{
			Title: title, PlatformSlug: slug, FilePath: path,
			Metadata: "{}", Source: "romm", SourceID: "romm:" + title,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[title] = id
	}
	add("nes", "Headered", "Headered (USA).nes", ines)
	add("nes", "Plain", "Plain (USA).nes", []byte("no header here"))
	add("nes", "Directory", "A Directory", nil)
	return env, ids
}

func gamarrMeta(t *testing.T, env *testEnv, id int64) map[string]interface{} {
	t.Helper()
	item, err := env.jobs.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem: %v", err)
	}
	var tree struct {
		Gamarr map[string]interface{} `json:"gamarr"`
	}
	if err := json.Unmarshal([]byte(item.Metadata), &tree); err != nil {
		t.Fatalf("metadata not JSON: %v (%s)", err, item.Metadata)
	}
	return tree.Gamarr
}

func TestHashRunEndToEnd(t *testing.T) {
	env, ids := hashEnv(t)

	// Before anything runs, the status reports the size of the job.
	rr := env.do("GET", hashStatusPath, "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if body["configured"] != true || body["running"] != false {
		t.Fatalf("idle status = %+v", body)
	}
	if got := body["pending_all"]; got != float64(3) {
		t.Errorf("pending_all = %v, want 3", got)
	}

	// A dry run reports the work and writes nothing.
	rr = env.do("POST", "/api/library/hash/run", `{"platform_slug":"nes","dry_run":true}`)
	wantStatus(t, rr, 200)
	if decodeMap(t, rr)["success"] != true {
		t.Fatalf("dry run refused: %s", rr.Body.String())
	}
	body = waitIdle(t, env, hashStatusPath)
	if body["hashed"] != float64(2) || body["stripped"] != float64(1) || body["skipped"] != float64(1) {
		t.Fatalf("dry-run status = %+v, want 2 hashed (1 stripped) and 1 skip", body)
	}
	if body["pending_all"] != float64(3) {
		t.Errorf("dry run changed pending: %v", body["pending_all"])
	}
	if got := gamarrMeta(t, env, ids["Headered"])["md5"]; got != nil {
		t.Errorf("dry run wrote a hash: %v", got)
	}

	// The results page carries both hashes, which is what makes a
	// spot-check against the catalog possible before committing.
	rr = env.do("GET", "/api/library/hash/results?page=1&page_size=100", "")
	wantStatus(t, rr, 200)
	var results struct {
		Items []struct {
			Status string `json:"status"`
			MD5    string `json:"md5"`
			UnhMD5 string `json:"unh_md5"`
			Header string `json:"header"`
			Reason string `json:"reason"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("results not JSON: %v", err)
	}
	if results.Total != 3 {
		t.Fatalf("results total = %d, want 3", results.Total)
	}
	var stripped int
	for _, it := range results.Items {
		if it.Header == "ines" && it.UnhMD5 != "" && it.MD5 != it.UnhMD5 {
			stripped++
		}
	}
	if stripped != 1 {
		t.Errorf("results carry %d stripped rows, want 1: %+v", stripped, results.Items)
	}

	// The real run writes.
	rr = env.do("POST", "/api/library/hash/run", `{"platform_slug":"nes"}`)
	wantStatus(t, rr, 200)
	body = waitIdle(t, env, hashStatusPath)
	if body["hashed"] != float64(2) {
		t.Fatalf("run status = %+v", body)
	}
	if body["pending_all"] != float64(0) {
		t.Errorf("pending_all = %v after the run, want 0 — the directory is a permanent skip", body["pending_all"])
	}
	meta := gamarrMeta(t, env, ids["Headered"])
	if meta["md5"] == nil {
		t.Error("headered row has no content hash")
	}
	unh, _ := meta["unh"].(map[string]interface{})
	if unh == nil || unh["md5"] == meta["md5"] {
		t.Errorf("headered row's payload hash missing or equal to the whole file's: %+v", meta)
	}
	if gamarrMeta(t, env, ids["Directory"])["hash_skipped"] != "directory" {
		t.Errorf("directory not marked: %+v", gamarrMeta(t, env, ids["Directory"]))
	}

	// Re-running is a no-op rather than a rewrite.
	rr = env.do("POST", "/api/library/hash/run", `{"platform_slug":"nes"}`)
	wantStatus(t, rr, 200)
	body = waitIdle(t, env, hashStatusPath)
	if body["total"] != float64(0) || body["hashed"] != float64(0) {
		t.Errorf("re-run status = %+v, want nothing to do", body)
	}
}

func TestHashRunRejectsBadRequests(t *testing.T) {
	env, _ := hashEnv(t)

	rr := env.do("POST", "/api/library/hash/run", `{}`)
	wantStatus(t, rr, 400)

	rr = env.do("POST", "/api/library/hash/run", `{"platform_slug":"nes"}`,
		withHeader("Content-Type", "text/plain"))
	wantStatus(t, rr, 415)
}

// The single-flight guard is asserted in internal/hashfill, not here.
// Proving it over HTTP means winning a race against a run that finishes in
// microseconds on a three-row fixture: the second POST legitimately succeeds
// when the first has already ended, and nothing observable afterwards
// distinguishes that from the guard failing. A test that cannot tell its
// pass from its failure is worse than no test. The runner can force the
// state and does.

func TestHashStopIsSafeWhenIdle(t *testing.T) {
	env, _ := hashEnv(t)
	rr := env.do("POST", "/api/library/hash/stop", "")
	wantStatus(t, rr, 200)
	if decodeMap(t, rr)["success"] != true {
		t.Errorf("stop when idle = %s", rr.Body.String())
	}
}
