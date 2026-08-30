package selection

import (
	"reflect"
	"strings"
	"testing"

	"gamarr/internal/models"
)

func TestParse(t *testing.T) {
	cases := []struct {
		title string
		want  models.ReleaseAttrs
	}{
		{
			"Final Fantasy VII (USA) (Disc 1)",
			models.ReleaseAttrs{
				CleanTitle: "Final Fantasy VII",
				SetKey:     "final fantasy vii (usa)",
				Regions:    []string{"usa"},
				DiscIndex:  1,
			},
		},
		{
			"Final Fantasy VII (USA) (Disc 2 of 3).cue",
			models.ReleaseAttrs{
				CleanTitle: "Final Fantasy VII",
				SetKey:     "final fantasy vii (usa)",
				Regions:    []string{"usa"},
				DiscIndex:  2,
				DiscTotal:  3,
				FormatHint: "cue",
			},
		},
		{
			"[BIOS] Sony PlayStation (USA).zip",
			models.ReleaseAttrs{
				CleanTitle: "Sony PlayStation",
				SetKey:     "[bios] sony playstation (usa)",
				Regions:    []string{"usa"},
				IsBIOS:     true,
				FormatHint: "zip",
			},
		},
		{
			"Chrono Trigger (USA) (Rev A)",
			models.ReleaseAttrs{
				CleanTitle: "Chrono Trigger",
				SetKey:     "chrono trigger (usa) (rev a)",
				Regions:    []string{"usa"},
				Revision:   1,
			},
		},
		{
			"Tetris (World) (Rev 1)",
			models.ReleaseAttrs{
				CleanTitle: "Tetris",
				SetKey:     "tetris (world) (rev 1)",
				Regions:    []string{"world"},
				Revision:   1,
			},
		},
		{
			"Mario Kart (USA, Europe) (En,Fr,De)",
			models.ReleaseAttrs{
				CleanTitle: "Mario Kart",
				SetKey:     "mario kart (usa, europe) (en,fr,de)",
				Regions:    []string{"usa", "europe"},
				Languages:  []string{"En", "Fr", "De"},
			},
		},
		{
			"Sonic (Proto)",
			models.ReleaseAttrs{
				CleanTitle: "Sonic",
				SetKey:     "sonic (proto)",
				IsProto:    true,
			},
		},
		{
			"Zelda (Beta 2)",
			models.ReleaseAttrs{
				CleanTitle: "Zelda",
				SetKey:     "zelda (beta 2)",
				IsProto:    true,
			},
		},
		{
			"Doom (Demo)",
			models.ReleaseAttrs{
				CleanTitle: "Doom",
				SetKey:     "doom (demo)",
				IsDemo:     true,
			},
		},
		{
			"Bubsy (USA) [b]",
			models.ReleaseAttrs{
				CleanTitle: "Bubsy",
				SetKey:     "bubsy (usa) [b]",
				Regions:    []string{"usa"},
				BadDump:    true,
			},
		},
		{
			"Kirby (USA) [!]",
			models.ReleaseAttrs{
				CleanTitle:   "Kirby",
				SetKey:       "kirby (usa) [!]",
				Regions:      []string{"usa"},
				VerifiedDump: true,
			},
		},
		{
			"Action 52 (USA) (Unl)",
			models.ReleaseAttrs{
				CleanTitle:   "Action 52",
				SetKey:       "action 52 (usa) (unl)",
				Regions:      []string{"usa"},
				IsUnlicensed: true,
			},
		},
		{
			"1942 (World) (Aftermarket) (Unl).a78",
			models.ReleaseAttrs{
				CleanTitle:    "1942",
				SetKey:        "1942 (world) (aftermarket) (unl)",
				Regions:       []string{"world"},
				IsUnlicensed:  true,
				IsAftermarket: true,
				FormatHint:    "raw",
			},
		},
		{
			"Super Mario 4 (Taiwan) (Pirate)",
			models.ReleaseAttrs{
				CleanTitle: "Super Mario 4",
				SetKey:     "super mario 4 (taiwan) (pirate)",
				Regions:    []string{"taiwan"},
				IsPirate:   true,
			},
		},
		{
			"Some Game.chd",
			models.ReleaseAttrs{
				CleanTitle: "Some Game",
				SetKey:     "some game",
				FormatHint: "chd",
			},
		},
		{
			"Just A Title",
			models.ReleaseAttrs{
				CleanTitle: "Just A Title",
				SetKey:     "just a title",
			},
		},
		{
			// Unrecognized tags stay part of the title.
			"Metal Gear Solid (USA) (Premium Package)",
			models.ReleaseAttrs{
				CleanTitle: "Metal Gear Solid (Premium Package)",
				SetKey:     "metal gear solid (usa) (premium package)",
				Regions:    []string{"usa"},
			},
		},
		{
			// .bin is one half of a bin/cue dump → cue hint.
			"Gran Turismo (Europe).bin",
			models.ReleaseAttrs{
				CleanTitle: "Gran Turismo",
				SetKey:     "gran turismo (europe)",
				Regions:    []string{"europe"},
				FormatHint: "cue",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := Parse(tc.title)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q)\n got  %+v\n want %+v", tc.title, got, tc.want)
			}
		})
	}
}

