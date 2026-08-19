package platform

import (
	"sort"
	"strings"
	"sync"
)

// Row is one platform in the registry: the canonical vocabulary every part of
// the app resolves a platform through. The key is our own slug — the name the
// database, the search layer and the on-disk directory tree already speak —
// and IGDBSlug/IGDBID carry the canonical *identity*, the way Radarr owns its
// own row id while TMDB's id rides along as a column.
//
// Every other name a platform answers to is a field here rather than a fresh
// map somewhere: before this existed there were nine separate hardcoded
// platform vocabularies, and each new need minted a tenth.
//
// A row may legitimately populate some fields and not others. A platform can
// have a DAT lane and no Prowlarr category, or a directory on disk and no
// IGDB identity at all. Absence means "no opinion", never "invalid".
type Row struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`

	// IGDBSlug/IGDBID are the canonical identity, unique across rows when set.
	IGDBSlug string `json:"igdb_slug"`
	IGDBID   int    `json:"igdb_id"`

	// RommFSSlug is the directory name under the ROM library root and the
	// slug RomM's API speaks. Load-bearing: it decides where a ROM is
	// written, so a wrong value does not misdisplay, it misfiles.
	RommFSSlug string `json:"romm_fs_slug"`

	ProwlarrCategories []int  `json:"prowlarr_categories"`
	TorznabCategory    string `json:"torznab_category"`

	// MediaClass is carts|discs|arcade|computer|pc — the platform's shape,
	// which is also the profile-template class (a DAT authority's kind for
	// platforms that have a lane).
	MediaClass    string `json:"media_class"`
	ConvertsToCHD bool   `json:"converts_to_chd"`

	// AcquisitionEnabled is the per-platform on/off switch for automatic
	// acquisition. IsSystem marks a directory that is not a platform at all
	// (forwarders, supporting files) — enumerable so the Library filter can
	// reach those rows, never acquirable.
	AcquisitionEnabled bool `json:"acquisition_enabled"`
	IsSystem           bool `json:"is_system"`

	// CollectionMode monitors the platform's whole 1G1R set: its gaps become
	// wanted work rather than each title being asked for by hand. Off unless
	// an operator turned it on — nothing opts a platform into acquiring a
	// whole catalog on its behalf.
	CollectionMode bool `json:"collection_mode"`

	// DefaultProfileID is the quality profile new titles on this platform
	// default to. Zero means none has been materialized yet.
	DefaultProfileID int64  `json:"default_profile_id"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// Registry supplies the rows. Declared here, in the leaf package, so every
// consumer can resolve a platform without importing internal/db — the same
// arrangement search.BandStore and archiveorg.CacheStore use. *db.JobStore
// satisfies it structurally.
type Registry interface {
	PlatformRows() []Row
}

// The table is memoized rather than queried per lookup: a display name is
// resolved once per search result, which would be thousands of SELECTs
// against a table that changes when an operator edits it. It is not snapshot
// at boot either — an edit has to take effect without a restart. Hence lazy
// load with explicit invalidation, exactly like the size bands.
var (
	regMu     sync.RWMutex
	regSource Registry
	regRows   map[string]Row
	regByCat  map[int][]string
	regByFS   map[string]string
	// The IGDB identity indices: our slug is the primary key and IGDB's is a
	// column, so resolving "which of our platforms is this IGDB platform?" is
	// a lookup on that column rather than a second vocabulary.
	regByIGDBSlug map[string]string
	regByIGDBID   map[int]string
	regLoaded     bool
)

// SetRegistry attaches the store. Called once at boot. A nil registry — every
// unit test, and any binary that does not wire one — falls back to slug-shaped
// answers rather than failing: not knowing a platform's pretty name must never
// be the thing that stops a grab.
func SetRegistry(r Registry) {
	regMu.Lock()
	defer regMu.Unlock()
	regSource = r
	regRows = nil
	regByCat = nil
	regByFS = nil
	regByIGDBSlug = nil
	regByIGDBID = nil
	regLoaded = false
}

// RefreshRegistry drops the memo so the next lookup re-reads the table.
// Called after any write to a platform row.
func RefreshRegistry() {
	regMu.Lock()
	defer regMu.Unlock()
	regRows = nil
	regByCat = nil
	regByFS = nil
	regLoaded = false
}

func rows() map[string]Row {
	regMu.RLock()
	if regLoaded {
		defer regMu.RUnlock()
		return regRows
	}
	regMu.RUnlock()

	regMu.Lock()
	defer regMu.Unlock()
	if regLoaded {
		return regRows
	}
	regRows = map[string]Row{}
	regByCat = map[int][]string{}
	regByFS = map[string]string{}
	regByIGDBSlug = map[string]string{}
	regByIGDBID = map[int]string{}
	if regSource != nil {
		for _, r := range regSource.PlatformRows() {
			regRows[r.Slug] = r
			for _, c := range r.ProwlarrCategories {
				regByCat[c] = append(regByCat[c], r.Slug)
			}
			if r.RommFSSlug != "" && r.RommFSSlug != r.Slug {
				regByFS[r.RommFSSlug] = r.Slug
			}
			if r.IGDBSlug != "" {
				regByIGDBSlug[r.IGDBSlug] = r.Slug
			}
			if r.IGDBID != 0 {
				regByIGDBID[r.IGDBID] = r.Slug
			}
		}
	}
	regLoaded = true
	return regRows
}

