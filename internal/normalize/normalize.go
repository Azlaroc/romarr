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
	"regexp"
	"strings"

	"gamarr/internal/config"
	"gamarr/internal/converto"
	"gamarr/internal/platform"
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

// Result records what Normalize/Convert did, for logging and job detail. The
// zero value means nothing happened (disabled, no binary, or no match).
type Result struct {
	Renamed   bool // a DAT rename ran without error
	Playlist  bool // a multi-disc .m3u pass ran without error
	Converted int  // number of disc images compressed to CHD (and verified)
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

// FormatPolicy returns the default target conversion format for a platform
// slug: "chd" for the disc systems whose registry row says so, "" (leave
// as-is) otherwise. The list used to be four hardcoded slugs here plus a
// hand-synced copy in the frontend; it is a registry column now, so turning
// CHD on for another disc platform is a row edit rather than two code edits.
func FormatPolicy(platformSlug string) string {
	if platform.ConvertsToCHD(platformSlug) {
		return "chd"
	}
	return ""
}

// Convert applies the default format policy to an organized artifact: disc
// systems are compressed to CHD with sole-copy safety — a source disc image is
// deleted only after its CHD passes `chd verify`. It returns the path to track.
//
// artifactPath is a game directory (the .cue+.bin case) or a bare disc image.
// hashStatus is the before-convert authenticity result from the download stage:
// "mismatch" skips convert entirely; "ok"/"skipped"/"" proceed (the after-verify
// still guards fidelity).
//
// Non-blocking by contract: a missing binary, a non-disc platform, a hash
// mismatch, or any per-disc error leaves that source untouched and returns the
// input path unchanged. It never fails an import.
func (n *Normalizer) Convert(ctx context.Context, artifactPath, platformSlug, hashStatus string) (string, Result, error) {
	var res Result
	if n == nil || n.cv == nil || !n.cv.Available() {
		return artifactPath, res, nil
	}
	if FormatPolicy(platformSlug) != "chd" {
		return artifactPath, res, nil
	}
	if hashStatus == "mismatch" {
		slog.Warn("convert: skipped — downloaded artifact failed hash verify", "path", sanitizeLog(artifactPath))
		return artifactPath, res, nil
	}
	fi, err := os.Stat(artifactPath)
	if err != nil {
		return artifactPath, res, nil
	}

	// Collect disc images: the images at the top of a game dir, or a bare file.
	var discs []string
	if fi.IsDir() {
		entries, _ := os.ReadDir(artifactPath)
		for _, e := range entries {
			if !e.IsDir() && isDiscImage(e.Name()) {
				discs = append(discs, filepath.Join(artifactPath, e.Name()))
			}
		}
	} else if isDiscImage(artifactPath) {
		discs = append(discs, artifactPath)
	}
	if len(discs) == 0 {
		return artifactPath, res, nil
	}

	finalPath := artifactPath
	for _, disc := range discs {
		chdPath, err := n.cv.CompressCHD(ctx, disc, converto.Options{OnConflict: "skip", Quiet: true})
		if err != nil {
			slog.Warn("convert: chd compress failed (source kept)", "disc", sanitizeLog(disc), "error", err)
			continue
		}
		// Sole-copy gate: verify the CHD before removing anything. On failure,
		// delete the unverified CHD and KEEP the source untouched.
		if err := n.cv.VerifyCHD(ctx, chdPath); err != nil {
			slog.Warn("convert: chd verify failed — removing bad CHD, keeping source", "disc", sanitizeLog(disc), "error", err)
			_ = os.Remove(chdPath)
			continue
		}
		// rom-converto writes its output 0600; library files must be group/
		// world-readable for downstream consumers (media-server byte-serving,
		// backup users).
		_ = os.Chmod(chdPath, 0o664)
		removeSourceDisc(disc)
		res.Converted++
		if !fi.IsDir() {
			finalPath = chdPath // a bare-file input is now tracked as the CHD
		}
	}
	if res.Converted == 0 {
		return artifactPath, res, nil
	}

	// Multi-disc: regenerate the .m3u so it lists the new .chd files (the #260
	// pass wrote one over the now-deleted .cue names). Single-disc writes none.
	if fi.IsDir() {
		for _, m := range globM3U(artifactPath) {
			_ = os.Remove(m)
		}
		_ = n.cv.Playlist(ctx, artifactPath, converto.Options{PlaylistMode: "multiple", Quiet: true})
		for _, m := range globM3U(artifactPath) {
			_ = os.Chmod(m, 0o664)
		}
	}

	slog.Info("converted to CHD", "platform", platformSlug, "count", res.Converted, "path", sanitizeLog(finalPath))
	return finalPath, res, nil
}

// isDiscImage reports whether name is a disc image rom-converto can compress to
// CHD (already-compressed .chd is excluded).
func isDiscImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cue", ".iso", ".gdi":
		return true
	}
	return false
}

var cueFileRe = regexp.MustCompile(`(?im)^\s*FILE\s+"([^"]+)"`)

// removeSourceDisc deletes a converted disc image and the track files it
// references (a .cue/.gdi points at sibling .bin/track files), so no orphan
// tracks are left beside the new CHD.
func removeSourceDisc(disc string) {
	dir := filepath.Dir(disc)
	switch strings.ToLower(filepath.Ext(disc)) {
	case ".cue", ".gdi":
		data, err := os.ReadFile(disc)
		if err == nil {
			for _, m := range cueFileRe.FindAllStringSubmatch(string(data), -1) {
				ref := filepath.Base(m[1]) // keep tracks inside the disc dir
				_ = os.Remove(filepath.Join(dir, ref))
			}
		}
	}
	_ = os.Remove(disc)
}

// globM3U returns the .m3u files at the top of dir.
func globM3U(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.m3u"))
	return matches
}
