// Package collection turns a platform's DAT catalog into its 1G1R set: one
// keeper per game, every other catalogued dump surplus, and a reason recorded
// on every row.
//
// This is the central policy object of RomArr 2.0's last core step. Collection
// mode and library declutter are the same reconciliation run in opposite
// directions — fill the deficit, prune the surplus — so both read the set
// built here rather than each growing its own opinion of what "the set" is.
//
// Two rules the shape of this package enforces:
//
//   - The set never rejects on a judgement you cannot see. Every member
//     carries the reason it is keeper, surplus or excluded, and every group
//     carries the reason it has no keeper.
//   - Region is a PREFERENCE, never a filter. A game that only ever released
//     in Japan keeps its best Japanese dump; the prune removes redundancy,
//     never a game. (David's #211 policy: English-preferred, keep Japan-only
//     orphans.)
//
// Layering: policy only. Value structs in, value structs out — the store maps
// rows across the boundary exactly as internal/datsvc does for the parser, and
// internal/db must never import this package.
package collection

import (
	"sort"
	"strings"

	"gamarr/internal/selection"
)

// Rom is one file of a catalogued dump. The hashes are the ownership oracle's
// join keys; the name carries tags the GAME name does not (No-Intro's Atari
// 7800 set names a game "1942 (World)" whose rom is
// "1942 (World) (Aftermarket) (Unl).a78"), which is why classification reads
// both.
type Rom struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
	CRC  string `json:"crc,omitempty"`
	MD5  string `json:"md5,omitempty"`
	SHA1 string `json:"sha1,omitempty"`
}

// Member is one catalogued dump considered for a platform's set.
type Member struct {
	GameID    int64  `json:"game_id"`
	Name      string `json:"name"`
	BareTitle string `json:"bare_title,omitempty"`
	Region    string `json:"region,omitempty"`    // comma-joined, lowered, as the catalog stores it
	Languages string `json:"languages,omitempty"` // comma-joined, e.g. "En,Fr,De"
	Revision  int    `json:"revision,omitempty"`
	Flags     string `json:"flags,omitempty"` // comma-joined: bios, proto, demo, unl, bad, verified
	TotalSize int64  `json:"total_size,omitempty"`
	Roms      []Rom  `json:"roms,omitempty"`
}

// Policy is what the operator wants the set to contain. Everything here comes
// from the platform's resolved quality profile plus the excluded catalog
// categories, so the set and the selector cannot drift apart.
type Policy struct {
	// RegionPriority is ordered best-first ("usa", "world", "europe", ...).
	// It ORDERS members; it never removes one.
	RegionPriority []string
	// AllowProto covers (Proto)/(Beta)/(Sample), AllowDemo (Demo), AllowBIOS
	// [BIOS], AllowUnlicensed (Unl)/(Aftermarket) — the same four gates the
	// selector applies to releases, read here against catalogued dumps.
	AllowProto      bool
	AllowDemo       bool
	AllowBIOS       bool
	AllowUnlicensed bool
	// ExcludeCategories names clone-list categories to leave out of the set
	// ("Applications", "Educational"). Retool's category vocabulary, not ours.
	ExcludeCategories []string
}

// Exclusion reasons. Stable strings: they are displayed, logged, and asserted
// on in tests.
const (
	ReasonProto      = "prototype/beta"
	ReasonDemo       = "demo"
	ReasonBIOS       = "BIOS"
	ReasonUnlicensed = "unlicensed/aftermarket"
	ReasonBadDump    = "bad dump"
	ReasonCategory   = "excluded category"
)

// Candidate is one member of a group with the verdict attached.
type Candidate struct {
	Member
	// Excluded is true when policy leaves this dump out of the set entirely.
	Excluded bool `json:"excluded,omitempty"`
	// Reason explains the verdict: why it was excluded, or why it won.
	Reason string `json:"reason,omitempty"`
	// Keeper is true for the single dump the set wants.
	Keeper bool `json:"keeper,omitempty"`
	// Owned is the library file satisfying this dump, when reconciliation ran
	// and found one. Nil means "not on disk" only after Reconcile; before it,
	// it means "not asked".
	Owned *Match `json:"owned,omitempty"`
}

// GroupSource records how a group's membership was decided.
const (
	SourceTitle     = "title"      // parsed catalog names agreed
	SourceCloneList = "clone-list" // an upstream clone list merged them
)