// Lookup returns a platform's row.
func Lookup(slug string) (Row, bool) {
	r, ok := rows()[slug]
	return r, ok
}

// Rows returns every registered platform, ordered by display name.
func Rows() []Row {
	all := rows()
	out := make([]Row, 0, len(all))
	for _, r := range all {
		out = append(out, r)
	}
	// Stable, human order: this enumeration is what fills every picker.
	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].DisplayName), strings.ToLower(out[j].DisplayName)
		if li != lj {
			return li < lj
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// DisplayName resolves a slug to its human name. An unregistered slug answers
// with the slug itself, upper-cased — the fallback the library scan and the
// platform list already used. It deliberately never answers "Unknown": that
// string reads as a failure to the operator when the truth is only that we
// have no prettier name than the one they typed.
func DisplayName(slug string) string {
	if r, ok := Lookup(slug); ok && r.DisplayName != "" {
		return r.DisplayName
	}
	if slug == "" {
		return ""
	}
	return strings.ToUpper(slug)
}

// TorznabCategory returns the Newznab category a platform's results are
// published under. Console/Other is the safe default, as before.
func TorznabCategory(slug string) string {
	if r, ok := Lookup(slug); ok && r.TorznabCategory != "" {
		return r.TorznabCategory
	}
	return "1090"
}

// shippedCHDPlatforms is the disc set the app converted before the policy
// became a registry column. It is the answer when NO registry is attached —
// a unit test, or any binary that wires none — and never overrides a registry
// that is attached: "no registry" and "the registry says no" are different
// answers, and only the first one should fall back.
//
// The distinction that decides which lookups get a fallback at all: a missing
// display name is cosmetic, so DisplayName degrades to the slug. A missing
// CHD policy, category set or fs_slug changes what the app DOES with real
// files, so those degrade to the shipped values instead.
var shippedCHDPlatforms = map[string]bool{"psx": true, "ps2": true, "psp": true, "dc": true}

// ConvertsToCHD reports whether a platform's disc images are compressed to
// CHD on import.
func ConvertsToCHD(slug string) bool {
	if r, ok := Lookup(slug); ok {
		return r.ConvertsToCHD
	}
	if len(rows()) == 0 {
		return shippedCHDPlatforms[slug]
	}
	return false
}

// AcquisitionEnabled reports whether automatic acquisition runs for a
// platform. An unregistered slug is allowed: the switch exists to turn a
// known platform off, not to gate unknown ones.
func AcquisitionEnabled(slug string) bool {
	if r, ok := Lookup(slug); ok {
		return r.AcquisitionEnabled
	}
	return true
}

// CollectionModeOn reports whether a platform monitors its whole 1G1R set.
//
// Unlike AcquisitionEnabled, an unregistered slug answers FALSE. The two
// defaults differ because the failure modes do: acquisition defaulting off
// would silently stop work an operator asked for, while collection mode
// defaulting on would start work nobody asked for — across an entire catalog.
func CollectionModeOn(slug string) bool {
	if r, ok := Lookup(slug); ok {
		return r.CollectionMode
	}
	return false
}

// CategoriesFor returns the Prowlarr category IDs a platform publishes under.
// An unregistered slug, or one with no categories of its own, answers with
// nothing — the caller decides what a blank answer means.
func CategoriesFor(slug string) []int {
	r, ok := Lookup(slug)
	if !ok {
		return nil
	}
	return r.ProwlarrCategories
}

// rommFSFor resolves a slug to its on-disk directory name, "" when the
// registry has nothing to say.
func rommFSFor(slug string) string {
	if r, ok := Lookup(slug); ok {
		return r.RommFSSlug
	}
	return ""
}

// slugForRommFS inverts rommFSFor through an index built with the memo, so
// the library sync can translate per ROM without scanning the table.
func slugForRommFS(fsSlug string) string {
	rows()
	regMu.RLock()
	defer regMu.RUnlock()
	return regByFS[fsSlug]
}

// StaticRegistry is a fixed set of rows, for callers that hold a vocabulary
// in hand rather than a database — package tests being the main one. It keeps
// those tests honest: a package whose behaviour depends on what a platform is
// has to say what the platform is, rather than inheriting a table that has
// quietly moved.
type StaticRegistry []Row

// PlatformRows implements Registry.
func (s StaticRegistry) PlatformRows() []Row { return []Row(s) }

// SlugForIGDB resolves an IGDB platform identity to our platform slug. The id
// is authoritative when present (IGDB slugs have been renamed before, ids
// have not); the slug is the fallback. Reports false when no row claims it,
// which is the honest answer for a platform RomArr has no lane for — the
// caller surfaces it rather than guessing.
func SlugForIGDB(igdbSlug string, igdbID int) (string, bool) {
	rows() // ensure the indices are built
	regMu.RLock()
	defer regMu.RUnlock()
	if igdbID != 0 {
		if slug, ok := regByIGDBID[igdbID]; ok {
			return slug, true
		}
	}
	if igdbSlug != "" {
		if slug, ok := regByIGDBSlug[igdbSlug]; ok {
			return slug, true
		}
	}
	return "", false
}
