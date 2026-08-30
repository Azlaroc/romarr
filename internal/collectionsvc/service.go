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
	"sort"
	"strings"
	"sync"

	"gamarr/internal/collection"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/selection"
)

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
	// ProfileID/ProfileName identify the COLLECTION profile that decided the
	// set (0 = the built-in Standard). Before the profile plane these named
	// the quality profile the policy was scraped from.
	ProfileID          int64    `json:"profile_id"`
	ProfileName        string   `json:"profile_name"`
	RegionPriority     []string `json:"region_priority"`
	EnglishPreferred   bool     `json:"english_preferred"`
	KeepWithoutEnglish bool     `json:"keep_without_english"`
	AllowProto         bool     `json:"allow_proto"`
	AllowDemo          bool     `json:"allow_demo"`
	AllowBIOS          bool     `json:"allow_bios"`
	AllowUnlicensed    bool     `json:"allow_unlicensed"`
	AllowAftermarket   bool     `json:"allow_aftermarket"`
	AllowPirate        bool     `json:"allow_pirate"`
	VerifiedOnly       bool     `json:"verified_only"`
	ExcludeCategories  []string `json:"exclude_categories"`
}

// SetResult is one platform's reconciled set.
type SetResult struct {
	Platform string             `json:"platform"`
	Entries  []collection.Entry `json:"entries"`
	Counts   collection.Counts  `json:"counts"`
	// Uncatalogued counts the platform's library files the catalog has never
	// heard of (hacks, homebrew, junk) — the hard-unmapped quadrant, distinct
	// from surplus (catalogued, unwanted) and out-on-disk (catalogued,
	// excluded by profile). Computed here because it needs the library
	// complement, which only the store can answer.
	Uncatalogued int              `json:"uncatalogued"`
	Policy       PolicySummary    `json:"policy"`
	CloneList    *db.CloneListRow `json:"clone_list,omitempty"`
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
	claimed := collection.ClaimedLibraryIDs(res.Entries)
	for _, item := range s.store.ListLibraryItemsForRename(slug) {
		if !claimed[item.ID] {
			res.Uncatalogued++
		}
	}
	return res
}

// policyFor resolves the platform's COLLECTION profile — the single owner of
// "what does this platform collect" since the profile plane landed. The
// quality profile is no longer consulted here; its region/category fields
// retire with the search rewire.
func (s *Service) policyFor(slug string) (collection.Policy, PolicySummary) {
	cp := s.store.ResolveCollectionProfile(slug)
	p := collection.Policy{
		RegionPriority:      cp.RegionPriority,
		AllowProto:          cp.AllowProto,
		AllowDemo:           cp.AllowDemo,
		AllowBIOS:           cp.AllowBIOS,
		AllowUnlicensed:     cp.AllowUnlicensed,
		AllowAftermarket:    cp.AllowAftermarket,
		AllowPirate:         cp.AllowPirate,
		VerifiedOnly:        cp.VerifiedOnly,
		NoEnglishPreference: !cp.EnglishPreferred,
		RequireEnglish:      !cp.KeepWithoutEnglish,
		ExcludeCategories:   cp.ExcludeCategories,
		ReReleaseTags:       s.cfg.ReReleaseTags(),
	}
	sum := PolicySummary{
		ProfileID:          cp.ID,
		ProfileName:        cp.Name,
		RegionPriority:     cp.RegionPriority,
		EnglishPreferred:   cp.EnglishPreferred,
		KeepWithoutEnglish: cp.KeepWithoutEnglish,
		AllowProto:         cp.AllowProto,
		AllowDemo:          cp.AllowDemo,
		AllowBIOS:          cp.AllowBIOS,
		AllowUnlicensed:    cp.AllowUnlicensed,
		AllowAftermarket:   cp.AllowAftermarket,
		AllowPirate:        cp.AllowPirate,
		VerifiedOnly:       cp.VerifiedOnly,
		ExcludeCategories:  cp.ExcludeCategories,
	}
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
	rows = collapseHeaderTwins(rows)
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
	// 🔴 Deterministic assembly. Two library titles routinely expand to the
	// SAME ownership key ("Boulder Dash (USA)" and "Boulder Dash (Aftermarket)"
	// both bare to "boulder dash"), and iterating the titles map directly let
	// Go's randomized map order pick the winner — a different library row per
	// rebuild, a different claim burned by the single-claim rule, and owned/gap
	// counts that wobbled between two requests on the SAME process. The rule,
	// here and in every library index: on a contested key, the LOWEST library
	// id wins.
	titles := s.store.GetAllLibraryTitles()
	keys := make([]string, 0, len(titles))
	for key := range titles {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := titles[keys[i]], titles[keys[j]]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		item := titles[key]
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
