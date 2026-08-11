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
embedded defaults ship an empty `archiveorg.items`, so on the live server the
fourth fan-out branch is a no-op until an operator opts a platform in via
`GAMARR_SOURCES_PATH` / `GAMARR_SOURCES_URL`:

```json
{
  "archiveorg": {
    "base_url": "https://archive.org",
    "items": {
      "psx":     "2024-sony-playstation-usa-hearto-1g1r-collection",
      "genesis": "nointro.md",
      "tg16":    "nointro.tg-16"
    }
  }
}
```

Proven items from RomSeerr: PSX = the hearto USA 1G1R set (`.zip` bin/cue,
restricted=None, HTTP 206); No-Intro cart systems = `nointro.<code>` items.
Redump disc items that are pre-made `.chd` are open; the bin/cue Redump items
are frequently auth-gated (401/403) — the selector should prefer open sources.

## Hand-off to F5 (normalize / convert)

A resolved `Release` / `SearchResult` now carries `MD5`/`SHA1`/`Size`. F5 (the
"igir gap" — DAT-rename + convert to `.chd`/`.rvz`/`.nsz`) consumes exactly
this: fetch the file, verify it against the release hash, then normalize. The
PS1 case above is the canonical example — a `.zip` of bin/cue that F5 turns into
a single `.chd`.

## Scope after the spike (F2 proper)

- Hoist Prowlarr + Vimm behind `SearchSource`; collapse the four `wg.Add(4)`
  fan-out sites into a single `FanOut` over a registered slice.
- Drop the dead Myrient driver (shut down 2026-03-31).
- Strip Vimm's `InsecureSkipVerify`.
- Order sources: archive.org native driver (path #1), Prowlarr's Internet
  Archive Cardigann indexer (#2, bulk), optional Cardigann/others (#3).
