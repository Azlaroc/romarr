package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
)

// pruneEnv gives the router a real roms tree with one game owned twice, so the
// declutter endpoints have something true to classify.
func pruneEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	roms := t.TempDir()
	env := newTestEnv(t, func(cfg *config.Config) { cfg.GamesRomsPath = roms })
	if err := os.MkdirAll(filepath.Join(roms, "atari7800"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := env.jobs.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "atari7800", Version: "v1",
	}, []db.DatGameRow{
		{Name: "Ace of Aces (USA)", BareTitle: "Ace of Aces", Region: "usa", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ace of Aces (USA).a78", Size: 1024, MD5: "aaa"}}},
		{Name: "Ace of Aces (Europe)", BareTitle: "Ace of Aces", Region: "europe", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ace of Aces (Europe).a78", Size: 1024, MD5: "bbb"}}},
	}); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
	for _, f := range []struct{ name, md5 string }{
		{"Ace of Aces (USA).a78", "aaa"},
		{"Ace of Aces (Europe).a78", "bbb"},
	} {
		path := filepath.Join(roms, "atari7800", f.name)
		if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := env.jobs.AddLibraryItem(&db.LibraryItem{
			Title: "Ace of Aces", PlatformSlug: "atari7800", FilePath: path,
			Metadata: `{"romm":{"md5":"` + f.md5 + `"}}`, Source: "scan", SourceID: path,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return env, roms
}

// waitIdle polls a runner's status endpoint until it reports not-running.
// Throttled deliberately: an unthrottled poll storm trips the router's own
// per-IP rate limit before a background run finishes.
func waitIdle(t *testing.T, env *testEnv, statusPath string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rr := env.do("GET", statusPath, "")
		wantStatus(t, rr, 200)
		body := decodeMap(t, rr)
		if body["running"] == false {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run polled at %s did not finish", statusPath)
	return nil
}

func TestPrunePreviewApplyArchives(t *testing.T) {
	env, roms := pruneEnv(t)

	rr := env.do("POST", "/api/library/prune/preview", `{"platform_slug":"atari7800"}`)
	wantStatus(t, rr, 200)
	if decodeMap(t, rr)["success"] != true {
		t.Fatalf("preview did not start: %v", decodeMap(t, rr))
	}
	status := waitIdle(t, env, "/api/library/prune/status")
	if status["total"] != float64(1) {
		t.Fatalf("planned = %v, want the single surplus dump", status["total"])
	}
	// Nothing has moved yet — a preview is a preview.
	if _, err := os.Stat(filepath.Join(roms, "atari7800", "Ace of Aces (Europe).a78")); err != nil {
		t.Fatalf("the preview moved a file: %v", err)
	}

	rr = env.do("GET", "/api/library/prune/preview/results", "")
	wantStatus(t, rr, 200)
	items, _ := decodeMap(t, rr)["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("rows = %d, want 1", len(items))
	}
	row, _ := items[0].(map[string]interface{})
	for _, key := range []string{"library_id", "verdict", "status", "reason", "keeper"} {
		if _, ok := row[key]; !ok {
			t.Errorf("row missing %q: %v", key, row)
		}
	}
	if row["verdict"] != "archive" || row["status"] != "planned" {
		t.Errorf("row = %v, want an archive plan", row)
	}

	rr = env.do("POST", "/api/library/prune/apply", `{"exclude_ids":[]}`)
	wantStatus(t, rr, 200)
	status = waitIdle(t, env, "/api/library/prune/status")
	if status["archived"] != float64(1) {
		t.Fatalf("archived = %v, want 1", status["archived"])
	}
	if _, err := os.Stat(filepath.Join(roms, ".archive", "atari7800", "Ace of Aces (Europe).a78")); err != nil {
		t.Errorf("archived file missing: %v", err)
	}
	// The keeper never moves.
	if _, err := os.Stat(filepath.Join(roms, "atari7800", "Ace of Aces (USA).a78")); err != nil {
		t.Errorf("the keeper was archived: %v", err)
	}
}

func TestPruneApplyRefusesWithoutAPreview(t *testing.T) {
	env, _ := pruneEnv(t)
	rr := env.do("POST", "/api/library/prune/apply", `{"exclude_ids":[]}`)
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if body["success"] != false {
		t.Errorf("apply = %v, want a refusal: there is no held preview to apply", body)
	}
}

func TestPrunePreviewRequiresAScope(t *testing.T) {
	env, _ := pruneEnv(t)
	rr := env.do("POST", "/api/library/prune/preview", `{}`)
	wantStatus(t, rr, 400)
}

func TestPruneStatusNamesTheArchiveRoot(t *testing.T) {
	env, roms := pruneEnv(t)
	rr := env.do("GET", "/api/library/prune/status", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	// Where files go is not a detail an operator should have to guess at.
	if got := body["archive_root"]; got != filepath.Join(roms, ".archive") {
		t.Errorf("archive_root = %v, want it stated", got)
	}
	if body["configured"] != true {
		t.Errorf("configured = %v", body["configured"])
	}
}
