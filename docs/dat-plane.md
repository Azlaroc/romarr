# DAT plane

RomArr's DAT plane answers one question: **what dumps are known to exist for a
platform, and how big should they be?**

That is the layer an acquisition tool never had. Metadata providers describe
*titles*; a DAT describes *dumps* — every known revision, region and disc, with
per-file hashes and sizes. The selector reasons about candidates against those
sizes, and the library can eventually be reconciled against that catalog.

Nothing here is a trust signal. DAT data is plausibility and scoring input; a
download is trusted solely by the post-extract hash gate.

## The objects

```
dat_authorities   who publishes catalogs      (no-intro, redump, mame)
dat_platforms     one authority per platform  (gb -> no-intro, psx -> redump)
dat_snapshots     one import of one platform  (version, size stats, diff, active)
dat_games         a catalogued dump
dat_roms          the files that make it up
```

Exactly one authority per platform; an absent row means the platform has no DAT
lane (`switch` is shop-native, `pc` has no authority). Two snapshots per platform
are retained — the active one and its predecessor.

## Authorities are data, not code

`fetch_driver` and `fetch_base` are columns. Repointing an authority at a mirror,
a CDN or a self-hosted clone is an edit rather than a deploy, and adding a fourth
authority is an INSERT. Nothing in the pipeline caches a provider identity, and
provider ids are never a join key — correlation is on facts we own: content
hashes and the canonical DAT name.

| driver | `dat_code` is | transport |
|---|---|---|
| `libretro` | the mirror's DAT name without extension, e.g. `Atari - 2600` | a bare clrmamepro DAT |
| `redump` | a Redump system code, e.g. `psx`, `ss`, `gc` | a zip holding one logiqx XML |
| `upload` | a free label | none — the catalog arrives by hand |

The pair is validated on write, because a code belonging to another driver would
otherwise fail only at fetch time — hours later, as a partial refresh nobody was
watching.

## Two rules that cost real outages

**Never point a fetch driver at Dat-o-Matic.** It is scriptable, which is the
trap rather than the reason: its anti-scrape cron bans IPs silently, and an
egress ban on a server surfaces hours later as "nobody can reach it". Every
project in this space reaches the same split — automate the others, keep
No-Intro human-in-the-loop. The libretro mirror republishes the same data with
full sizes and hashes, and uploading a daily pack by hand is the first-class
alternative.

**Use `redump.info`, not `redump.org`.** The `.org` host serves no TLS on 443
and lags behind. Do not let a later cleanup "fix" the scheme.

Both are refused by validation, so neither can be reintroduced by an edit.

## Refreshing

`POST /api/dat/authorities/{name}/refresh` walks every enabled platform assigned
to that authority, one at a time, with a politeness gap between fetches. Per
platform: fetch → hash the transport bytes → unwrap any container → parse →
write the catalog beside the database → store a snapshot.

- A re-fetch whose hash matches the active snapshot is a **no-op**, so nothing
  churns and no phantom diff is recorded.
- A platform that fails **keeps its previous snapshot** — stale beats absent.
  The authority's `last_status` becomes `partial`, with per-platform detail in
  `last_error`.
- `pinned_version` freezes the **automatic** path only. The button stays live.

Automatic refresh is off by default (`dat_auto_refresh_enabled`). A catalog that
silently advances underneath the selector is what pinning exists to prevent.

## Uploading (the escape hatch)

`POST /api/dat/authorities/{name}/upload` takes **multipart/form-data** with a
`file` part. The transport is not a style choice: raw request bodies are capped
at 1 MB, and a daily pack is tens of megabytes.

- With `?platform=<slug>`, the body is one catalog for that platform — a bare
  DAT, or a zip holding a single one. This is the path for a Redump zip, whose
  member name is a datfile title rather than the system code.
- Without it, a zip is treated as a pack: each member is matched against the
  `dat_code` of every platform assigned to that authority, matches are imported,
  and the rest are reported as `skipped`. Skipping most of a daily pack is the
  normal case, not a failure.

An upload goes through the same import path as a fetch, so it fully replaces
whatever was there.

## Coverage

`GET /api/dat/coverage` reports two **independent** counts per platform: how many
files are owned, and how many dumps the active catalog knows about. Owned files
are not matched against catalog entries, so the numbers are presented as
`N owned · M known` and never as a completion percentage.

## Adding a platform

No code change:

```
PUT /api/dat/platforms/<slug>
{"authority": "redump", "dat_code": "ps3", "enabled": true}
```

Then refresh that authority. There is no delete — set `enabled: false` to retire
an assignment, which keeps its catalog readable.
