// Package collectionsvc reconciles a platform's catalog against its library:
// it loads the DAT members, the clone-list overlay and the platform's policy,
// runs internal/collection over them, and answers "what does the set contain,
// what do we have, and what is surplus".
//
// It is the third package the layering forces, exactly as internal/datsvc is
// for the parser: internal/collection is pure policy over value structs and
// must not import internal/db, while internal/db owns rows and must not import
// policy. The mapping lives here.
package collectionsvc

import (
	"strings"
	"sync"

	"gamarr/internal/collection"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/selection"
)

// defaultExcludedCategories are the clone-list categories left out of a set by
// default. Applications are the DS's "Photo Channel"-class entries: catalogued
// dumps that are not games, so a set that wants them would report gaps nobody
// intends to fill.
var defaultExcludedCategories = []string{"Applications"}

// Service answers set questions for one install.
type Service struct {
	cfg   *config.Config
	store *db.JobStore

	// The fetch side is lazily built (see refresh.go): reading a set must not
	// depend on an HTTP client existing.
	once   sync.Once
	runner *runner
}

// New builds a Service. It owns no goroutine and holds no cache: a set is
// derived from rows that other planes own, and a stale copy of it would be a
// second source of truth.
func New(cfg *config.Config, store *db.JobStore) *Service {
	return &Service{cfg: cfg, store: store}
}

// PolicySummary is what decided the set, in the operator's terms. It ships
// with every set response because a keeper choice nobody can explain is the
// same defect as a rejection on an invisible number.
type PolicySummary struct {
	ProfileID         int64    `json:"profile_id"`
	ProfileName       string   `json:"profile_name"`
	RegionPriority    []string `json:"region_priority"`
	AllowProto        bool     `json:"allow_proto"`
	AllowDemo         bool     `json:"allow_demo"`
	AllowBIOS         bool     `json:"allow_bios"`
	AllowUnlicensed   bool     `json:"allow_unlicensed"`
	ExcludeCategories []string `json:"exclude_categories"`
}

// SetResult is one platform's reconciled set.
type SetResult struct {
	Platform  string             `json:"platform"`
	Entries   []collection.Entry `json:"entries"`
	Counts    collection.Counts  `json:"counts"`
	Policy    PolicySummary      `json:"policy"`
	CloneList *db.CloneListRow   `json:"clone_list,omitempty"`
	// Grouping names what decided membership, so "no clone list" is a visible
	// state rather than a silently weaker answer.
	Grouping string `json:"grouping"`
}

// Set builds a platform's reconciled 1G1R set.
//
// Returns an empty set (not an error) for a platform with no catalog: having
// no DAT lane is a legitimate state — switch is shop-native, arcade's MAME
// authority ships dormant — and the caller renders "no catalog", not a failure.
func (s *Service) Set(slug string) SetResult {
	return s.NewCycle().Set(slug)
}

// Set builds a platform's reconciled 1G1R set against this cycle's snapshot.
func (c *Cycle) Set(slug string) SetResult {
	s := c.svc
	slug = strings.TrimSpace(slug)
	res := SetResult{Platform: slug, Grouping: "title"}
	if slug == "" {
		return res
	}

	policy, summary := s.policyFor(slug)
	res.Policy = summary

	overlay := s.overlayFor(slug)
	if list, ok := s.store.GetCloneList(slug); ok && list.GroupCount > 0 {
		res.CloneList = &list
		res.Grouping = "title + clone list"
	}

	members := mapMembers(s.store.DatSetMembers(slug))
	if len(members) == 0 {
		return res
	}
	groups := collection.Build(members, overlay, policy)
	res.Entries = collection.Reconcile(groups, c.index(slug))
	res.Counts = collection.Summarise(res.Entries)
	return res
}

// policyFor resolves the platform's policy from its default profile. It
// deliberately calls ResolveProfileForItem rather than EnsurePlatformProfile:
// looking at a set must not materialize a profile as a side effect.
func (s *Service) policyFor(slug string) (collection.Policy, PolicySummary) {
	prof := s.store.ResolveProfileForItem(0, slug)
	p := collection.Policy{ExcludeCategories: defaultExcludedCategories}
	sum := PolicySummary{ExcludeCategories: defaultExcludedCategories}
	if prof != nil {
		p.RegionPriority = prof.RegionPriority
		p.AllowProto, p.AllowDemo, p.AllowBIOS = prof.AllowProto, prof.AllowDemo, prof.AllowBIOS
		sum.ProfileID, sum.ProfileName = prof.ID, prof.Name
		sum.RegionPriority = prof.RegionPriority
		sum.AllowProto, sum.AllowDemo, sum.AllowBIOS = prof.AllowProto, prof.AllowDemo, prof.AllowBIOS
	}
	// Unlicensed and aftermarket dumps have no profile flag of their own yet;
	// the set excludes them, which is what makes atari2600's homebrew pile
	// surplus rather than 1,786 gaps nobody asked for.
	sum.AllowUnlicensed = p.AllowUnlicensed
	return p, sum
}

