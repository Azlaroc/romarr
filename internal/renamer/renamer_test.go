package renamer

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/converto"
)

// fakeConverto writes an executable standing in for rom-converto. The staged
// input arrives under a random sentinel name, so behavior is keyed on file
// CONTENT, not filename:
//
//	MATCH:<canonical-filename>:  → rename the input to <canonical-filename>
//	FAIL...                      → exit 1
//	anything else                → exit 0 untouched (DAT miss)
func fakeConverto(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
for last in "$@"; do :; done
c=$(cat "$last")
case "$c" in
  MATCH:*)
    new=${c#MATCH:}
    new=${new%%:*}
    mv "$last" "$(dirname "$last")/$new"
    ;;
  FAIL*) exit 1 ;;
  *) : ;;
esac
exit 0
`
	path := filepath.Join(dir, "rom-converto")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestIdentifier(t *testing.T) (*Identifier, string) {
	t.Helper()
	cfg := &config.Config{
		ConvertoBin:        fakeConverto(t),
		ConvertoTimeoutSec: 30,
		DataDir:            t.TempDir(),
	}
	workRoot := t.TempDir()
	return NewIdentifier(converto.New(cfg), workRoot), workRoot
}

func writeROM(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestIdentifyRawMatch(t *testing.T) {
	id, workRoot := newTestIdentifier(t)
	lib := t.TempDir()
	content := "MATCH:Hagane - The Final Conflict (USA).sfc:payload"
	src := writeROM(t, lib, "Hagane - The Final Conflict (U).sfc", content)

	got, err := id.Identify(context.Background(), src)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Hagane - The Final Conflict (USA).sfc" {
		t.Errorf("got %+v, want match with canonical name", got)
	}
	if got.CanonicalStem != "Hagane - The Final Conflict (USA)" {
		t.Errorf("stem = %q", got.CanonicalStem)
	}
	if got.MD5 != md5hex(content) {
		t.Errorf("md5 = %q, want hash of raw bytes", got.MD5)
	}
	// Library copy untouched.
	if b, err := os.ReadFile(src); err != nil || string(b) != content {
		t.Errorf("library file modified: %v %q", err, b)
	}
	// Workspace cleaned.
	ents, _ := os.ReadDir(workRoot)
	if len(ents) != 0 {
		t.Errorf("workRoot not cleaned: %v", ents)
	}
}

func TestIdentifyRawMiss(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()
	content := "no dat entry for this one"
	src := writeROM(t, lib, "Homebrew Thing.gb", content)

	got, err := id.Identify(context.Background(), src)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.Matched || got.SkipReason != SkipNoDatMatch {
		t.Errorf("got %+v, want unmatched with %q", got, SkipNoDatMatch)
	}
	if got.MD5 != md5hex(content) {
		t.Errorf("md5 should still be computed on a miss: %q", got.MD5)
	}
}

func TestIdentifyAlreadyCanonical(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()
	name := "Perfect Game (USA).sfc"
	src := writeROM(t, lib, name, "MATCH:"+name+":x")

	got, err := id.Identify(context.Background(), src)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	// The sentinel staging makes this a MATCH whose proposed name equals the
	// current one — the runner classifies it as a noop, never a DAT miss.
	if !got.Matched || got.ProposedName != name {
		t.Errorf("got %+v, want match proposing the same name", got)
	}
}

func TestIdentifyConvertoFailure(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()
	src := writeROM(t, lib, "Broken.gb", "FAIL now")

	if _, err := id.Identify(context.Background(), src); err == nil {
		t.Fatal("want error when rom-converto exits non-zero")
	}
}

func TestIdentifySkips(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()

	dir := filepath.Join(lib, "Multi Disc Game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := id.Identify(context.Background(), dir); err != nil || got.SkipReason != SkipMultiFile {
		t.Errorf("dir: got %+v err=%v, want skip %q", got, err, SkipMultiFile)
	}

	rar := writeROM(t, lib, "Old Pack.rar", "rar bytes")
	if got, err := id.Identify(context.Background(), rar); err != nil || got.SkipReason != SkipRar {
		t.Errorf("rar: got %+v err=%v, want skip %q", got, err, SkipRar)
	}

	if _, err := id.Identify(context.Background(), filepath.Join(lib, "missing.gb")); err == nil {
		t.Error("missing file: want error")
	}
}

func write7zGatedZip(t *testing.T, dir, name string, entries map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not on PATH")
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, c := range entries {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIdentifyZipSingleFile(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()
	inner := "MATCH:Bubble Ghost (USA, Europe).gb:zippayload"
	// Nested folder inside the archive exercises the flatten path.
	src := write7zGatedZip(t, lib, "Bubble Ghost (U) [!].zip",
		map[string]string{"inner/Bubble Ghost (U) [!].gb": inner})

	got, err := id.Identify(context.Background(), src)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Bubble Ghost (USA, Europe).zip" {
		t.Errorf("got %+v, want canonical stem + original .zip ext", got)
	}
	if got.MD5 != md5hex(inner) {
		t.Errorf("md5 = %q, want hash of INNER bytes", got.MD5)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("archive touched: %v", err)
	}
}

func TestIdentifyZipMultiFile(t *testing.T) {
	id, _ := newTestIdentifier(t)
	lib := t.TempDir()
	src := write7zGatedZip(t, lib, "Pack.zip",
		map[string]string{"a.gb": "one", "b.gb": "two"})

	got, err := id.Identify(context.Background(), src)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.SkipReason != SkipMultiFile {
		t.Errorf("got %+v, want skip %q", got, SkipMultiFile)
	}
}

func TestStageRawFallbackBehavior(t *testing.T) {
	// Behavior contract: dest exists with identical bytes whether the link
	// or the copy branch ran (cross-device forcing is not portable).
	src := writeROM(t, t.TempDir(), "g.sfc", "content-bytes")
	destDir := t.TempDir()
	dest, err := stageRaw(src, destDir)
	if err != nil {
		t.Fatalf("stageRaw: %v", err)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "content-bytes" {
		t.Errorf("staged copy wrong: %v %q", err, b)
	}
}
