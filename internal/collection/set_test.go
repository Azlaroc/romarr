package collection

import (
	"strings"
	"testing"
)

// defaultPolicy mirrors what a shipped ROM profile resolves to: the region
// order the built-in profile carries, and every "allow" gate off.
func defaultPolicy() Policy {
	return Policy{
		RegionPriority:    []string{"usa", "world", "europe", "japan"},
		ExcludeCategories: []string{"Applications"},
	}
}

func member(id int64, name, region string, romNames ...string) Member {
	m := Member{GameID: id, Name: name, BareTitle: bare(name), Region: region}
	if len(romNames) == 0 {
		romNames = []string{name + ".rom"}
	}
	for _, rn := range romNames {
		m.Roms = append(m.Roms, Rom{Name: rn, Size: 1024})
	}
	return m
}

// bare strips every parenthetical, the way the catalog's stored bare_title is
// derived. Computed rather than hand-written so a fixture cannot disagree with
// what the real parser produces.
func bare(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	return name
}

// The Atari 7800 case, from the live catalog: No-Intro lists one game once per
// dump FORMAT, so "1942 (World)" appears twice — headered .a78 and headerless
// .bin — and both rows are byte-identical apart from the rom. A set that
// treated rows as games would report 352 members for 141 titles.
func TestFormatVariantsCollapseToOneGroup(t *testing.T) {
	members := []Member{
		member(1, "1942 (World)", "world", "1942 (World).a78"),
		member(2, "1942 (World)", "world", "1942 (World).bin"),
	}
	groups := Build(members, nil, defaultPolicy())
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if len(groups[0].Members) != len(members) {
		t.Errorf("members = %d, want %d", len(groups[0].Members), len(members))
	}
	keeper, ok := groups[0].Keeper()
	if !ok {
		t.Fatal("group has no keeper")
	}
	keepers := 0
	for _, c := range groups[0].Members {
		if c.Keeper {
			keepers++
		}
	}
	if keepers != 1 {
		t.Errorf("keepers = %d, want exactly 1 (kept %q)", keepers, keeper.Name)
	}
}

func TestRegionPriorityOrdersKeeper(t *testing.T) {
	p := defaultPolicy()
	members := []Member{
		member(1, "Ace of Aces (Europe)", "europe"),
		member(2, "Ace of Aces (USA)", "usa"),
	}
	groups := Build(members, nil, p)
	keeper, ok := groups[0].Keeper()
	if !ok {
		t.Fatal("no keeper")
	}
	// The expectation is computed from the policy, not restated: whichever
	// member sits earliest in RegionPriority must win.
	wantRegion := p.RegionPriority[0]
	if keeper.Region != wantRegion {
		t.Errorf("keeper region = %q, want the top-priority region %q (kept %q)", keeper.Region, wantRegion, keeper.Name)
	}
	if !strings.Contains(keeper.Reason, wantRegion) {
		t.Errorf("keeper reason %q does not name the region it won on", keeper.Reason)
	}
	for _, c := range groups[0].Members {
		if !c.Keeper && !strings.Contains(c.Reason, "surplus") {
			t.Errorf("non-keeper %q reason = %q, want a surplus reason", c.Name, c.Reason)
		}
	}
}

// David's #211 policy: English-preferred, keep Japan-only orphans. A game with
// no dump in any preferred region keeps its best available one and SAYS so —
// the prune removes redundancy, never a game.
func TestJapanOnlyOrphanIsKeptAndSaysWhy(t *testing.T) {
	p := defaultPolicy()
	p.RegionPriority = []string{"usa", "world", "europe"} // deliberately no japan
	groups := Build([]Member{member(1, "Mother 3 (Japan)", "japan")}, nil, p)
	keeper, ok := groups[0].Keeper()
	if !ok {
		t.Fatal("a Japan-only game was dropped from the set")
	}
	if keeper.Region != "japan" {
		t.Errorf("keeper region = %q, want japan", keeper.Region)
	}
	if !strings.Contains(strings.ToLower(keeper.Reason), "no preferred-region") {
		t.Errorf("reason = %q, want it to name the orphan case", keeper.Reason)
	}
}

