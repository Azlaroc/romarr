package db

import "log/slog"

// migrateSettings creates the runtime settings key-value store. A row's
// absence means "not set" — readers fall through to the env-derived default,
// so fresh installs behave exactly like pre-DB builds and each key falls
// back individually (unlike the old whole-file settings.json fallback).
func (s *JobStore) migrateSettings() {
	ddl := `CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	if _, err := s.db.Exec(ddl); err != nil {
		slog.Warn("migrate settings", "error", err)
	}
}

// GetSetting returns the stored value for key; ok=false when no row exists.
func (s *JobStore) GetSetting(key string) (string, bool) {
	var v string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

// SetSetting stores value under key, replacing any existing row.
func (s *JobStore) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value)
	return err
}

// DeleteSetting removes key's row so readers fall back to the env default.
func (s *JobStore) DeleteSetting(key string) error {
	_, err := s.db.Exec("DELETE FROM settings WHERE key = ?", key)
	return err
}

// SettingsCount reports how many of the given keys have stored rows.
func (s *JobStore) SettingsCount(keys ...string) int {
	n := 0
	for _, k := range keys {
		if _, ok := s.GetSetting(k); ok {
			n++
		}
	}
	return n
}
