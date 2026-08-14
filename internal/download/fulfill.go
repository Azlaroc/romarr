package download

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gamarr/internal/normalize"
	"gamarr/internal/platform"
)

// romDestDir returns the library directory a platform's imports land in. It is
// the single place that computes this path: every import source must produce
// the exact layout the RomM sync reports back (romm.LocalPath), or the sync's
// adopt-on-path-collision check misses and the same file gets a duplicate
// library row. platSlug arrives from a download request; keep the fs_slug a
// single path component so it cannot climb out of the ROM library root.
func (m *Manager) romDestDir(platSlug string) string {
	return filepath.Join(m.cfg.GamesRomsPath, sanitizeFilename(platform.ToRommFSSlug(platSlug)))
}

// fulfillMeta carries source identity through the shared ROM fulfillment
// pipeline.
type fulfillMeta struct {
	JobID        string // "" => no job status/detail writes
	Title        string
	Platform     string // display name, e.g. "PS1"
	PlatformSlug string // internal slug (pre-ToRommFSSlug), never ""
	Source       string // "ddl" | "torrent" | "nzb" — library source + sidecar tag
	SourceClient string // "ddl" | "prowlarr" | "sabnzbd" | "nzbget"
	SourceID     string // fixed library source_id; "" derives Source+":"+finalPath
	MD5, SHA1    string // expected content hashes; both empty => verify skipped
	// DiscSet marks this payload as one member of a multi-disc group: it
	// lands inside the shared DiscSet.Dir game directory and the pipeline
	// tail (normalize/convert/m3u/sidecar/track) is deferred to the set
	// barrier (maybeFinalizeDiscSet) instead of running per member.
	DiscSet DiscSet
}

