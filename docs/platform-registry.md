# The platform registry

One canonical platform vocabulary, in the database, that every other name a
platform answers to hangs off.

## Why it exists

A ROM manager has to talk about platforms to a lot of strangers, and every one
of them spells them differently. An indexer says category `100015`. A DAT
authority says `Sega - Mega Drive - Genesis`. RomM says `genesis-slash-megadrive`,
which is also the directory name on disk. IGDB says `ps` where we say `psx`.
Torznab wants a Newznab number. The renamer wants to know whether the platform
is discs or cartridges.

Before the registry, each of those lived in its own hardcoded map, and there
were nine of them. Nothing anchored them to each other, so every new need
minted a tenth — and the gaps between them were user-visible. The Prowlarr
category map had never carried `atari2600`, `gbc`, `sms` or `gg`; the display
name lookup scanned that map; so every search result and every imported file on
those platforms was labelled **"Unknown"**, a word that reads as a failure when
the slug was right the whole time. The platform list served to pickers was
built from those same maps plus whatever happened to be in the library — which
meant a platform you had never acquired for could not be enumerated, and
therefore could not be added to.

The fix is the one the arrs already use. Prowlarr keeps a single canonical
category vocabulary and every indexer declares its own names onto it. Radarr
does not invent movie ids, it adopts TMDB's. So: one table, one row per
platform, every foreign name a column on that row.

## The shape

`platforms` is keyed on our own slug — the name the database, the search layer
and the on-disk directory tree already speak. The canonical *identity* is the
IGDB platform slug and id, unique across rows where claimed. That split is
deliberate and it is the same one Radarr makes: a row has its own key, and the
external authority's identifier rides along as a column. Adopting a vocabulary
does not require rekeying forty existing call sites onto it.

| Column | What it replaces |
|---|---|
| `slug` (PK) | — the vocabulary everything already speaks |
| `display_name` | two divergent slug→name maps, and the literal `"Unknown"` |
| `igdb_slug`, `igdb_id` | the canonical identity — nothing before |
| `romm_fs_slug` | the one-entry alias map (`genesis`) |
| `prowlarr_categories` | the outbound half of the category map |
| `torznab_category` | a 26-slug switch statement |
| `media_class` | nothing — carts/discs/arcade/computer/pc, also the profile-template class |
| `converts_to_chd` | a four-slug map, plus its hand-synced copy in the frontend |
| `acquisition_enabled` | nothing — the per-platform switch |
| `default_profile_id` | the platform column on quality profiles |
| `is_system` | nothing — a directory that is not a platform |

The shipped vocabulary is a seed (`platformSeed`), written **once**, on a table
that has never been written — so an operator's edits survive upgrades and a
fresh install matches what ships. Identity values were lifted from RomM's
IGDB-backed platform list at authoring time and committed rather than fetched:
a fresh install and CI have to be able to say what a platform is without any
external service to ask.

## What the registry does not do

**It does not absorb the other slug-keyed tables.** `dat_platforms` (which
authority catalogues a platform) keeps its own table and its own screen. The
registry is the vocabulary it enumerates from. The catalog vocabulary being
*wider* than the download-side category map is intentional and documented
where it is enforced; the registry anchors that divergence rather than forcing
a join.

**It does not replace the parsers.** `internal/platform` still holds an
extension map, a set of metadata-name aliases and an ordered list of title
hints. Those turn an arbitrary string or a file on disk into a slug — they are
parsers, not vocabulary, and the answer they produce is a registry slug. A test
asserts that every slug the parsers can emit names a real registry row, which
is the property that actually matters: a detection must never succeed into a
platform the app cannot then describe. That is exactly how `"Unknown"` ended up
written into the library in the first place.

**It does not let an operator invent platforms.** There is no POST and no
DELETE. Which platforms exist is not a preference; how RomArr treats one is.

## Using it

```go
platform.DisplayName(slug)     // never returns "Unknown" — an unknown slug answers with itself
platform.TorznabCategory(slug) // Console/Other for anything unmapped
platform.ConvertsToCHD(slug)
platform.CategoriesFor(slug)   // Prowlarr categories; empty means "search everything"
platform.ToRommFSSlug(slug)    // the on-disk directory name
platform.Rows()                // the whole vocabulary, ordered for humans
```

Lookups are memoized with explicit invalidation, the same arrangement the size
bands use: a table read once per search result would otherwise be thousands of
queries, and snapshotting at boot would mean an edit needed a restart to take
effect. `internal/platform` never imports `internal/db` — the store is injected
at boot and satisfies the `Registry` interface structurally.

🔴 `ToRommFSSlug` decides which directory a ROM is written to. A wrong value
there does not misdisplay a name, it misfiles a file — so a slug the registry
has no row for falls back to the shipped alias map rather than to the bare
slug, and a test pins the round trip for every seeded platform.

## API

| Route | |
|---|---|
| `GET /api/platforms` | `{id, name}` for pickers, plus an `all` sentinel |
| `GET /api/platforms?full=1` | whole registry rows, with each platform's DAT lane |
| `GET /api/platforms/{slug}` | one row |
| `PUT /api/platforms/{slug}` | admin; sparse `{display_name, media_class, converts_to_chd, acquisition_enabled}` |

Reads are open because every picker in the app needs the vocabulary. Edits are
configuration, so they sit behind the same admin gate as the DAT assignments.
Identity fields are not editable: they are what a platform *is*, not how you
treat it.
