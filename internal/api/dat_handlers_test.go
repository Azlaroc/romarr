package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// datUpload builds a multipart body carrying one DAT file part.
func datUpload(t *testing.T, filename string, body []byte) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf.String(), mw.FormDataContentType()
}

// clrmamepro renders a minimal catalog with n games.
func clrmamepro(version string, n int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "clrmamepro (\n\tname \"Test\"\n\tversion \"%s\"\n)\n\n", version)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "game (\n\tname \"Game %d (USA)\"\n"+
			"\trom ( name \"Game %d (USA).rom\" size %d crc %08x sha1 %040x )\n)\n\n", i, i, 4096*i, i, i*31)
	}
	return []byte(b.String())
}

func TestDatAuthoritiesAndAssignmentsAreServed(t *testing.T) {
	env := newTestEnv(t, nil)
	rr := env.do("GET", "/api/dat/authorities", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	auths, _ := body["authorities"].([]interface{})
	plats, _ := body["platforms"].([]interface{})
	if len(auths) != 3 {
		t.Fatalf("got %d authorities, want the three shipped", len(auths))
	}
	if len(plats) < 20 {
		t.Fatalf("got %d assignments, want the shipped pack", len(plats))
	}
}

// The transport pin. A raw body over 1MB never reaches the handler:
// requestSizeLimitMiddleware rejects it on Content-Length, which is exactly
// why this endpoint is multipart and why its 415 has to teach.
func TestDatUploadTransportIsMultipartOnly(t *testing.T) {
	env := newTestEnv(t, nil)
	big := strings.Repeat("x", 1<<20+512)

	rr := env.do("POST", "/api/dat/authorities/no-intro/upload", big,
		withHeader("Content-Type", "application/octet-stream"))
	wantStatus(t, rr, http.StatusRequestEntityTooLarge)

	// Under the cap, the same non-multipart body is refused by the handler
	// with an explanation rather than a bare 400.
	rr = env.do("POST", "/api/dat/authorities/no-intro/upload", "not multipart",
		withHeader("Content-Type", "application/octet-stream"))
	wantStatus(t, rr, http.StatusUnsupportedMediaType)
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "multipart/form-data") {
		t.Fatalf("error = %q, want it to name the required transport", msg)
	}
}

// A multipart body sails past the 1MB middleware cap, which is the point:
// a daily pack is tens of megabytes.
func TestDatUploadAcceptsABodyOverTheMiddlewareCap(t *testing.T) {
	env := newTestEnv(t, nil)
	catalog := clrmamepro("2026.08.17", 8000) // comfortably over 1MB
	if len(catalog) <= 1<<20 {
		t.Fatalf("fixture is %d bytes; the test needs one over the 1MB cap", len(catalog))
	}
	body, ct := datUpload(t, "Nintendo - Game Boy.dat", catalog)

	rr := env.do("POST", "/api/dat/authorities/no-intro/upload?platform=gb", body, withHeader("Content-Type", ct))
	wantStatus(t, rr, 200)
	resp := decodeMap(t, rr)
	if ok, _ := resp["success"].(bool); !ok {
		t.Fatalf("upload failed: %v", resp["error"])
	}
	imported, _ := resp["imported"].([]interface{})
	if len(imported) != 1 {
		t.Fatalf("imported = %v, want one platform", resp["imported"])
	}
	if _, ok := env.jobs.ActiveDatSnapshot("gb"); !ok {
		t.Fatal("no active snapshot after a successful upload")
	}
}

func TestDatUploadRequiresAPlatformForABareCatalog(t *testing.T) {
	env := newTestEnv(t, nil)
	body, ct := datUpload(t, "Nintendo - Game Boy.dat", clrmamepro("2026.08.17", 3))
	rr := env.do("POST", "/api/dat/authorities/no-intro/upload", body, withHeader("Content-Type", ct))
	wantStatus(t, rr, 200)
	if ok, _ := decodeMap(t, rr)["success"].(bool); ok {
		t.Fatal("a bare DAT with no platform is ambiguous and must not import")
	}
}

func TestDatUploadRejectsUnknownAuthority(t *testing.T) {
	env := newTestEnv(t, nil)
	body, ct := datUpload(t, "x.dat", clrmamepro("2026.08.17", 3))
	rr := env.do("POST", "/api/dat/authorities/tosec/upload?platform=gb", body, withHeader("Content-Type", ct))
	wantStatus(t, rr, 200)
	if ok, _ := decodeMap(t, rr)["success"].(bool); ok {
		t.Fatal("an unknown authority must not import")
	}
}

func TestDatRefreshUnknownAuthorityIs404(t *testing.T) {
	env := newTestEnv(t, nil)
	wantStatus(t, env.do("POST", "/api/dat/authorities/tosec/refresh", ""), http.StatusNotFound)
}

func TestDatRefreshRejectsAHandFedAuthority(t *testing.T) {
	env := newTestEnv(t, nil)
	// MAME ships dormant on the upload driver.
	rr := env.do("POST", "/api/dat/authorities/mame/refresh", "")
	wantStatus(t, rr, http.StatusBadRequest)
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "upload") {
		t.Fatalf("error = %q, want it to point at the upload endpoint", msg)
	}
}

