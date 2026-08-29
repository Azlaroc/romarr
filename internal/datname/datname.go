// Package datname turns local DAT catalog hash hits into a proposed library
// filename. It is the single naming resolver: the bulk renamer, the
// import-time normalizer and the drift tool all answer "what should this
// file be called?" through here, so a name they disagree on cannot exist.
//
// The resolver is pure — callers do the DB lookup (db.LookupDatRomsByHash)
// and hand the candidates over, which keeps this importable from any plane.
package datname

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Candidate is one catalog rom a file's hashes landed on.
type Candidate struct {
	RomName  string // dat_roms.name — canonical file name, extension included
	GameName string // dat_games.name
}

// Outcome classifies what the candidates amount to.
type Outcome int

const (
	// NoMatch: the hashes are not in the catalog — uncatalogued content,
	// which keeps its name (it knowingly forgoes DAT name authority).
	NoMatch Outcome = iota
	// Resolved: exactly one canonical name answers.
	Resolved
	// Ambiguous: byte-identical dumps under distinct catalog names and no
	// tie-breaker applies — a human call, never an automatic rename.
	Ambiguous
)

// Resolution is Resolve's answer.
type Resolution struct {
	Outcome  Outcome
	Stem     string // canonical name minus extension, when Resolved
	Ext      string // the winning candidate's extension (may be empty)
	GameName string
	Stems    []string // the distinct stems, sorted — populated when Ambiguous
}

// unheaderedExt marks a catalog row that hashes a rom's payload without its
// container header (nes). It names a hash domain, not a file format — no
// file on disk should ever be called *.unh.
const unheaderedExt = ".unh"

func stemOf(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

// Resolve reduces hash-lookup candidates to one canonical name, or refuses.
//
// Candidates are deduped by stem first: a headered/headerless twin pair is
// two catalog games with the same name whose roms share a stem (.nes beside
// .unh), i.e. ONE answer — the non-.unh extension wins the merged entry.
// Distinct stems are a real tie (byte-identical dumps under different
// names). One tie-breaker applies, from the compilation-extraction incident:
// modern re-release DATs carry entries byte-identical to the original
// release, so when exactly one candidate is NOT a compilation entry, it is
// the original. Anything else is Ambiguous.
func Resolve(cands []Candidate) Resolution {
	type entry struct {
		ext      string
		gameName string
	}
	byStem := make(map[string]entry)
	var stems []string
	for _, c := range cands {
		if c.RomName == "" {
			continue
		}
		stem, ext := stemOf(c.RomName), filepath.Ext(c.RomName)
		e, seen := byStem[stem]
		if !seen {
			byStem[stem] = entry{ext: ext, gameName: c.GameName}
			stems = append(stems, stem)
			continue
		}
		if strings.EqualFold(e.ext, unheaderedExt) && !strings.EqualFold(ext, unheaderedExt) {
			byStem[stem] = entry{ext: ext, gameName: c.GameName}
		}
	}
	if len(stems) == 0 {
		return Resolution{Outcome: NoMatch}
	}
	sort.Strings(stems)
	if len(stems) == 1 {
		e := byStem[stems[0]]
		return Resolution{Outcome: Resolved, Stem: stems[0], Ext: e.ext, GameName: e.gameName}
	}
	var nonComp []string
	for _, s := range stems {
		if !LooksLikeCompilationEntry(s) {
			nonComp = append(nonComp, s)
		}
	}
	if len(nonComp) == 1 {
		e := byStem[nonComp[0]]
		return Resolution{Outcome: Resolved, Stem: nonComp[0], Ext: e.ext, GameName: e.gameName}
	}
	return Resolution{Outcome: Ambiguous, Stems: stems}
}

// compilationEntryRe flags DAT names that look like extractions from modern
// compilation/re-release products rather than original releases. Some
// No-Intro DATs carry byte-identical entries for both ("Super Pocket - The
// Atari Collection (World) (Extracted)", "(Atari Anthology)", "(Atari Lynx
// Collection 1)"), making the hash lookup ambiguous — the resolver can
// legitimately return the compilation entry. Only parenthesized tags match,
// and "Collection" only with a trailing number, so title-position words
// ("Konami GB Collection Vol. 1 (Europe)") are never flagged.
var compilationEntryRe = regexp.MustCompile(`\([^)]*\b(?:Anthology|Collection \d|Extracted)\b[^)]*\)`)

// LooksLikeCompilationEntry reports whether a DAT name carries a
// compilation/re-release tag.
func LooksLikeCompilationEntry(name string) bool {
	return compilationEntryRe.MatchString(name)
}

// ProposedName maps a resolved canonical stem+ext onto the file it names.
// Archives keep their outer extension (the DAT names the inner file). A
// datExt of "" or ".unh" falls back to the file's own extension — .unh is a
// hash-domain marker, never a filename.
func ProposedName(stem, datExt, currentName, archiveExt string) string {
	if archiveExt != "" {
		return stem + archiveExt
	}
	if datExt == "" || strings.EqualFold(datExt, unheaderedExt) {
		return stem + filepath.Ext(currentName)
	}
	return stem + datExt
}
