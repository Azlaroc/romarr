# Torrent downloads

## Completion and seeding

Torrent jobs are driven by the persistent completion watcher. A job is
associated with its torrent by infohash (parsed from the magnet link or search
result; learned from a per-job qBittorrent tag for `.torrent` URLs). On
completion the payload is **copied** out of the seeding location into the
library and run through the fulfillment pipeline (extract → normalize →
convert → track) — the torrent and its files are left untouched, so seeding
continues under qBittorrent's own share limits.

- `REMOVE_AFTER_IMPORT=true` deletes the torrent and its files immediately
  after a successful import (ratio-free/public sources only — this ends
  seeding).
- `SEED_JANITOR_ENABLED=true` (default off) lets the watcher delete a torrent
  and its files once it is both imported and stopped by qBittorrent's share
  limits (`stoppedUP`/`pausedUP`), reclaiming disk automatically.

## Pack torrents (selective download, #256)

A wanted ROM often ships inside a multi-ROM pack torrent (a No-Intro set, a
1G1R collection). Two cases:

**Case B — loose-file pack (solvable):** pass `target_file` in the download
request — an exact in-torrent path (`Pack Name/Game (USA).gb`) or a bare
filename. The add goes in stopped (`.torrent` URLs; magnets must start to
fetch metadata and accept a brief head start), the watcher matches the target
in the file list, sets **every other file to priority 0**, and resumes. Only
the target downloads; only the target is imported. Piece-boundary spillover in
the prio-0 neighbors is ignored — the import copies the single target file.

If the target cannot be matched (or the client rejects `filePrio`), the job
falls back to the whole-pack download rather than wedging.

Plucked packs are skipped by the seed janitor: the same pack may be plucked
again later for a different title.

**Case A — pack is one big archive (`.zip`/`.7z` of the whole set):**
BitTorrent cannot select inside a single file. The whole archive downloads and
the fulfillment pipeline's extract stage unpacks it; unwanted contents can be
pruned in RomM. Prefer a native per-file source (archive.org driver) for these
titles when one exists.

## Whole-pack fallback

With no `target_file`, a multi-file torrent imports as one game directory
named after the torrent. That is the documented fallback — scattering a pack's
contents into per-game entries is a curation feature (F4+), not a download
feature.
