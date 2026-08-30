package romfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Measuring a library entry: the one sequence every sweep that asks "what are
// this entry's ROM bytes?" runs — stat it, refuse the shapes that have no
// single ROM identity, extract an archive's inner file into a scratch dir,
// hash. Extracted from the hash backfill's visit loop so the library scanner
// cannot grow a second, slightly different reading of the same file.

// Classifications a measurement can end in instead of a hash. They are
// sentinels, not failures: each one names a permanent fact about the entry
// (except ErrNoSpace, which is today's weather), and callers report them as
// skips with their own vocabulary.
var (
	// ErrIsDirectory: a multi-file entry has no single ROM identity.
	ErrIsDirectory = errors.New("directory")
	// ErrRarArchive: no rar extractor in the image.
	ErrRarArchive = errors.New("rar unsupported")
	// ErrNoSpace: extracting would not leave enough free space.
	ErrNoSpace = errors.New("not enough free space to extract")
)

// extractHeadroom is how many times the archive's own size must be free
// before extracting it. ROMs compress well; 4x covers the ratios seen in the
// library without being so generous it refuses ordinary work.
const extractHeadroom = 4

// Measure computes the payload-aware hashes of one library entry without
// modifying it: an archive's inner ROM is extracted into a scratch dir under
// workRoot that is removed before returning, and a raw file is hashed in
// place — no staging, because nothing here renames anything and a hardlink
// would only be work.
//
// A missing file surfaces as the os.Stat error (os.IsNotExist reads it); a
// directory, a rar and an archive that would not fit surface as the sentinels
// above; a multi-file archive surfaces as *MultiFileError.
func Measure(ctx context.Context, path, workRoot string) (Result, error) {
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		return Result{}, err
	case fi.IsDir():
		return Result{}, ErrIsDirectory
	}
	if strings.EqualFold(filepath.Ext(path), ".rar") {
		return Result{}, ErrRarArchive
	}

	target := path
	if IsArchive(path) {
		if err := os.MkdirAll(workRoot, 0o755); err != nil {
			return Result{}, err
		}
		itemDir, err := os.MkdirTemp(workRoot, "item-")
		if err != nil {
			return Result{}, err
		}
		defer os.RemoveAll(itemDir)

		// An extraction needs room for the inner ROM, which can be several
		// times the archive. Refusing loudly beats filling the volume the
		// library lives on and failing every remaining row.
		if free := FreeBytes(workRoot); free > 0 && uint64(fi.Size())*extractHeadroom > free {
			return Result{}, ErrNoSpace
		}

		extracted, err := ExtractSingle(ctx, path, itemDir)
		if err != nil {
			return Result{}, err
		}
		target = extracted
	}

	return HashPayload(target)
}

// FreeBytes returns the filesystem free space for the nearest existing parent
// of path, or 0 when nothing resolves — in which case the caller proceeds, on
// the grounds that an unmeasurable volume is not evidence of a full one.
func FreeBytes(path string) uint64 {
	for p := path; p != "" && p != "/"; p = filepath.Dir(p) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err == nil {
			return st.Bavail * uint64(st.Bsize)
		}
	}
	return 0
}
