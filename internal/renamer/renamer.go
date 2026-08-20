// Package renamer implements the on-demand bulk library rename (arr-style
// "Rename" preview + apply) over already-imported ROMs.
//
// The library stores most ROMs as .zip/.7z archives, which rom-converto's
// dat engine skips (it hashes raw ROM bytes). Identification therefore runs
// in a temp workspace: the raw file is HARDLINKED (or an archive's inner ROM
// extracted) into a scratch dir, the real `dat rename` runs on that temp
// entry, and the canonical name is read back by diffing the scratch dir —
// no CLI output parsing. The library copy is never touched.
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
	"os"
	"path/filepath"
	"strings"

	"gamarr/internal/converto"
	"gamarr/internal/romfile"
)

// Skip reasons surfaced in preview rows. Exposed as constants so tests and
// the runner classify consistently.
const (
	SkipMultiFile  = "multi-file — unsupported in v1"
	SkipRar        = "rar unsupported (no extractor in image)"
	SkipNoDatMatch = "no DAT match"
)

// Identity is the result of a temp-workspace rename-as-identify pass.
type Identity struct {
	// Matched is true when the DAT engine renamed the temp entry — i.e. the
	// ROM was identified and a canonical name exists.
	Matched bool
	// CanonicalStem is the canonical filename without extension.
	CanonicalStem string
	// ProposedName is the outer filename the library entry should have:
	// the canonical name as-is for raw files, or CanonicalStem plus the
	// original archive extension for .zip/.7z entries.
	ProposedName string
	// MD5 is the lowercased hex md5 of the raw/inner ROM bytes, hashed while
	// the temp copy is in hand. Powers collision verdicts against stored
	// $.romm/$.gamarr hashes.
	MD5 string
	// SkipReason is non-empty when the entry cannot be identified in v1
	// (directories, multi-file archives, rar).
	SkipReason string
}

// Identifier stages library entries into a scratch workspace and identifies
// them via rom-converto without touching library bytes.
type Identifier struct {
	cv       *converto.Client
	workRoot string
}

// NewIdentifier returns an Identifier staging into workRoot. workRoot should
// live on the same filesystem as the library so raw files stage as hardlinks
// (zero copy); a cross-device workRoot silently degrades to copies.
func NewIdentifier(cv *converto.Client, workRoot string) *Identifier {
	return &Identifier{cv: cv, workRoot: workRoot}
}

// Identify classifies and identifies one library entry. It never modifies
// filePath; all work happens on a staged temp entry that is removed before
// returning. A DAT miss is not an error: Identity.Matched is false and
// SkipReason is SkipNoDatMatch.
func (id *Identifier) Identify(ctx context.Context, filePath string) (Identity, error) {
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

	if err := os.MkdirAll(id.workRoot, 0o755); err != nil {
		return Identity{}, fmt.Errorf("workspace: %w", err)
	}
	itemDir, err := os.MkdirTemp(id.workRoot, "item-")
	if err != nil {
		return Identity{}, fmt.Errorf("stage dir: %w", err)
	}
	defer os.RemoveAll(itemDir)

	// Stage: archives contribute their single inner ROM; raw files a hardlink.
	archiveExt := ""
	var staged string
	if ext == ".zip" || ext == ".7z" {
		archiveExt = ext
		staged, err = romfile.ExtractSingle(ctx, filePath, itemDir)
	} else {
		staged, err = romfile.Link(filePath, itemDir)
	}
	if err != nil {
		var multi *romfile.MultiFileError
		if errors.As(err, &multi) {
			return Identity{SkipReason: SkipMultiFile}, nil
		}
		return Identity{}, err
	}

	// Re-stage under a random sentinel stem (extension preserved): a DAT
	// match then ALWAYS renames the entry — including when the canonical
	// name equals the library's current one — so "already canonical" is
	// distinguishable from "no DAT match" (sentinel untouched).
	sentinel := filepath.Join(itemDir, stageName(filepath.Ext(staged)))
	if err := os.Rename(staged, sentinel); err != nil {
		return Identity{}, err
	}
	staged = sentinel

	h, err := romfile.Hash(staged)
	if err != nil {
		return Identity{}, fmt.Errorf("hash staged copy: %w", err)
	}
	sum := h.MD5

	before, err := dirEntries(itemDir)
	if err != nil {
		return Identity{}, err
	}
	if err := id.cv.DatRename(ctx, staged, converto.Options{OnConflict: "skip", Quiet: true}); err != nil {
		return Identity{}, err
	}
	renamed, ok := newEntryAfterRename(itemDir, staged, before)
	if !ok {
		return Identity{MD5: sum, SkipReason: SkipNoDatMatch}, nil
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
		MD5:           sum,
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
