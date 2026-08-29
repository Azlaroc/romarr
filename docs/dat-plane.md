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

## Reading the catalog

Two questions the catalog exists to answer, both against the platform's
**active** snapshot:

```
GET /api/dat/games?platform=gb&q=tetris&page=1&page_size=50
GET /api/dat/games/{id}/roms
```

The first is the browse door — *what exists for this platform?* — paginated,
with `total` reporting the match count rather than the page length. The second
returns one dump's files, which for a disc is the cue plus every track; that is
why it is a separate call and not a column on the game.

Only the active snapshot is visible. A refresh that replaces a catalog makes the
old one invisible to both calls immediately, so a browse never mixes two
generations of the same platform.

## The trust gate

The catalog is the only place RomArr can answer *"is this the real dump?"*, and
the import pipeline asks it exactly once: **after extraction**, before anything
downstream treats the import as real.

🔴 **The gate hashes the extracted ROM, never the downloaded file.**
archive.org's md5 is the hash of the `.7z` it stores; No-Intro's crc32 is the
hash of the `.gb` inside it, and recompressing the identical ROM changes the
first without touching the second. A gate built on the source hash passes every
file — including a corrupt one — and looks healthy while doing it. The
source-hash check that already runs before a destructive convert answers a
different question ("did the bytes arrive intact?") and stays.

Three verdicts, recorded on the library row under `$.gamarr.catalog`:

| verdict | what it means | what happens |
|---|---|---|
| `verified` | a catalogued dump has this file's hash | import proceeds |
| `unknown` | nothing matches by hash **or** by name | import proceeds, recorded |
| `mismatch` | the catalog knows a file by this name for this platform and its hash is **not** this one | content comes back out of the library, job fails, release is blocklisted, selector moves to the next candidate |

A rejection needs the catalog to **disagree**, not merely to be silent. Hacks,
homebrew, translations and dumps newer than the snapshot are all `unknown`, and
on some platforms they outnumber the catalogued ones several times over —
atari2600 carries 2,691 owned files against 905 catalogued. Rejecting silence
would make most of a platform unacquirable.

Hash order matters: a hash hit is checked first, so a correctly dumped ROM that
someone renamed is verified rather than accused. Only when no hash matches does
the name lookup decide between disagreement and silence. A file that cannot be
hashed at all is `unknown` — an unreadable file is not evidence of a bad
release.

Disc sets are not gated in this pass: their members converge at a set barrier
that already has its own verification step, and a per-member verdict there would
condemn a set for one late track.

## The naming authority

The snapshot names files. The bulk renamer resolves a library entry's stored
hashes (`$.gamarr`, including the header-stripped `unh` set on headered
platforms, then `$.romm` — the same inner-content domain) against `dat_roms`
and proposes the catalog's canonical name; a row with no stored hashes is
staged, hashed once and the result persisted, so it never stages again. The
file's name and the file's verdict come from the same book, and naming works
offline.

The lookup is tie-aware — every row the hashes land on is considered, unlike
the gate's single-verdict lookup. A headered/headerless twin pair collapses to
one answer (the `.unh` extension never reaches a filename); a hash tie between
an original release and a compilation extraction resolves to the original;
anything else is surfaced as *review*, never renamed automatically.

A local miss **fails loudly**: the row reads `no local DAT match`, is counted
(`dat_misses`), and keeps its name — uncatalogued content forgoes name
authority by design. The online Playmatch engine survives only behind the
`normalize_online_fallback` setting (default off); when enabled its answers
land as review with `name_source: "playmatch"`, and an engine failure degrades
to the loud miss rather than aborting the run.

## Adding a platform

No code change:

```
PUT /api/dat/platforms/<slug>
{"authority": "redump", "dat_code": "ps3", "enabled": true}
```

Then refresh that authority. There is no delete — set `enabled: false` to retire
an assignment, which keeps its catalog readable.

## Size definitions (retired)

There used to be a per-platform size band here — `platform_size_definitions`,
derived from each catalog's `size_p01`/`size_p99`, editable on its own screen,
enforced as a hard reject and a score tier in selection, with per-profile
`preferred_size_min`/`max` overrides on top.

The whole plane is retired. The DAT knows every dump's exact bytes before a
search runs, and the post-extract trust gate measures the actual bytes after a
download, so a size bound could only ever reject on a *proxy* for information
the pipeline measures directly — and the measured record of the bands was
that they rejected almost nothing except, once, legitimate tiny cartridge
dumps. An absurd candidate now costs one wasted download before the gate
blocklists it and moves on: bandwidth, not correctness.

What remains is measurement, not machinery: the `size_p01`/`size_p50`/
`size_p99` columns on `dat_snapshots` are still recorded at import, as inert
data about the catalog. Nothing reads them into a decision.

## Where this lives in the UI

Two screens, deliberately apart, because they answer different questions.

**Settings → Metadata** owns the authorities: which catalogs are fetched, from
where, pinned to what, how often, and what each platform's assignment is. It
also shows coverage, rendering the server's `summary` string verbatim so the
two independent counts can never be dressed up as a completion figure. Refresh
and upload live here as immediate actions; a busy service answers 200 with
`success:false`, which the screen reports as "already running" rather than as
an error.

**Settings → Quality Definitions** owns the numbers: per-platform minimum and
maximum size, editable, with the platform's provenance beside them. It sits
next to Profiles rather than under Metadata because it is the same thing arr
calls Quality Definitions — global limits that profiles then override — and not
a property of where a catalog came from.

Three display rules on that screen are load-bearing rather than cosmetic, and
all three come from the same principle: *an arr rejects on size, but never on a
number you cannot see.*

- **`0` renders as Unlimited**, per end. It is a real value, not an empty field.
- **The number shown is the number enforced.** The input carries exact bytes; a
  humanized figure sits beside it, never in place of it.
- **A row with no stored definition says so** instead of claiming a source it
  does not have, and its reset offers to *clear* rather than to *restore*
  — which is what `has_catalog` on each row is for.

Edits on both screens batch behind a save bar and write on Save, so a set of
related changes is reviewed before it is committed; the guard prompts if you
navigate away with edits pending.
