package platform

import "testing"

// The registry customs exist on at most one tracker's private list; the
// standard Newznab category (and its root) is what indexers actually
// normalize releases to. The filter set must carry both — the customs-only
// answer silently emptied every Prowlarr search for platforms like ps2.
func TestGetCategoriesForPlatform_RegistryAddsStandardCats(t *testing.T) {
	SetRegistry(StaticRegistry{
		{Slug: "ps2", DisplayName: "PS2", ProwlarrCategories: []int{100011}, TorznabCategory: "1090"},
		{Slug: "gb", DisplayName: "Game Boy", TorznabCategory: "1090"},
		{Slug: "ps3", DisplayName: "PS3", ProwlarrCategories: []int{100043}, TorznabCategory: "1080"},
	})
	t.Cleanup(func() { SetRegistry(nil) })

	assertCats := func(slug string, want []int) {
		t.Helper()
		got := GetCategoriesForPlatform(slug)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", slug, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", slug, got, want)
				return
			}
		}
	}

	// Customs first (tracker-specific), then the standard cat, then its root.
	assertCats("ps2", []int{100011, 1090, 1000})
	// No customs at all: the standard cats alone — NOT the old fallback of
	// every OTHER platform's customs, which kept wrong-platform releases and
	// dropped right ones.
	assertCats("gb", []int{1090, 1000})
	assertCats("ps3", []int{100043, 1080, 1000})

	// A slug the registry does not know keeps the legacy every-category
	// fallback: the caller would rather over-return than silently empty.
	if got := GetCategoriesForPlatform("zx-spectrum"); len(got) != len(PlatformMap) {
		t.Errorf("unknown slug: got %d cats, want all %d", len(got), len(PlatformMap))
	}
}

func TestStandardCategories(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"1090", []int{1090, 1000}},
		{"1080", []int{1080, 1000}},
		{"4070", []int{4070, 4000}},
		{"1000", []int{1000}}, // already the root — no duplicate
		{"", nil},
		{"junk", nil},
	}
	for _, c := range cases {
		got := standardCategories(c.in)
		if len(got) != len(c.want) {
			t.Errorf("standardCategories(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("standardCategories(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestDetectPlatformFromTitle(t *testing.T) {
	cases := []struct {
		title    string
		wantSlug string
		wantOK   bool
	}{
		// The live shape that motivated the rescue: a PS2 release misfiled
		// under a sibling console category, named honestly in the title.
		{"Grand Theft Auto Vice City v1 02 USA PS2-PS4 -PSN", "ps2", true},
		{"Gran Turismo PS3", "ps3", true},
		{"Zelda [NSP] Switch", "switch", true},
		// The vocabularies that used to pass as evidence-free: remaster
		// platforms and the NSW scene tag.
		{"Grand.Theft.Auto.Vice.City.The.Definitive.Edition.PS4-DUPLEX", "ps4", true},
		{"Grand Theft Auto Vice City The Definitive Edition Update v1 0 8 NSW-VENOM", "switch", true},
		{"Persona 4 Golden PS Vita", "psvita", true},
		{"Grand.Theft.Auto.Vice.City.XBOX-WAM", "xbox", true},
		{"just some text file", "", false},
	}
	for _, c := range cases {
		info, ok := DetectPlatformFromTitle(c.title)
		if ok != c.wantOK {
			t.Errorf("%q: ok=%v, want %v", c.title, ok, c.wantOK)
			continue
		}
		if ok && info.Slug != c.wantSlug {
			t.Errorf("%q: slug=%q, want %q", c.title, info.Slug, c.wantSlug)
		}
	}
}