// The two source rules the whole design leans on, enforced at the edge.
func TestDatAuthorityPatchRefusesBannedSources(t *testing.T) {
	env := newTestEnv(t, nil)
	for _, base := range []string{
		"https://datomatic.no-intro.org/",
		"https://redump.org/datfile/",
		"not-a-url",
		"/relative/path/",
	} {
		rr := env.do("PATCH", "/api/dat/authorities/redump", `{"fetch_base":"`+base+`"}`)
		wantStatus(t, rr, http.StatusBadRequest)
	}
	// Repointing at a mirror stays a data edit.
	rr := env.do("PATCH", "/api/dat/authorities/redump", `{"fetch_base":"http://127.0.0.1:9/datfile/"}`)
	wantStatus(t, rr, 200)
	auth, err := env.jobs.GetDatAuthority("redump")
	if err != nil || auth.FetchBase != "http://127.0.0.1:9/datfile/" {
		t.Fatalf("fetch_base = %q (%v), want the repointed base", auth.FetchBase, err)
	}
}

func TestDatAuthorityPatchWarnsWhenADriverChangeInvalidatesAssignments(t *testing.T) {
	env := newTestEnv(t, nil)
	// no-intro's eleven assignments carry DAT names, which no Redump code
	// rule accepts — the operator gets told rather than silently broken.
	rr := env.do("PATCH", "/api/dat/authorities/no-intro",
		`{"fetch_driver":"redump","fetch_base":"https://redump.info/datfile/"}`)
	wantStatus(t, rr, 200)
	warnings, _ := decodeMap(t, rr)["warnings"].([]interface{})
	if len(warnings) < 11 {
		t.Fatalf("warnings = %v, want every now-invalid assignment reported", warnings)
	}
}

func TestDatPlatformAssignmentValidatesThePair(t *testing.T) {
	env := newTestEnv(t, nil)
	// A Redump row carrying a No-Intro DAT name would 404 forever at fetch.
	rr := env.do("PUT", "/api/dat/platforms/ps3", `{"authority":"redump","dat_code":"Sony - PlayStation 3"}`)
	wantStatus(t, rr, http.StatusBadRequest)

	rr = env.do("PUT", "/api/dat/platforms/ps3", `{"authority":"redump","dat_code":"ps3"}`)
	wantStatus(t, rr, 200)

	rr = env.do("PUT", "/api/dat/platforms/Bad_Slug", `{"authority":"redump","dat_code":"ps3"}`)
	wantStatus(t, rr, http.StatusBadRequest)
}

// Coverage must never look like completion: the two counts are independent
// until owned files are matched against catalog entries, which v1 does not do.
func TestDatCoverageNeverImpliesCompletion(t *testing.T) {
	env := newTestEnv(t, nil)
	rr := env.do("GET", "/api/dat/coverage", "")
	wantStatus(t, rr, 200)
	raw := rr.Body.String()
	for _, forbidden := range []string{"percent", "complete", "verified", "missing"} {
		if strings.Contains(strings.ToLower(raw), forbidden) {
			t.Fatalf("coverage body contains %q: %s", forbidden, raw)
		}
	}
	var resp struct {
		Coverage []struct {
			Summary string `json:"summary"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, row := range resp.Coverage {
		if !strings.Contains(row.Summary, "owned") || !strings.Contains(row.Summary, "known") {
			t.Fatalf("summary = %q, want the owned/known rendering", row.Summary)
		}
	}
}

func TestDatStatusIsServedBeforeAnyRun(t *testing.T) {
	env := newTestEnv(t, nil)
	rr := env.do("GET", "/api/dat/status", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if running, _ := body["running"].(bool); running {
		t.Fatal("a fresh service must not report a run in flight")
	}
	if enabled, _ := body["enabled"].(bool); enabled {
		t.Fatal("the cadence must ship off")
	}
}

func TestDatEndpointsSurviveANilService(t *testing.T) {
	// normalize_test.go builds a router with nils; a missing service must
	// answer, not panic.
	env := newTestEnv(t, nil)
	env.router = NewRouter(env.cfg, env.mgr, nil, nil, nil, nil, nil)
	wantStatus(t, env.do("POST", "/api/dat/authorities/no-intro/refresh", ""), http.StatusServiceUnavailable)
	wantStatus(t, env.do("GET", "/api/dat/status", ""), 200)
}

// A settingSpec without its line in the hand-written handleGetSettings map
// is a silently invisible knob — the UI would render nothing and a PUT would
// look like it did nothing.
func TestDatCadenceKnobsAreVisibleAndValidated(t *testing.T) {
	env := newTestEnv(t, nil)

	body := decodeMap(t, env.do("GET", "/api/settings", ""))
	if _, ok := body["dat_auto_refresh_enabled"]; !ok {
		t.Fatal("dat_auto_refresh_enabled missing from the settings document")
	}
	if _, ok := body["dat_refresh_interval_days"]; !ok {
		t.Fatal("dat_refresh_interval_days missing from the settings document")
	}
	if on, _ := body["dat_auto_refresh_enabled"].(bool); on {
		t.Fatal("the cadence must default off")
	}

	wantStatus(t, env.do("PUT", "/api/settings", `{"dat_refresh_interval_days":0}`), http.StatusBadRequest)

	rr := env.do("PUT", "/api/settings", `{"dat_auto_refresh_enabled":true,"dat_refresh_interval_days":7}`)
	wantStatus(t, rr, 200)
	updated := decodeMap(t, rr)
	if on, _ := updated["dat_auto_refresh_enabled"].(bool); !on {
		t.Fatal("the toggle did not persist")
	}
	if days, _ := updated["dat_refresh_interval_days"].(float64); days != 7 {
		t.Fatalf("interval = %v, want 7", updated["dat_refresh_interval_days"])
	}
}
