package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear relevant env vars
	for _, k := range []string{"PROWLARR_URL", "PROWLARR_API_KEY", "QB_URL", "QB_USER", "QB_PASS",
		"GAMARR_PORT", "MAX_RETRIES", "METRICS_ENABLED", "PROWLARR_GAME_INDEXERS",
		"EXTRACT_ARCHIVES", "SABNZBD_URL", "SABNZBD_API_KEY",
		"SELECTOR_SET_TIMEOUT_HOURS"} {
		os.Unsetenv(k)
	}

	cfg := Load()

	if cfg.ProwlarrURL != "http://prowlarr:9696" {
		t.Errorf("ProwlarrURL=%q", cfg.ProwlarrURL)
	}
	if cfg.QBUser != "admin" {
		t.Errorf("QBUser=%q", cfg.QBUser)
	}
	if cfg.Port != 5001 {
		t.Errorf("Port=%d, want 5001", cfg.Port)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries=%d, want 2", cfg.MaxRetries)
	}
	if !cfg.MetricsEnabled {
		t.Error("MetricsEnabled should default to true")
	}
	if cfg.ExtractArchives {
		t.Error("ExtractArchives should default to false")
	}
	if cfg.SelectorSetTimeoutHours != 24 {
		t.Errorf("SelectorSetTimeoutHours=%d, want 24", cfg.SelectorSetTimeoutHours)
	}

	// Default is empty = unscoped Prowlarr search across all indexers.
	if len(cfg.ProwlarrGameIndexers) != 0 {
		t.Fatalf("ProwlarrGameIndexers=%v, want empty", cfg.ProwlarrGameIndexers)
	}
}

func TestLoad_FromEnv(t *testing.T) {
	os.Setenv("GAMARR_PORT", "8080")
	os.Setenv("PROWLARR_API_KEY", "testkey123")
	os.Setenv("QB_USER", "jam")
	os.Setenv("QB_PASS", "secret")
	os.Setenv("PROWLARR_GAME_INDEXERS", "1,2,3")
	os.Setenv("EXTRACT_ARCHIVES", "1")
	os.Setenv("SELECTOR_SET_TIMEOUT_HOURS", "72")
	defer func() {
		os.Unsetenv("GAMARR_PORT")
		os.Unsetenv("PROWLARR_API_KEY")
		os.Unsetenv("QB_USER")
		os.Unsetenv("QB_PASS")
		os.Unsetenv("PROWLARR_GAME_INDEXERS")
		os.Unsetenv("EXTRACT_ARCHIVES")
		os.Unsetenv("SELECTOR_SET_TIMEOUT_HOURS")
	}()

	cfg := Load()

	if cfg.Port != 8080 {
		t.Errorf("Port=%d, want 8080", cfg.Port)
	}
	if cfg.ProwlarrAPIKey != "testkey123" {
		t.Errorf("ProwlarrAPIKey=%q", cfg.ProwlarrAPIKey)
	}
	if cfg.QBUser != "jam" {
		t.Errorf("QBUser=%q", cfg.QBUser)
	}
	if cfg.QBPass != "secret" {
		t.Errorf("QBPass=%q", cfg.QBPass)
	}
	if len(cfg.ProwlarrGameIndexers) != 3 || cfg.ProwlarrGameIndexers[0] != 1 {
		t.Errorf("ProwlarrGameIndexers=%v", cfg.ProwlarrGameIndexers)
	}
	if !cfg.ExtractArchives {
		t.Error("ExtractArchives should be true from env '1'")
	}
	if cfg.SelectorSetTimeoutHours != 72 {
		t.Errorf("SelectorSetTimeoutHours=%d, want 72", cfg.SelectorSetTimeoutHours)
	}
}

func TestDiscSetTimeoutAccessors(t *testing.T) {
	cases := []struct {
		raw         int
		wantTimeout int
		wantHorizon int
	}{
		{0, 24, 24},  // zero-value Config (bare test fixtures) keeps the 24h default
		{-1, 24, 24}, // invalid → default
		{6, 6, 24},   // short timeout never shrinks the stale horizon below 24h
		{24, 24, 24},
		{72, 72, 72}, // long timeout widens the stale horizon with it
	}
	for _, tc := range cases {
		c := &Config{SelectorSetTimeoutHours: tc.raw}
		if got := c.DiscSetTimeoutHours(); got != tc.wantTimeout {
			t.Errorf("DiscSetTimeoutHours(%d)=%d, want %d", tc.raw, got, tc.wantTimeout)
		}
		if got := c.StaleJobHorizonHours(); got != tc.wantHorizon {
			t.Errorf("StaleJobHorizonHours(%d)=%d, want %d", tc.raw, got, tc.wantHorizon)
		}
	}
}

func TestLoad_ExplicitEmptyQBURLDisablesQBittorrent(t *testing.T) {
	t.Setenv("QB_URL", "")
	cfg := Load()
	if cfg.QBURL != "" {
		t.Fatalf("QBURL=%q, want empty", cfg.QBURL)
	}
	if cfg.HasQBittorrent() {
		t.Fatal("qBittorrent should be disabled by an explicitly empty QB_URL")
	}
}

func TestHasProwlarr(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		key    string
		expect bool
	}{
		{"both set", "http://prowlarr:9696", "key", true},
		{"no key", "http://prowlarr:9696", "", false},
		{"no url", "", "key", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ProwlarrURL: tt.url, ProwlarrAPIKey: tt.key}
			if cfg.HasProwlarr() != tt.expect {
				t.Errorf("HasProwlarr()=%v, want %v", cfg.HasProwlarr(), tt.expect)
			}
		})
	}
}

