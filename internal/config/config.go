// Package config loads Gamarr's runtime configuration from environment
// variables, including the source-driver registry consumed by search drivers.
package config

import (
	"os"
	"strconv"
	"strings"

	"gamarr/internal/sources"
)

type Config struct {
	// Sources is the runtime source-driver registry. Drivers read base URLs
	// and per-platform mappings from here instead of from hardcoded constants.
	// Always non-nil after Load() returns.
	Sources *sources.Registry

	// Prowlarr
	ProwlarrURL    string
	ProwlarrAPIKey string

	// qBittorrent
	QBURL      string
	QBUser     string
	QBPass     string
	QBSavePath string
	QBCategory string

	// Library destination paths
	GamesVaultPath string
	GamesRomsPath  string

	// Data directory
	DataDir string

	// ClamAV
	ClamAVContainer string
	ClamAVSocket    string
	DockerSocket    string

	// Experimental
	ExtractArchives bool

	// ROM conversion (rom-converto). The binary is baked into the image;
	// ConvertoBin lets a deployment override the path. ConvertoAPIBase points
	// the dat/Playmatch verbs at a self-hosted instance (empty = public).
	ConvertoBin        string
	ConvertoAPIBase    string
	ConvertoTimeoutSec int

	// NormalizeROMs gates the F5 normalize step (DAT 1G1R rename + multi-disc
	// .m3u) on import. Default off — the settings toggle is the runtime switch;
	// this is only the initial value LoadSettings falls back to.
	NormalizeROMs bool

	// ConvertROMs gates the F5 convert step (disc systems → CHD, verify before
	// replacing the source). Default off; the settings toggle is the runtime
	// switch. Initial fallback value only.
	ConvertROMs bool

	// SABnzbd (Usenet)
	SABnzbdURL      string
	SABnzbdAPIKey   string
	SABnzbdCategory string

	// External services
	RomMURL string

	// RomM API (library ownership sync; RomMURL doubles as the API base)
	RomMAPIUser          string
	RomMAPIPass          string
	RomMSyncEnabled      bool
	RomMSyncIntervalS    int
	RomMExcludePlatforms []string
	// RomMConnectEnabled turns on the Connect plane: after each ROM import a
	// targeted RomM scan is triggered over socket.io. Needs a RomM account
	// with the tasks.run scope (admin role in RomM 5.x); with lesser roles
	// the notifier degrades to warnings and RomM simply isn't notified.
	RomMConnectEnabled bool
	// RomMConnectFlushIdleS / RomMConnectTickS override the notifier's
	// coalescing window and poll interval in seconds (0 = built-in defaults:
	// 120/30). Mostly for tests and impatient setups.
	RomMConnectFlushIdleS int
	RomMConnectTickS      int

	// Webhooks (default from env, additional via DB)
	WebhookURL  string
	WebhookType string // "discord" or "generic"

	// Scheduler
	SchedulerEnabled        bool
	SchedulerIntervalHours  int
	SchedulerAutoDownload   bool
	SchedulerMinScore       int
	SelectorMode            string // off | shadow | enforce
	SelectorSetTimeoutHours int    // disc-set degrade timeout; read via DiscSetTimeoutHours()

	// Retry
	MaxRetries          int
	RetryBackoffSeconds int

	// Auth
	AuthUsername string
	AuthPassword string
	APIKey       string

	// OIDC / SSO
	OIDCEnabled         bool
	OIDCProviderName    string
	OIDCIssuer          string
	OIDCClientID        string
	OIDCClientSecret    string
	OIDCRedirectURI     string
	OIDCAutoCreateUsers bool
	OIDCDefaultRole     string

	// Circuit breaker
	CircuitBreakerThreshold int
	CircuitBreakerTimeoutS  int

	// Torrent completion watcher
	WatcherEnabled    bool
	WatcherIntervalS  int
	RemoveAfterImport bool
	// SeedJanitorEnabled lets the watcher delete (torrent AND files) imported
	// torrents that qBittorrent's own share limits have stopped
	// (stoppedUP/pausedUP). Off by default: deletion is unrecoverable.
	SeedJanitorEnabled bool

	// Server
	Port           int
	MetricsEnabled bool

	// Torznab — when set, Gamarr exposes /torznab/api (and /api alias) as
	// a Torznab indexer so Prowlarr / Sonarr / other *arr apps can query
	// Gamarr for game results. Empty key disables the API-key check.
	TorznabAPIKey string

	// Prowlarr game indexer IDs
	ProwlarrGameIndexers []int

	// Rename on import
	RenameEnabled bool
	RenamePattern string

	// Auto-upgrade
	AutoUpgradeEnabled bool
}