// fulfillLocalROM is the one ROM fulfillment pipeline behind every download
// source: verify → move into the library → extract → normalize → convert →
// sidecar → track. stagingPath must be content the manager owns outright (a
// finished DDL file, a completed usenet dir, or a copy of a torrent payload —
// never a live seeding payload): every stage below may rename, rewrite, or
// delete inside it. When stagingPath is already the library destination the
// move is skipped and the remaining stages re-run idempotently — the
// restart-recovery re-entry case. Returns the tracked path.
func (m *Manager) fulfillLocalROM(stagingPath string, meta fulfillMeta) (string, error) {
	// Authenticate the download before any destructive convert: stagingPath
	// still matches the source hash here — before it is moved/extracted/renamed
	// — and we only pay the hash when a convert is actually eligible.
	convertHashStatus := ""
	if m.LoadSettings().ConvertROMs && normalize.FormatPolicy(meta.PlatformSlug) == "chd" {
		convertHashStatus = verifyDownloadHash(stagingPath, meta.MD5, meta.SHA1)
		if convertHashStatus == "mismatch" {
			slog.Warn("download hash mismatch; convert will be skipped",
				"title", sanitizeLog(meta.Title), "platform", meta.PlatformSlug)
		}
	}

	destDir := m.romDestDir(meta.PlatformSlug)
	if meta.DiscSet.valid() {
		// Set members converge one level deeper, into the shared game dir.
		// romDestDir itself stays the single platform-path authority.
		destDir = filepath.Join(destDir, sanitizeFilename(meta.DiscSet.Dir))
	}
	os.MkdirAll(destDir, 0755)
	dest := filepath.Join(destDir, sanitizeFilename(filepath.Base(stagingPath)))
	if stagingPath != dest {
		if err := moveContent(stagingPath, dest); err != nil {
			if meta.JobID != "" {
				m.jobs.UpdateMulti(meta.JobID, map[string]interface{}{
					"status": "error", "error": fmt.Sprintf("Organize failed: %v", err),
				})
			}
			return stagingPath, err
		}
	}

	// If extraction is enabled, unpack archives into clean game-named
	// directories (not "<name>.zip.extracted" wrappers), remove the consumed
	// archive, and track the extracted content so file_path/file_size reflect
	// the real ROM rather than an archive that no longer exists. A directory
	// payload (multi-file torrent/usenet) gets each top-level archive unpacked
	// in place. Extraction failure is non-fatal: keep the archive.
	finalPath := dest
	detail := fmt.Sprintf("Moved to RomM (%s)", meta.Platform)
	if m.LoadSettings().ExtractArchives {
		extracted := 0
		if isExtractableArchive(dest) {
			if outDir, err := extractToGameDir(dest); err == nil {
				os.Remove(dest)
				finalPath = outDir
				extracted++
			} else {
				slog.Warn("archive extraction failed; keeping archive",
					"archive", sanitizeLog(filepath.Base(dest)), "error", err)
			}
		} else if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
			entries, _ := os.ReadDir(dest)
			for _, e := range entries {
				if e.IsDir() || !isExtractableArchive(e.Name()) {
					continue
				}
				archive := filepath.Join(dest, e.Name())
				if _, err := extractToGameDir(archive); err == nil {
					os.Remove(archive)
					extracted++
				} else {
					slog.Warn("archive extraction failed; keeping archive",
						"archive", sanitizeLog(e.Name()), "error", err)
				}
			}
		}
		if extracted > 0 {
			detail += " (extracted)"
		}
	}

	// Disc-set member: STOP here. The tail (normalize/convert/m3u/sidecar/
	// library row) runs once over the whole set dir when the last member
	// lands — running it per member would DAT-rename and convert siblings
	// mid-flight and mint one library row per disc.
	if meta.DiscSet.valid() {
		// An extracted archive (or a directory payload) landed as its own
		// subdir inside the set dir. Flatten it so every disc's .cue sits at
		// the set dir's top level — that is the layout the m3u writer groups.
		if fi, err := os.Stat(finalPath); err == nil && fi.IsDir() && filepath.Dir(finalPath) == destDir {
			finalPath = flattenSetMemberDir(finalPath, destDir)
		}
		if meta.JobID != "" {
			m.jobs.UpdateMulti(meta.JobID, map[string]interface{}{
				"status": "completed",
				"detail": fmt.Sprintf("Disc %d of %d imported; awaiting set",
					meta.DiscSet.Index, meta.DiscSet.Total),
				"set_member_done":     true,
				"imported_path":       finalPath,
				"convert_hash_status": convertHashStatus,
			})
		}
		slog.Info("disc-set member imported", "set", meta.DiscSet.ID,
			"disc", meta.DiscSet.Index, "of", meta.DiscSet.Total, "dest", sanitizeLog(finalPath))
		m.maybeFinalizeDiscSet(meta.DiscSet.ID)
		return finalPath, nil
	}

	if meta.JobID != "" {
		m.jobs.UpdateMulti(meta.JobID, map[string]interface{}{
			"status": "completed", "detail": detail,
		})
	}

	// F5 normalize (DAT 1G1R rename + multi-disc .m3u) on the clean, extracted
	// path, then convert (disc systems → CHD, verify before replacing the
	// source). No-ops unless enabled; never block import.
	finalPath = m.MaybeNormalize(meta.JobID, finalPath, meta.PlatformSlug)
	finalPath = m.MaybeConvert(meta.JobID, finalPath, meta.PlatformSlug, convertHashStatus)

	writeMetadataSidecar(finalPath, meta.Title, meta.Platform, meta.PlatformSlug, false, meta.Source)
	sourceID := meta.SourceID
	if sourceID == "" {
		sourceID = meta.Source + ":" + finalPath
	}
	m.TrackInLibrary(meta.Title, meta.Platform, meta.PlatformSlug, false, finalPath,
		contentSize(finalPath), meta.Source, meta.SourceClient, sourceID, meta.JobID)
	if meta.JobID != "" {
		m.jobs.LogActivity("download_completed", meta.Title, activityMessage(meta.Source, meta.Platform), meta.JobID, nil)
	}
	slog.Info("ROM organized", "source", meta.Source, "dest", sanitizeLog(finalPath))
	return finalPath, nil
}

// activityMessage keeps each source's historical activity-log wording.
func activityMessage(source, platf string) string {
	switch source {
	case "ddl":
		return fmt.Sprintf("DDL to %s", platf)
	case "nzb":
		return fmt.Sprintf("NZB to RomM (%s)", platf)
	default:
		return fmt.Sprintf("Organized to %s", platf)
	}
}