func TestHasQBittorrent(t *testing.T) {
	cfg := &Config{QBURL: "http://qbit:8080"}
	if !cfg.HasQBittorrent() {
		t.Error("expected true when URL is set")
	}
	cfg.QBURL = ""
	if cfg.HasQBittorrent() {
		t.Error("expected false when URL is empty")
	}
}

func TestHasSABnzbd(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		key    string
		expect bool
	}{
		{"both set", "http://sab:8080", "apikey", true},
		{"no key", "http://sab:8080", "", false},
		{"no url", "", "apikey", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{SABnzbdURL: tt.url, SABnzbdAPIKey: tt.key}
			if cfg.HasSABnzbd() != tt.expect {
				t.Errorf("HasSABnzbd()=%v, want %v", cfg.HasSABnzbd(), tt.expect)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		value    string
		fallback bool
		expect   bool
	}{
		{"true", false, true},
		{"TRUE", false, true},
		{"1", false, true},
		{"yes", false, true},
		{"false", true, false},
		{"no", true, false},
		{"0", true, false},
		{"", false, false},
		{"", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			os.Setenv("TEST_BOOL", tt.value)
			defer os.Unsetenv("TEST_BOOL")
			got := envBool("TEST_BOOL", tt.fallback)
			if got != tt.expect {
				t.Errorf("envBool(%q, %v) = %v, want %v", tt.value, tt.fallback, got, tt.expect)
			}
		})
	}
}

func TestEnvIntSlice(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback []int
		expect   []int
	}{
		{"valid", "1,2,3", nil, []int{1, 2, 3}},
		{"with spaces", " 1 , 2 , 3 ", nil, []int{1, 2, 3}},
		{"empty uses fallback", "", []int{7, 5}, []int{7, 5}},
		{"invalid uses fallback", "abc,def", []int{1}, []int{1}},
		{"mixed valid invalid", "1,abc,3", nil, []int{1, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TEST_SLICE", tt.value)
			defer os.Unsetenv("TEST_SLICE")
			got := envIntSlice("TEST_SLICE", tt.fallback)
			if len(got) != len(tt.expect) {
				t.Fatalf("got %v, want %v", got, tt.expect)
			}
			for i, v := range got {
				if v != tt.expect[i] {
					t.Errorf("got[%d]=%d, want %d", i, v, tt.expect[i])
				}
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	if got := envInt("TEST_INT", 0); got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	os.Setenv("TEST_INT", "not_a_number")
	if got := envInt("TEST_INT", 99); got != 99 {
		t.Errorf("got %d, want fallback 99", got)
	}

	os.Unsetenv("TEST_INT")
	if got := envInt("TEST_INT", 7); got != 7 {
		t.Errorf("got %d, want fallback 7", got)
	}
}

// fakeSettings is a map-backed SettingsSource for accessor tests.
type fakeSettings map[string]string

func (f fakeSettings) GetSetting(k string) (string, bool) {
	v, ok := f[k]
	return v, ok
}

func TestRuntimeSettingAccessors(t *testing.T) {
	c := &Config{SchedulerAutoDownload: true, SchedulerMinScore: 55}

	// No source attached: accessors return the env-derived fields.
	if c.RemoveTorrentAfterImport() {
		t.Error("RemoveTorrentAfterImport should default false")
	}
	if !c.AutoDownload() {
		t.Error("AutoDownload should reflect env true")
	}
	if c.MinScore() != 55 {
		t.Errorf("MinScore = %d, want env 55", c.MinScore())
	}

	// Stored rows win over env.
	c.AttachSettings(fakeSettings{
		"remove_torrent_after_import": "true",
		"seed_janitor_enabled":        "true",
		"scheduler_auto_download":     "false",
		"scheduler_min_score":         "85",
	})
	if !c.RemoveTorrentAfterImport() {
		t.Error("row true should win over env false")
	}
	if !c.SeedJanitor() {
		t.Error("SeedJanitor row true should win")
	}
	if c.AutoDownload() {
		t.Error("row false should win over env true")
	}
	if c.MinScore() != 85 {
		t.Errorf("MinScore = %d, want row 85", c.MinScore())
	}

	// Zero has never been a legal min score: row "0" resolves to 70.
	c.AttachSettings(fakeSettings{"scheduler_min_score": "0"})
	if c.MinScore() != 70 {
		t.Errorf("MinScore(0 row) = %d, want 70", c.MinScore())
	}

	// A garbage row is ignored; env value applies.
	c.AttachSettings(fakeSettings{"scheduler_min_score": "abc", "scheduler_auto_download": "maybe"})
	if c.MinScore() != 55 {
		t.Errorf("MinScore(garbage row) = %d, want env 55", c.MinScore())
	}
	if !c.AutoDownload() {
		t.Error("garbage bool row should fall back to env true")
	}
}

func TestAttachSettingsConcurrent(t *testing.T) {
	// Attachment is an atomic swap read from many goroutines; the -race gate
	// in CI is the real assertion here.
	c := &Config{SchedulerMinScore: 55}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			c.AttachSettings(fakeSettings{"scheduler_min_score": "85"})
		}
	}()
	for i := 0; i < 1000; i++ {
		if got := c.MinScore(); got != 55 && got != 85 {
			t.Fatalf("MinScore = %d, want 55 or 85", got)
		}
	}
	<-done
}
