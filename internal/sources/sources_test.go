package sources

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_EmbeddedRegistryIsComplete(t *testing.T) {
	r, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	checks := map[string]bool{
		"Vimm.BaseURL":         r.Vimm.BaseURL == "",
		"Vimm.PlatformSystems": len(r.Vimm.PlatformSystems) == 0,
		"ArchiveOrg.BaseURL":   r.ArchiveOrg.BaseURL == "",
	}
	for field, empty := range checks {
		if empty {
			t.Errorf("embedded registry: %s is empty", field)
		}
	}
	// Spot-check a platform mapping is preserved.
	if r.Vimm.PlatformSystems["psx"] != "PS1" {
		t.Errorf("Vimm.PlatformSystems[psx] missing or wrong: %q", r.Vimm.PlatformSystems["psx"])
	}
	// ArchiveOrg items are intentionally empty in the embedded defaults so the
	// driver stays inert until an operator opts a platform in.
	if len(r.ArchiveOrg.Items) != 0 {
		t.Errorf("expected empty ArchiveOrg.Items in embedded defaults, got %d", len(r.ArchiveOrg.Items))
	}
}

func TestLoad(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":3,"vimm":{"base_url":"https://url-vimm.test/","platform_systems":{"foo":"FOO"}}}`))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	dir := t.TempDir()
	goodFile := filepath.Join(dir, "good.json")
	_ = os.WriteFile(goodFile, []byte(`{"version":2,"vimm":{"base_url":"https://file-vimm.test/","platform_systems":{"abc":"ABC"}}}`), 0o644)
	brokenFile := filepath.Join(dir, "broken.json")
	_ = os.WriteFile(brokenFile, []byte(`{not json`), 0o644)

	cases := []struct {
		name        string
		path, url   string
		wantVimm    string // "" => embedded fallback expected
		wantVersion int    // 0 => don't care
	}{
		{"empty -> embedded", "", "", "", 0},
		{"file overrides", goodFile, "", "https://file-vimm.test/", 2},
		{"url overrides", "", good.URL, "https://url-vimm.test/", 3},
		{"path beats url", goodFile, good.URL, "https://file-vimm.test/", 2},
		{"bad url falls back to embedded", "", bad.URL, "", 0},
		{"bad file falls back to embedded", brokenFile, "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Load(tc.path, tc.url)
			if r == nil {
				t.Fatal("Load returned nil")
			}
			if tc.wantVimm == "" {
				// Embedded fallback expected.
				if r.Vimm.BaseURL == "" {
					t.Errorf("expected embedded Vimm.BaseURL, got empty")
				}
				return
			}
			// A loaded file/url is a full replace of the registry: the value
			// comes wholly from the source, defaults do not bleed through.
			if r.Vimm.BaseURL != tc.wantVimm {
				t.Errorf("Vimm.BaseURL = %q, want %q", r.Vimm.BaseURL, tc.wantVimm)
			}
			if tc.wantVersion > 0 && r.Version != tc.wantVersion {
				t.Errorf("Version = %d, want %d", r.Version, tc.wantVersion)
			}
		})
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	cases := []struct {
		name     string
		envs     map[string]string
		wantVimm string // "" => embedded default
	}{
		{"unset leaves values", nil, ""},
		{"VIMM_URL overrides", map[string]string{"VIMM_URL": "https://vimm-override.test/"}, "https://vimm-override.test/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := Default()
			origV := r.Vimm.BaseURL
			r.ApplyEnvOverrides(func(k string) string { return tc.envs[k] })
			wantV := tc.wantVimm
			if wantV == "" {
				wantV = origV
			}
			if r.Vimm.BaseURL != wantV {
				t.Errorf("Vimm.BaseURL = %q, want %q", r.Vimm.BaseURL, wantV)
			}
		})
	}
}

func TestEnabledFlagSemantics(t *testing.T) {
	// Absent field → enabled: registry files written before the flag existed
	// keep working unchanged.
	var legacy Registry
	if err := json.Unmarshal([]byte(`{
		"version": 1,
		"vimm": {"base_url": "v", "platform_systems": {"snes": "SNES"}},
		"archiveorg": {"base_url": "a", "items": {"psx": "item"}}
	}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.Vimm.IsEnabled() || !legacy.ArchiveOrg.IsEnabled() {
		t.Error("absent enabled field must mean enabled")
	}
	if !legacy.VimmActive() || !legacy.ArchiveOrgActive() {
		t.Error("legacy registry with mappings must be active")
	}

	// Explicit false → disabled, regardless of mappings.
	var off Registry
	if err := json.Unmarshal([]byte(`{
		"version": 1,
		"vimm": {"base_url": "v", "enabled": false, "platform_systems": {"snes": "SNES"}},
		"archiveorg": {"base_url": "a", "enabled": false, "items": {"psx": "item"}}
	}`), &off); err != nil {
		t.Fatal(err)
	}
	if off.Vimm.IsEnabled() || off.ArchiveOrg.IsEnabled() {
		t.Error("enabled:false must disable")
	}
	if off.VimmActive() || off.ArchiveOrgActive() {
		t.Error("disabled specs must not be active")
	}

	// Enabled but nothing mapped → inactive (the driver can't produce
	// correctly-slugged results without a mapping).
	empty := Registry{Vimm: VimmSpec{BaseURL: "v"}, ArchiveOrg: ArchiveOrgSpec{BaseURL: "a"}}
	if empty.VimmActive() || empty.ArchiveOrgActive() {
		t.Error("specs with no mappings must not be active")
	}

	// Nil receiver tolerated.
	var nilReg *Registry
	if nilReg.VimmActive() || nilReg.ArchiveOrgActive() {
		t.Error("nil registry must not be active")
	}
}
