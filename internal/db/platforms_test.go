package db

import (
	"path/filepath"
	"sort"
	"testing"

	"gamarr/internal/platform"
)

// registryStore returns a store with the shipped vocabulary seeded and
// attached, and detaches it afterwards — the registry memo is process-global.
func registryStore(t *testing.T) *JobStore {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "platforms.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })
	return store
}

// The names, categories and aliases below are the values the app served
// BEFORE the registry existed, transcribed from the maps it replaced. They
// are the point of these tests: the registry is allowed to know more than the
// old maps did, and is not allowed to answer differently where they had an
// answer.

var preRegistryNames = map[string]string{
	// platform.PlatformMap
	"ps2": "PS2", "psp": "PSP", "xbox": "Xbox", "xbox360": "Xbox 360",
	"psx": "PS1", "dc": "Dreamcast", "ps3": "PS3", "wii": "Wii", "nds": "DS",
	"ngc": "GameCube", "3ds": "3DS", "ps4": "PS4", "switch": "Switch",
	// platform.ExtraPlatforms
	"n64": "Nintendo 64", "snes": "SNES", "nes": "NES", "gb": "Game Boy",
	"gba": "Game Boy Advance", "genesis": "Sega Genesis", "saturn": "Sega Saturn",
	"wiiu": "Wii U", "psvita": "PS Vita",
	// download.platformNameFromSlug carried one the others did not
	"gbc": "Game Boy Color",
	"pc":  "PC",
}

var preRegistryTorznab = map[string]string{
	"pc": "4070", "nds": "1010", "3ds": "1010", "psp": "1020", "psvita": "1020",
	"wii": "1030", "wiiu": "1030", "xbox": "1040", "xbox360": "1050", "ps3": "1080",
}

var preRegistryProwlarr = map[string][]int{
	"pc": {4000, 100010}, "ps2": {100011}, "psp": {100012}, "xbox": {100013},
	"xbox360": {100014}, "psx": {100015}, "dc": {100016}, "ps3": {100043},
	"wii": {100044}, "nds": {100045}, "ngc": {100046}, "3ds": {100072},
	"ps4": {100077}, "switch": {4050, 100082},
}

func TestPlatformSeedShipsTheWholeVocabulary(t *testing.T) {
	registryStore(t)

	rows := platform.Rows()
	if len(rows) != len(platformSeed) {
		t.Fatalf("registry has %d rows, seed has %d", len(rows), len(platformSeed))
	}

	// Every platform with a DAT lane must be in the vocabulary: the lane seed
	// is 30 slugs wide and used to have no overlap guarantee with the
	// download-side platform list at all.
	for _, lane := range datPlatformSeed {
		if _, ok := platform.Lookup(lane.PlatformSlug); !ok {
			t.Errorf("DAT lane %q has no registry row", lane.PlatformSlug)
		}
	}

	// The IGDB identity is what makes this vocabulary adopted rather than
	// invented, so every real platform must carry one.
	for _, r := range rows {
		if r.IsSystem {
			continue
		}
		if r.IGDBSlug == "" || r.IGDBID == 0 {
			t.Errorf("%s has no IGDB identity (slug=%q id=%d)", r.Slug, r.IGDBSlug, r.IGDBID)
		}
	}

	// Values a future session would plausibly "correct" from memory, and be
	// wrong: these are IGDB's actual slugs.
	for slug, want := range map[string]string{
		"psx": "ps", "tg16": "turbografx16--1", "ps4": "ps4--1",
		"genesis": "genesis-slash-megadrive", "pc": "win",
	} {
		r, _ := platform.Lookup(slug)
		if r.IGDBSlug != want {
			t.Errorf("%s igdb_slug = %q, want %q", slug, r.IGDBSlug, want)
		}
	}
}

func TestPlatformDisplayNamesMatchPreRegistryValues(t *testing.T) {
	registryStore(t)

	for slug, want := range preRegistryNames {
		if got := platform.DisplayName(slug); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", slug, got, want)
		}
	}

	// The canary: these are the slugs whose results were labelled "Unknown"
	// because the Prowlarr category map never carried them.
	for _, slug := range []string{"atari2600", "gbc", "sms", "gamegear", "colecovision", "tg16"} {
		got := platform.DisplayName(slug)
		if got == "Unknown" || got == "" {
			t.Errorf("DisplayName(%q) = %q — the registry exists to end that answer", slug, got)
		}
	}

	// An unregistered slug degrades to the slug, never to "Unknown".
	if got := platform.DisplayName("nonesuch"); got != "NONESUCH" {
		t.Errorf("DisplayName(unknown) = %q, want the slug back", got)
	}
}

// TestRommFSSlugRoundTripUnchanged is the load-bearing one: ToRommFSSlug
// decides which directory a ROM is written to, so a registry that disagrees
// with the shipped alias map would not misdisplay a name, it would misfile a
// file. genesis is the only slug that has ever differed.
func TestRommFSSlugRoundTripUnchanged(t *testing.T) {
	registryStore(t)

	for _, r := range platformSeed {
		want := r.Slug
		if want == "genesis" {
			want = "genesis-slash-megadrive"
		}
		if got := platform.ToRommFSSlug(r.Slug); got != want {
			t.Errorf("ToRommFSSlug(%q) = %q, want %q", r.Slug, got, want)
		}
		if got := platform.FromRommFSSlug(want); got != r.Slug {
			t.Errorf("FromRommFSSlug(%q) = %q, want %q", want, got, r.Slug)
		}
	}

	// Unregistered slugs still fall back to the shipped alias map rather than
	// to the bare slug.
	platform.SetRegistry(nil)
	if got := platform.ToRommFSSlug("genesis"); got != "genesis-slash-megadrive" {
		t.Errorf("with no registry, ToRommFSSlug(genesis) = %q", got)
	}
}