func Load() *Config {
	registry := sources.Load(
		envStr("GAMARR_SOURCES_PATH", ""),
		envStr("GAMARR_SOURCES_URL", ""),
	).ApplyEnvOverrides(os.Getenv)

	return &Config{
		Sources: registry,

		ProwlarrURL:    envStr("PROWLARR_URL", "http://prowlarr:9696"),
		ProwlarrAPIKey: envStr("PROWLARR_API_KEY", ""),

		QBURL:      envStrAllowEmpty("QB_URL", "http://qbittorrent:8080"),
		QBUser:     envStr("QB_USER", "admin"),
		QBPass:     envStr("QB_PASS", ""),
		QBSavePath: envStr("QB_SAVE_PATH", "/data/incoming/"),
		QBCategory: envStr("QB_CATEGORY", "games"),

		GamesVaultPath: envStr("GAMES_VAULT_PATH", "/data/vault"),
		GamesRomsPath:  envStr("GAMES_ROMS_PATH", "/data/roms"),

		DataDir: envStr("DATA_DIR", "/data/gamarr"),

		ClamAVContainer: envStr("CLAMAV_CONTAINER", "clamav"),
		ClamAVSocket:    envStr("CLAMAV_SOCKET", "/run/clamav/clamd.sock"),
		DockerSocket:    envStr("DOCKER_SOCKET", "/var/run/docker.sock"),

		ExtractArchives: envBool("EXTRACT_ARCHIVES", false),

		ConvertoBin:        envStr("CONVERTO_BIN", "rom-converto"),
		ConvertoAPIBase:    envStr("CONVERTO_API_BASE", ""),
		ConvertoTimeoutSec: envInt("CONVERTO_TIMEOUT_SEC", 3600),

		NormalizeROMs: envBool("NORMALIZE_ROMS", false),
		ConvertROMs:   envBool("CONVERT_ROMS", false),

		SABnzbdURL:      envStr("SABNZBD_URL", ""),
		SABnzbdAPIKey:   envStr("SABNZBD_API_KEY", ""),
		SABnzbdCategory: envStr("SABNZBD_CATEGORY", "games"),

		RomMURL: envStr("ROMM_URL", ""),

		RomMAPIUser:           envStr("ROMM_API_USER", ""),
		RomMAPIPass:           envStr("ROMM_API_PASS", ""),
		RomMSyncEnabled:       envBool("ROMM_SYNC_ENABLED", true),
		RomMSyncIntervalS:     envInt("ROMM_SYNC_INTERVAL", 1800),
		RomMExcludePlatforms:  envStrSlice("ROMM_EXCLUDE_PLATFORMS"),
		RomMConnectEnabled:    envBool("ROMM_CONNECT_ENABLED", true),
		RomMConnectFlushIdleS: envInt("ROMM_CONNECT_FLUSH_IDLE", 0),
		RomMConnectTickS:      envInt("ROMM_CONNECT_TICK", 0),

		WebhookURL:  envStr("WEBHOOK_URL", ""),
		WebhookType: envStr("WEBHOOK_TYPE", "generic"),

		SchedulerEnabled:        envBool("SCHEDULER_ENABLED", false),
		SchedulerIntervalHours:  envInt("SCHEDULER_INTERVAL_HOURS", 24),
		SchedulerAutoDownload:   envBool("SCHEDULER_AUTO_DOWNLOAD", true),
		SchedulerMinScore:       envInt("SCHEDULER_MIN_SCORE", 70),
		SelectorMode:            envStr("SELECTOR_MODE", "shadow"),
		SelectorSetTimeoutHours: envInt("SELECTOR_SET_TIMEOUT_HOURS", 24),

		AuthUsername: envStr("AUTH_USERNAME", ""),
		AuthPassword: envStr("AUTH_PASSWORD", ""),
		APIKey:       envStr("API_KEY", ""),

		OIDCEnabled:         envBool("OIDC_ENABLED", false),
		OIDCProviderName:    envStr("OIDC_PROVIDER_NAME", "SSO"),
		OIDCIssuer:          envStr("OIDC_ISSUER", ""),
		OIDCClientID:        envStr("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:    envStr("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURI:     envStr("OIDC_REDIRECT_URI", ""),
		OIDCAutoCreateUsers: envBool("OIDC_AUTO_CREATE_USERS", true),
		OIDCDefaultRole:     envStr("OIDC_DEFAULT_ROLE", "user"),

		CircuitBreakerThreshold: envInt("CIRCUIT_BREAKER_THRESHOLD", 3),
		CircuitBreakerTimeoutS:  envInt("CIRCUIT_BREAKER_TIMEOUT", 300),

		WatcherEnabled:     envBool("WATCHER_ENABLED", true),
		WatcherIntervalS:   envInt("WATCHER_INTERVAL", 30),
		RemoveAfterImport:  envBool("REMOVE_TORRENT_AFTER_IMPORT", false),
		SeedJanitorEnabled: envBool("SEED_JANITOR_ENABLED", false),

		MaxRetries:          envInt("MAX_RETRIES", 2),
		RetryBackoffSeconds: envInt("RETRY_BACKOFF_SECONDS", 60),

		Port:           envInt("GAMARR_PORT", 5001),
		MetricsEnabled: envBool("METRICS_ENABLED", true),
		TorznabAPIKey:  envStr("TORZNAB_API_KEY", ""),

		// Empty (the default) means unscoped Prowlarr search across all
		// configured indexers; set IDs only to restrict the search.
		ProwlarrGameIndexers: envIntSlice("PROWLARR_GAME_INDEXERS", nil),

		RenameEnabled: envBool("RENAME_ENABLED", false),
		RenamePattern: envStr("RENAME_PATTERN", "{title} ({platform}).{ext}"),

		AutoUpgradeEnabled: envBool("AUTO_UPGRADE_ENABLED", false),
	}
}

func (c *Config) HasAuth() bool {
	return c.AuthUsername != "" && c.AuthPassword != ""
}

func (c *Config) HasAPIKey() bool {
	return c.APIKey != ""
}

func (c *Config) HasProwlarr() bool {
	return c.ProwlarrURL != "" && c.ProwlarrAPIKey != ""
}

func (c *Config) HasQBittorrent() bool {
	return c.QBURL != ""
}

func (c *Config) HasSABnzbd() bool {
	return c.SABnzbdURL != "" && c.SABnzbdAPIKey != ""
}

// HasRomMAPI reports whether the RomM REST client can be constructed. RomMURL
// alone only powers the UI link-out; the API needs credentials too.
func (c *Config) HasRomMAPI() bool {
	return c.RomMURL != "" && c.RomMAPIUser != "" && c.RomMAPIPass != ""
}

func (c *Config) HasOIDC() bool {
	return c.OIDCEnabled && c.OIDCIssuer != "" && c.OIDCClientID != ""
}

func (c *Config) HasClamAV() bool {
	if c.DockerSocket == "" {
		return false
	}
	_, err := os.Stat(c.DockerSocket)
	return err == nil
}

// DiscSetTimeoutHours returns SelectorSetTimeoutHours normalized to the
// historical 24h default when unset or invalid — zero-value Configs (tests
// build bare &Config{} literals) must not degrade disc sets instantly.
func (c *Config) DiscSetTimeoutHours() int {
	if c.SelectorSetTimeoutHours <= 0 {
		return 24
	}
	return c.SelectorSetTimeoutHours
}

// StaleJobHorizonHours returns the CleanupStaleDownloads horizon: never below
// the historical 24h, and never below the disc-set timeout. A stale-deleted
// pending set member zeroes the sweep's pending count and fires the degrade
// arm early, which would silently cap SELECTOR_SET_TIMEOUT_HOURS at ~24h.
func (c *Config) StaleJobHorizonHours() int {
	if h := c.DiscSetTimeoutHours(); h > 24 {
		return h
	}
	return 24
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envStrAllowEmpty distinguishes an unset variable from an explicitly empty
// one. This lets deployments disable integrations which otherwise have a
// non-empty default (notably QB_URL).
func envStrAllowEmpty(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	s := strings.ToLower(os.Getenv(key))
	if s == "" {
		return fallback
	}
	return s == "true" || s == "1" || s == "yes"
}

func envStrSlice(key string) []string {
	s := os.Getenv(key)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func envIntSlice(key string, fallback []int) []int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	parts := strings.Split(s, ",")
	result := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		result = append(result, n)
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}
