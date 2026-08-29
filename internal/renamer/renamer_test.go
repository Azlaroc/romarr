package renamer

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/converto"
	"gamarr/internal/db"
)

// fakeConverto writes an executable standing in for rom-converto — the
// ONLINE fallback arm only. The staged input arrives under a random sentinel
// name, so behavior is keyed on file CONTENT, not filename:
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

// fakeStore is an in-memory identifyStore: catalog hits keyed on
// "<slug>|<hash>", persisted hashes recorded for assertions.
type fakeStore struct {
	byHash map[string][]db.DatRomMatch
	saved  map[int64]db.LibraryHashes
}

func newFakeStore() *fakeStore {
	return &fakeStore{byHash: map[string][]db.DatRomMatch{}, saved: map[int64]db.LibraryHashes{}}
}

func (f *fakeStore) put(slug, hash string, ms ...db.DatRomMatch) {
	f.byHash[slug+"|"+strings.ToLower(hash)] = ms
}

func (f *fakeStore) LookupDatRomsByHash(slug, crc, md5, sha1 string) []db.DatRomMatch {
	for _, h := range []string{crc, md5, sha1} {
		if h == "" {
			continue
		}
		if m, ok := f.byHash[slug+"|"+strings.ToLower(h)]; ok {
			return m
		}
	}
	return nil
}

func (f *fakeStore) SaveLibraryHashes(id int64, h db.LibraryHashes) error {
	f.saved[id] = h
	return nil
}

