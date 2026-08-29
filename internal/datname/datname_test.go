package datname

import (
	"reflect"
	"testing"
)

func TestResolveEmpty(t *testing.T) {
	if got := Resolve(nil); got.Outcome != NoMatch {
		t.Fatalf("Resolve(nil) = %+v, want NoMatch", got)
	}
	if got := Resolve([]Candidate{{RomName: ""}}); got.Outcome != NoMatch {
		t.Fatalf("Resolve(empty rom name) = %+v, want NoMatch", got)
	}
}

func TestResolveSingle(t *testing.T) {
	got := Resolve([]Candidate{{RomName: "Tetris (World) (Rev 1).gb", GameName: "Tetris (World) (Rev 1)"}})
	if got.Outcome != Resolved || got.Stem != "Tetris (World) (Rev 1)" || got.Ext != ".gb" {
		t.Fatalf("single candidate = %+v", got)
	}
	if got.GameName != "Tetris (World) (Rev 1)" {
		t.Errorf("GameName = %q", got.GameName)
	}
}

// The nes header twins: two catalog games with the same name, one .nes rom
// and one .unh rom sharing a stem. One answer, headered extension wins —
// regardless of candidate order.
func TestResolveHeaderTwinsCollapse(t *testing.T) {
	twins := [][]Candidate{
		{
			{RomName: "10-Yard Fight (USA, Europe).nes", GameName: "10-Yard Fight (USA, Europe)"},
			{RomName: "10-Yard Fight (USA, Europe).unh", GameName: "10-Yard Fight (USA, Europe)"},
		},
		{
			{RomName: "10-Yard Fight (USA, Europe).unh", GameName: "10-Yard Fight (USA, Europe)"},
			{RomName: "10-Yard Fight (USA, Europe).nes", GameName: "10-Yard Fight (USA, Europe)"},
		},
	}
	for i, cands := range twins {
		got := Resolve(cands)
		if got.Outcome != Resolved || got.Stem != "10-Yard Fight (USA, Europe)" || got.Ext != ".nes" {
			t.Errorf("order %d: twins = %+v, want Resolved stem with .nes ext", i, got)
		}
	}
}

// A .unh-only match (the whole-file hash missed, the payload hash hit) still
// resolves; the .unh extension is carried and neutralized by ProposedName.
func TestResolveUnhOnly(t *testing.T) {
	got := Resolve([]Candidate{{RomName: "Metroid (USA).unh", GameName: "Metroid (USA)"}})
	if got.Outcome != Resolved || got.Stem != "Metroid (USA)" || got.Ext != ".unh" {
		t.Fatalf("unh-only = %+v", got)
	}
}

// The #290 shape: a hash tie between the original release and a modern
// compilation extraction. Exactly one non-compilation candidate wins.
func TestResolveCompilationTie(t *testing.T) {
	got := Resolve([]Candidate{
		{RomName: "Dark Cavern (USA).a26", GameName: "Dark Cavern (USA)"},
		{RomName: "Super Pocket - The Atari Collection (World) (Extracted).a26", GameName: "Super Pocket - The Atari Collection (World) (Extracted)"},
	})
	if got.Outcome != Resolved || got.Stem != "Dark Cavern (USA)" {
		t.Fatalf("compilation tie = %+v, want the original release", got)
	}
}

// Two compilation entries and no original: nothing to prefer, ambiguous.
func TestResolveAllCompilation(t *testing.T) {
	got := Resolve([]Candidate{
		{RomName: "Game A (Atari Anthology).a26"},
		{RomName: "Game A (Atari Lynx Collection 1).a26"},
	})
	if got.Outcome != Ambiguous || len(got.Stems) != 2 {
		t.Fatalf("all-compilation = %+v, want Ambiguous with both stems", got)
	}
}

// Two distinct non-compilation names for the same bytes: a human call.
func TestResolveTrueAmbiguity(t *testing.T) {
	got := Resolve([]Candidate{
		{RomName: "Contra (USA).nes"},
		{RomName: "Probotector (Europe).nes"},
	})
	if got.Outcome != Ambiguous {
		t.Fatalf("true tie = %+v, want Ambiguous", got)
	}
	want := []string{"Contra (USA)", "Probotector (Europe)"}
	if !reflect.DeepEqual(got.Stems, want) {
		t.Errorf("Stems = %v, want sorted %v", got.Stems, want)
	}
}

func TestLooksLikeCompilationEntry(t *testing.T) {
	flagged := []string{
		"Super Pocket - The Atari Collection (World) (Extracted).a26",
		"Basketbrawl (USA, Europe) (Atari Lynx Collection 1) (Unl).zip",
		"Dark Cavern (Atari Anthology).a26",
	}
	clean := []string{
		"Konami GB Collection Vol. 1 (Europe).gb",
		"Collection of Mana (USA).zip",
		"Checkered Flag (USA, Europe).zip",
		"Secret of Mana (Collection of Mana) (USA).zip", // Collection without digit
	}
	for _, s := range flagged {
		if !LooksLikeCompilationEntry(s) {
			t.Errorf("%q should be flagged", s)
		}
	}
	for _, s := range clean {
		if LooksLikeCompilationEntry(s) {
			t.Errorf("%q should NOT be flagged", s)
		}
	}
}

func TestProposedName(t *testing.T) {
	cases := []struct {
		name                           string
		stem, datExt, current, archive string
		want                           string
	}{
		{"archive keeps outer ext", "Tetris (World)", ".gb", "tetris.zip", ".zip", "Tetris (World).zip"},
		{"7z keeps outer ext", "Metroid (USA)", ".unh", "Metroid (U)_nes.7z", ".7z", "Metroid (USA).7z"},
		{"bare file takes dat ext", "Hagane (USA)", ".sfc", "Hagane (U).smc", "", "Hagane (USA).sfc"},
		{"unh never emitted", "Metroid (USA)", ".unh", "Metroid (U).nes", "", "Metroid (USA).nes"},
		{"empty dat ext keeps own", "Game (USA)", "", "Game (U).bin", "", "Game (USA).bin"},
	}
	for _, c := range cases {
		if got := ProposedName(c.stem, c.datExt, c.current, c.archive); got != c.want {
			t.Errorf("%s: ProposedName = %q, want %q", c.name, got, c.want)
		}
	}
}
