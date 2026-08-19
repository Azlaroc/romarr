package dat

import (
	"os"
	"strings"
	"testing"
)

// Both fixtures are real authority output, trimmed to a few entries: the
// clrmamepro one from the libretro No-Intro mirror, the logiqx one from a
// Redump PSX datfile.

func load(t *testing.T, name string) *File {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return f
}

func TestParseClrMameProHeaderAndGames(t *testing.T) {
	f := load(t, "nointro_sample.dat")

	if f.Header.Name != "Atari - 2600" {
		t.Errorf("header name = %q", f.Header.Name)
	}
	// The header version is the snapshot stamp we pin on.
	if f.Header.Version != "2026.08.01" {
		t.Errorf("header version = %q, want 2026.08.01", f.Header.Version)
	}
	if len(f.Games) != 3 {
		t.Fatalf("games = %d, want 3", len(f.Games))
	}

	g := f.Games[0]
	if g.Name != "3-D Tic-Tac-Toe (USA)" {
		t.Errorf("game name = %q", g.Name)
	}
	// A parenthesis inside a quoted name must not break block nesting.
	if g.BareTitle != "3-D Tic-Tac-Toe" {
		t.Errorf("bare title = %q, want 3-D Tic-Tac-Toe", g.BareTitle)
	}
	if g.Region != "usa" {
		t.Errorf("region = %q, want usa (from the explicit DAT attribute)", g.Region)
	}
	if len(g.Roms) != 1 {
		t.Fatalf("roms = %d, want 1 (cartridge)", len(g.Roms))
	}
	r := g.Roms[0]
	if r.Size != 2048 || g.TotalSize() != 2048 {
		t.Errorf("size = %d / total = %d, want 2048", r.Size, g.TotalSize())
	}
	// Authorities disagree on hash case; we normalise so these stay join keys.
	if r.MD5 != "0db4f4150fecf77e4ce72ca4d04c052f" {
		t.Errorf("md5 = %q, want lowercase", r.MD5)
	}
	if r.CRC != "58805709" {
		t.Errorf("crc = %q", r.CRC)
	}
	if r.SHA1 != strings.ToLower(r.SHA1) || r.SHA1 == "" {
		t.Errorf("sha1 not normalised: %q", r.SHA1)
	}
}

func TestParseClrMameProFlagsAndRegions(t *testing.T) {
	f := load(t, "nointro_sample.dat")
	if got := f.Games[1].Flags; !strings.Contains(got, "proto") {
		t.Errorf("(Proto) game flags = %q, want to contain proto", got)
	}
	if got := f.Games[0].Flags; got != "" {
		t.Errorf("plain game flags = %q, want empty", got)
	}
	if got := f.Games[2].Region; got != "europe" {
		t.Errorf("region = %q, want europe", got)
	}
}

func TestParseLogiqxMultiTrackDiscSumsEveryTrack(t *testing.T) {
	f := load(t, "redump_sample.dat")

	if f.Header.Version != "2026-06-15 11-55-46" {
		t.Errorf("header version = %q", f.Header.Version)
	}
	if len(f.Games) != 3 {
		t.Fatalf("games = %d, want 3", len(f.Games))
	}

	// The multi-track entry is the regression guard for the whole size-band
	// design: a disc's size is the sum of its .cue and every .bin track. A
	// parser that kept only the first rom would silently understate it, which
	// is exactly the defect in libretro's mirrored Redump files.
	multi := f.Games[0]
	if len(multi.Roms) < 3 {
		t.Fatalf("multi-track roms = %d, want >= 3", len(multi.Roms))
	}
	var sum, largest int64
	for _, r := range multi.Roms {
		sum += r.Size
		if r.Size > largest {
			largest = r.Size
		}
	}
	if multi.TotalSize() != sum {
		t.Errorf("total = %d, want sum %d", multi.TotalSize(), sum)
	}
	if multi.TotalSize() <= largest {
		t.Errorf("total %d not greater than largest track %d — tracks are not being summed", multi.TotalSize(), largest)
	}
	if multi.Region != "europe" {
		t.Errorf("region = %q, want europe (derived from the name)", multi.Region)
	}
	// (En,Fr,De,Es,It)
	if n := strings.Count(multi.Languages, ",") + 1; n != 5 {
		t.Errorf("languages = %q, want 5 entries", multi.Languages)
	}
}

