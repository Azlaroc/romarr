package converto

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gamarr/internal/config"
)

// testClient builds a Client pointed at the real binary with a short timeout
// and a throwaway cache dir.
func testClient(t *testing.T) *Client {
	t.Helper()
	return New(&config.Config{
		ConvertoBin:        "rom-converto",
		ConvertoTimeoutSec: 120,
		DataDir:            t.TempDir(),
	})
}

// requireBinary skips a test when rom-converto is not on PATH, mirroring the
// 7z-gated skip in download/manager_test.go. CI installs the pinned binary, so
// these run there; a bare dev box without it still passes.
func requireBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rom-converto"); err != nil {
		t.Skip("rom-converto not installed")
	}
}

func TestVersion(t *testing.T) {
	requireBinary(t)
	v, err := testClient(t).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !strings.Contains(v, "rom-converto") {
		t.Fatalf("version %q missing rom-converto marker", v)
	}
}

// TestCompressCHD is the story's smoke conversion: a tiny committed CD fixture
// compresses to a CHD and passes the tool's own structural verify. Offline —
// no Playmatch — so it is deterministic in CI.
func TestCompressCHD(t *testing.T) {
	requireBinary(t)

	dir := t.TempDir()
	for _, name := range []string{"game.cue", "track.bin"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	c := testClient(t)
	out, err := c.CompressCHD(context.Background(), filepath.Join(dir, "game.cue"), Options{
		Quiet:      true,
		OnConflict: "overwrite",
	})
	if err != nil {
		t.Fatalf("CompressCHD: %v", err)
	}
	if filepath.Base(out) != "game.chd" {
		t.Fatalf("unexpected output path %q, want basename game.chd", out)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty CHD at %s (stat err=%v)", out, err)
	}
	if err := exec.Command("rom-converto", "chd", "verify", out).Run(); err != nil {
		t.Fatalf("chd verify failed on produced file: %v", err)
	}
}

// TestDatRename exercises the online Playmatch path. Network-gated so required
// CI never depends on the public Playmatch API; set CONVERTO_E2E_PLAYMATCH=1 to
// run it. A dry-run over the fixture must not error (an unknown ROM is a no-op,
// not a failure).
func TestDatRename(t *testing.T) {
	requireBinary(t)
	if os.Getenv("CONVERTO_E2E_PLAYMATCH") == "" {
		t.Skip("set CONVERTO_E2E_PLAYMATCH=1 to run the online Playmatch dat test")
	}

	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("testdata", "track.bin"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "track.bin"), b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := testClient(t).DatRename(context.Background(), dir, Options{
		DryRun:    true,
		Recursive: true,
		Quiet:     true,
	}); err != nil {
		t.Fatalf("DatRename dry-run: %v", err)
	}
}

// TestVerifyCHD compresses the fixture and confirms VerifyCHD passes on the
// produced CHD and fails on a non-CHD file. Gated on the binary.
func TestVerifyCHD(t *testing.T) {
	requireBinary(t)

	dir := t.TempDir()
	for _, name := range []string{"game.cue", "track.bin"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	c := testClient(t)
	out, err := c.CompressCHD(context.Background(), filepath.Join(dir, "game.cue"), Options{Quiet: true, OnConflict: "overwrite"})
	if err != nil {
		t.Fatalf("CompressCHD: %v", err)
	}
	if err := c.VerifyCHD(context.Background(), out); err != nil {
		t.Fatalf("VerifyCHD on a freshly-made CHD: %v", err)
	}
	if err := c.VerifyCHD(context.Background(), filepath.Join(dir, "game.cue")); err == nil {
		t.Error("VerifyCHD on a non-CHD file should return an error")
	}
}

// TestChdOutputPath pins the output-path derivation without needing the binary,
// so it runs everywhere.
func TestChdOutputPath(t *testing.T) {
	c := New(&config.Config{})
	cases := []struct {
		in   string
		o    Options
		want string
	}{
		{"/x/game.cue", Options{}, "/x/game.chd"},
		{"/x/game.iso", Options{OutputDir: "/out"}, "/out/game.chd"},
		{"/x/game.cue", Options{Output: "/o/custom.chd"}, "/o/custom.chd"},
	}
	for _, tc := range cases {
		if got := c.chdOutputPath(tc.in, tc.o); got != tc.want {
			t.Errorf("chdOutputPath(%q, %+v) = %q, want %q", tc.in, tc.o, got, tc.want)
		}
	}
}

// TestBaseArgs checks the global-flag ordering the clap parser expects.
func TestBaseArgs(t *testing.T) {
	c := New(&config.Config{})
	got := strings.Join(c.baseArgs(Options{Quiet: true, DryRun: true}), " ")
	want := "--no-update-check --quiet --dry-run"
	if got != want {
		t.Fatalf("baseArgs = %q, want %q", got, want)
	}
	if got := strings.Join(c.baseArgs(Options{}), " "); got != "--no-update-check" {
		t.Fatalf("baseArgs default = %q, want --no-update-check", got)
	}
}
