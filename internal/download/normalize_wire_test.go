package download

import (
	"path/filepath"
	"testing"
)

// With NormalizeROMs enabled but the rom-converto binary unavailable, import
// must still complete and track the moved ROM at its original path — the
// normalize step is non-blocking by contract. Pointing ConvertoBin at an absent
// binary keeps this deterministic even in CI, where rom-converto is installed.
func TestOrganizeDDLFileNormalizeNonBlockingWhenBinaryAbsent(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.ConvertoBin = "rom-converto-absent-" + t.Name() // force Available()==false
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	m.SaveSettings(&Settings{NormalizeROMs: true})

	src := filepath.Join(cfg.QBSavePath, "Some Game (USA).gba")
	writeFileT(t, src, []byte("rom-bytes"))

	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "organizing", "error": nil})
	m.organizeDDLFile(jobID, src, "Some Game (USA)", "Game Boy Advance", "gba", false, "", "")

	dest := filepath.Join(cfg.GamesRomsPath, "gba", "Some Game (USA).gba")
	if !pathExists(dest) {
		t.Fatalf("ROM not organized to %s", dest)
	}
	if !jobs.LibraryHasSourceID("ddl:" + dest) {
		t.Errorf("library not tracking the moved ROM at %q", dest)
	}
	job, _ := jobs.Get(jobID)
	if st, _ := job["status"].(string); st != "completed" {
		t.Errorf("status = %q, want completed", st)
	}
}

// MaybeNormalize is a pure pass-through when the setting is off (the shipped
// default), regardless of binary availability.
func TestMaybeNormalizeDisabledIsPassthrough(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil) // NormalizeROMs defaults false

	const p = "/roms/psx/Some Game (USA).chd"
	if got := m.MaybeNormalize("", p, "psx"); got != p {
		t.Fatalf("MaybeNormalize(disabled) = %q, want passthrough %q", got, p)
	}
}
