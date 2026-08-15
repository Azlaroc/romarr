package selection

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"

	"gamarr/internal/db"
	"gamarr/internal/models"
)

// Action is what the selector decided to do for one wishlist/request item.
type Action int

const (
	// ActionSkip means grab nothing; Decision.Reason says why.
	ActionSkip Action = iota
	// ActionGrab means download Grabs[0].
	ActionGrab
	// ActionGrabSet means download all Grabs as one disc set converging into
	// Grabs[0].SetDir.
	ActionGrabSet
)

func (a Action) String() string {
	switch a {
	case ActionGrab:
		return "grab"
	case ActionGrabSet:
		return "grab_set"
	default:
		return "skip"
	}
}

// Grab is one download the selector wants executed.
type Grab struct {
	Result *models.SearchResult
	// TargetFile names a single file inside a pack torrent for the #256
	// pluck executor. Populated when PR-5's enforce wiring lands a confident
	// prediction; "" downloads the whole payload (safe default — the watcher
	// falls back to whole-pack on a miss anyway).
	TargetFile string
	// Disc-set stamps; zero values for a single grab.
	DiscSetID string
	DiscIndex int
	DiscTotal int
	// SetDir is the shared game-directory name all set members converge
	// into (disc token stripped, single path component).
	SetDir string
}

// Decision is the selector's verdict for one item.
type Decision struct {
	Action   Action
	Grabs    []Grab
	Reason   string
	Rejected []Rejection
}

// SelectOpts carries the per-item context Select ranks against.
type SelectOpts struct {
	Query        string
	PlatformSlug string
	// MinScore gates the winner's quality score (SearchResult.Score, the
	// tier-6 value) — the SCHEDULER_MIN_SCORE analog of the legacy gate.
	MinScore int
	// Profile is the resolved per-platform policy; nil uses the built-in
	// default.
	Profile *db.QualityProfile
	// Owned returns the library item this title already resolves to, or nil.
	// Nil func disables the check (PR-5 wires it on the scheduler path).
	Owned func(title, platformSlug string) *db.LibraryItem
	// ActiveGrab reports an in-flight job/set already downloading this
	// title. Nil func disables the check (PR-5 wires it).
	ActiveGrab func(title, platformSlug string) bool
	// OwnedByHash returns the library item whose stored hash identity matches
	// the given release hashes, or nil. Consulted for the ranked winner only:
	// a winner byte-identical to a library item means the item is already
	// fulfilled, regardless of how the titles compare. Nil func disables the
	// check (wired on the scheduler's enforce path).
	OwnedByHash func(md5, sha1 string) *db.LibraryItem
}

