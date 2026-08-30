package api

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// settingSpec declares one runtime-settings key: its type and validation.
// PUT /api/settings accepts only declared keys; a JSON null deletes the
// stored row so the env-derived default applies again. Unknown keys are
// silently ignored (legacy clients send them).
type settingSpec struct {
	Key  string
	Kind string   // "bool" | "int" | "enum" | "string"
	Min  int      // int kind: minimum legal value
	Enum []string // enum kind: allowed values
}

var settingSpecs = []settingSpec{
	{Key: "extract_archives", Kind: "bool"},
	{Key: "normalize_roms", Kind: "bool"},
	{Key: "normalize_online_fallback", Kind: "bool"},
	{Key: "convert_roms", Kind: "bool"},
	{Key: "remove_torrent_after_import", Kind: "bool"},
	{Key: "seed_janitor_enabled", Kind: "bool"},
	{Key: "scheduler_auto_download", Kind: "bool"},
	{Key: "scheduler_min_score", Kind: "int", Min: 1},
	{Key: "watcher_enabled", Kind: "bool"},
	{Key: "watcher_interval_seconds", Kind: "int", Min: 1},
	{Key: "scheduler_enabled", Kind: "bool"},
	{Key: "scheduler_interval_hours", Kind: "int", Min: 1},
	{Key: "selector_mode", Kind: "enum", Enum: []string{"off", "shadow", "enforce"}},
	{Key: "selector_set_timeout_hours", Kind: "int", Min: 1},
	{Key: "romm_connect_enabled", Kind: "bool"},
	{Key: "dat_auto_refresh_enabled", Kind: "bool"},
	{Key: "dat_refresh_interval_days", Kind: "int", Min: 1},
	{Key: "clonelist_fetch_base", Kind: "string"},
	{Key: "collection_fill_per_cycle", Kind: "int", Min: 0},
}

// handleGetSettings returns the merged runtime settings document: stored
// rows where present, env-derived defaults otherwise (resolution happens in
// the config accessors / LoadSettings).
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st := s.mgr.LoadSettings()
	writeJSON(w, 200, map[string]interface{}{
		"extract_archives":            st.ExtractArchives,
		"normalize_roms":              st.NormalizeROMs,
		"normalize_online_fallback":   s.cfg.OnlineFallbackOn(),
		"convert_roms":                st.ConvertROMs,
		"remove_torrent_after_import": s.cfg.RemoveTorrentAfterImport(),
		"seed_janitor_enabled":        s.cfg.SeedJanitor(),
		"scheduler_auto_download":     s.cfg.AutoDownload(),
		"scheduler_min_score":         s.cfg.MinScore(),
		"watcher_enabled":             s.cfg.WatcherOn(),
		"watcher_interval_seconds":    s.cfg.WatcherIntervalSeconds(),
		"scheduler_enabled":           s.cfg.SchedulerOn(),
		"scheduler_interval_hours":    s.cfg.SchedulerIntervalHrs(),
		"selector_mode":               s.cfg.SelectorModeEffective(),
		"selector_set_timeout_hours":  s.cfg.DiscSetTimeoutHours(),
		"romm_connect_enabled":        s.cfg.RomMConnectOn(),
		"dat_auto_refresh_enabled":    s.cfg.DatAutoRefreshOn(),
		"dat_refresh_interval_days":   s.cfg.DatRefreshIntervalDays(),
		"clonelist_fetch_base":        s.cfg.CloneListFetchBase(),
		"collection_fill_per_cycle":   s.cfg.CollectionFill(),
	})
}

// handleUpdateSettings sparse-merges the request body: only keys present in
// the body change; declared keys are validated first so a bad value applies
// nothing. Responds with the full merged document.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate everything before applying anything.
	type change struct {
		key    string
		delete bool
		value  string
	}
	var changes []change
	for _, spec := range settingSpecs {
		raw, present := req[spec.Key]
		if !present {
			continue
		}
		if raw == nil {
			changes = append(changes, change{key: spec.Key, delete: true})
			continue
		}
		switch spec.Kind {
		case "bool":
			v, ok := raw.(bool)
			if !ok {
				writeError(w, 400, spec.Key+" must be a boolean")
				return
			}
			changes = append(changes, change{key: spec.Key, value: strconv.FormatBool(v)})
		case "int":
			f, ok := raw.(float64)
			if !ok || f != float64(int(f)) {
				writeError(w, 400, spec.Key+" must be an integer")
				return
			}
			n := int(f)
			if n < spec.Min {
				writeError(w, 400, fmt.Sprintf("%s must be at least %d", spec.Key, spec.Min))
				return
			}
			changes = append(changes, change{key: spec.Key, value: strconv.Itoa(n)})
		case "enum":
			v, ok := raw.(string)
			if !ok || !slices.Contains(spec.Enum, v) {
				writeError(w, 400, fmt.Sprintf("%s must be one of %s", spec.Key, strings.Join(spec.Enum, "|")))
				return
			}
			changes = append(changes, change{key: spec.Key, value: v})
		case "string":
			v, ok := raw.(string)
			if !ok {
				writeError(w, 400, spec.Key+" must be a string")
				return
			}
			changes = append(changes, change{key: spec.Key, value: v})
		}
	}

	jobs := s.mgr.Jobs()
	changedKeys := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.delete {
			jobs.DeleteSetting(c.key)
		} else if err := jobs.SetSetting(c.key, c.value); err != nil {
			writeError(w, 500, "failed to persist "+c.key)
			return
		}
		changedKeys = append(changedKeys, c.key)
	}

	// Re-arm affected loops synchronously: when this PUT returns, the new
	// state is live (the supervisor serializes concurrent applies).
	if s.sup != nil && len(changedKeys) > 0 {
		s.sup.Apply(changedKeys)
	}

	s.handleGetSettings(w, r)
}
