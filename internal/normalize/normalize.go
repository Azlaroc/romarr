// Package normalize is the F5 normalize step: it canonicalizes a freshly
// organized ROM path, plus one .m3u per multi-disc set. It is download-free
// (imported by download, exactly as converto and safety are) so the organize
// pipeline can call it without an import cycle.
//
// Naming authority (contract C6): a single file is named by the LOCAL DAT
// snapshot — the import gate already hashed the extracted ROM, and those
// same hashes resolve to the catalog”s canonical name here, so the file”s
// name and its verdict come from the same book. A local miss is logged
// loudly and keeps the import name; the online Playmatch engine runs only
// behind the normalize_online_fallback setting. Directories (disc sets) stay
// on the rom-converto recursive rename — DAT rows are per-track, so a
// set-level name from one track is ill-defined.
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
	"gamarr/internal/datname"
	"gamarr/internal/db"
	"gamarr/internal/platform"
	"gamarr/internal/romfile"
)

// Policy selects normalize/convert behavior. Story #260 leaves it empty —
// rename + playlist run unconditionally when the feature is enabled — while
// #261 will add a target format so the convert stage can pick CHD per platform
// without changing this signature.
type Policy struct {
	// TargetFormat is reserved for #261 (e.g. "chd"); empty = leave as-is.
	TargetFormat string
}

// datStore is the snapshot lookup normalize needs — satisfied by db.JobStore.
type datStore interface {
	LookupDatRomsByHash(platformSlug, crc, md5, sha1 string) []db.DatRomMatch
}

// Normalizer canonicalizes organized ROM paths: single files against the
// local DAT snapshot, directories via the converto engine.
type Normalizer struct {
	cfg   *config.Config
	cv    *converto.Client
	store datStore
}

// New builds a Normalizer. store may be nil (tests without a catalog), which
// leaves single files on the fallback path only.
func New(cfg *config.Config, store datStore) *Normalizer {
	return &Normalizer{cfg: cfg, cv: converto.New(cfg), store: store}
}

// Result records what Normalize/Convert did, for logging and job detail. The
// zero value means nothing happened (disabled, no binary, or no match).
type Result struct {
	Renamed    bool   // a DAT rename ran without error
	NameSource string // "dat" (local snapshot) | "playmatch" (online fallback) | ""
	Playlist   bool   // a multi-disc .m3u pass ran without error
	Converted  int    // number of disc images compressed to CHD (and verified)
}

// Normalize canonicalizes artifactPath in place and returns the path to track
// in the library. artifactPath must be the specific organized artifact — a
// single ROM file or a per-game directory — never a shared platform root, since
// a recursive rename there would touch every ROM of the platform.
//
// It is non-blocking by contract: a missing or unrunnable binary, an offline
// Playmatch, or an unmatched ROM are all no-ops that return artifactPath
// unchanged with a nil error. It never fails an import.
func (n *Normalizer) Normalize(ctx context.Context, artifactPath, platformSlug string, pre *db.LibraryHashes, _ Policy) (string, Result, error) {
	var res Result
	if n == nil {
		return artifactPath, res, nil
	}
	fi, err := os.Stat(artifactPath)
	if err != nil {
		return artifactPath, res, nil
	}
	if !fi.IsDir() {
		return n.normalizeFile(ctx, artifactPath, platformSlug, pre)
	}
	// Directory (disc set): the converto carve-out. A recursive rename and
	// the .m3u land inside it, so the tracked path never changes and needs no
	// reconciliation.
	if n.cv == nil || !n.cv.Available() {
		return artifactPath, res, nil
	}
	if err := n.cv.DatRename(ctx, artifactPath, converto.Options{
		Recursive:  true,
		OnConflict: "skip",
		Quiet:      true,
	}); err != nil {
		slog.Warn("normalize: dat rename failed (non-fatal)", "path", sanitizeLog(artifactPath), "error", err)
	} else {
		res.Renamed = true
		res.NameSource = "playmatch"
	}
	// Multi-disc grouping — one .m3u per game (filename grouping, offline).
	if err := n.cv.Playlist(ctx, artifactPath, converto.Options{
		PlaylistMode: "multiple",
		Quiet:        true,
	}); err != nil {
		slog.Warn("normalize: playlist failed (non-fatal)", "dir", sanitizeLog(artifactPath), "error", err)
	} else {
		res.Playlist = true
	}
	if res.Renamed || res.Playlist {
		slog.Info("normalized ROM", "platform", platformSlug,
			"path", sanitizeLog(artifactPath), "renamed", res.Renamed, "playlist", res.Playlist, "source", res.NameSource)
	}
	return artifactPath, res, nil
}

