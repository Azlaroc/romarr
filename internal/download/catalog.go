package download

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gamarr/internal/db"
	"gamarr/internal/romfile"
)

// The trust gate.
//
// Everything before this point is scoring: sizes, names, seeders, the source's
// own published hashes. This is the one place a download is judged on evidence
// rather than on what it claimed to be.
//
// 🔴 It hashes the EXTRACTED ROM and compares that to the catalog — never the
// downloaded file. archive.org's md5 is the hash of the .7z it stores;
// No-Intro's crc32 is the hash of the .gb inside it, and recompressing the
// identical ROM changes the first without touching the second. A gate built on
// the source hash passes every file, including a corrupt one, and looks
// healthy while doing it. (The source-hash check that already exists answers a
// different question — "did the bytes arrive intact?" — and stays.)
//
// A rejection needs the catalog to DISAGREE, not merely to be silent. A file
// the catalog has never heard of is imported and recorded as unknown: hacks,
// homebrew, translations and dumps newer than the snapshot all land there, and
// on some platforms they outnumber the catalogued ones several times over.
// Rejecting those would make most of a platform unacquirable.

// catalogMaxFile caps what the gate will hash. A file bigger than this is
// recorded as unknown rather than silently skipped.
const catalogMaxFile = 8 << 30

// nonROMExtensions are the files that ride along with a ROM and are never
// themselves catalogued, so hashing them only produces noise.
var nonROMExtensions = map[string]bool{
	".json": true, ".txt": true, ".nfo": true, ".sfv": true, ".md5": true,
	".sha1": true, ".m3u": true, ".jpg": true, ".png": true, ".xml": true,
	".dat": true, ".log": true, ".torrent": true,
}

// catalogVerdict measures imported content against the platform's active DAT
// snapshot. A directory is judged by its files: one disagreement condemns the
// import, otherwise one verified file vouches for it.
//
// It also returns the hashes it computed, when the import is exactly one ROM.
// The gate was already measuring the bytes at the only moment they are
// guaranteed to exist unarchived, and throwing the answer away meant the same
// file had to be read again later by a backfill sweep. A multi-file import has
// no single ROM identity, so it returns none — $.gamarr.md5 could not mean
// anything for it.
func (m *Manager) catalogVerdict(path, platformSlug string) (db.DatVerdict, *db.LibraryHashes) {
	if platformSlug == "" {
		return db.DatVerdict{Status: db.CatalogUnknown}, nil
	}
	files := romFilesUnder(path)
	if len(files) == 0 {
		return db.DatVerdict{Status: db.CatalogUnknown}, nil
	}
	var hashes *db.LibraryHashes
	best := db.DatVerdict{Status: db.CatalogUnknown}
	for _, f := range files {
		res, err := romfile.HashPayload(f)
		if err != nil {
			slog.Warn("catalog gate: cannot hash file", "file", sanitizeLog(filepath.Base(f)), "error", err)
			continue
		}
		if len(files) == 1 {
			hashes = libraryHashes(res)
		}
		v := m.jobs.MatchDatRom(platformSlug, filepath.Base(f), res.CRC, res.MD5, res.SHA1)
		// A headered platform's catalog publishes the PAYLOAD's hashes, so a
		// whole-file miss there is not a miss. Asking again with the stripped
		// hashes can only turn unknown into verified: MatchDatRom reaches its
		// mismatch verdict solely through the NAME lookup, which runs only
		// when no hash matched at all.
		if v.Status != db.CatalogVerified && res.Stripped {
			if pv := m.jobs.MatchDatRom(platformSlug, filepath.Base(f), res.Payload.CRC, res.Payload.MD5, res.Payload.SHA1); pv.Status == db.CatalogVerified {
				v = pv
			}
		}
		switch v.Status {
		case db.CatalogMismatch:
			return v, hashes
		case db.CatalogVerified:
			best = v
		}
	}
	return best, hashes
}

// libraryHashes maps a measurement onto the row shape the store persists.
func libraryHashes(res romfile.Result) *db.LibraryHashes {
	h := &db.LibraryHashes{CRC: res.CRC, MD5: res.MD5, SHA1: res.SHA1}
	if res.Stripped {
		h.Unh = &db.UnheaderedHashes{
			CRC: res.Payload.CRC, MD5: res.Payload.MD5, SHA1: res.Payload.SHA1,
			Header: res.HeaderKind,
		}
	}
	return h
}

// romFilesUnder lists the candidate ROM files at path: the file itself, or
// every plausible ROM inside a directory.
func romFilesUnder(path string) []string {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		if catalogCandidate(path, fi) {
			return []string{path}
		}
		return nil
	}
	var out []string
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if catalogCandidate(p, info) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func catalogCandidate(path string, fi os.FileInfo) bool {
	if fi.Size() == 0 || fi.Size() > catalogMaxFile {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	return !nonROMExtensions[strings.ToLower(filepath.Ext(base))]
}
