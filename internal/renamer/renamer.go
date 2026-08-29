// Package renamer implements the on-demand bulk library rename (arr-style
// "Rename" preview + apply) over already-imported ROMs.
//
// Naming authority: the local DAT snapshot (dat_roms). Identification asks
// the catalog by hash — stored $.gamarr hashes when the row carries them
// (zero staging I/O), else the bytes are staged and hashed here and the
// result persisted, closing the coverage gap as a side effect. The online
// Playmatch engine (rom-converto `dat rename`) survives only as an OPTIONAL
// fallback for local misses, off by default; its answers are review-only and
// its failures degrade to a loud "no local DAT match" — never an error, so
// an outage cannot abort a run.
//
// The fallback (and the hashless arm) stage into a temp workspace: the raw
// file is HARDLINKED (or an archive's inner ROM extracted) into a scratch
// dir; for the fallback the real `dat rename` runs on that temp entry and
// the canonical name is read back by diffing the scratch dir — no CLI
// output parsing. The library copy is never touched.
//
// Staging and extraction live in internal/romfile, shared with the import
// gate and the hash backfill so "the bytes of this ROM" has one definition.
//
// Hardlink safety: `dat rename` renames directory entries and never rewrites
// file bytes, so a renamed hardlink cannot alter the shared inode. If that
// contract ever changes upstream, romfile.Link's copy fallback is the escape
// hatch.
package renamer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gamarr/internal/converto"
	"gamarr/internal/datname"
	"gamarr/internal/db"
	"gamarr/internal/romfile"
)

// Skip reasons surfaced in preview rows. Exposed as constants so tests and
// the runner classify consistently.
const (
	SkipMultiFile = "multi-file — unsupported in v1"
	SkipRar       = "rar unsupported (no extractor in image)"
	// SkipNoDatMatch is the loud miss: the local snapshot does not know these
	// bytes. Uncatalogued content keeps its name by design (contract C6) —
	// but the miss is counted and surfaced, never buried.
	SkipNoDatMatch = "no local DAT match"
	// SkipFallbackUnavailable: the local snapshot missed AND the enabled
	// online fallback could not answer (binary/network failure). Still a
	// loud miss — deliberately not an identify error, so a Playmatch outage
	// cannot feed the circuit breaker.
	SkipFallbackUnavailable = "no local DAT match — online fallback unavailable"
)

// Name sources recorded on proposals, for the preview UI and logs.
const (
	NameSourceDat       = "dat"
	NameSourcePlaymatch = "playmatch"
)

// Identity is the result of identifying one library entry.
type Identity struct {
	// Matched is true when a canonical name exists for the entry.
	Matched bool
	// CanonicalStem is the canonical filename without extension.
	CanonicalStem string
	// ProposedName is the outer filename the library entry should have:
	// canonical stem + the original archive extension for .zip/.7z entries,
	// the catalog's own extension for raw files (never .unh — a payload-hash
	// match keeps the file's extension).
	ProposedName string
	// GameName is the catalog game the winning rom belongs to.
	GameName string
	// NameSource records which authority produced ProposedName:
	// NameSourceDat (the local snapshot) or NameSourcePlaymatch (the online
	// fallback). Set whenever Matched or Ambiguous.
	NameSource string
	// Ambiguous is true when the hashes land on multiple distinct catalog
	// names and no tie-breaker applies. AmbiguousWith lists the stems.
	// Never auto-applied.
	Ambiguous     bool
	AmbiguousWith []string
	// MD5 is the lowercased hex md5 of the raw/inner ROM bytes (stored or
	// computed). Powers collision verdicts against stored $.romm/$.gamarr
	// hashes.
	MD5 string
	// SkipReason is non-empty when the entry cannot be identified
	// (directories, multi-file archives, rar) or the catalog does not know
	// it (SkipNoDatMatch / SkipFallbackUnavailable).
	SkipReason string
}

// identifyStore is the narrow slice of db.JobStore identification needs.
type identifyStore interface {
	LookupDatRomsByHash(platformSlug, crc, md5, sha1 string) []db.DatRomMatch
	SaveLibraryHashes(id int64, h db.LibraryHashes) error
}

// Identifier resolves library entries against the local DAT snapshot,
// staging bytes only when a row carries no usable stored hashes (or for the
// optional online fallback).
type Identifier struct {
	store          identifyStore
	cv             *converto.Client
	workRoot       string
	onlineFallback bool
}

// NewIdentifier returns an Identifier. workRoot should live on the same
// filesystem as the library so raw files stage as hardlinks (zero copy); a
// cross-device workRoot silently degrades to copies. onlineFallback arms the
// Playmatch arm for local misses.
func NewIdentifier(store identifyStore, cv *converto.Client, workRoot string, onlineFallback bool) *Identifier {
	return &Identifier{store: store, cv: cv, workRoot: workRoot, onlineFallback: onlineFallback}
}

