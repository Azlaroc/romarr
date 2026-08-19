package collection

import "testing"

// fakeIndex answers each tier from its own map, and records which tiers were
// asked — the order matters as much as the answers.
type fakeIndex struct {
	hashes map[string]int64
	names  map[string]int64
	titles map[string]int64
	asked  []string
}

func (f *fakeIndex) ByHash(md5, sha1 string) (Match, bool) {
	f.asked = append(f.asked, MatchHash)
	for _, h := range []string{md5, sha1} {
		if id, ok := f.hashes[h]; ok && h != "" {
			return Match{LibraryID: id, Title: "by hash"}, true
		}
	}
	return Match{}, false
}

func (f *fakeIndex) ByName(name string) (Match, bool) {
	f.asked = append(f.asked, MatchName)
	if id, ok := f.names[name]; ok {
		return Match{LibraryID: id, Title: "by name"}, true
	}
	return Match{}, false
}

func (f *fakeIndex) ByTitle(keys []string) (Match, bool) {
	f.asked = append(f.asked, MatchTitle)
	for _, k := range keys {
		if id, ok := f.titles[k]; ok {
			return Match{LibraryID: id, Title: "by title"}, true
		}
	}
	return Match{}, false
}

func hashedMember(id int64, name, region, md5 string) Member {
	m := member(id, name, region)
	m.Roms[0].MD5 = md5
	return m
}

func TestReconcileOwnedGapAndSurplus(t *testing.T) {
	p := defaultPolicy()
	members := []Member{
		hashedMember(1, "Ace of Aces (USA)", "usa", "aaa"),
		hashedMember(2, "Ace of Aces (Europe)", "europe", "bbb"),
		hashedMember(3, "Ballblazer (USA)", "usa", "ccc"),
	}
	idx := &fakeIndex{hashes: map[string]int64{"aaa": 11, "bbb": 22}}
	entries := Reconcile(Build(members, nil, p), idx)

	byTitle := map[string]Entry{}
	for _, e := range entries {
		byTitle[e.Title] = e
	}
	ace := byTitle["Ace of Aces"]
	if ace.Status != StatusOwned {
		t.Errorf("Ace of Aces status = %q, want %q", ace.Status, StatusOwned)
	}
	// The USA dump is the keeper and is owned; the European one is owned too,
	// so it is surplus — the prune's work list, not a gap.
	if len(ace.Surplus) != 1 {
		t.Fatalf("surplus = %d, want 1: %+v", len(ace.Surplus), ace.Surplus)
	}
	if ace.Surplus[0].Region != "europe" {
		t.Errorf("surplus region = %q, want europe", ace.Surplus[0].Region)
	}
	if bb := byTitle["Ballblazer"]; bb.Status != StatusGap {
		t.Errorf("Ballblazer status = %q, want %q", bb.Status, StatusGap)
	}

	counts := Summarise(entries)
	// Arithmetic, computed from the fixture: two groups in the set, one owned,
	// one gap, one surplus copy.
	if counts.Groups != 2 || counts.Owned != 1 || counts.Gaps != 1 || counts.Surplus != 1 {
		t.Errorf("counts = %+v, want groups 2 / owned 1 / gaps 1 / surplus 1", counts)
	}
	if got := len(Gaps(entries)); got != counts.Gaps {
		t.Errorf("Gaps() returned %d entries, counts say %d", got, counts.Gaps)
	}
}

// The tiers are ordered by strength: a hash is proof, a name is strong, a
// parsed title is a guess. A dump whose hash we hold must never be reported
// under a weaker tier.
func TestHashTierBeatsNameAndTitle(t *testing.T) {
	m := hashedMember(1, "Tetris (World)", "world", "deadbeef")
	idx := &fakeIndex{
		hashes: map[string]int64{"deadbeef": 7},
		names:  map[string]int64{"Tetris (World)": 8},
		titles: map[string]int64{"tetris": 9},
	}
	entries := Reconcile(Build([]Member{m}, nil, defaultPolicy()), idx)
	owned := entries[0].Members[0].Owned
	if owned == nil {
		t.Fatal("hash-owned dump reported as a gap")
	}
	if owned.MatchedBy != MatchHash || owned.LibraryID != 7 {
		t.Errorf("matched by %q (id %d), want %q (id 7)", owned.MatchedBy, owned.LibraryID, MatchHash)
	}
	if len(idx.asked) != 1 || idx.asked[0] != MatchHash {
		t.Errorf("tiers asked = %v, want the hash tier to answer alone", idx.asked)
	}
}

func TestTitleTierIsTheLastResort(t *testing.T) {
	m := member(1, "Kirby's Dream Land 2 (USA, Europe) (SGB Enhanced)", "usa")
	idx := &fakeIndex{titles: map[string]int64{"kirby's dream land 2": 5}}
	entries := Reconcile(Build([]Member{m}, nil, defaultPolicy()), idx)
	owned := entries[0].Members[0].Owned
	if owned == nil || owned.MatchedBy != MatchTitle {
		t.Fatalf("owned = %+v, want a title-tier match", owned)
	}
	// No hash tier in the trace: this catalog entry carries no hashes, and
	// asking "do we own the file with no hash" is a question with no meaning.
	// The tiers that CAN answer are tried in order — name (game, then rom),
	// then title.
	want := []string{MatchName, MatchName, MatchTitle}
	if len(idx.asked) != len(want) {
		t.Fatalf("tiers asked = %v, want every answerable tier tried in order %v", idx.asked, want)
	}
	for i := range want {
		if idx.asked[i] != want[i] {
			t.Fatalf("tiers asked = %v, want %v", idx.asked, want)
		}
	}
}

