// Package normalize is the F5 normalize step: it canonicalizes a freshly
// organized ROM path with the rom-converto engine — a Playmatch (DAT)
// 1G1R-aware rename, plus one .m3u per multi-disc set. It is download-free
// (imported by download, exactly as converto and safety are) so the organize
// pipeline can call it without an import cycle.
//
// Conversion to CHD and verify-before-replace are a later story (#261); this
// package deliberately does rename + playlist only.
package normalize

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gamarr/internal/config"
	"gamarr/internal/converto"
)

// Policy selects normalize/convert behavior. Story #260 leaves it empty —
// rename + playlist run unconditionally when the feature is enabled — while
// #261 will add a target format so the convert stage can pick CHD per platform
// without changing this signature.
type Policy struct {
	// TargetFormat is reserved for #261 (e.g. "chd"); empty = leave as-is.
	TargetFormat string
}

// Normalizer canonicalizes organized ROM paths via a converto client.
type Normalizer struct {
	cv *converto.Client
}

// New builds a Normalizer over a converto client from config.
func New(cfg *config.Config) *Normalizer {
	return &Normalizer{cv: converto.New(cfg)}
}

// Result records what Normalize did, for logging and job detail. The zero value
// means nothing happened (disabled, no binary, or no Playmatch match).
type Result struct {
	Renamed  bool // a DAT rename ran without error
	Playlist bool // a multi-disc .m3u pass ran without error
}

// Normalize canonicalizes artifactPath in place and returns the path to track
// in the library. artifactPath must be the specific organized artifact — a
// single ROM file or a per-game directory — never a shared platform root, since
// a recursive rename there would touch every ROM of the platform.
//
// It is non-blocking by contract: a missing or unrunnable binary, an offline
// Playmatch, or an unmatched ROM are all no-ops that return artifactPath
// unchanged with a nil error. It never fails an import.
func (n *Normalizer) Normalize(ctx context.Context, artifactPath, platformSlug string, _ Policy) (string, Result, error) {
	var res Result
	if n == nil || n.cv == nil || !n.cv.Available() {
		return artifactPath, res, nil
	}
	fi, err := os.Stat(artifactPath)
	if err != nil {
		return artifactPath, res, nil
	}
	isDir := fi.IsDir()
	finalPath := artifactPath

	// A directory's tracked path is the directory itself: a recursive rename and
	// the .m3u land inside it, so the tracked path never changes and needs no
	// reconciliation. A single file is renamed in place and dat rename returns no
	// path, so snapshot the parent to discover the new name afterwards.
	var (
		dir    = artifactPath
		before map[string]struct{}
	)
	if !isDir {
		dir = filepath.Dir(artifactPath)
		before = dirEntries(dir)
	}

	// 1) DAT 1G1R-aware rename (online Playmatch). An unmatched ROM is a no-op,
	// not an error, so torrent/Vimm grabs without a Playmatch hit still import.
	if err := n.cv.DatRename(ctx, artifactPath, converto.Options{
		Recursive:  isDir,
		OnConflict: "skip",
		Quiet:      true,
	}); err != nil {
		slog.Warn("normalize: dat rename failed (non-fatal)", "path", sanitizeLog(artifactPath), "error", err)
	} else {
		res.Renamed = true
	}

	// Reconcile a single-file rename to its new name, best-effort: if the input
	// is gone and exactly one new entry appeared, that is the renamed file.
	// Anything ambiguous keeps the original path — the library pointer may lag a
	// rescan, but the import still succeeds.
	if !isDir {
		if _, err := os.Stat(artifactPath); err != nil {
			if renamed, ok := soleNewEntry(dir, before); ok {
				finalPath = renamed
			}
		}
	}

	// 2) Multi-disc grouping — one .m3u per game. Directory-only and offline
	// (filename grouping, no DAT lookup); nothing to group for a single file.
	if isDir {
		if err := n.cv.Playlist(ctx, artifactPath, converto.Options{
			PlaylistMode: "multiple",
			Quiet:        true,
		}); err != nil {
			slog.Warn("normalize: playlist failed (non-fatal)", "dir", sanitizeLog(artifactPath), "error", err)
		} else {
			res.Playlist = true
		}
	}

	if res.Renamed || res.Playlist {
		slog.Info("normalized ROM", "platform", platformSlug,
			"path", sanitizeLog(finalPath), "renamed", res.Renamed, "playlist", res.Playlist)
	}
	return finalPath, res, nil
}

// dirEntries returns the set of entry names in dir (empty on error).
func dirEntries(dir string) map[string]struct{} {
	set := map[string]struct{}{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return set
	}
	for _, e := range entries {
		set[e.Name()] = struct{}{}
	}
	return set
}

// soleNewEntry returns the single entry present in dir now but absent from
// before. ok is false when zero or more than one new entry appeared, so an
// ambiguous rename never mis-points the library.
func soleNewEntry(dir string, before map[string]struct{}) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var added []string
	for _, e := range entries {
		if _, seen := before[e.Name()]; !seen {
			added = append(added, e.Name())
		}
	}
	if len(added) != 1 {
		return "", false
	}
	return filepath.Join(dir, added[0]), true
}

// sanitizeLog strips newlines from a path before it reaches a log line,
// matching the per-package duplication already in this repo.
func sanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}
