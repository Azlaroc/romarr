package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gamarr/internal/config"
)

const scanStatusPath = "/api/library/scan/status"

func TestScanRunEndToEnd(t *testing.T) {
	roms := t.TempDir()
	env := newTestEnv(t, func(cfg *config.Config) { cfg.GamesRomsPath = roms })

	arrival := filepath.Join(roms, "nes", "Arrival (USA).nes")
	if err := os.MkdirAll(filepath.Dir(arrival), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arrival, []byte("out of band bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := env.do("GET", scanStatusPath, "")
	wantStatus(t, rr, 200)
	if body := decodeMap(t, rr); body["configured"] != true || body["running"] != false {
		t.Fatalf("idle status = %+v", body)
	}

	// Dry run: reports the arrival, writes nothing.
	rr = env.do("POST", "/api/library/scan/run", `{"platform_slug":"nes","dry_run":true}`)
	wantStatus(t, rr, 200)
	if decodeMap(t, rr)["success"] != true {
		t.Fatalf("dry run refused: %s", rr.Body.String())
	}
	body := waitIdle(t, env, scanStatusPath)
	if body["created"] != float64(1) {
		t.Fatalf("dry-run status = %+v, want 1 created", body)
	}
	if env.jobs.LibraryItemByFilePath(arrival) != nil {
		t.Fatal("dry run created a library row")
	}

	rr = env.do("GET", "/api/library/scan/results?page=1&page_size=100", "")
	wantStatus(t, rr, 200)
	var results struct {
		Items []struct {
			Status  string `json:"status"`
			Path    string `json:"path"`
			Catalog string `json:"catalog"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("results not JSON: %v", err)
	}
	if results.Total != 1 || results.Items[0].Status != "created" {
		t.Fatalf("results = %+v", results)
	}

	// Real run writes the row, sourced libscan.
	rr = env.do("POST", "/api/library/scan/run", `{"platform_slug":"nes"}`)
	wantStatus(t, rr, 200)
	waitIdle(t, env, scanStatusPath)
	item := env.jobs.LibraryItemByFilePath(arrival)
	if item == nil {
		t.Fatal("no row created")
	}
	if item.Source != "libscan" {
		t.Errorf("source = %q, want libscan", item.Source)
	}

	// Re-run adopts.
	rr = env.do("POST", "/api/library/scan/run", `{"platform_slug":"nes"}`)
	wantStatus(t, rr, 200)
	body = waitIdle(t, env, scanStatusPath)
	if body["created"] != float64(0) || body["adopted"] != float64(1) {
		t.Fatalf("re-run status = %+v, want 0 created / 1 adopted", body)
	}
}

func TestScanRunRejectsBadRequests(t *testing.T) {
	env := newTestEnv(t, nil)

	rr := env.do("POST", "/api/library/scan/run", `{}`)
	wantStatus(t, rr, 400)

	rr = env.do("POST", "/api/library/scan/run", `{"platform_slug":"nes"}`,
		withHeader("Content-Type", "text/plain"))
	wantStatus(t, rr, 415)
}