// Select picks the release(s) to grab from prepared candidates (Pipeline.
// Prepare output: annotated, filtered, tier-sorted). It is pure over its
// inputs: same candidates + opts, same decision.
func Select(cands []*models.SearchResult, opts SelectOpts) Decision {
	prof := opts.Profile
	if prof == nil {
		prof = db.DefaultQualityProfile()
	}

	if opts.Owned != nil {
		if item := opts.Owned(opts.Query, opts.PlatformSlug); item != nil {
			return Decision{Action: ActionSkip, Reason: "owned"}
		}
	}
	if opts.ActiveGrab != nil && opts.ActiveGrab(opts.Query, opts.PlatformSlug) {
		return Decision{Action: ActionSkip, Reason: "already downloading"}
	}

	// Hard filters.
	var rejected []Rejection
	var survivors []*models.SearchResult
	for _, r := range cands {
		if reason := filterCandidate(r, prof); reason != "" {
			rejected = append(rejected, Rejection{Title: r.Title, Reason: reason})
			continue
		}
		survivors = append(survivors, r)
	}

	// Disc-set grouping: multi-disc releases only compete as complete sets.
	type setGroup struct {
		key   string
		discs map[int]*models.SearchResult // disc index -> best release
		total int                          // declared "of M" total, 0 if undeclared
	}
	groups := map[string]*setGroup{}
	var singles []*models.SearchResult
	for _, r := range survivors {
		a := r.Attrs
		if a == nil || a.DiscIndex == 0 {
			singles = append(singles, r)
			continue
		}
		key := a.SetKey + "|" + strings.ToLower(r.Indexer)
		g := groups[key]
		if g == nil {
			g = &setGroup{key: key, discs: map[int]*models.SearchResult{}}
			groups[key] = g
		}
		if g.discs[a.DiscIndex] == nil {
			g.discs[a.DiscIndex] = r
		}
		if a.DiscTotal > g.total {
			g.total = a.DiscTotal
		}
	}

	// A set is complete when discs are contiguous 1..N and N matches the
	// declared total (when one was declared). Complete sets are represented
	// in ranking by disc 1; incomplete sets are rejected wholesale so a lone
	// "(Disc 2 of 3)" is never grabbed solo.
	type candidate struct {
		rep *models.SearchResult
		set *setGroup
	}
	candidates := make([]candidate, 0, len(singles)+len(groups))
	for _, r := range singles {
		candidates = append(candidates, candidate{rep: r})
	}
	// Deterministic group iteration: sort keys.
	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	for _, k := range groupKeys {
		g := groups[k]
		n := len(g.discs)
		contiguous := true
		for i := 1; i <= n; i++ {
			if g.discs[i] == nil {
				contiguous = false
				break
			}
		}
		if !contiguous || (g.total != 0 && n != g.total) {
			want := g.total
			if want == 0 {
				want = n
			}
			rejected = append(rejected, Rejection{
				Title:  g.discs[minDiscIndex(g.discs)].Title,
				Reason: fmt.Sprintf("incomplete disc set (%d of %d)", n, want),
			})
			continue
		}
		candidates = append(candidates, candidate{rep: g.discs[1], set: g})
	}

	if len(candidates) == 0 {
		return Decision{Action: ActionSkip, Reason: "no candidates", Rejected: rejected}
	}

	// Rank set representatives against singles; candidates arrive
	// tier-sorted from Prepare but set grouping changed the population, so
	// re-rank here for correctness independent of input order.
	qTokens := titleTokens(opts.Query)
	sort.SliceStable(candidates, func(i, j int) bool {
		return buildRankKey(candidates[i].rep, prof, qTokens).less(buildRankKey(candidates[j].rep, prof, qTokens))
	})
	winner := candidates[0]

	// A winner byte-identical to a library item means the item is already
	// fulfilled — ownership is terminal, so it outranks the score gate. Only
	// the winner is checked: filtering hash-owned candidates instead would
	// promote a byte-different variant of a game already on disk. Disc sets
	// are represented by disc 1 (sets carry no hashes today anyway).
	if opts.OwnedByHash != nil && (winner.rep.MD5 != "" || winner.rep.SHA1 != "") {
		if item := opts.OwnedByHash(winner.rep.MD5, winner.rep.SHA1); item != nil {
			return Decision{Action: ActionSkip, Reason: "owned", Rejected: rejected}
		}
	}

	if winner.rep.Score < opts.MinScore {
		return Decision{
			Action:   ActionSkip,
			Reason:   fmt.Sprintf("below min score (%d < %d)", winner.rep.Score, opts.MinScore),
			Rejected: rejected,
		}
	}

	if winner.set == nil {
		return Decision{
			Action:   ActionGrab,
			Grabs:    []Grab{{Result: winner.rep}},
			Reason:   "best single release",
			Rejected: rejected,
		}
	}

	setID := newSetID()
	setDir := sanitizeComponent(StripDiscToken(winner.rep.Title))
	total := len(winner.set.discs)
	grabs := make([]Grab, 0, total)
	for i := 1; i <= total; i++ {
		grabs = append(grabs, Grab{
			Result:    winner.set.discs[i],
			DiscSetID: setID,
			DiscIndex: i,
			DiscTotal: total,
			SetDir:    setDir,
		})
	}
	return Decision{
		Action:   ActionGrabSet,
		Grabs:    grabs,
		Reason:   fmt.Sprintf("best release (disc set of %d)", total),
		Rejected: rejected,
	}
}

func minDiscIndex(discs map[int]*models.SearchResult) int {
	min := 0
	for i := range discs {
		if min == 0 || i < min {
			min = i
		}
	}
	return min
}

// newSetID mints a disc-set identifier in the job-id style (8 hex chars,
// "set-" prefixed so it is recognizable in job blobs and logs).
func newSetID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("set-%x", b)
}

// sanitizeComponent reduces a title to a single safe path component; the
// fulfillment side sanitizes again on use, this keeps the stamp itself clean.
func sanitizeComponent(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}
