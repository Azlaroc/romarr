package romfile

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func require7z(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not on PATH")
	}
}

// writeZip builds a .zip with the given members. 7z reads plain zips, so the
// archive path is exercised without shelling out to build the fixture.
func writeZip(t *testing.T, name string, members map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for n, body := range members {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("zip entry %s: %v", n, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return p
}

func TestLinkFallbackBehavior(t *testing.T) {
	// Behavior contract: dest exists with identical bytes whether the link
	// or the copy branch ran (cross-device forcing is not portable).
	src := write(t, "g.sfc", []byte("content-bytes"))
	destDir := t.TempDir()
	dest, err := Link(src, destDir)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "content-bytes" {
		t.Errorf("staged copy wrong: %v %q", err, b)
	}
}

func TestExtractSingle(t *testing.T) {
	require7z(t)
	src := writeZip(t, "One.zip", map[string]string{"game.gb": "inner"})
	dest := t.TempDir()
	got, err := ExtractSingle(context.Background(), src, dest)
	if err != nil {
		t.Fatalf("ExtractSingle: %v", err)
	}
	if filepath.Dir(got) != dest {
		t.Errorf("extracted to %s, want a top-level entry in %s", got, dest)
	}
	if b, _ := os.ReadFile(got); string(b) != "inner" {
		t.Errorf("extracted bytes %q", b)
	}
}

func TestExtractSingleFlattensInnerFolder(t *testing.T) {
	require7z(t)
	src := writeZip(t, "Nested.zip", map[string]string{"folder/game.gb": "inner"})
	dest := t.TempDir()
	got, err := ExtractSingle(context.Background(), src, dest)
	if err != nil {
		t.Fatalf("ExtractSingle: %v", err)
	}
	if filepath.Dir(got) != dest {
		t.Errorf("extracted to %s, want it flattened into %s", got, dest)
	}
}

func TestExtractSingleRefusesMultiFile(t *testing.T) {
	require7z(t)
	src := writeZip(t, "Pack.zip", map[string]string{"a.gb": "one", "b.gb": "two"})
	_, err := ExtractSingle(context.Background(), src, t.TempDir())
	var multi *MultiFileError
	if !errors.As(err, &multi) {
		t.Fatalf("got %v, want a *MultiFileError", err)
	}
	if multi.N != 2 {
		t.Errorf("N = %d, want 2", multi.N)
	}
}

func TestIsArchive(t *testing.T) {
	for path, want := range map[string]bool{
		"a.zip": true, "a.7z": true, "A.ZIP": true,
		"a.nes": false, "a.rar": false, "a": false,
	} {
		if got := IsArchive(path); got != want {
			t.Errorf("IsArchive(%q) = %v, want %v", path, got, want)
		}
	}
}

// The tests above need a real 7z and therefore only run in CI. Exec7z is a
// var precisely so the logic AROUND the extractor — flattening, the
// exactly-one-file rule, a failing extractor — can be proven here too, where
// a gated test is a blind spot rather than a check.
func stubExtractor(t *testing.T, contents map[string]string, fail bool) {
	t.Helper()
	prev := Exec7z
	t.Cleanup(func() { Exec7z = prev })
	Exec7z = func(ctx context.Context, archive, destDir string) *exec.Cmd {
		if fail {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'Unsupported method' >&2; exit 2")
		}
		script := ""
		for name, body := range contents {
			script += "mkdir -p \"$(dirname \"$1/" + name + "\")\" && printf %s '" + body + "' > \"$1/" + name + "\" && "
		}
		return exec.CommandContext(ctx, "sh", "-c", script+"true", "sh", destDir)
	}
}

func TestExtractSingleStubbed(t *testing.T) {
	stubExtractor(t, map[string]string{"folder/game.gb": "inner"}, false)
	dest := t.TempDir()
	got, err := ExtractSingle(context.Background(), "ignored.7z", dest)
	if err != nil {
		t.Fatalf("ExtractSingle: %v", err)
	}
	if filepath.Dir(got) != dest || filepath.Base(got) != "game.gb" {
		t.Errorf("got %s, want game.gb flattened into %s", got, dest)
	}
	if b, _ := os.ReadFile(got); string(b) != "inner" {
		t.Errorf("bytes %q", b)
	}
}

func TestExtractSingleStubbedMultiFile(t *testing.T) {
	stubExtractor(t, map[string]string{"a.gb": "one", "b.gb": "two"}, false)
	_, err := ExtractSingle(context.Background(), "ignored.7z", t.TempDir())
	var multi *MultiFileError
	if !errors.As(err, &multi) || multi.N != 2 {
		t.Fatalf("got %v, want *MultiFileError{N:2}", err)
	}
}

func TestExtractSingleStubbedEmptyArchive(t *testing.T) {
	// Zero files is the same class of answer as two: no single ROM identity.
	stubExtractor(t, nil, false)
	_, err := ExtractSingle(context.Background(), "ignored.7z", t.TempDir())
	var multi *MultiFileError
	if !errors.As(err, &multi) || multi.N != 0 {
		t.Fatalf("got %v, want *MultiFileError{N:0}", err)
	}
}

func TestExtractSingleExtractorFailure(t *testing.T) {
	// A broken archive is an error, not a skip — the caller must not record
	// "no single file inside" for something it could not open at all.
	stubExtractor(t, nil, true)
	_, err := ExtractSingle(context.Background(), "broken.7z", t.TempDir())
	if err == nil {
		t.Fatal("extractor failure returned nil error")
	}
	var multi *MultiFileError
	if errors.As(err, &multi) {
		t.Errorf("extractor failure classified as multi-file: %v", err)
	}
	if !strings.Contains(err.Error(), "Unsupported method") {
		t.Errorf("error %q drops the extractor's own output", err)
	}
}