// Group is one game: every catalogued dump of it, and which one the set keeps.
type Group struct {
	// Key is the grouping key, stable across refreshes: the clone-list group
	// name when one claimed the members, else the parsed bare title.
	Key string `json:"key"`
	// Title is what to show a human.
	Title string `json:"title"`
	// Source is SourceTitle or SourceCloneList.
	Source string `json:"source"`
	// Categories are the clone list's categories for this group, if any.
	Categories []string    `json:"categories,omitempty"`
	Members    []Candidate `json:"members"`
	// KeeperIndex indexes Members, or -1 when policy excluded every dump —
	// a group of nothing but prototypes is not a game you are missing.
	KeeperIndex int `json:"keeper_index"`
	// Reason explains a keeperless group.
	Reason string `json:"reason,omitempty"`
}

// Keeper returns the dump the set wants, and false when the group has none.
func (g Group) Keeper() (Candidate, bool) {
	if g.KeeperIndex < 0 || g.KeeperIndex >= len(g.Members) {
		return Candidate{}, false
	}
	return g.Members[g.KeeperIndex], true
}

// Build groups a platform's catalogued dumps and picks each group's keeper.
// overlay may be nil, in which case grouping is by parsed title alone.
//
// Output order is stable (by group title, then key) so a paged view and a
// diff both stay put across calls.
func Build(members []Member, overlay *Overlay, p Policy) []Group {
	type bucket struct {
		key        string
		title      string
		source     string
		categories []string
		members    []Member
		// titlePriority is the clone list's preference between the different
		// titles it merged (1 = the group's preferred title).
		titlePriority map[int64]int
	}
	order := []string{}
	buckets := map[string]*bucket{}

	for _, m := range members {
		key, title, source, cats, prio := groupOf(m, overlay)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{key: key, title: title, source: source, categories: cats, titlePriority: map[int64]int{}}
			buckets[key] = b
			order = append(order, key)
		}
		// A clone list beats title agreement wherever the two meet: it is the
		// upstream opinion, and it is the only thing that can merge dumps whose
		// names differ (Probotector / Operation C / Contra).
		if source == SourceCloneList && b.source != SourceCloneList {
			b.source, b.title, b.categories = source, title, cats
		}
		b.members = append(b.members, m)
		b.titlePriority[m.GameID] = prio
	}

	out := make([]Group, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		g := Group{Key: b.key, Title: b.title, Source: b.source, Categories: b.categories, KeeperIndex: -1}
		for _, m := range b.members {
			c := Candidate{Member: m}
			if reason, excluded := excludedBy(m, b.categories, p); excluded {
				c.Excluded, c.Reason = true, reason
			}
			g.Members = append(g.Members, c)
		}
		chooseKeeper(&g, b.titlePriority, p)
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// groupOf decides which group a dump belongs to: the clone list's answer when
// it has one, the parsed bare title otherwise.
func groupOf(m Member, overlay *Overlay) (key, title, source string, categories []string, priority int) {
	if overlay != nil {
		if hit, ok := overlay.Match(m); ok {
			return "clone:" + strings.ToLower(hit.Group), hit.Group, SourceCloneList, hit.Categories, hit.Priority
		}
	}
	title = strings.TrimSpace(m.BareTitle)
	if title == "" {
		title = strings.TrimSpace(m.Name)
	}
	return "title:" + strings.ToLower(title), title, SourceTitle, nil, 1
}

// excludedBy applies the policy gates. Classification reads the game's stored
// flags AND its rom names, because the discriminating tag is not always on the
// game (see Rom).
func excludedBy(m Member, categories []string, p Policy) (string, bool) {
	f := classify(m)
	switch {
	case f.bad:
		// Always excluded, with no knob: a bad dump is not a copy of the game,
		// it is a broken file. Owning one makes it surplus, wanting one is
		// never right.
		return ReasonBadDump, true
	case f.bios && !p.AllowBIOS:
		return ReasonBIOS, true
	case f.proto && !p.AllowProto:
		return ReasonProto, true
	case f.demo && !p.AllowDemo:
		return ReasonDemo, true
	case f.unlicensed && !p.AllowUnlicensed:
		return ReasonUnlicensed, true
	}
	for _, want := range p.ExcludeCategories {
		for _, have := range categories {
			if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(have)) {
				return ReasonCategory + " (" + have + ")", true
			}
		}
	}
	return "", false
}

type flags struct{ bios, proto, demo, unlicensed, bad, verified bool }

