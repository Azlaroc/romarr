package collection

import (
	"strings"

	"gamarr/internal/selection"
)

// Reconciliation measures a platform's set against what is actually on disk.
// One function answers both directions of #318: the fill side reads the gaps,
// the prune side reads the surplus.

// Match is a library file that satisfies a catalogued dump.
type Match struct {
	LibraryID int64  `json:"library_id"`
	Title     string `json:"title"`
	FilePath  string `json:"file_path"`
	// MatchedBy names the tier that found it, because the tiers differ in
	// strength and an operator deciding to archive a file deserves to know
	// whether we matched its hash or guessed from its name.
	MatchedBy string `json:"matched_by"`
}

// Ownership tiers, strongest first.
const (
	MatchHash  = "hash"  // a catalogued rom hash equals a stored library hash
	MatchName  = "name"  // the canonical dump name equals the library file's
	MatchTitle = "title" // parsed titles agree — the weakest, and the last resort
)

// Index is the library, viewed only as the three questions ownership asks. The
// store builds it; keeping the TIERING here (rather than behind one Find call)
// is deliberate — the order hash > name > title is policy, and policy lives in
// this package.
type Index interface {
	ByHash(md5, sha1 string) (Match, bool)
	ByName(name string) (Match, bool)
	ByTitle(keys []string) (Match, bool)
}

// Entry statuses.
const (
	StatusOwned = "owned" // the set's keeper is on disk
	StatusGap   = "gap"   // the set wants this game and we do not have it
	StatusOut   = "out"   // policy leaves this group out of the set entirely
)

// Entry is one reconciled group: the set's verdict plus what we hold.
type Entry struct {
	Group
	Status string `json:"status"`
	// Surplus lists owned dumps the set does not want — the prune's work list.
	// A group can be owned AND carry surplus (the keeper plus four regional
	// duplicates), which is the normal case on a bulk-imported platform.
	Surplus []Candidate `json:"surplus,omitempty"`
}

// Reconcile attaches ownership to every candidate and classifies each group.
//
// 🔴 A library file satisfies AT MOST ONE catalogued dump. Without that rule
// the weakest tier hands the same file to every member of a group — parsed
// titles cannot tell "Ace of Aces (USA)" from "Ace of Aces (Europe)" — and the
// prune direction then reports the KEEPER'S OWN FILE as surplus to be
// archived. So matching runs tier by tier across the whole platform, strongest
// first, and each file is claimed once.
//
// Within a tier the keeper is offered first, so a file that could satisfy
// either the keeper or a surplus dump lands on the keeper and the group reads
// as owned rather than as a gap next to a stray duplicate.
//
// Reconcile annotates the groups it is given rather than copying them: the
// ownership verdict belongs on the same rows that carry the policy verdict.
func Reconcile(groups []Group, idx Index) []Entry {
	claimed := map[int64]bool{}
	for _, tier := range []string{MatchHash, MatchName, MatchTitle} {
		for gi := range groups {
			for _, mi := range memberOrder(groups[gi]) {
				c := &groups[gi].Members[mi]
				if c.Owned != nil {
					continue
				}
				m, ok := lookupTier(c.Member, idx, tier)
				if !ok || claimed[m.LibraryID] {
					continue
				}
				claimed[m.LibraryID] = true
				m.MatchedBy = tier
				c.Owned = &m
			}
		}
	}

	out := make([]Entry, 0, len(groups))
	for _, g := range groups {
		e := Entry{Group: g, Status: StatusOut}
		if keeper, ok := g.Keeper(); ok {
			if keeper.Owned != nil {
				e.Status = StatusOwned
			} else {
				e.Status = StatusGap
			}
		}
		for _, c := range g.Members {
			if c.Owned == nil || c.Keeper {
				continue
			}
			e.Surplus = append(e.Surplus, c)
		}
		out = append(out, e)
	}
	return out
}

// memberOrder yields member indices with the keeper first.
func memberOrder(g Group) []int {
	order := make([]int, 0, len(g.Members))
	if g.KeeperIndex >= 0 && g.KeeperIndex < len(g.Members) {
		order = append(order, g.KeeperIndex)
	}
	for i := range g.Members {
		if i != g.KeeperIndex {
			order = append(order, i)
		}
	}
	return order
}

// lookupTier asks one tier. A dump with no hashes never reaches the hash tier:
// "do we own the file with no hash" is a question with no meaning, and asking
// it would let an empty-hash index entry answer yes.
func lookupTier(m Member, idx Index, tier string) (Match, bool) {
	if idx == nil {
		return Match{}, false
	}
	switch tier {
	case MatchHash:
		for _, r := range m.Roms {
			if r.MD5 == "" && r.SHA1 == "" {
				continue
			}
			if match, ok := idx.ByHash(strings.ToLower(r.MD5), strings.ToLower(r.SHA1)); ok {
				return match, true
			}
		}
	case MatchName:
		for _, name := range append([]string{m.Name}, romNames(m)...) {
			if match, ok := idx.ByName(name); ok {
				return match, true
			}
		}
	case MatchTitle:
		if match, ok := idx.ByTitle(selection.OwnershipKeys(m.Name)); ok {
			return match, true
		}
	}
	return Match{}, false
}

func romNames(m Member) []string {
	out := make([]string, 0, len(m.Roms))
	for _, r := range m.Roms {
		out = append(out, r.Name)
	}
	return out
}

// Gaps returns the entries collection mode has to fill, in the order it should
// work through them: the set's own order, which is alphabetical by title.
func Gaps(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Status == StatusGap {
			out = append(out, e)
		}
	}
	return out
}

// Counts summarises a reconciled platform for a header line. Counting here
// rather than in the API keeps every caller's arithmetic identical.
type Counts struct {
	Groups  int `json:"groups"`  // groups in the set (keeper exists)
	Owned   int `json:"owned"`   // set entries we hold
	Gaps    int `json:"gaps"`    // set entries we do not
	Out     int `json:"out"`     // groups policy excluded entirely
	Surplus int `json:"surplus"` // owned dumps the set does not want
}

// Summarise counts a reconciled set.
func Summarise(entries []Entry) Counts {
	var c Counts
	for _, e := range entries {
		switch e.Status {
		case StatusOwned:
			c.Groups++
			c.Owned++
		case StatusGap:
			c.Groups++
			c.Gaps++
		default:
			c.Out++
		}
		c.Surplus += len(e.Surplus)
	}
	return c
}
