package collection

import (
	"strings"
	"testing"
)

// gbCloneListFixture is the real shape and real content of Retool's Game Boy
// list, trimmed to the groups under test. Both groups below are verbatim from
// upstream on 2026-08-19 — including the fact that "Contra" belongs to the
// group named "Operation C", which is exactly the kind of thing no parser
// could infer.
const gbCloneListFixture = `{
  "description": {"name": "Nintendo - Game Boy (No-Intro)", "lastUpdated": "2026-01-02 11:07:19"},
  "variants": [
    {"group": "Operation C", "titles": [
      {"searchTerm": "Contra"}, {"searchTerm": "Operation C"}, {"searchTerm": "Probotector"}]},
    {"group": "Contra - The Alien Wars", "titles": [
      {"searchTerm": "Contra - The Alien Wars"}, {"searchTerm": "Contra Spirits"}, {"searchTerm": "Probotector 2"}]},
    {"group": "Arcade Classic No. 2 - Centipede & Millipede", "titles": [
      {"searchTerm": "Arcade Classic No. 2 - Centipede & Millipede"},
      {"searchTerm": "Centipede (Accolade)", "priority": 2},
      {"searchTerm": "Centipede (Majesco)", "priority": 3}]},
    {"group": "Game Boy Camera", "categories": ["Applications"], "titles": [
      {"searchTerm": "Game Boy Camera"}]}
  ]
}`

func TestParseCloneList(t *testing.T) {
	list, err := ParseCloneList([]byte(gbCloneListFixture))
	if err != nil {
		t.Fatalf("ParseCloneList: %v", err)
	}
	if list.Name == "" || list.LastUpdated == "" {
		t.Errorf("description lost: name=%q lastUpdated=%q", list.Name, list.LastUpdated)
	}
	if len(list.Groups) != 4 {
		t.Fatalf("groups = %d, want 4", len(list.Groups))
	}
	// An absent priority means "the group's preferred title", i.e. 1 — stored
	// explicitly so nothing downstream has to know that 0 means 1.
	for _, g := range list.Groups {
		for _, tl := range g.Titles {
			if tl.Priority < 1 {
				t.Errorf("%s/%s priority = %d, want >= 1", g.Name, tl.SearchTerm, tl.Priority)
			}
		}
	}
}

func TestParseCloneListRejectsEmpty(t *testing.T) {
	if _, err := ParseCloneList([]byte(`{"variants": []}`)); err == nil {
		t.Error("an empty list parsed as usable")
	}
	if _, err := ParseCloneList([]byte(`not json`)); err == nil {
		t.Error("garbage parsed as a clone list")
	}
}

// The regional-retitle case, with real catalog names from the live Game Boy
// snapshot. Parsed titles alone produce three games; upstream says one.
func TestOverlayMergesRegionalRetitles(t *testing.T) {
	list, err := ParseCloneList([]byte(gbCloneListFixture))
	if err != nil {
		t.Fatal(err)
	}
	members := []Member{
		member(1, "Contra (Japan) (En)", "japan"),
		member(2, "Operation C (World) (Contra Anniversary Collection)", "world"),
		member(3, "Probotector (Europe)", "europe"),
		member(4, "Contra - The Alien Wars (USA) (SGB Enhanced)", "usa"),
		member(5, "Probotector 2 (Europe) (SGB Enhanced)", "europe"),
	}
	withList := Build(members, NewOverlay(list), defaultPolicy())
	withoutList := Build(members, nil, defaultPolicy())

	if len(withoutList) <= len(withList) {
		t.Fatalf("clone list did not merge anything: %d groups with, %d without", len(withList), len(withoutList))
	}
	byTitle := map[string]Group{}
	for _, g := range withList {
		byTitle[g.Title] = g
	}
	opc, ok := byTitle["Operation C"]
	if !ok {
		t.Fatalf("no group named for the clone list's own group name; got %v", titles(withList))
	}
	if len(opc.Members) != 3 {
		t.Errorf("Operation C members = %d, want the 3 regional titles", len(opc.Members))
	}
	if opc.Source != SourceCloneList {
		t.Errorf("group source = %q, want %q", opc.Source, SourceCloneList)
	}
	// One keeper for one game: the whole point of the merge.
	keeper, hasKeeper := opc.Keeper()
	if !hasKeeper {
		t.Fatal("merged group has no keeper")
	}
	if keeper.Region != "world" {
		t.Errorf("keeper = %q (%s), want the world dump — europe and japan rank lower", keeper.Name, keeper.Region)
	}
}

// Two different games that share a title: the list keeps them apart by the
// publisher parenthetical, which only the most-specific match can see.
func TestOverlayMostSpecificTermWins(t *testing.T) {
	list, _ := ParseCloneList([]byte(gbCloneListFixture))
	o := NewOverlay(list)
	hit, ok := o.Match(member(1, "Centipede (USA) (Majesco)", "usa"))
	if !ok {
		t.Fatal("no clone-list match for a publisher-qualified title")
	}
	if hit.Priority != 3 {
		t.Errorf("priority = %d, want 3 — the term matched must be the Majesco one", hit.Priority)
	}
}

func TestCandidateKeysOfferEveryReadingOfAName(t *testing.T) {
	keys := candidateKeys("Centipede (USA) (Majesco)")
	want := []string{"Centipede (USA) (Majesco)", "Centipede (Majesco)", "Centipede"}
	for _, w := range want {
		found := false
		for _, k := range keys {
			if strings.EqualFold(k, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("candidate keys %v missing %q", keys, w)
		}
	}
	// Longest first, so the caller's first hit is the most specific one.
	for i := 1; i < len(keys); i++ {
		if len(keys[i-1]) < len(keys[i]) {
			t.Fatalf("candidate keys not ordered most-specific-first: %v", keys)
		}
	}
}

func TestNilOverlayMatchesNothing(t *testing.T) {
	var o *Overlay
	if _, ok := o.Match(member(1, "Anything (USA)", "usa")); ok {
		t.Error("a nil overlay claimed a match")
	}
}

func titles(groups []Group) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Title)
	}
	return out
}
