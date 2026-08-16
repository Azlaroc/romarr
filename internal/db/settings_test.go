package db

import (
	"path/filepath"
	"testing"
)

func TestSettingsCRUD(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, ok := store.GetSetting("missing"); ok {
		t.Fatal("missing key should report ok=false")
	}

	if err := store.SetSetting("scheduler_min_score", "85"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if v, ok := store.GetSetting("scheduler_min_score"); !ok || v != "85" {
		t.Fatalf("GetSetting = %q,%v, want 85,true", v, ok)
	}

	// Upsert replaces.
	if err := store.SetSetting("scheduler_min_score", "90"); err != nil {
		t.Fatalf("SetSetting upsert: %v", err)
	}
	if v, _ := store.GetSetting("scheduler_min_score"); v != "90" {
		t.Fatalf("upsert value = %q, want 90", v)
	}

	if n := store.SettingsCount("scheduler_min_score", "missing"); n != 1 {
		t.Fatalf("SettingsCount = %d, want 1", n)
	}

	// Rows survive a reopen (the whole point of the store).
	store.Close()
	store2, err := New(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()
	if v, ok := store2.GetSetting("scheduler_min_score"); !ok || v != "90" {
		t.Fatalf("after reopen = %q,%v, want 90,true", v, ok)
	}

	if err := store2.DeleteSetting("scheduler_min_score"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if _, ok := store2.GetSetting("scheduler_min_score"); ok {
		t.Fatal("deleted key should report ok=false")
	}
	// Deleting a missing key is a no-op, not an error.
	if err := store2.DeleteSetting("scheduler_min_score"); err != nil {
		t.Fatalf("DeleteSetting missing: %v", err)
	}
}