// Identify classifies and identifies one library entry. It never modifies
// the library file; staging happens on a temp copy that is removed before
// returning. A DAT miss is not an error: Identity.Matched is false and
// SkipReason says why, loudly.
func (id *Identifier) Identify(ctx context.Context, item *db.LibraryItem) (Identity, error) {
	filePath := item.FilePath
	fi, err := os.Stat(filePath)
	if err != nil {
		return Identity{}, err
	}
	if fi.IsDir() {
		return Identity{SkipReason: SkipMultiFile}, nil
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".rar" {
		return Identity{SkipReason: SkipRar}, nil
	}
	archiveExt := ""
	if ext == ".zip" || ext == ".7z" {
		archiveExt = ext
	}
	base := filepath.Base(filePath)

	// Stored-hash arm: the row already knows its bytes — ask the catalog
	// directly, no staging. Two stored families share the DAT's inner-content
	// domain: our $.gamarr identity (distrusted when older than the file's
	// mtime) and RomM's $.romm hashes (rewritten on every sync, so current by
	// construction — most sync-born rows carry ONLY these).
	gh, haveGamarr := db.ParseGamarrHashes(item.Metadata)
	if haveGamarr && staleStoredHashes(gh.HashedAt, fi) {
		haveGamarr = false
	}
	rCRC, rMD5, rSHA1, haveRomm := db.ParseRommContentHashes(item.Metadata)
	if haveGamarr || haveRomm {
		var matches []db.DatRomMatch
		if haveGamarr {
			matches = id.store.LookupDatRomsByHash(item.PlatformSlug, gh.CRC, gh.MD5, gh.SHA1)
			if len(matches) == 0 && gh.Unh != nil {
				matches = id.store.LookupDatRomsByHash(item.PlatformSlug, gh.Unh.CRC, gh.Unh.MD5, gh.Unh.SHA1)
			}
		}
		if len(matches) == 0 && haveRomm {
			matches = id.store.LookupDatRomsByHash(item.PlatformSlug, rCRC, rMD5, rSHA1)
		}
		itemMD5 := gh.MD5
		if itemMD5 == "" {
			itemMD5 = rMD5
		}
		if ident, decided := classifyMatches(matches, base, archiveExt, itemMD5); decided {
			return ident, nil
		}
		if haveGamarr {
			// Our own full identity missed: uncatalogued — re-hashing cannot
			// change the answer. Only the online fallback can.
			return id.playmatchFallback(ctx, filePath, archiveExt, gh.MD5)
		}
		// Only RomM's hashes missed. Fall through and hash once ourselves:
		// that distinguishes true uncatalogued content from a hash of the
		// wrong object (RomM's multi-file treatment), and persists $.gamarr
		// so this row never stages again.
	}

	// Hashless arm: stage, hash, persist (the self-heal — this row never
	// needs staging again), then ask the catalog.
	if err := os.MkdirAll(id.workRoot, 0o755); err != nil {
		return Identity{}, fmt.Errorf("workspace: %w", err)
	}
	itemDir, err := os.MkdirTemp(id.workRoot, "item-")
	if err != nil {
		return Identity{}, fmt.Errorf("stage dir: %w", err)
	}
	defer os.RemoveAll(itemDir)

	staged, err := id.stage(ctx, filePath, archiveExt, itemDir)
	if err != nil {
		var multi *romfile.MultiFileError
		if errors.As(err, &multi) {
			return Identity{SkipReason: SkipMultiFile}, nil
		}
		return Identity{}, err
	}

	res, err := romfile.HashPayload(staged)
	if err != nil {
		return Identity{}, fmt.Errorf("hash staged copy: %w", err)
	}
	if saveErr := id.store.SaveLibraryHashes(item.ID, toLibraryHashes(res)); saveErr != nil {
		// Best-effort: the lookup below still runs off the computed values.
		slog.Warn("renamer: persist hashes failed", "library_id", item.ID, "error", saveErr)
	}

	matches := id.store.LookupDatRomsByHash(item.PlatformSlug, res.CRC, res.MD5, res.SHA1)
	if len(matches) == 0 && res.Stripped {
		matches = id.store.LookupDatRomsByHash(item.PlatformSlug, res.Payload.CRC, res.Payload.MD5, res.Payload.SHA1)
	}
	if ident, decided := classifyMatches(matches, base, archiveExt, res.MD5); decided {
		return ident, nil
	}
	return id.playmatchStaged(ctx, staged, itemDir, archiveExt, res.MD5)
}

// stage places the entry's ROM bytes into itemDir: archives contribute their
// single inner ROM, raw files a hardlink.
func (id *Identifier) stage(ctx context.Context, filePath, archiveExt, itemDir string) (string, error) {
	if archiveExt != "" {
		return romfile.ExtractSingle(ctx, filePath, itemDir)
	}
	return romfile.Link(filePath, itemDir)
}

// classifyMatches reduces catalog hits to an Identity. decided=false means
// the catalog does not know these bytes (NoMatch).
func classifyMatches(matches []db.DatRomMatch, base, archiveExt, md5 string) (Identity, bool) {
	cands := make([]datname.Candidate, 0, len(matches))
	for _, m := range matches {
		cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
	}
	res := datname.Resolve(cands)
	switch res.Outcome {
	case datname.Resolved:
		return Identity{
			Matched:       true,
			CanonicalStem: res.Stem,
			ProposedName:  datname.ProposedName(res.Stem, res.Ext, base, archiveExt),
			GameName:      res.GameName,
			NameSource:    NameSourceDat,
			MD5:           md5,
		}, true
	case datname.Ambiguous:
		return Identity{
			Ambiguous:     true,
			AmbiguousWith: res.Stems,
			NameSource:    NameSourceDat,
			MD5:           md5,
		}, true
	}
	return Identity{}, false
}

// staleStoredHashes distrusts stored hashes when the file was modified after
// they were computed (renames preserve mtime; imports rewrite hashes). A
// missing or unparsable timestamp is treated as stale — one re-hash repairs
// it permanently.
func staleStoredHashes(hashedAt string, fi os.FileInfo) bool {
	t, err := time.Parse(time.RFC3339, hashedAt)
	if err != nil {
		return true
	}
	return fi.ModTime().After(t)
}

// toLibraryHashes maps a romfile result onto the persisted identity shape.
func toLibraryHashes(res romfile.Result) db.LibraryHashes {
	h := db.LibraryHashes{CRC: res.CRC, MD5: res.MD5, SHA1: res.SHA1}
	if res.Stripped {
		h.Unh = &db.UnheaderedHashes{
			CRC: res.Payload.CRC, MD5: res.Payload.MD5, SHA1: res.Payload.SHA1,
			Header: res.HeaderKind,
		}
	}
	return h
}

// playmatchFallback is the fallback arm for the stored-hash path: nothing is
// staged yet, so it stages first (failures degrade — the local answer "no
// match" already stands and the fallback is best-effort).
func (id *Identifier) playmatchFallback(ctx context.Context, filePath, archiveExt, md5 string) (Identity, error) {
	if !id.onlineFallback {
		return Identity{MD5: md5, SkipReason: SkipNoDatMatch}, nil
	}
	if err := os.MkdirAll(id.workRoot, 0o755); err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	itemDir, err := os.MkdirTemp(id.workRoot, "item-")
	if err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	defer os.RemoveAll(itemDir)
	staged, err := id.stage(ctx, filePath, archiveExt, itemDir)
	if err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	return id.playmatchStaged(ctx, staged, itemDir, archiveExt, md5)
}

// playmatchStaged runs the online engine over an already-staged copy: sentinel
// rename, `dat rename`, dir-diff. Engine failures degrade to a loud miss with
// a nil error — an outage must not feed the circuit breaker.
func (id *Identifier) playmatchStaged(ctx context.Context, staged, itemDir, archiveExt, md5 string) (Identity, error) {
	if !id.onlineFallback {
		return Identity{MD5: md5, SkipReason: SkipNoDatMatch}, nil
	}
	// Re-stage under a random sentinel stem (extension preserved): a DAT
	// match then ALWAYS renames the entry — including when the canonical
	// name equals the library's current one — so "already canonical" is
	// distinguishable from "no DAT match" (sentinel untouched).
	sentinel := filepath.Join(itemDir, stageName(filepath.Ext(staged)))
	if err := os.Rename(staged, sentinel); err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	staged = sentinel

	before, err := dirEntries(itemDir)
	if err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	if err := id.cv.DatRename(ctx, staged, converto.Options{OnConflict: "skip", Quiet: true}); err != nil {
		return Identity{MD5: md5, SkipReason: SkipFallbackUnavailable}, nil
	}
	renamed, ok := newEntryAfterRename(itemDir, staged, before)
	if !ok {
		return Identity{MD5: md5, SkipReason: SkipNoDatMatch}, nil
	}

	canonical := filepath.Base(renamed)
	stem := strings.TrimSuffix(canonical, filepath.Ext(canonical))
	proposed := canonical
	if archiveExt != "" {
		proposed = stem + archiveExt
	}
	return Identity{
		Matched:       true,
		CanonicalStem: stem,
		ProposedName:  proposed,
		NameSource:    NameSourcePlaymatch,
		MD5:           md5,
	}, nil
}

// stageName mints a random sentinel filename preserving the ROM extension.
func stageName(ext string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "stage-" + hex.EncodeToString(b) + ext
}

// dirEntries snapshots the names in dir (the normalize package's dir-diff
// idiom, kept local to avoid coupling).
func dirEntries(dir string) (map[string]struct{}, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(ents))
	for _, e := range ents {
		set[e.Name()] = struct{}{}
	}
	return set, nil
}

// newEntryAfterRename reports the staged file's post-rename path: the staged
// input must be gone and exactly one new entry present, else the rename is
// treated as a no-op (DAT miss or ambiguity).
func newEntryAfterRename(dir, staged string, before map[string]struct{}) (string, bool) {
	if _, err := os.Stat(staged); err == nil {
		return "", false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var fresh []string
	for _, e := range ents {
		if _, ok := before[e.Name()]; !ok {
			fresh = append(fresh, e.Name())
		}
	}
	if len(fresh) != 1 {
		return "", false
	}
	return filepath.Join(dir, fresh[0]), true
}