func TestLaterRevisionWins(t *testing.T) {
	a := member(1, "Tetris (World)", "world")
	b := member(2, "Tetris (World) (Rev 1)", "world")
	b.Revision = 1
	groups := Build([]Member{a, b}, nil, defaultPolicy())
	keeper, _ := groups[0].Keeper()
	if keeper.Revision != 1 {
		t.Errorf("keeper revision = %d, want the higher revision 1 (kept %q)", keeper.Revision, keeper.Name)
	}
}

func TestPolicyGatesExcludeAndCanBeOpened(t *testing.T) {
	cases := []struct {
		name   string
		flags  string
		reason string
		allow  func(*Policy)
	}{
		{"proto", "proto", ReasonProto, func(p *Policy) { p.AllowProto = true }},
		{"demo", "demo", ReasonDemo, func(p *Policy) { p.AllowDemo = true }},
		{"bios", "bios", ReasonBIOS, func(p *Policy) { p.AllowBIOS = true }},
		{"unl", "unl", ReasonUnlicensed, func(p *Policy) { p.AllowUnlicensed = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := member(1, "Some Game (USA)", "usa")
			m.Flags = tc.flags
			groups := Build([]Member{m}, nil, defaultPolicy())
			if _, ok := groups[0].Keeper(); ok {
				t.Fatalf("%s dump became a keeper under the default policy", tc.name)
			}
			if got := groups[0].Members[0].Reason; !strings.Contains(got, tc.reason) {
				t.Errorf("exclusion reason = %q, want it to name %q", got, tc.reason)
			}
			p := defaultPolicy()
			tc.allow(&p)
			groups = Build([]Member{m}, nil, p)
			if _, ok := groups[0].Keeper(); !ok {
				t.Errorf("%s dump still excluded after the policy allowed it", tc.name)
			}
		})
	}
}

// 🔴 The discriminating tag is not always on the game. No-Intro's Atari 7800
// set names the game "1942 (World)" — no flags at all — while the rom is
// "1942 (World) (Aftermarket) (Unl).a78". Classifying the game alone would
// pull the entire homebrew pile into the set.
func TestAftermarketRomNameClassifiesTheDump(t *testing.T) {
	m := member(1, "1942 (World)", "world", "1942 (World) (Aftermarket) (Unl).a78")
	if m.Flags != "" {
		t.Fatalf("fixture is wrong: the game row must carry no flags, got %q", m.Flags)
	}
	groups := Build([]Member{m}, nil, defaultPolicy())
	if _, ok := groups[0].Keeper(); ok {
		t.Fatal("an aftermarket/unlicensed dump became the set's keeper")
	}
	if got := groups[0].Members[0].Reason; !strings.Contains(got, ReasonUnlicensed) {
		t.Errorf("reason = %q, want %q", got, ReasonUnlicensed)
	}
}

func TestGroupWithNothingButExcludedDumpsLeavesTheSet(t *testing.T) {
	a := member(1, "Unreleased Thing (USA) (Proto)", "usa")
	a.Flags = "proto"
	b := member(2, "Unreleased Thing (USA) (Proto 2)", "usa")
	b.Flags = "proto"
	groups := Build([]Member{a, b}, nil, defaultPolicy())
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if _, ok := groups[0].Keeper(); ok {
		t.Fatal("a prototype-only group produced a keeper")
	}
	if groups[0].Reason == "" {
		t.Error("a keeperless group must say why")
	}
}

func TestExcludedCategoryLeavesTheSet(t *testing.T) {
	list := CloneList{Groups: []CloneGroup{{
		Name:       "Photo Channel",
		Categories: []string{"Applications"},
		Titles:     []CloneTitle{{SearchTerm: "Photo Channel", Priority: 1}},
	}}}
	groups := Build([]Member{member(1, "Photo Channel (USA)", "usa")}, NewOverlay(list), defaultPolicy())
	if _, ok := groups[0].Keeper(); ok {
		t.Fatal("an Applications-category entry became a keeper")
	}
	if got := groups[0].Members[0].Reason; !strings.Contains(got, ReasonCategory) {
		t.Errorf("reason = %q, want the category exclusion", got)
	}
}