// Discs of one set share a SetKey; a different region is a different set.
func TestParseSetKeyGroupsDiscs(t *testing.T) {
	d1 := Parse("Final Fantasy VII (USA) (Disc 1)")
	d2 := Parse("Final Fantasy VII (USA) (Disc 2)")
	d3 := Parse("Final Fantasy VII (USA) (Disc 3).cue")
	if d1.SetKey != d2.SetKey || d2.SetKey != d3.SetKey {
		t.Errorf("FF7 discs disagree on SetKey: %q / %q / %q", d1.SetKey, d2.SetKey, d3.SetKey)
	}
	eu := Parse("Final Fantasy VII (Europe) (Disc 1)")
	if eu.SetKey == d1.SetKey {
		t.Errorf("Europe disc shares SetKey with USA set: %q", eu.SetKey)
	}
}

func TestParseRevisionLetters(t *testing.T) {
	if got := Parse("Game (USA) (Rev B)").Revision; got != 2 {
		t.Errorf("Rev B = %d, want 2", got)
	}
	if got := Parse("Game (USA) (Rev 3)").Revision; got != 3 {
		t.Errorf("Rev 3 = %d, want 3", got)
	}
}

// 🔴 An extension this package does not know stays inside CleanTitle and
// BareTitle, so every title-based comparison for that platform silently fails
// to match — ownership checks, duplicate detection, the lot. Nine cart lanes
// were lit up after the list was first written; five of their extensions never
// arrived with them.
func TestCartLaneExtensionsAreStripped(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Asteroids (USA).a78", "Asteroids"},
		{"Chip's Challenge (USA, Europe).lnx", "Chip's Challenge"},
		{"Mario's Tennis (Japan, USA).vb", "Mario's Tennis"},
		{"Rockman EXE WS (Japan).wsc", "Rockman EXE WS"},
		{"Sonic the Hedgehog Pocket Adventure (World).ngc", "Sonic the Hedgehog Pocket Adventure"},
		{"Knuckles' Chaotix (USA).32x", "Knuckles' Chaotix"},
	} {
		if got := BareTitle(tc.name); got != tc.want {
			t.Errorf("BareTitle(%q) = %q, want %q", tc.name, got, tc.want)
		}
		if attrs := Parse(tc.name); strings.Contains(attrs.CleanTitle, ".") {
			t.Errorf("Parse(%q).CleanTitle = %q, still carries an extension", tc.name, attrs.CleanTitle)
		}
	}
}

// Parser v3 classifies (Aftermarket)/(Pirate), which strips them from
// CleanTitle. Ownership matching must survive that shift in BOTH directions:
// the bare key is what a tagged library file and an untagged wishlist title
// (or DAT bare_title) meet on, and it predates v3 — so a v2-named file and a
// v3-named file resolve to the same identity.
func TestOwnershipKeysSurviveAftermarketClassification(t *testing.T) {
	keys := OwnershipKeys("1942 (World) (Aftermarket) (Unl).a78")
	found := false
	for _, k := range keys {
		if k == "1942" {
			found = true
		}
	}
	if !found {
		t.Fatalf("OwnershipKeys missing bare key %q: %v", "1942", keys)
	}
	// And the pirate tag likewise never blocks the bare meeting point.
	keys = OwnershipKeys("Super Mario 4 (Taiwan) (Pirate)")
	if keys[len(keys)-1] != "super mario 4" {
		t.Fatalf("bare key not last for pirate-tagged title: %v", keys)
	}
}