func TestParseLogiqxCloneOfAndRevision(t *testing.T) {
	f := load(t, "redump_sample.dat")
	clone := f.Games[2]
	if clone.CloneOf == "" {
		t.Fatal("cloneof not captured — 1G1R parent/clone grouping depends on it")
	}
	if clone.Revision != 1 {
		t.Errorf("revision = %d, want 1", clone.Revision)
	}
	if clone.CloneOf != f.Games[1].Name {
		t.Errorf("cloneof = %q, want parent %q", clone.CloneOf, f.Games[1].Name)
	}
}

func TestSizeStats(t *testing.T) {
	f := load(t, "nointro_sample.dat")
	st, ok := f.SizeStats()
	if !ok {
		t.Fatal("SizeStats not ok")
	}
	if st.Min != 2048 || st.Max != 8192 {
		t.Errorf("min/max = %d/%d, want 2048/8192", st.Min, st.Max)
	}
	if st.P01 < st.Min || st.P99 > st.Max {
		t.Errorf("percentiles outside observed range: %+v", st)
	}
	// A file with no sizes must report not-ok rather than a zero floor.
	empty := &File{Games: []Game{{Name: "x"}}}
	if _, ok := empty.SizeStats(); ok {
		t.Error("SizeStats ok on a sizeless catalog")
	}
}

func TestParseStripsByteOrderMark(t *testing.T) {
	// Hand-uploaded DATs are often saved by Windows tooling with a BOM; it must
	// not send an XML file to the clrmamepro parser.
	for _, name := range []string{"redump_sample.dat", "nointro_sample.dat"} {
		raw, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		f, err := Parse(append([]byte("\ufeff"), raw...))
		if err != nil {
			t.Fatalf("parse BOM-prefixed %s: %v", name, err)
		}
		if len(f.Games) != 3 {
			t.Errorf("%s: games = %d, want 3", name, len(f.Games))
		}
	}
}

func TestParseSniffsFormatAndRejectsEmpty(t *testing.T) {
	if _, err := Parse([]byte("   \n\t ")); err == nil {
		t.Error("empty input parsed without error")
	}
	if _, err := Parse([]byte("not a dat at all")); err == nil {
		t.Error("garbage input parsed without error")
	}
	xmlF := load(t, "redump_sample.dat")
	cmpF := load(t, "nointro_sample.dat")
	if xmlF.RomCount() == 0 || cmpF.RomCount() == 0 {
		t.Error("RomCount zero")
	}
}

// 🔴 The mirror's DATs declare ONE primary region while the name lists every
// region the dump covers: `region "USA"` on a game named
// "Air-Sea Battle ~ Target Fun (Japan, USA) (En)". Preferring the attribute
// alone threw away the fact that it is a USA release, and that cost a real
// 1G1R keeper — the dump filed as Japan-only lost to a European one under a
// USA-first region order.
func TestRegionUnionsTheAttributeAndTheName(t *testing.T) {
	data := []byte("clrmamepro (\n\tname \"Test\"\n\tversion \"1\"\n)\n\n" +
		"game (\n\tname \"Air-Sea Battle ~ Target Fun (Japan, USA) (En)\"\n" +
		"\tregion \"Japan\"\n" +
		"\trom ( name \"a.a26\" size 2048 crc 00000001 )\n)\n\n" +
		"game (\n\tname \"Solo Game (Europe)\"\n\tregion \"Europe\"\n" +
		"\trom ( name \"b.a26\" size 2048 crc 00000002 )\n)\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Games) != 2 {
		t.Fatalf("games = %d, want 2", len(f.Games))
	}

	// Both regions survive, the declared one first.
	got := f.Games[0].Region
	for _, want := range []string{"japan", "usa"} {
		if !strings.Contains(got, want) {
			t.Errorf("region = %q, want it to carry %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "japan") {
		t.Errorf("region = %q, want the DAT's declared region first", got)
	}
	// A single-region entry is unchanged and gains no duplicates.
	if f.Games[1].Region != "europe" {
		t.Errorf("single-region entry = %q, want europe", f.Games[1].Region)
	}
}

func TestParserVersionIsStated(t *testing.T) {
	// A derivation change that does not bump this never reaches a catalog
	// whose bytes have not moved — which is most of them.
	if ParserVersion < 2 {
		t.Errorf("ParserVersion = %d, want the region-union derivation's version", ParserVersion)
	}
}
