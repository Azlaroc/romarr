package search

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gamarr/internal/sources"
)

// recordingServer is an httptest.Server that captures every path it receives.
type recordingServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newRecordingServer(t *testing.T, status int, body string) *recordingServer {
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.paths = append(rs.paths, r.URL.RequestURI())
		rs.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(rs.Server.Close)
	return rs
}

func (rs *recordingServer) hit() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.paths) > 0
}

func (rs *recordingServer) requestURIs() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]string, len(rs.paths))
	copy(out, rs.paths)
	return out
}

// TestRegistryFlow proves that each refactored driver issues its HTTP request
// against the URL recorded in the runtime sources registry — i.e. the
// registry value actually flows into the wire request, with no hardcoded URL
// shadowing it.
func TestRegistryFlow(t *testing.T) {
	// Reset the package-level circuit breakers so prior tests don't bleed in.
	t.Cleanup(func() {
		RecordSearchSuccess("vimm")
	})

	t.Run("vimm hits reg.Vimm.BaseURL", func(t *testing.T) {
		srv := newRecordingServer(t, 200, `<html><body></body></html>`)
		reg, _ := sources.Default()
		reg.Vimm.BaseURL = srv.URL + "/"
		// Force a recognized platform so the system param is set.
		reg.Vimm.PlatformSystems = map[string]string{"nes": "NES"}

		_ = SearchVimm(reg, "game", "nes")
		if !srv.hit() {
			t.Fatalf("Vimm did not call the registry URL")
		}
		got := srv.requestURIs()[0]
		if !strings.Contains(got, "q=game") {
			t.Errorf("expected ?q=game in URL, got %q", got)
		}
		if !strings.Contains(got, "system=NES") {
			t.Errorf("expected system=NES (from registry platform_systems), got %q", got)
		}
	})

	t.Run("vimm GUID is built from reg.Vimm.BaseURL", func(t *testing.T) {
		srv := newRecordingServer(t, 200, `<a href="/vault/42">A Game (NES)</a>`)
		reg, _ := sources.Default()
		reg.Vimm.BaseURL = srv.URL + "/"
		reg.Vimm.PlatformSystems = map[string]string{"nes": "NES"}

		results := SearchVimm(reg, "game", "nes")
		if len(results) == 0 {
			t.Fatal("expected at least one parsed result")
		}
		wantPrefix := srv.URL + "/"
		if !strings.HasPrefix(results[0].GUID, wantPrefix) {
			t.Errorf("expected GUID built from registry base URL %q, got %q", wantPrefix, results[0].GUID)
		}
	})
}

// TestRegistryFlow_LegacyEnvOverride confirms the VIMM_URL env var still takes
// precedence over the registry value when set.
func TestRegistryFlow_LegacyEnvOverride(t *testing.T) {
	cases := []struct {
		name   string
		envKey string
		envVal string
		check  func(*sources.Registry) string
		want   string
	}{
		{"VIMM_URL overrides Vimm.BaseURL", "VIMM_URL", "https://vimm-override.test/",
			func(r *sources.Registry) string { return r.Vimm.BaseURL }, "https://vimm-override.test/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, _ := sources.Default()
			reg.ApplyEnvOverrides(func(k string) string {
				if k == tc.envKey {
					return tc.envVal
				}
				return ""
			})
			if got := tc.check(reg); got != tc.want {
				t.Errorf("after ApplyEnvOverrides(%s=%s): got %q, want %q", tc.envKey, tc.envVal, got, tc.want)
			}
		})
	}
}

// TestSourceEnableGuards proves the drivers are inert — no HTTP issued, nil
// results — when the registry disables them, maps no platforms, or (Vimm)
// has no system mapping for the requested platform. The dead-base_url
// workaround these guards replace relied on connection failures, which
// burned circuit-breaker state and logged warnings every cycle.
func TestSourceEnableGuards(t *testing.T) {
	t.Cleanup(func() {
		RecordSearchSuccess("vimm")
		RecordSearchSuccess("archiveorg")
	})
	off := false

	cases := []struct {
		name string
		reg  func(base string) *sources.Registry
		slug string
	}{
		{"vimm disabled", func(base string) *sources.Registry {
			return &sources.Registry{Vimm: sources.VimmSpec{BaseURL: base, Enabled: &off,
				PlatformSystems: map[string]string{"snes": "SNES"}}}
		}, "snes"},
		{"vimm empty platform map", func(base string) *sources.Registry {
			return &sources.Registry{Vimm: sources.VimmSpec{BaseURL: base}}
		}, "snes"},
		{"vimm unmapped platform", func(base string) *sources.Registry {
			return &sources.Registry{Vimm: sources.VimmSpec{BaseURL: base,
				PlatformSystems: map[string]string{"snes": "SNES"}}}
		}, "psx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := newRecordingServer(t, 200, "<html></html>")
			got := SearchVimm(tc.reg(rs.URL), "mario", tc.slug)
			if got != nil {
				t.Errorf("results = %d, want none", len(got))
			}
			if rs.hit() {
				t.Error("guarded Vimm search still issued HTTP")
			}
		})
	}

	t.Run("vimm nil registry", func(t *testing.T) {
		if got := SearchVimm(nil, "mario", "snes"); got != nil {
			t.Errorf("nil registry: results = %d, want none", len(got))
		}
	})

	t.Run("archiveorg disabled", func(t *testing.T) {
		rs := newRecordingServer(t, 200, "{}")
		reg := &sources.Registry{ArchiveOrg: sources.ArchiveOrgSpec{BaseURL: rs.URL, Enabled: &off,
			Items: map[string]string{"psx": "some-item"}}}
		if got := SearchArchiveOrg(reg, "mario", "psx"); got != nil {
			t.Errorf("results = %d, want none", len(got))
		}
		if rs.hit() {
			t.Error("disabled archive.org search still issued HTTP")
		}
	})

	// Positive control: enabled + mapped issues the request (the guards must
	// not overshoot). TestRegistryFlow covers the full positive wire path;
	// this pins that an explicit enabled:true behaves like the absent field.
	t.Run("vimm explicitly enabled", func(t *testing.T) {
		on := true
		rs := newRecordingServer(t, 200, "<html></html>")
		reg := &sources.Registry{Vimm: sources.VimmSpec{BaseURL: rs.URL, Enabled: &on,
			PlatformSystems: map[string]string{"snes": "SNES"}}}
		SearchVimm(reg, "mario", "snes")
		if !rs.hit() {
			t.Error("enabled Vimm search issued no HTTP")
		}
	})
}