func testConvertoClient(t *testing.T) *converto.Client {
	t.Helper()
	return converto.New(&config.Config{
		ConvertoBin:        fakeConverto(t),
		ConvertoTimeoutSec: 30,
		DataDir:            t.TempDir(),
	})
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

// hashedMeta builds a metadata blob carrying stored $.gamarr hashes stamped
// far enough in the future that the file's mtime can never out-date them.
func hashedMeta(md5 string) string {
	return `{"gamarr":{"md5":"` + md5 + `","hashed_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`
}

func libItem(id int64, path, slug, metadata string) *db.LibraryItem {
	return &db.LibraryItem{ID: id, FilePath: path, PlatformSlug: slug, Metadata: metadata}
}

// ── stored-hash arm ──────────────────────────────────────────────────────────

func TestIdentifyStoredHashArmNoStaging(t *testing.T) {
	store := newFakeStore()
	store.put("snes", "aabb01", db.DatRomMatch{GameName: "Hagane (USA)", RomName: "Hagane (USA).sfc"})
	workRoot := filepath.Join(t.TempDir(), "never-created")
	id := NewIdentifier(store, testConvertoClient(t), workRoot, false)

	lib := t.TempDir()
	src := writeROM(t, lib, "Hagane (U).sfc", "irrelevant bytes")

	got, err := id.Identify(context.Background(), libItem(1, src, "snes", hashedMeta("aabb01")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Hagane (USA).sfc" || got.NameSource != NameSourceDat {
		t.Errorf("got %+v, want dat-sourced canonical name", got)
	}
	if got.MD5 != "aabb01" {
		t.Errorf("md5 = %q, want the stored hash", got.MD5)
	}
	if _, err := os.Stat(workRoot); !os.IsNotExist(err) {
		t.Error("stored-hash arm must not stage anything")
	}
	if len(store.saved) != 0 {
		t.Errorf("stored-hash arm must not re-persist: %v", store.saved)
	}
}

func TestIdentifyStoredHashArchiveKeepsOuterExt(t *testing.T) {
	store := newFakeStore()
	store.put("gb", "cc02", db.DatRomMatch{GameName: "Tetris (World)", RomName: "Tetris (World).gb"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "tetris (u).7z", "not really an archive")
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", hashedMeta("cc02")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.ProposedName != "Tetris (World).7z" {
		t.Errorf("proposed = %q, want canonical stem + outer .7z", got.ProposedName)
	}
}

func TestIdentifyUnhMatchKeepsOwnExt(t *testing.T) {
	// Whole-file hashes miss; the stored unh (payload) hash hits a .unh
	// catalog row. The proposal must keep the file's own extension.
	store := newFakeStore()
	store.put("nes", "ff03", db.DatRomMatch{GameName: "Metroid (USA)", RomName: "Metroid (USA).unh"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "Metroid (U).nes", "headered bytes")
	meta := `{"gamarr":{"md5":"nomatch","hashed_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) +
		`","unh":{"sha1":"ff03","header":"ines"}}}`
	got, err := id.Identify(context.Background(), libItem(1, src, "nes", meta))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Metroid (USA).nes" {
		t.Errorf("got %+v, want payload match keeping .nes", got)
	}
}

// Sync-born rows carry only RomM's $.romm hashes — the same inner-content
// domain. They must resolve with zero staging I/O.
func TestIdentifyRommHashArmNoStaging(t *testing.T) {
	store := newFakeStore()
	store.put("gb", "beef07", db.DatRomMatch{GameName: "Kirby (USA)", RomName: "Kirby (USA).gb"})
	workRoot := filepath.Join(t.TempDir(), "never-created")
	id := NewIdentifier(store, testConvertoClient(t), workRoot, false)

	src := writeROM(t, t.TempDir(), "kirby (u).gb", "irrelevant")
	meta := `{"romm":{"md5":"BEEF07"}}`
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", meta))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Kirby (USA).gb" || got.NameSource != NameSourceDat {
		t.Errorf("got %+v, want dat match via $.romm hashes", got)
	}
	if got.MD5 != "beef07" {
		t.Errorf("md5 = %q, want the romm hash lowered", got.MD5)
	}
	if _, err := os.Stat(workRoot); !os.IsNotExist(err) {
		t.Error("romm-hash hit must not stage anything")
	}
}

// A romm-only row whose hash misses falls through to the hashless arm: hash
// once, persist $.gamarr, decide from our own measurement.
func TestIdentifyRommMissFallsThroughToSelfHeal(t *testing.T) {
	content := "actual bytes"
	store := newFakeStore()
	store.put("gb", md5hex(content), db.DatRomMatch{GameName: "Real (USA)", RomName: "Real (USA).gb"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "real (u).gb", content)
	meta := `{"romm":{"md5":"ffffffffffffffffffffffffffffffff"}}` // multi-file-style wrong-object hash
	got, err := id.Identify(context.Background(), libItem(11, src, "gb", meta))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Real (USA).gb" {
		t.Errorf("got %+v, want match via self-heal re-hash", got)
	}
	if saved := store.saved[11]; saved.MD5 != md5hex(content) {
		t.Errorf("self-heal not persisted: %+v", saved)
	}
}

func TestIdentifyStoredMissIsLoudWithoutFallback(t *testing.T) {
	store := newFakeStore() // knows nothing
	workRoot := filepath.Join(t.TempDir(), "never-created")
	id := NewIdentifier(store, testConvertoClient(t), workRoot, false)

	// Content the fake converto WOULD match — proving the fallback is not
	// consulted when disabled.
	src := writeROM(t, t.TempDir(), "Mystery (U).gb", "MATCH:Should Not Happen (USA).gb:x")
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", hashedMeta("dd04")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.Matched || got.SkipReason != SkipNoDatMatch {
		t.Errorf("got %+v, want loud %q", got, SkipNoDatMatch)
	}
	if _, err := os.Stat(workRoot); !os.IsNotExist(err) {
		t.Error("disabled fallback must not stage")
	}
}

func TestIdentifyAmbiguousStems(t *testing.T) {
	store := newFakeStore()
	store.put("nes", "ee05",
		db.DatRomMatch{GameName: "Contra (USA)", RomName: "Contra (USA).nes"},
		db.DatRomMatch{GameName: "Probotector (Europe)", RomName: "Probotector (Europe).nes"},
	)
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "contra.nes", "shared bytes")
	got, err := id.Identify(context.Background(), libItem(1, src, "nes", hashedMeta("ee05")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Ambiguous || len(got.AmbiguousWith) != 2 || got.NameSource != NameSourceDat {
		t.Errorf("got %+v, want ambiguous with both stems", got)
	}
}

// ── hashless arm (self-heal) ─────────────────────────────────────────────────

func TestIdentifyHashlessArmSelfHeals(t *testing.T) {
	content := "raw rom bytes"
	store := newFakeStore()
	store.put("gb", md5hex(content), db.DatRomMatch{GameName: "Game (USA)", RomName: "Game (USA).gb"})
	workRoot := t.TempDir()
	id := NewIdentifier(store, testConvertoClient(t), workRoot, false)

	src := writeROM(t, t.TempDir(), "game (u).gb", content)
	got, err := id.Identify(context.Background(), libItem(7, src, "gb", "{}"))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Game (USA).gb" || got.NameSource != NameSourceDat {
		t.Errorf("got %+v", got)
	}
	saved, ok := store.saved[7]
	if !ok || saved.MD5 != md5hex(content) {
		t.Errorf("self-heal persist missing: %v", store.saved)
	}
	if ents, _ := os.ReadDir(workRoot); len(ents) != 0 {
		t.Errorf("workRoot not cleaned: %v", ents)
	}
}

func TestIdentifyHashlessPayloadRetry(t *testing.T) {
	// A headered iNES file: whole-file hashes miss, payload hashes hit the
	// catalog's .unh row. The header is NES\x1a + nonzero PRG count.
	header := "NES\x1a\x01AAAAAAAAAAA" // 16 bytes
	payload := "PAYLOADBYTES"
	content := header + payload
	store := newFakeStore()
	store.put("nes", md5hex(payload), db.DatRomMatch{GameName: "Zelda (USA)", RomName: "Zelda (USA).unh"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "Zelda (U).nes", content)
	got, err := id.Identify(context.Background(), libItem(9, src, "nes", "{}"))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Zelda (USA).nes" {
		t.Errorf("got %+v, want payload match keeping .nes", got)
	}
	saved := store.saved[9]
	if saved.Unh == nil || saved.Unh.MD5 != md5hex(payload) || saved.Unh.Header != "ines" {
		t.Errorf("unh hashes not persisted: %+v", saved)
	}
}

func TestIdentifyStaleHashesRehash(t *testing.T) {
	// Stored hashes older than the file's mtime are distrusted: the bytes
	// are re-hashed, re-persisted, and the fresh hash decides.
	content := "current bytes"
	store := newFakeStore()
	store.put("gb", md5hex(content), db.DatRomMatch{GameName: "Fresh (USA)", RomName: "Fresh (USA).gb"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)

	src := writeROM(t, t.TempDir(), "fresh (u).gb", content)
	stale := `{"gamarr":{"md5":"0ldhash","hashed_at":"2000-01-01T00:00:00Z"}}`
	got, err := id.Identify(context.Background(), libItem(3, src, "gb", stale))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Fresh (USA).gb" {
		t.Errorf("got %+v, want match via re-hash", got)
	}
	if saved := store.saved[3]; saved.MD5 != md5hex(content) {
		t.Errorf("re-hash not persisted: %+v", saved)
	}
}

// ── online fallback arm ──────────────────────────────────────────────────────

func TestIdentifyFallbackMatchIsPlaymatchSourced(t *testing.T) {
	store := newFakeStore() // local snapshot knows nothing
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), true)

	content := "MATCH:Online Name (USA).gb:payload"
	src := writeROM(t, t.TempDir(), "offline (u).gb", content)
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", hashedMeta("ab99")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if !got.Matched || got.ProposedName != "Online Name (USA).gb" || got.NameSource != NameSourcePlaymatch {
		t.Errorf("got %+v, want playmatch-sourced match", got)
	}
}

