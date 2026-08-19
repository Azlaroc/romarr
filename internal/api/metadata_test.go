package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gamarr/internal/config"
)

// igdbStubServer answers Twitch's token endpoint and IGDB's /games.
func igdbStubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"tok","expires_in":5000000}`))
	})
	mux.HandleFunc("/games", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1074,"name":"Chrono Trigger","slug":"chrono-trigger",
			"cover":{"url":"//images.igdb.com/igdb/image/upload/t_thumb/co2h5j.jpg"},
			"platforms":[{"id":19,"name":"SNES","slug":"snes"}]}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withIGDB(srv *httptest.Server) func(*config.Config) {
	return func(c *config.Config) {
		c.IGDBClientID = "client"
		c.IGDBClientSecret = "secret"
		c.IGDBAPIBase = srv.URL
		c.IGDBAuthBase = srv.URL
	}
}

// The settings screen has to be able to say "no provider is configured"
// without a search failing later to reveal it.
func TestMetadataProvidersReportsConfiguration(t *testing.T) {
	t.Run("unconfigured is an honest state", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/metadata/providers", "")
		wantStatus(t, rr, http.StatusOK)
		providers, _ := decodeMap(t, rr)["providers"].([]interface{})
		if len(providers) != 1 {
			t.Fatalf("providers = %v", providers)
		}
		p, _ := providers[0].(map[string]interface{})
		if p["name"] != "igdb" || p["configured"] != false {
			t.Errorf("provider = %v, want igdb reported unconfigured", p)
		}
	})

	t.Run("configured says so", func(t *testing.T) {
		env := newTestEnv(t, withIGDB(igdbStubServer(t)))
		rr := env.do("GET", "/api/metadata/providers", "")
		providers, _ := decodeMap(t, rr)["providers"].([]interface{})
		p, _ := providers[0].(map[string]interface{})
		if p["configured"] != true {
			t.Errorf("provider = %v, want configured", p)
		}
	})
}

func TestMetadataSearch(t *testing.T) {
	t.Run("without credentials it explains itself", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/metadata/search?q=chrono", "")
		wantStatus(t, rr, http.StatusServiceUnavailable)
		if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "IGDB_CLIENT_ID") {
			t.Errorf("error = %q, want it to name what is missing", msg)
		}
	})

	t.Run("an empty query is empty, not an error", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/metadata/search?q=", "")
		wantStatus(t, rr, http.StatusOK)
		games, _ := decodeMap(t, rr)["games"].([]interface{})
		if len(games) != 0 {
			t.Errorf("games = %v", games)
		}
	})

	t.Run("returns games with art and our platform slugs", func(t *testing.T) {
		env := newTestEnv(t, withIGDB(igdbStubServer(t)))
		rr := env.do("GET", "/api/metadata/search?q=chrono", "")
		wantStatus(t, rr, http.StatusOK)
		m := decodeMap(t, rr)
		if m["provider"] != "igdb" {
			t.Errorf("provider = %v", m["provider"])
		}
		games, _ := m["games"].([]interface{})
		if len(games) != 1 {
			t.Fatalf("games = %v", games)
		}
		g, _ := games[0].(map[string]interface{})
		if g["name"] != "Chrono Trigger" {
			t.Errorf("game = %v", g)
		}
		if cover, _ := g["cover_url"].(string); !strings.HasPrefix(cover, "https://") {
			t.Errorf("cover = %q, want an https URL", cover)
		}
		plats, _ := g["platforms"].([]interface{})
		if len(plats) != 1 || plats[0] != "snes" {
			t.Errorf("platforms = %v, want our slug", plats)
		}
	})
}
