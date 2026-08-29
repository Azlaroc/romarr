package normalize

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/platform"
)

// TestFormatPolicy asserts against the vocabulary TestMain attaches — the CHD
// list is a registry column now, not a map in this package. Deliberately does
// NOT detach it afterwards: every test here depends on it.
func TestFormatPolicy(t *testing.T) {
	for _, p := range []string{"psx", "psp", "dc", "ps2"} {
		if got := FormatPolicy(p); got != "chd" {
			t.Errorf("FormatPolicy(%q) = %q, want chd", p, got)
		}
	}
	for _, p := range []string{"gba", "nes", "snes", "switch", ""} {
		if got := FormatPolicy(p); got != "" {
			t.Errorf("FormatPolicy(%q) = %q, want empty (leave-as-is)", p, got)
		}
	}
}

func TestIsDiscImage(t *testing.T) {
	for _, n := range []string{"a.cue", "b.ISO", "c.gdi", "dir/Game (USA).cue"} {
		if !isDiscImage(n) {
			t.Errorf("isDiscImage(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"a.bin", "a.chd", "a.gba", "noext"} {
		if isDiscImage(n) {
			t.Errorf("isDiscImage(%q) = true, want false", n)
		}
	}
}

func TestConvertNoBinaryIsNoop(t *testing.T) {
	dir := t.TempDir()
	writeFixtureDisc(t, dir, "Game (USA)")
	got, res, err := absentNormalizer(t).Convert(context.Background(), dir, "psx", "ok")
	if err != nil || got != dir || res.Converted != 0 {
		t.Fatalf("Convert(no binary) = (%q, %+v, %v), want passthrough", got, res, err)
	}
	if !fileExists(filepath.Join(dir, "Game (USA).cue")) {
		t.Error("source cue must be untouched without a binary")
	}
}

// TestConvertToCHD is the story's core: a real disc compresses to a CHD that
// passes verify, and the source is deleted only after that verify. Gated on the
// binary (CI installs it). Also covers the mismatch-skip and non-disc gates.
func TestConvertToCHD(t *testing.T) {
	requireBinary(t)

	newN := func(t *testing.T) *Normalizer {
		return New(&config.Config{ConvertoBin: "rom-converto", ConvertoTimeoutSec: 120, DataDir: t.TempDir()}, nil)
	}

	t.Run("disc converts to a verified CHD; source removed", func(t *testing.T) {
		dir := t.TempDir()
		writeFixtureDisc(t, dir, "Some Game (USA)")
		got, res, err := newN(t).Convert(context.Background(), dir, "psx", "ok")
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		if got != dir {
			t.Errorf("finalPath = %q, want the game dir %q", got, dir)
		}
		if res.Converted != 1 {
			t.Errorf("Converted = %d, want 1", res.Converted)
		}
		if !fileExists(filepath.Join(dir, "Some Game (USA).chd")) {
			t.Error("expected Some Game (USA).chd")
		} else if fi, err := os.Stat(filepath.Join(dir, "Some Game (USA).chd")); err == nil && fi.Mode().Perm() != 0o664 {
			// rom-converto writes 0600; the convert step must loosen it so
			// downstream consumers can read the library file.
			t.Errorf("chd mode = %v, want 0664", fi.Mode().Perm())
		}
		if fileExists(filepath.Join(dir, "Some Game (USA).cue")) || fileExists(filepath.Join(dir, "Some Game (USA).bin")) {
			t.Error("source cue/bin must be deleted only after a verified convert — here they should be gone")
		}
	})

	t.Run("hash mismatch skips convert; source intact", func(t *testing.T) {
		dir := t.TempDir()
		writeFixtureDisc(t, dir, "Some Game (USA)")
		got, res, _ := newN(t).Convert(context.Background(), dir, "psx", "mismatch")
		if got != dir || res.Converted != 0 {
			t.Errorf("mismatch Convert = (%q, %d converted), want no-op", got, res.Converted)
		}
		if !fileExists(filepath.Join(dir, "Some Game (USA).cue")) {
			t.Error("source must be intact on a hash mismatch")
		}
	})

	t.Run("non-disc platform is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		writeFixtureDisc(t, dir, "Cart Game (USA)")
		got, res, _ := newN(t).Convert(context.Background(), dir, "gba", "ok")
		if got != dir || res.Converted != 0 {
			t.Errorf("non-disc Convert = (%q, %d), want no-op", got, res.Converted)
		}
	})
}

// writeFixtureDisc writes a minimal valid MODE1/2048 cue+bin pair (byte-identical
// to the committed converto CHD fixture: 16 sectors of byte(i*7+3)).
func writeFixtureDisc(t *testing.T, dir, base string) {
	t.Helper()
	bin := make([]byte, 32768) // 16 sectors × 2048
	for i := range bin {
		bin[i] = byte(i*7 + 3)
	}
	if err := os.WriteFile(filepath.Join(dir, base+".bin"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	cue := "FILE \"" + base + ".bin\" BINARY\n  TRACK 01 MODE1/2048\n    INDEX 01 00:00:00\n"
	if err := os.WriteFile(filepath.Join(dir, base+".cue"), []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func requireBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rom-converto"); err != nil {
		t.Skip("rom-converto not installed")
	}
}

// TestMain attaches the platform vocabulary this package's behaviour depends
// on. FormatPolicy reads the registry's converts_to_chd column, so a test
// that converts a disc has to say which platforms are disc platforms —
// otherwise it asserts against a silently empty vocabulary, which is how the
// rom-converto-gated conversion test would pass locally (skipped, no binary)
// and fail in CI (binary installed, nothing converts).
func TestMain(m *testing.M) {
	platform.SetRegistry(platform.StaticRegistry{
		{Slug: "psx", DisplayName: "PS1", ConvertsToCHD: true},
		{Slug: "ps2", DisplayName: "PS2", ConvertsToCHD: true},
		{Slug: "psp", DisplayName: "PSP", ConvertsToCHD: true},
		{Slug: "dc", DisplayName: "Dreamcast", ConvertsToCHD: true},
		{Slug: "gba", DisplayName: "Game Boy Advance"},
		{Slug: "nes", DisplayName: "NES"},
		{Slug: "snes", DisplayName: "SNES"},
		{Slug: "switch", DisplayName: "Switch"},
	})
	os.Exit(m.Run())
}
