package normalize

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/db"
)

type fakeDatStore struct{ byHash map[string][]db.DatRomMatch }

func (f *fakeDatStore) put(slug, hash string, ms ...db.DatRomMatch) {
	if f.byHash == nil {
		f.byHash = map[string][]db.DatRomMatch{}
	}
	f.byHash[slug+"|"+strings.ToLower(hash)] = ms
}

func (f *fakeDatStore) LookupDatRomsByHash(slug, crc, md5, sha1 string) []db.DatRomMatch {
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

func md5s(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

type fakeSettings map[string]string

func (f fakeSettings) GetSetting(key string) (string, bool) {
	v, ok := f[key]
	return v, ok
}

// localNormalizer has NO usable converto binary — proving the primary naming
// path needs nothing online.
func localNormalizer(t *testing.T, store datStore) *Normalizer {
	t.Helper()
	return New(&config.Config{
		ConvertoBin:        "rom-converto-absent-" + t.Name(),
		ConvertoTimeoutSec: 5,
		DataDir:            t.TempDir(),
	}, store)
}

func TestNormalizeFileLocalRename(t *testing.T) {
	dir := t.TempDir()
	content := []byte("gb bytes")
	p := filepath.Join(dir, "old name (u).gb")
	os.WriteFile(p, content, 0o644)
	store := &fakeDatStore{}
	store.put("gb", md5s(content), db.DatRomMatch{GameName: "New (USA)", RomName: "New (USA).gb"})

	got, res, err := localNormalizer(t, store).Normalize(context.Background(), p, "gb", nil, Policy{})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := filepath.Join(dir, "New (USA).gb")
	if got != want || !res.Renamed || res.NameSource != "dat" {
		t.Fatalf("got (%q, %+v), want local rename", got, res)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
}

// The import gate's hashes decide — the file is NOT re-read when pre is given.
func TestNormalizeFileUsesPreHashes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "grab (u).gb")
	os.WriteFile(p, []byte("bytes the store does not know"), 0o644)
	store := &fakeDatStore{}
	store.put("gb", "feedbead", db.DatRomMatch{GameName: "Gate Says (USA)", RomName: "Gate Says (USA).gb"})

	pre := &db.LibraryHashes{MD5: "feedbead"}
	got, res, _ := localNormalizer(t, store).Normalize(context.Background(), p, "gb", pre, Policy{})
	if !res.Renamed || filepath.Base(got) != "Gate Says (USA).gb" {
		t.Fatalf("got (%q, %+v), want rename decided by the gate's hashes", got, res)
	}
}

func TestNormalizeFileCollisionKeepsName(t *testing.T) {
	dir := t.TempDir()
	content := []byte("dup bytes")
	p := filepath.Join(dir, "copy (u).gb")
	os.WriteFile(p, content, 0o644)
	os.WriteFile(filepath.Join(dir, "Held (USA).gb"), []byte("other"), 0o644)
	store := &fakeDatStore{}
	store.put("gb", md5s(content), db.DatRomMatch{GameName: "Held (USA)", RomName: "Held (USA).gb"})

	got, res, _ := localNormalizer(t, store).Normalize(context.Background(), p, "gb", nil, Policy{})
	if got != p || res.Renamed {
		t.Fatalf("got (%q, %+v), want conflict to keep the import name", got, res)
	}
}

func TestNormalizeFileAmbiguousKeepsName(t *testing.T) {
	dir := t.TempDir()
	content := []byte("shared bytes")
	p := filepath.Join(dir, "which.nes")
	os.WriteFile(p, content, 0o644)
	store := &fakeDatStore{}
	store.put("nes", md5s(content),
		db.DatRomMatch{GameName: "Contra (USA)", RomName: "Contra (USA).nes"},
		db.DatRomMatch{GameName: "Probotector (Europe)", RomName: "Probotector (Europe).nes"},
	)

	got, res, _ := localNormalizer(t, store).Normalize(context.Background(), p, "nes", nil, Policy{})
	if got != p || res.Renamed {
		t.Fatalf("got (%q, %+v), want ambiguity to keep the import name", got, res)
	}
}

func TestNormalizeFileMissKeepsNameWithoutFallback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "unknown (u).gb")
	os.WriteFile(p, []byte("uncatalogued"), 0o644)

	got, res, _ := localNormalizer(t, &fakeDatStore{}).Normalize(context.Background(), p, "gb", nil, Policy{})
	if got != p || res.Renamed || res.NameSource != "" {
		t.Fatalf("got (%q, %+v), want loud miss keeping the name", got, res)
	}
}

// The online fallback runs only when the operator opted in — and then only
// after the local snapshot missed.
func TestNormalizeFileFallbackWhenEnabled(t *testing.T) {
	fakeBin := filepath.Join(t.TempDir(), "rom-converto")
	script := `#!/bin/sh
for last in "$@"; do :; done
c=$(cat "$last")
case "$c" in
  MATCH:*)
    new=${c#MATCH:}
    new=${new%%:*}
    mv "$last" "$(dirname "$last")/$new"
    ;;
esac
exit 0
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ConvertoBin: fakeBin, ConvertoTimeoutSec: 30, DataDir: t.TempDir()}

	dir := t.TempDir()
	p := filepath.Join(dir, "offline (u).gb")
	os.WriteFile(p, []byte("MATCH:Online (USA).gb:x"), 0o644)

	// Opted out (default): the engine is never consulted.
	n := New(cfg, &fakeDatStore{})
	got, res, _ := n.Normalize(context.Background(), p, "gb", nil, Policy{})
	if got != p || res.Renamed {
		t.Fatalf("fallback ran without opt-in: (%q, %+v)", got, res)
	}

	// Opted in: a local miss consults the engine, review-free at import but
	// tagged with its source.
	cfg.AttachSettings(fakeSettings{"normalize_online_fallback": "true"})
	got, res, _ = n.Normalize(context.Background(), p, "gb", nil, Policy{})
	if !res.Renamed || res.NameSource != "playmatch" || filepath.Base(got) != "Online (USA).gb" {
		t.Fatalf("fallback with opt-in = (%q, %+v)", got, res)
	}
}
