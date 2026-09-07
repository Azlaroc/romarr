package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The org listing pinned in testdata: refresh by hand when curating a new
// platform —
//
//	curl -sS -H "Authorization: Bearer $TOKEN" \
//	  "https://api.github.com/orgs/libretro-thumbnails/repos?per_page=100&page=N"
//
// This test is what turns a guessed repository name into a build failure
// instead of a silent 404 on every art fetch for that platform.
func TestArtVocabThumbReposExist(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "libretro-thumb-repos.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			known[line] = true
		}
	}
	if len(known) < 100 {
		t.Fatalf("fixture holds %d repos — a partial refresh would let every name pass", len(known))
	}
	for slug, v := range artVocab {
		if v.LibretroThumbRepo == "" {
			continue
		}
		if !known[v.LibretroThumbRepo] {
			t.Errorf("%s: thumb repo %q is not in the libretro-thumbnails org listing", slug, v.LibretroThumbRepo)
		}
	}
}

func TestArtVocabAccentFormat(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for slug, v := range artVocab {
		if !hex.MatchString(v.AccentColor) {
			t.Errorf("%s: accent %q is not lowercase #rrggbb", slug, v.AccentColor)
		}
	}
}

// Every committed photo reference must point at a real file in the frontend
// bundle source — a typo here is a broken tile for exactly one platform,
// which nobody notices until they scroll past it.
func TestArtVocabArtRefsExist(t *testing.T) {
	assetsDir := filepath.Join("..", "..", "web", "frontend", "public", "assets", "platforms")
	for slug, v := range artVocab {
		if v.ArtRef == "" {
			continue
		}
		name, ok := strings.CutPrefix(v.ArtRef, "asset:")
		if !ok {
			t.Errorf("%s: art ref %q does not use the asset: scheme", slug, v.ArtRef)
			continue
		}
		if _, err := os.Stat(filepath.Join(assetsDir, name)); err != nil {
			t.Errorf("%s: art ref %q has no file under public/assets/platforms", slug, v.ArtRef)
		}
	}
}

// ApplyArtVocab merges without disturbing anything else on the row, and
// leaves uncurated rows honestly empty.
func TestApplyArtVocab(t *testing.T) {
	p := Row{Slug: "nes", DisplayName: "NES"}
	ApplyArtVocab(&p)
	if p.AccentColor == "" || p.LibretroThumbRepo == "" || p.DisplayName != "NES" {
		t.Fatalf("nes after apply: %+v", p)
	}
	q := Row{Slug: "some-library-only-slug"}
	ApplyArtVocab(&q)
	if q.AccentColor != "" || q.ArtRef != "" || q.LibretroThumbRepo != "" {
		t.Fatalf("uncurated slug gained art vocab: %+v", q)
	}
}