func TestPlatformCategoryMappingsUnchanged(t *testing.T) {
	registryStore(t)

	for slug, want := range preRegistryProwlarr {
		got := platform.GetCategoriesForPlatform(slug)
		sort.Ints(got)
		sorted := append([]int(nil), want...)
		sort.Ints(sorted)
		if len(got) != len(sorted) {
			t.Errorf("GetCategoriesForPlatform(%q) = %v, want %v", slug, got, sorted)
			continue
		}
		for i := range got {
			if got[i] != sorted[i] {
				t.Errorf("GetCategoriesForPlatform(%q) = %v, want %v", slug, got, sorted)
				break
			}
		}
	}

	// A platform with no categories of its own still searches everything —
	// that fallback is what lets an unmapped slug return results at all.
	if got := platform.GetCategoriesForPlatform("atari2600"); len(got) != len(platform.AllGameCategories()) {
		t.Errorf("unmapped platform searched %d categories, want all %d", got, len(platform.AllGameCategories()))
	}

	for _, r := range platformSeed {
		want := preRegistryTorznab[r.Slug]
		if want == "" {
			want = "1090"
		}
		if got := platform.TorznabCategory(r.Slug); got != want {
			t.Errorf("TorznabCategory(%q) = %q, want %q", r.Slug, got, want)
		}
	}
	if got := platform.TorznabCategory("nonesuch"); got != "1090" {
		t.Errorf("TorznabCategory(unknown) = %q, want Console/Other", got)
	}
}

// TestPlatformParsersResolveIntoTheRegistry pins the boundary this work drew:
// the extension map, the metadata aliases and the title hints stay in code as
// PARSERS, but every slug they can emit has to name a real registry row —
// otherwise a detection succeeds into a platform the app cannot describe,
// which is how "Unknown" got written to the library in the first place.
func TestPlatformParsersResolveIntoTheRegistry(t *testing.T) {
	registryStore(t)

	for _, slug := range platform.ParserSlugs() {
		if _, ok := platform.Lookup(slug); !ok {
			t.Errorf("parser can emit %q, which has no registry row", slug)
		}
	}
}

func TestConvertsToCHDMatchesPreRegistryList(t *testing.T) {
	registryStore(t)

	want := map[string]bool{"psx": true, "ps2": true, "psp": true, "dc": true}
	for _, r := range platformSeed {
		if got := platform.ConvertsToCHD(r.Slug); got != want[r.Slug] {
			t.Errorf("ConvertsToCHD(%q) = %v, want %v", r.Slug, got, want[r.Slug])
		}
	}
}

func TestPatchPlatformIsSparseAndLive(t *testing.T) {
	store := registryStore(t)

	before, _ := platform.Lookup("atari2600")
	yes := true
	if err := store.PatchPlatform("atari2600", PlatformPatch{ConvertsToCHD: &yes}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}
	after, ok := platform.Lookup("atari2600")
	if !ok {
		t.Fatal("row vanished after patch")
	}
	if !after.ConvertsToCHD {
		t.Error("patched field did not take effect without a restart")
	}
	if after.DisplayName != before.DisplayName || after.IGDBSlug != before.IGDBSlug {
		t.Errorf("sparse patch changed untouched fields: %+v -> %+v", before, after)
	}
	if err := store.PatchPlatform("nonesuch", PlatformPatch{ConvertsToCHD: &yes}); err == nil {
		t.Error("patching an unknown platform should fail")
	}
}

func TestSeedRunsOnceAndLeavesEditsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reseed.db")
	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	name := "Atari VCS"
	if err := store.PatchPlatform("atari2600", PlatformPatch{DisplayName: &name}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}
	store.Close()

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	row, ok := reopened.GetPlatformRow("atari2600")
	if !ok || row.DisplayName != name {
		t.Errorf("re-migration overwrote an operator edit: %q", row.DisplayName)
	}
}

func TestBackfillRepairsUnknownPlatformNames(t *testing.T) {
	store := registryStore(t)

	// Exactly the shape the archive.org driver wrote for a year.
	if _, err := store.db.Exec(`INSERT INTO library_items (title, platform, platform_slug, file_path)
		VALUES ('Pitfall!', 'Unknown', 'atari2600', '/roms/atari2600/pitfall.a26')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO library_items (title, platform, platform_slug, file_path)
		VALUES ('Kirby', 'Game Boy', 'gb', '/roms/gb/kirby.gb')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if n := store.BackfillLibraryPlatformNames(); n != 1 {
		t.Errorf("backfill repaired %d rows, want 1", n)
	}
	var name string
	store.db.QueryRow(`SELECT platform FROM library_items WHERE platform_slug = 'atari2600'`).Scan(&name)
	if name != "Atari 2600" {
		t.Errorf("platform name after backfill = %q", name)
	}
	// Idempotent: a second pass has nothing left to do.
	if n := store.BackfillLibraryPlatformNames(); n != 0 {
		t.Errorf("second backfill touched %d rows, want 0", n)
	}
}
