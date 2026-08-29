package normalize

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gamarr/internal/config"
)

// absentNormalizer builds a Normalizer whose binary does not resolve on PATH,
// so every Normalize call must be an inert no-op — the non-blocking contract.
func absentNormalizer(t *testing.T) *Normalizer {
	t.Helper()
	return New(&config.Config{
		ConvertoBin:        "rom-converto-absent-" + t.Name(),
		ConvertoTimeoutSec: 5,
		DataDir:            t.TempDir(),
	}, nil)
}

func TestNormalizeNoBinaryIsNoop(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Game (USA).gba")
	if err := os.WriteFile(file, []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, res, err := absentNormalizer(t).Normalize(context.Background(), file, "gba", nil, Policy{})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if got != file {
		t.Errorf("path = %q, want unchanged %q", got, file)
	}
	if res.Renamed || res.Playlist {
		t.Errorf("result = %+v, want zero value (no binary)", res)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("file should be untouched: %v", err)
	}
}

func TestNormalizeMissingPathIsNoop(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ghost.gba")
	got, res, err := absentNormalizer(t).Normalize(context.Background(), missing, "gba", nil, Policy{})
	if err != nil || got != missing || res.Renamed || res.Playlist {
		t.Fatalf("Normalize(missing) = (%q,%+v,%v), want (%q, zero, nil)", got, res, err, missing)
	}
}

func TestNilNormalizerIsNoop(t *testing.T) {
	var n *Normalizer
	const p = "/roms/psx/game.chd"
	got, _, err := n.Normalize(context.Background(), p, "psx", nil, Policy{})
	if err != nil || got != p {
		t.Fatalf("nil Normalize = (%q,%v), want passthrough of %q", got, err, p)
	}
}

func TestSoleNewEntry(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"other.gba", "renamed (USA).gba"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// One entry present now but absent before → the renamed file.
	before := map[string]struct{}{"other.gba": {}, "old.gba": {}}
	got, ok := soleNewEntry(dir, before)
	if !ok || filepath.Base(got) != "renamed (USA).gba" {
		t.Fatalf("soleNewEntry = (%q,%v), want the single new entry", got, ok)
	}

	// Two new entries → ambiguous, ok=false (never mis-point the library).
	if _, ok := soleNewEntry(dir, map[string]struct{}{}); ok {
		t.Error("soleNewEntry with 2 new entries should be ambiguous (ok=false)")
	}

	// No new entries → ok=false.
	all := map[string]struct{}{"other.gba": {}, "renamed (USA).gba": {}}
	if _, ok := soleNewEntry(dir, all); ok {
		t.Error("soleNewEntry with 0 new entries should be ok=false")
	}
}

// TestNormalizeE2E exercises the real binary + online Playmatch, gated twice so
// required CI never depends on it. It only asserts the call is non-fatal and
// that a directory's tracked path is returned unchanged.
func TestNormalizeE2E(t *testing.T) {
	if _, err := exec.LookPath("rom-converto"); err != nil {
		t.Skip("rom-converto not installed")
	}
	if os.Getenv("CONVERTO_E2E_PLAYMATCH") == "" {
		t.Skip("set CONVERTO_E2E_PLAYMATCH=1 to run the online Playmatch normalize test")
	}

	gameDir := filepath.Join(t.TempDir(), "Some Game (USA)")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "Some Game (USA).bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	n := New(&config.Config{ConvertoBin: "rom-converto", ConvertoTimeoutSec: 120, DataDir: t.TempDir()}, nil)
	got, _, err := n.Normalize(context.Background(), gameDir, "psx", nil, Policy{})
	if err != nil {
		t.Fatalf("Normalize e2e: %v", err)
	}
	if got != gameDir {
		t.Errorf("directory path should be unchanged, got %q", got)
	}
}