func (s *Service) overlayFor(slug string) *collection.Overlay {
	rows := s.store.ListCloneGroups(slug)
	if len(rows) == 0 {
		return nil
	}
	byGroup := map[string]*collection.CloneGroup{}
	var list collection.CloneList
	for _, r := range rows {
		g, ok := byGroup[r.GroupName]
		if !ok {
			list.Groups = append(list.Groups, collection.CloneGroup{Name: r.GroupName})
			g = &list.Groups[len(list.Groups)-1]
			byGroup[r.GroupName] = g
			if r.Categories != "" {
				g.Categories = strings.Split(r.Categories, ",")
			}
		}
		g.Titles = append(g.Titles, collection.CloneTitle{SearchTerm: r.SearchTerm, Priority: r.Priority})
	}
	return collection.NewOverlay(list)
}

func mapMembers(rows []db.DatSetMember) []collection.Member {
	out := make([]collection.Member, 0, len(rows))
	for _, r := range rows {
		m := collection.Member{
			GameID: r.GameID, Name: r.Name, BareTitle: r.BareTitle,
			Region: r.Region, Languages: r.Languages, Revision: r.Revision,
			Flags: r.Flags, TotalSize: r.TotalSize,
		}
		for _, rom := range r.Roms {
			m.Roms = append(m.Roms, collection.Rom{
				Name: rom.Name, Size: rom.Size,
				CRC: rom.CRC, MD5: rom.MD5, SHA1: rom.SHA1,
			})
		}
		out = append(out, m)
	}
	return out
}

// A Cycle amortises the library snapshot across many platforms.
//
// Reconciling one platform needs the whole library indexed three ways. The
// scheduler reconciles every collection-mode platform in a pass, so building
// those indexes per platform would re-read the library thirty times for one
// cycle's work. A Cycle builds them once and answers for any platform.
//
// It is a snapshot on purpose: a cycle-stale view is fine — an import landing
// mid-cycle is caught by the next one — and a live view would mean a query per
// catalogued dump.
type Cycle struct {
	svc    *Service
	hash   map[string]*db.LibraryItem
	names  map[string]map[string]*db.LibraryItem
	titles map[string]map[string]*db.LibraryItem
}

// NewCycle snapshots the library.
func (s *Service) NewCycle() *Cycle {
	c := &Cycle{
		svc:    s,
		hash:   s.store.LibraryHashIndex(),
		names:  s.store.LibraryNameIndexByPlatform(),
		titles: map[string]map[string]*db.LibraryItem{},
	}
	for key, item := range s.store.GetAllLibraryTitles() {
		cut := strings.LastIndex(key, "|")
		if cut < 0 {
			continue
		}
		titleish, slug := key[:cut], key[cut+1:]
		byTitle, ok := c.titles[slug]
		if !ok {
			byTitle = map[string]*db.LibraryItem{}
			c.titles[slug] = byTitle
		}
		for _, k := range selection.OwnershipKeys(titleish) {
			if _, dup := byTitle[k]; !dup {
				byTitle[k] = item
			}
		}
	}
	return c
}

func (c *Cycle) index(slug string) collection.Index {
	return &libraryIndex{byHash: c.hash, byName: c.names[slug], byTitle: c.titles[slug]}
}

// libraryIndex is the ownership oracle for one platform.
type libraryIndex struct {
	byHash  map[string]*db.LibraryItem
	byName  map[string]*db.LibraryItem
	byTitle map[string]*db.LibraryItem
}

func (i *libraryIndex) ByHash(md5, sha1 string) (collection.Match, bool) {
	for _, key := range []string{"md5:" + md5, "sha1:" + sha1} {
		if strings.HasSuffix(key, ":") {
			continue
		}
		if item, ok := i.byHash[key]; ok && item != nil {
			return match(item), true
		}
	}
	return collection.Match{}, false
}

func (i *libraryIndex) ByName(name string) (collection.Match, bool) {
	for _, k := range nameKeys(name) {
		if item, ok := i.byName[k]; ok && item != nil {
			return match(item), true
		}
	}
	return collection.Match{}, false
}

func (i *libraryIndex) ByTitle(keys []string) (collection.Match, bool) {
	for _, k := range keys {
		if item, ok := i.byTitle[k]; ok && item != nil {
			return match(item), true
		}
	}
	return collection.Match{}, false
}

// nameKeys mirrors the store's indexing: the name as given and the name minus
// one trailing extension, both lowered. A catalogued "Tetris (World).gb" has
// to meet a library "Tetris (World).zip", and both have to meet a raw
// "Tetris (World)".
func nameKeys(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	keys := []string{strings.ToLower(name)}
	if i := strings.LastIndex(name, "."); i > 0 {
		keys = append(keys, strings.ToLower(name[:i]))
	}
	return keys
}

func match(item *db.LibraryItem) collection.Match {
	return collection.Match{LibraryID: item.ID, Title: item.Title, FilePath: item.FilePath}
}