// classify merges the catalog's own flags with what the rom names say.
func classify(m Member) flags {
	var f flags
	for _, tok := range strings.Split(m.Flags, ",") {
		switch strings.TrimSpace(tok) {
		case "bios":
			f.bios = true
		case "proto":
			f.proto = true
		case "demo":
			f.demo = true
		case "unl":
			f.unlicensed = true
		case "aftermarket":
			// Folded into unlicensed until aftermarket gets its own gate;
			// stored by parser v3 catalogs.
			f.unlicensed = true
		case "bad":
			f.bad = true
		case "verified":
			f.verified = true
		}
	}
	for _, r := range m.Roms {
		a := selection.Parse(r.Name)
		f.bios = f.bios || a.IsBIOS
		f.proto = f.proto || a.IsProto
		f.demo = f.demo || a.IsDemo
		f.unlicensed = f.unlicensed || a.IsUnlicensed
		f.bad = f.bad || a.BadDump
		f.verified = f.verified || a.VerifiedDump
		// "(Aftermarket)" is a modern No-Intro homebrew tag, parsed
		// first-class since parser v3; folded into unlicensed until it
		// gets its own gate.
		f.unlicensed = f.unlicensed || a.IsAftermarket
	}
	if ga := selection.Parse(m.Name); ga.IsAftermarket {
		// Live parse, not stored flags alone: catalogs imported by parser
		// v2 lack the aftermarket token until their next refresh.
		f.unlicensed = true
	}
	return f
}

// chooseKeeper picks the single dump the set wants and records why.
//
// Order: the clone list's title preference, then region priority, then an
// English language tag, then the later revision, then a verified dump, then
// the name for stability. Region ORDERS and never excludes, which is what
// makes a Japan-only game keep its Japanese dump instead of vanishing.
func chooseKeeper(g *Group, titlePriority map[int64]int, p Policy) {
	best := -1
	for i := range g.Members {
		if g.Members[i].Excluded {
			continue
		}
		if best < 0 || less(g.Members[i], g.Members[best], titlePriority, p) {
			best = i
		}
	}
	if best < 0 {
		g.Reason = "every catalogued dump is excluded by policy"
		return
	}
	g.KeeperIndex = best
	g.Members[best].Keeper = true
	g.Members[best].Reason = keeperReason(g.Members[best], g.Members, p)
	for i := range g.Members {
		if i != best && !g.Members[i].Excluded {
			g.Members[i].Reason = "surplus: the set keeps " + g.Members[best].Name
		}
	}
}

func less(a, b Candidate, titlePriority map[int64]int, p Policy) bool {
	if pa, pb := titlePriority[a.GameID], titlePriority[b.GameID]; pa != pb {
		return pa < pb
	}
	if ra, rb := regionRank(a.Region, p.RegionPriority), regionRank(b.Region, p.RegionPriority); ra != rb {
		return ra < rb
	}
	if ea, eb := englishRank(a), englishRank(b); ea != eb {
		return ea < eb
	}
	if a.Revision != b.Revision {
		return a.Revision > b.Revision
	}
	if va, vb := classify(a.Member).verified, classify(b.Member).verified; va != vb {
		return va
	}
	return a.Name < b.Name
}

// regionRank is the member's best position in the priority list. A region the
// list does not mention ranks after every one it does — ordered last, never
// dropped.
func regionRank(region string, priority []string) int {
	best := len(priority) + 1
	for _, r := range strings.Split(strings.ToLower(region), ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		for i, want := range priority {
			if r == strings.ToLower(strings.TrimSpace(want)) && i < best {
				best = i
			}
		}
	}
	return best
}

func englishRank(c Candidate) int {
	for _, l := range strings.Split(c.Languages, ",") {
		if strings.EqualFold(strings.TrimSpace(l), "En") {
			return 0
		}
	}
	// A dump with no language tag at all is not evidence of a foreign one;
	// only an explicitly non-English tag list ranks behind.
	if strings.TrimSpace(c.Languages) == "" {
		return 0
	}
	return 1
}

// keeperReason states the ground the keeper won on, in the operator's terms.
//
// The orphan case is checked FIRST, and independently of how many dumps the
// group holds: "no preferred-region dump exists" is the fact an operator needs
// in order to trust that an English-preferred set still contains a Japan-only
// game, and it is true whether that game has one dump or five.
func keeperReason(k Candidate, all []Candidate, p Policy) string {
	if regionRank(k.Region, p.RegionPriority) > len(p.RegionPriority) {
		return "no preferred-region dump exists; kept " + displayRegion(k.Region)
	}
	eligible := 0
	for _, c := range all {
		if !c.Excluded {
			eligible++
		}
	}
	if eligible == 1 {
		return "only catalogued dump in the set"
	}
	return "preferred region: " + displayRegion(k.Region)
}

func displayRegion(region string) string {
	if strings.TrimSpace(region) == "" {
		return "unknown region"
	}
	return region
}
