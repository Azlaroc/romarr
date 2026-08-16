# Source plane (F2 spike)

RomArr's source plane answers two questions for any place a game can come from:

1. **Search** — "what releases match this query?"
2. **Fetch** — "download this release to disk."

Torrent indexers (via Prowlarr), scrape sites (Vimm), and native API clients
(archive.org) all reduce to that two-method shape. This document covers the F2
spike: the `SearchSource` contract and the first driver written against it, the
native **Internet Archive** driver.

## The contract — `internal/sources/driver`

```go
type SearchSource interface {
    Name() string
    Search(ctx, Query) ([]Release, error)   // (nil,nil) == healthy-but-empty
    Fetch(ctx, Release, destDir) (localPath, error)
}
```

A **`Release` is a single file**, and it carries `MD5`, `SHA1`, and `Size`.
This is the property archive.org forces into the contract that Torznab never
modelled. Torznab hands you a whole torrent/pack; a No-Intro or Redump
collection on archive.org is *one item containing 1,500 per-game files*, each
independently addressable and hash-identified. Modelling a release as a file is
what lets the selector (F4) and the normalize/convert hand-off (F5) work on
verifiable per-file identity instead of a whole-pack blob.

`FanOut(ctx, []SearchSource, Query)` runs every source concurrently, merges
their releases, and isolates failures (a source that errors — or panics —
contributes an error, never aborts the others). It is the shape the four
current fan-out call sites (`api.go`, `requests.go`, `torznab_wire.go`,
`cmd/gamarr/main.go`) converge on once Prowlarr/Vimm are hoisted behind
`SearchSource`.

## The archive.org driver — `internal/sources/archiveorg`

Ported from the RomSeerr prototype (proven end-to-end for Genesis and PSX):

| Step | Request |
|------|---------|
| catalog | `GET https://archive.org/metadata/<item>` → `files[]` (name, size, md5, sha1) |
| fetch   | `GET https://archive.org/download/<item>/<name>` — Range-resumable |

- **Search** fetches the platform's collection item (cached 1h), keeps only
  rom-extension files, drops non-English regional dumps, and returns the files
  whose names cover every query word.
- **Fetch** streams to a `.part` file, resumes from any partial via HTTP
  `Range`, validates the final byte count against `Release.Size`, then renames
  into place.
- The `<name>` in metadata already leads with `<item>/` for collections whose
  files sit in an item-named subfolder (the hearto PSX 1G1R set is one), so the
  download path legitimately repeats the identifier. We pass `name` verbatim —
  stripping it 404s.

### Buried-in-pack, proven live

A single PS1 title resolves out of a 1,512-file collection item and fetches as
one file (`ROMARR_LIVE_IA=1 go test ./internal/sources/archiveorg -run Live`):

```
resolved: Castlevania - Symphony of the Night (USA).zip
  size=392826195  md5=6452e9da539aed682573794c8a794c83
  url=https://archive.org/download/2024-sony-playstation-usa-hearto-1g1r-collection/2024-sony-playstation-usa-hearto-1g1r-collection/Sony%20-%20Playstation%20-%20USA/Castlevania%20-%20Symphony%20of%20the%20Night%20%28USA%29.zip
Range GET ok: 206 Partial Content  content-range="bytes 0-15/392826195"
```

The `Content-Range` total matches the metadata `size` exactly, and the server
honours `Range` → the fetch is resumable (`curl -C -` semantics).

## Enabling a platform (registry, not code)

The driver is **inert until a platform is mapped to a collection item**. The
embedded defaults ship an empty `archiveorg.items`, so on a live server the
archive.org branch is a no-op until an operator opts a platform in via
`GAMARR_SOURCES_PATH` / `GAMARR_SOURCES_URL`:

```json
{
  "archiveorg": {
    "base_url": "https://archive.org",
    "items": {
      "psx":     "2024-sony-playstation-usa-hearto-1g1r-collection",
      "genesis": "nointro.md",
      "tg16":    "nointro.tg-16",
      "snes":    "ef_nintendo_snes_no-intro_2024-04-20",
      "nes":     "No-Intro_NES",
      "gb":      "No-Intro_GB",
      "gbc":     "No-Intro_GBC",
      "gba":     "ef_gba_no-intro_2024-02-21",
      "n64":     "ef_nintendo_64_no-intro_2024-02-10",
      "nds":     "ni-n-ds-dec_202401"
    }
  }
}
```

Loading properties worth knowing:

