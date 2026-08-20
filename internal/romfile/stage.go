package romfile

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Staging a library entry into a scratch dir so its ROM bytes can be read
// without touching the library copy.
//
// Hardlink safety: callers that rename the staged entry (the bulk renamer
// does) rely on renames never rewriting file bytes, so a renamed hardlink
// cannot alter the shared inode. Link's copy fallback is the escape hatch
// when the scratch dir lands on another filesystem.

// MultiFileError marks an archive holding anything other than exactly one
// file. It is a classification, not a failure: a multi-file archive has no
// single ROM identity, and callers report it as a skip.
type MultiFileError struct{ N int }

func (e *MultiFileError) Error() string { return fmt.Sprintf("archive holds %d files", e.N) }

// IsArchive reports whether path is an archive ExtractSingle can open.
func IsArchive(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".zip", ".7z":
		return true
	}
	return false
}

// Exec7z builds the extraction command; a var so tests can stub it, and bare
// "7z" to match the import pipeline's convention (p7zip in the image).
var Exec7z = func(ctx context.Context, archive, destDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "7z", "x", "-o"+destDir, "-y", archive)
}

// Link links src into destDir, falling back to a copy when the filesystem
// refuses hardlinks (cross-device destDir).
func Link(src, destDir string) (string, error) {
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

// ExtractSingle extracts archive into destDir and returns the path of the
// single extracted file. Archives holding anything other than exactly one
// file yield a *MultiFileError.
func ExtractSingle(ctx context.Context, archive, destDir string) (string, error) {
	cmd := Exec7z(ctx, archive, destDir)
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
		return "", &MultiFileError{N: len(files)}
	}
	// Flatten: callers expect the file at destDir's top level (archives may
	// carry an inner folder).
	if filepath.Dir(files[0]) != destDir {
		flat := filepath.Join(destDir, filepath.Base(files[0]))
		if err := os.Rename(files[0], flat); err != nil {
			return "", err
		}
		return flat, nil
	}
	return files[0], nil
}