// A group policy left out of the set is not a gap: nothing should ever try to
// acquire a prototype nobody asked for.
func TestExcludedGroupIsNeverAGap(t *testing.T) {
	m := member(1, "Some Proto (USA)", "usa")
	m.Flags = "proto"
	entries := Reconcile(Build([]Member{m}, nil, defaultPolicy()), &fakeIndex{})
	if entries[0].Status != StatusOut {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusOut)
	}
	if len(Gaps(entries)) != 0 {
		t.Error("an excluded group appeared in the gap list")
	}
	if c := Summarise(entries); c.Groups != 0 || c.Out != 1 {
		t.Errorf("counts = %+v, want groups 0 / out 1", c)
	}
}

// An owned dump in a group the set excludes is still surplus: that is how
// atari2600's homebrew pile becomes a prune candidate rather than invisible.
func TestOwnedDumpInAnExcludedGroupIsSurplus(t *testing.T) {
	m := hashedMember(1, "Some Hack (World)", "world", "abc")
	m.Flags = "unl"
	entries := Reconcile(Build([]Member{m}, nil, defaultPolicy()), &fakeIndex{hashes: map[string]int64{"abc": 3}})
	if entries[0].Status != StatusOut {
		t.Fatalf("status = %q, want %q", entries[0].Status, StatusOut)
	}
	if len(entries[0].Surplus) != 1 {
		t.Errorf("surplus = %d, want the owned excluded dump", len(entries[0].Surplus))
	}
}

// 🔴 One file satisfies one dump. Parsed titles cannot tell the USA dump from
// the European one, so without claiming, a single owned file would be reported
// as BOTH the keeper and a surplus copy of itself — and the prune direction
// would offer the keeper's own file up for archiving.
func TestOneFileSatisfiesOnlyOneDump(t *testing.T) {
	members := []Member{
		member(1, "Ace of Aces (USA)", "usa"),
		member(2, "Ace of Aces (Europe)", "europe"),
	}
	idx := &fakeIndex{titles: map[string]int64{"ace of aces": 42}}
	entries := Reconcile(Build(members, nil, defaultPolicy()), idx)

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 group", len(entries))
	}
	if entries[0].Status != StatusOwned {
		t.Errorf("status = %q, want %q — the one file we hold is the keeper's", entries[0].Status, StatusOwned)
	}
	if len(entries[0].Surplus) != 0 {
		t.Fatalf("surplus = %+v, want none: there is only one file", entries[0].Surplus)
	}
	owned := 0
	for _, c := range entries[0].Members {
		if c.Owned != nil {
			owned++
		}
	}
	if owned != 1 {
		t.Errorf("%d members claim the same library row, want 1", owned)
	}
}

// The keeper gets first refusal on a file any member of its group could match,
// so a group never reads as a gap while a surplus row holds the only copy.
func TestKeeperClaimsBeforeSurplus(t *testing.T) {
	members := []Member{
		member(1, "Ace of Aces (Europe)", "europe"), // listed first on purpose
		member(2, "Ace of Aces (USA)", "usa"),       // the keeper under this policy
	}
	entries := Reconcile(Build(members, nil, defaultPolicy()), &fakeIndex{titles: map[string]int64{"ace of aces": 7}})
	keeper, ok := entries[0].Keeper()
	if !ok {
		t.Fatal("no keeper")
	}
	if keeper.Owned == nil {
		t.Errorf("keeper %q reads as a gap while another member holds the file", keeper.Name)
	}
}

// A stronger tier must win the file even when a weaker one is offered first by
// an earlier member: hash beats title across the whole platform, not just
// within one dump's lookup.
func TestStrongerTierWinsAcrossMembers(t *testing.T) {
	weak := member(1, "Tetris (Europe)", "europe")
	strong := hashedMember(2, "Tetris (Japan)", "japan", "abc")
	p := defaultPolicy()
	p.RegionPriority = []string{"europe"} // make the hashless dump the keeper
	entries := Reconcile(Build([]Member{weak, strong}, nil, p),
		&fakeIndex{hashes: map[string]int64{"abc": 5}, titles: map[string]int64{"tetris": 5}})

	var byHash, byTitle int
	for _, c := range entries[0].Members {
		if c.Owned == nil {
			continue
		}
		switch c.Owned.MatchedBy {
		case MatchHash:
			byHash++
		case MatchTitle:
			byTitle++
		}
	}
	if byHash != 1 || byTitle != 0 {
		t.Errorf("matches = %d by hash / %d by title, want the hash tier to claim the file", byHash, byTitle)
	}
}