- **The registry is database-managed.** The file (or `GAMARR_SOURCES_URL`)
  seeds the `source_registry` table on **first boot only** — after that the
  database is authoritative and file edits have no effect. Edit sources in
  Settings › Indexers (or `PUT /api/source-registry/{name}`); changes apply
  immediately, no restart.
- When it IS consulted (first boot), the file is a **full replace** of the
  embedded defaults (a plain whole-document unmarshal, no merge), and a
  malformed file logs a warning and **silently falls back to the embedded
  defaults** — validate the JSON (`jq .`) before first boot.

### Verified per-title collection items (2026-08)

| slug | item | shape |
|------|------|-------|
| `psx` | `2024-sony-playstation-usa-hearto-1g1r-collection` | 1,501 `.zip` (bin/cue), USA 1G1R; names nest under an item-named subfolder |
| `genesis` | `nointro.md` | per-game `.7z` |
| `tg16` | `nointro.tg-16` | per-game `.zip` |
| `snes` | `ef_nintendo_snes_no-intro_2024-04-20` | 4,122 `.zip` |
| `nes` | `No-Intro_NES` | 5,359 `.zip` |
| `gb` | `No-Intro_GB` | 1,896 `.7z` |
| `gbc` | `No-Intro_GBC` | 1,931 `.7z` |
| `gba` | `ef_gba_no-intro_2024-02-21` | 3,555 `.zip` |
| `n64` | `ef_nintendo_64_no-intro_2024-02-10` | 1,216 `.zip` (BigEndian `.z64`) |
| `nds` | `ni-n-ds-dec_202401` | 266 `.7z` — **partial set**; the only per-title DS item found |

All ten verified the same way: unrestricted, `md5`+`sha1` on every `files[]`
entry, No-Intro region-tagged naming (survives the driver's English filter),
and a ranged download GET returning 206.

**The `nointro.<code>` naming does NOT generalize.** `nointro.snes` and
`nointro.gba` don't exist; `nointro.snes_202203` and `nointro.n64_202203`
exist but are single-zip packs (one 3–12 GB archive), useless to a per-file
driver; `nointro-gb-20260731` / `nointro-gbc-20260731` are single RARs. Every
candidate item needs verification before it's mapped:

1. `GET /metadata/<item>` — `files[]` must be per-title `.zip`/`.7z` (not one
   pack/RAR), each entry carrying `md5`+`sha1`, names region-tagged
   (`(USA)`, `(Europe)`, …) so the English filter can work.
2. One ranged `GET /download/<item>/<name>` (`Range: bytes=0-1023`) must return
   **206** — items exist whose metadata is public but whose downloads are
   auth-gated (401/403).
3. Anonymous metadata calls rate-limit around 60/hr — batch probes.

Redump disc items that are pre-made `.chd` are open; the bin/cue Redump items
are frequently auth-gated — prefer open sources.

## Disabling a source

Flip the source's **Enabled** toggle in Settings › Indexers (or PUT
`{"enabled": false}` to `/api/source-registry/{name}`) — every search skips it
immediately. A source is *active* only when enabled AND it has platforms
mapped: an enabled source with an empty mapping is skipped too (the old
empty-mapping fall-through that returned cross-platform, mis-slugged hits is
guarded by the Active predicates). The legacy dead-port trick
(`"base_url": "http://127.0.0.1:1/"`) still works but is obsolete.
- A hashless-but-live search paired with a dead fetch lane is the worst
  combination: the search keeps feeding the selector candidates whose download
  always fails, so every scheduler cycle produces a guaranteed error job. If a
  source's fetch lane dies, disable the whole source.

## Hand-off to F5 (normalize / convert)

A resolved `Release` / `SearchResult` now carries `MD5`/`SHA1`/`Size`. F5 (the
"igir gap" — DAT-rename + convert to `.chd`/`.rvz`/`.nsz`) consumes exactly
this: fetch the file, verify it against the release hash, then normalize. The
PS1 case above is the canonical example — a `.zip` of bin/cue that F5 turns into
a single `.chd`.

## Scope after the spike (F2 proper) — since landed

- ✅ Prowlarr + Vimm hoisted behind a single fan-out (`search.Source` /
  `search.FanOut` at the `SearchResult` level; `driver.SearchSource` remains
  the narrow native-driver contract).
- ✅ Dead Myrient driver dropped (service shut down 2026-03-31).
- ✅ Vimm's `InsecureSkipVerify` stripped.
- Prowlarr's Internet Archive Cardigann indexer (path #2, bulk) remains
  unwired — it's the fallback for platforms whose per-title IA coverage is
  thin (see `nds` above).
