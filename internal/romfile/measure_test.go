package romfile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMeasureRawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "game.bin")
	if err := os.WriteFile(path, []byte("raw cartridge bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Measure(context.Background(), path, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	want, err := HashPayload(path)
	if err != nil {
		t.Fatal(err)
	}
	if res != want {
		t.Errorf("Measure = %+v, want HashPayload's %+v", res, want)
	}
}

func TestMeasureHeadered(t *testing.T) {
	dir := t.TempDir()
	ines := append([]byte{'N', 'E', 'S', 0x1a, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		[]byte("cartridge payload")...)
	path := filepath.Join(dir, "game.nes")
	if err := os.WriteFile(path, ines, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Measure(context.Background(), path, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if !res.Stripped || res.Payload.MD5 == "" || res.Payload.MD5 == res.MD5 {
		t.Errorf("headered measurement lost its payload digests: %+v", res)
	}
}

func TestMeasureClassifications(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")

	t.Run("missing", func(t *testing.T) {
		_, err := Measure(context.Background(), filepath.Join(dir, "ghost.bin"), work)
		if !os.IsNotExist(err) {
			t.Errorf("err = %v, want IsNotExist", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		sub := filepath.Join(dir, "a-dir")
		os.MkdirAll(sub, 0o755)
		if _, err := Measure(context.Background(), sub, work); !errors.Is(err, ErrIsDirectory) {
			t.Errorf("err = %v, want ErrIsDirectory", err)
		}
	})
	t.Run("rar", func(t *testing.T) {
		path := filepath.Join(dir, "game.rar")
		os.WriteFile(path, []byte("not really rar"), 0o644)
		if _, err := Measure(context.Background(), path, work); !errors.Is(err, ErrRarArchive) {
			t.Errorf("err = %v, want ErrRarArchive", err)
		}
	})
}

// The archive arm needs 7z, which CI installs and the dev container lacks.
func TestMeasureArchive(t *testing.T) {
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not installed")
	}
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.bin")
	os.WriteFile(inner, []byte("inner rom"), 0o644)
	archive := filepath.Join(dir, "game.zip")
	if out, err := exec.Command("7z", "a", archive, inner).CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v: %s", err, out)
	}
	res, err := Measure(context.Background(), archive, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	want, _ := HashPayload(inner)
	if res.MD5 != want.MD5 {
		t.Errorf("archive measurement = %q, want the INNER file's %q", res.MD5, want.MD5)
	}

	// Two inner files → no single ROM identity.
	second := filepath.Join(dir, "second.bin")
	os.WriteFile(second, []byte("more"), 0o644)
	multi := filepath.Join(dir, "multi.zip")
	if out, err := exec.Command("7z", "a", multi, inner, second).CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v: %s", err, out)
	}
	var multiErr *MultiFileError
	if _, err := Measure(context.Background(), multi, filepath.Join(dir, "work")); !errors.As(err, &multiErr) {
		t.Errorf("err = %v, want MultiFileError", err)
	}
}

// The union vocabulary: one entry from each of the two copies it merged
// (.chd was only in the library scan's, .rpx only in manual import's), the
// shared core, and the negatives. Also pins what the list is NOT: a
// cartridge extension like .a26 is absent by design — enumeration filters
// by sidecar-exclusion, not by this list.
func TestExtensionVocabulary(t *testing.T) {
	for _, name := range []string{"a.chd", "b.rpx", "c.nes", "d.zip", "E.NSP"} {
		if !IsGameExtension(name) {
			t.Errorf("expected %q recognized", name)
		}
	}
	for _, name := range []string{"x.txt", "y.a26", "z.pdf"} {
		if IsGameExtension(name) {
			t.Errorf("expected %q NOT recognized", name)
		}
	}
	for _, name := range []string{"p.m3u", "q.jpg", "r.dat", "s.gamarr.json"} {
		if !IsSidecarExtension(name) {
			t.Errorf("expected %q sidecar", name)
		}
	}
	if IsSidecarExtension("g.nes") {
		t.Error(".nes must not be a sidecar")
	}
}