// normalizeFile names one file from the local DAT snapshot. pre carries the
// import gate's already-computed hashes of the extracted ROM; nil (manual
// import, disc-set lead) hashes here — one read, header-aware.
func (n *Normalizer) normalizeFile(ctx context.Context, path, platformSlug string, pre *db.LibraryHashes) (string, Result, error) {
	var res Result
	base := filepath.Base(path)
	archiveExt := ""
	if ext := strings.ToLower(filepath.Ext(base)); ext == ".zip" || ext == ".7z" {
		archiveExt = ext
	}

	h := pre
	if h == nil && n.store != nil {
		if r, err := romfile.HashPayload(path); err == nil {
			lh := db.LibraryHashes{CRC: r.CRC, MD5: r.MD5, SHA1: r.SHA1}
			if r.Stripped {
				lh.Unh = &db.UnheaderedHashes{
					CRC: r.Payload.CRC, MD5: r.Payload.MD5, SHA1: r.Payload.SHA1,
					Header: r.HeaderKind,
				}
			}
			h = &lh
		}
	}

	if h != nil && n.store != nil {
		matches := n.store.LookupDatRomsByHash(platformSlug, h.CRC, h.MD5, h.SHA1)
		if len(matches) == 0 && h.Unh != nil {
			matches = n.store.LookupDatRomsByHash(platformSlug, h.Unh.CRC, h.Unh.MD5, h.Unh.SHA1)
		}
		cands := make([]datname.Candidate, 0, len(matches))
		for _, m := range matches {
			cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
		}
		switch r := datname.Resolve(cands); r.Outcome {
		case datname.Resolved:
			proposed := datname.ProposedName(r.Stem, r.Ext, base, archiveExt)
			res.NameSource = "dat"
			if proposed == base {
				return path, res, nil // already canonical
			}
			target := filepath.Join(filepath.Dir(path), proposed)
			if _, err := os.Stat(target); err == nil {
				// OnConflict parity with the old engine: never overwrite.
				slog.Info("normalize: canonical name already exists — keeping import name",
					"path", sanitizeLog(path), "canonical", sanitizeLog(proposed))
				return path, res, nil
			}
			if err := os.Rename(path, target); err != nil {
				slog.Warn("normalize: rename failed (non-fatal)", "path", sanitizeLog(path), "error", err)
				return path, res, nil
			}
			res.Renamed = true
			slog.Info("normalized ROM", "platform", platformSlug,
				"path", sanitizeLog(target), "renamed", true, "source", "dat")
			return target, res, nil
		case datname.Ambiguous:
			// The catalog knows the bytes under several names — a human call;
			// the bulk preview surfaces it as review later.
			slog.Info("normalize: ambiguous DAT candidates — keeping import name",
				"path", sanitizeLog(path), "candidates", strings.Join(r.Stems, " | "))
			return path, res, nil
		}
	}

	// The loud miss (contract C6): uncatalogued content keeps its name,
	// visibly. The online engine runs only when the operator opted in.
	slog.Info("imported without canonical name — no local DAT match",
		"platform", platformSlug, "path", sanitizeLog(path))
	if n.cfg == nil || !n.cfg.OnlineFallbackOn() || n.cv == nil || !n.cv.Available() {
		return path, res, nil
	}
	dir := filepath.Dir(path)
	before := dirEntries(dir)
	if err := n.cv.DatRename(ctx, path, converto.Options{OnConflict: "skip", Quiet: true}); err != nil {
		slog.Warn("normalize: online fallback failed (non-fatal)", "path", sanitizeLog(path), "error", err)
		return path, res, nil
	}
	finalPath := path
	// Reconcile best-effort: input gone plus exactly one new entry is the
	// renamed file; anything ambiguous keeps the original path.
	if _, err := os.Stat(path); err != nil {
		if renamed, ok := soleNewEntry(dir, before); ok {
			finalPath = renamed
			res.Renamed = true
			res.NameSource = "playmatch"
			slog.Info("normalized ROM", "platform", platformSlug,
				"path", sanitizeLog(finalPath), "renamed", true, "source", "playmatch")
		}
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
