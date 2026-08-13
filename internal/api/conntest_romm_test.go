package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gamarr/internal/config"
)

func TestConnectionTestRomM(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		env := newTestEnv(t, nil) // no RomM credentials
		rr := env.do("POST", "/api/test/romm", "")
		wantStatus(t, rr, 200)
		m := decodeMap(t, rr)
		if m["success"] != false || m["error"] != "Not configured" {
			t.Errorf("got %v, want success=false error=Not configured", m)
		}
	})

	t.Run("success with basic auth forwarded", func(t *testing.T) {
		var gotUser, gotPass string
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/platforms" {
				w.WriteHeader(404)
				return
			}
			gotUser, gotPass, _ = r.BasicAuth()
			w.Write([]byte(`[{"id": 1, "slug": "nes", "fs_slug": "nes", "name": "NES", "rom_count": 763}]`))
		}))
		defer mock.Close()

		env := newTestEnv(t, func(c *config.Config) {
			c.RomMURL = mock.URL
			c.RomMAPIUser = "romarr"
			c.RomMAPIPass = "pw"
		})
		rr := env.do("POST", "/api/test/romm", "")
		wantStatus(t, rr, 200)
		m := decodeMap(t, rr)
		if m["success"] != true {
			t.Errorf("got %v, want success=true", m)
		}
		if m["platforms"] != float64(1) {
			t.Errorf("got platforms=%v, want 1", m["platforms"])
		}
		if gotUser != "romarr" || gotPass != "pw" {
			t.Errorf("basic auth not forwarded: user=%q pass=%q", gotUser, gotPass)
		}
	})

	t.Run("bad credentials", func(t *testing.T) {
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer mock.Close()

		env := newTestEnv(t, func(c *config.Config) {
			c.RomMURL = mock.URL
			c.RomMAPIUser = "romarr"
			c.RomMAPIPass = "wrong"
		})
		rr := env.do("POST", "/api/test/romm", "")
		wantStatus(t, rr, 200)
		if m := decodeMap(t, rr); m["success"] != false {
			t.Errorf("got %v, want success=false", m)
		}
	})
}