func TestIdentifyFallbackErrorDegradesLoudly(t *testing.T) {
	store := newFakeStore()
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), true)

	src := writeROM(t, t.TempDir(), "broken (u).gb", "FAIL always")
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", hashedMeta("cd88")))
	if err != nil {
		t.Fatalf("fallback failure must not be an error (breaker): %v", err)
	}
	if got.SkipReason != SkipFallbackUnavailable {
		t.Errorf("got %+v, want %q", got, SkipFallbackUnavailable)
	}
}

func TestIdentifyFallbackMissStaysLoud(t *testing.T) {
	store := newFakeStore()
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), true)

	src := writeROM(t, t.TempDir(), "unknown (u).gb", "no dat entry anywhere")
	got, err := id.Identify(context.Background(), libItem(1, src, "gb", hashedMeta("ef77")))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.SkipReason != SkipNoDatMatch {
		t.Errorf("got %+v, want %q", got, SkipNoDatMatch)
	}
}

// ── structural skips ─────────────────────────────────────────────────────────

func TestIdentifySkips(t *testing.T) {
	store := newFakeStore()
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)
	lib := t.TempDir()

	dir := filepath.Join(lib, "Game Dir (USA)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := id.Identify(context.Background(), libItem(1, dir, "psx", "{}")); err != nil || got.SkipReason != SkipMultiFile {
		t.Errorf("dir: got %+v, %v", got, err)
	}
	rar := writeROM(t, lib, "Game.rar", "rar bytes")
	if got, err := id.Identify(context.Background(), libItem(2, rar, "gb", "{}")); err != nil || got.SkipReason != SkipRar {
		t.Errorf("rar: got %+v, %v", got, err)
	}
	if _, err := id.Identify(context.Background(), libItem(3, filepath.Join(lib, "missing.gb"), "gb", "{}")); err == nil {
		t.Error("missing file must be an identify error")
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
	inner := "zip inner payload"
	store := newFakeStore()
	store.put("gb", md5hex(inner), db.DatRomMatch{GameName: "Bubble Ghost (USA, Europe)", RomName: "Bubble Ghost (USA, Europe).gb"})
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)
	lib := t.TempDir()
	// Nested folder inside the archive exercises the flatten path.
	src := write7zGatedZip(t, lib, "Bubble Ghost (U) [!].zip",
		map[string]string{"inner/Bubble Ghost (U) [!].gb": inner})

	got, err := id.Identify(context.Background(), libItem(4, src, "gb", "{}"))
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
	store := newFakeStore()
	id := NewIdentifier(store, testConvertoClient(t), t.TempDir(), false)
	src := write7zGatedZip(t, t.TempDir(), "Pack.zip",
		map[string]string{"a.gb": "one", "b.gb": "two"})

	got, err := id.Identify(context.Background(), libItem(5, src, "gb", "{}"))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if got.SkipReason != SkipMultiFile {
		t.Errorf("got %+v, want skip %q", got, SkipMultiFile)
	}
}
