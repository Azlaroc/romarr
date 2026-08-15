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
// Hardlink safety: `dat rename` renames directory entries and never rewrites
// file bytes, so a renamed hardlink cannot alter the shared inode. If that
// contract ever changes upstream, stageRaw's copy fallback is the escape
// hatch.
package renamer

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gamarr/internal/converto"
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
		staged, err = extractSingle(ctx, filePath, itemDir)
	} else {
		staged, err = stageRaw(filePath, itemDir)
	}
	if err != nil {
		var multi *multiFileError
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

	sum, err := md5File(staged)
	if err != nil {
		return Identity{}, fmt.Errorf("hash staged copy: %w", err)
	}

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

// multiFileError marks an archive with != 1 inner file.
type multiFileError struct{ n int }

func (e *multiFileError) Error() string { return fmt.Sprintf("archive holds %d files", e.n) }

// stageName mints a random sentinel filename preserving the ROM extension.
func stageName(ext string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "stage-" + hex.EncodeToString(b) + ext
}

// exec7z builds the extraction command; a var so tests could stub, matching
// the bare-"7z" convention used by the import pipeline (p7zip in the image).
func exec7z(ctx context.Context, archive, destDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "7z", "x", "-o"+destDir, "-y", archive)
}

// stageRaw links src into destDir, falling back to a copy when the
// filesystem refuses hardlinks (cross-device workRoot).
func stageRaw(src, destDir string) (string, error) {
	dest := filepath.Join(destDir, filepath.Base(src))
	if err := os.Link(src, dest); err == nil {
		return dest, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return dest, nil
}

// extractSingle extracts archive into destDir and returns the path of the
// single extracted file. Archives holding anything other than exactly one
// file yield a *multiFileError.
func extractSingle(ctx context.Context, archive, destDir string) (string, error) {
	cmd := exec7z(ctx, archive, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("7z extract failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var files []string
	err := filepath.Walk(destDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) != 1 {
		return "", &multiFileError{n: len(files)}
	}
	// Flatten: the DAT pass and dir-diff expect the file at destDir's top
	// level (archives may carry an inner folder).
	if filepath.Dir(files[0]) != destDir {
		flat := filepath.Join(destDir, filepath.Base(files[0]))
		if err := os.Rename(files[0], flat); err != nil {
			return "", err
		}
		return flat, nil
	}
	return files[0], nil
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
