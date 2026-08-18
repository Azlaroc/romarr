package api

import (
	"net/http"
	"strings"
	"testing"

	"gamarr/internal/db"
)

// seedHashOwned adds a library row carrying both hash families so gate tests
// can match against either.
func seedHashOwned(t *testing.T, env *testEnv) {
	t.Helper()
	if _, err := env.jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Hagane - The Final Conflict (USA)", Platform: "SNES", PlatformSlug: "snes",
		FilePath: "/roms/snes/Hagane - The Final Conflict (USA)", FileSize: 1,
		Source: "romm", SourceType: "romm", SourceID: "romm:hagane",
		Metadata: `{"romm":{"md5":"aa11","sha1":"bb22"},"gamarr":{"md5":"dd44"}}`,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadHashGate(t *testing.T) {
	ddlBody := func(extra string) string {
		return `{"source_type":"ddl","download_url":"http://127.0.0.1:9/rom.zip",
			"title":"Different Name","platform":"SNES","platform_slug":"snes"` + extra + `}`
	}

	t.Run("hash match blocks with 409", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", ddlBody(`,"md5":"AA11"`))
		wantStatus(t, rr, http.StatusConflict)
		resp := decodeMap(t, rr)
		if resp["code"] != "duplicate_hash" || resp["success"] != false {
			t.Errorf("resp = %v", resp)
		}
		owned, _ := resp["owned"].(map[string]interface{})
		if owned == nil || owned["title"] != "Hagane - The Final Conflict (USA)" {
			t.Errorf("owned = %v", owned)
		}
		if n := len(env.jobs.Items()); n != 0 {
			t.Errorf("jobs = %d, want 0 (blocked download must not create a job)", n)
		}
	})

	t.Run("cross-platform hash match still blocks", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", `{"source_type":"ddl","download_url":"http://127.0.0.1:9/rom.zip",
			"title":"Different Name","platform":"GBA","platform_slug":"gba","sha1":"bb22"}`)
		wantStatus(t, rr, http.StatusConflict)
	})

	t.Run("gamarr-family hash blocks", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", ddlBody(`,"md5":"DD44"`))
		wantStatus(t, rr, http.StatusConflict)
	})

	t.Run("force bypasses the gate", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", ddlBody(`,"md5":"aa11","force":true`))
		wantStatus(t, rr, http.StatusOK)
		resp := decodeMap(t, rr)
		if resp["success"] != true || resp["job_id"] == "" {
			t.Errorf("resp = %v", resp)
		}
	})

	t.Run("hashless request passes the gate", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", ddlBody(``))
		wantStatus(t, rr, http.StatusOK)
	})

	t.Run("title match stays a warning", func(t *testing.T) {
		env := newTestEnv(t, nil)
		seedHashOwned(t, env)
		rr := env.do("POST", "/api/download", `{"source_type":"ddl","download_url":"http://127.0.0.1:9/rom.zip",
			"title":"Hagane - The Final Conflict (USA)","platform":"SNES","platform_slug":"snes"}`)
		wantStatus(t, rr, http.StatusOK)
		resp := decodeMap(t, rr)
		if w, _ := resp["warning"].(string); !strings.Contains(w, "already exists") {
			t.Errorf("warning = %v", resp["warning"])
		}
	})
}
