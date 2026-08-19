# Collection plane

The collection plane answers: **what should this platform's library contain,
what of it do we have, and what is surplus?**

Its unit is the **1G1R set** — one game, one ROM. That set is the central policy
object: filling its gaps and pruning its surplus are the same reconciliation run
in opposite directions, so both read one definition rather than each growing an
opinion of what "the collection" is.

Nothing here is a trust signal either. The set says which dump it *wants*; a
download is still trusted solely by the post-extract hash gate.

## The objects

```
dat_games / dat_roms   the catalog (DAT plane) — the input
clone_lists            one platform's stored upstream clone list
clone_groups           one row per (group, search term) — flat on purpose
dat_platforms
  .clonelist_name      the locator: a SEPARATE upstream vocabulary
```

There is no `collection_*` table yet: a set is **derived**, and a stored copy of
a derivation is a second source of truth waiting to disagree.

## Why clone lists exist

The plan assumed the catalog's `clone_of` column would define the set. It does
not: **every one of the 93,814 games imported on 2026-08-19 carries an empty
`clone_of`**, because both authorities publish standard DATs rather than
parent/clone ones. Grouping therefore comes from two places:

1. **Parsed titles** (`bare_title`), which does most of the work. Atari 7800's
   clone list holds six groups against 141 titles.
2. **The clone list**, which corrects it where upstream knows better. Two cases
   no parser can get right:
   - *Different names, one game* — `Contra`, `Operation C` and `Probotector` are
     one Game Boy game with three regional titles.
   - *One name, different games* — `Centipede (Accolade)` and
     `Centipede (Majesco)` share a title and are not the same game.

RomArr adopts [Retool's lists](https://github.com/unexpectedpanda/retool-clonelists-metadata)
(BSD-3) rather than minting its own opinion. Matching offers every reading of a
dump's name — `Centipede (USA) (Majesco)` → `Centipede (USA) (Majesco)`,
`Centipede (Majesco)`, `Centipede` — and the **longest** matching search term
wins, so a variant is never mistaken for its own base title.

🔴 The locator is **not** the DAT code. Five of thirty disagree, and two are
exact reversals of each other (`TurboGrafx 16` vs `TurboGrafx-16`,
`Neo Geo Pocket Color` vs `NeoGeo Pocket Color`). That is why it is seeded data
with a test rather than derived.

## What the set decides

Per group, one keeper. The order:

1. the clone list's title preference,
2. **region priority** — from the platform's own quality profile,
3. an English language tag,
4. the later revision,
5. a verified dump,
6. the name, for stability.

**Region ORDERS; it never filters.** A game with no dump in any preferred region
keeps its best available one and says so — "no preferred-region dump exists;
kept japan". That is the English-preferred, keep-Japan-only-orphans policy: the
prune removes redundancy, never a game.

Excluded from the set entirely: bad dumps (always — a broken file is not a copy
of the game), and prototypes, demos, BIOS images and unlicensed/aftermarket
dumps unless the profile allows them. A group with nothing left is not a gap;
nobody is missing a game that only exists as a prototype.

🔴 The discriminating tag is not always on the game. No-Intro names an Atari 7800
game `1942 (World)` with no flags at all while its rom is
`1942 (World) (Aftermarket) (Unl).a78`, so classification reads the rom names
too.

## Ownership: three tiers, one file per dump

| tier | evidence | strength |
|---|---|---|
| `hash` | a catalogued rom hash equals a stored library hash | proof |
| `name` | the canonical dump name equals the library file's | strong |
| `title` | parsed titles agree | a guess |

Matching runs tier by tier across the whole platform, strongest first, and
**each library file is claimed once**. Without that rule the title tier hands
the same file to every member of a group — parsed titles cannot tell
`Ace of Aces (USA)` from `Ace of Aces (Europe)` — and the prune direction then
offers the keeper's own file up for archiving. The keeper is offered each file
first, so a group never reads as a gap while a surplus row holds the only copy.

Hash coverage is uneven in practice (some platforms are ~100% hashed, `nes` and
`atari2600` far less), which is exactly why the weaker tiers exist and why they
are ranked rather than merged.

## Statuses

| status | meaning |
|---|---|
| `owned` | the set's keeper is on disk |
| `gap` | the set wants this game and we do not have it |
| `out` | policy excludes the group entirely |

An owned dump in an `out` group is still surplus — that is how a platform's
homebrew pile becomes a prune candidate rather than something invisible.

Counts always describe the whole set, never the filtered page: "84 gaps" is the
number an operator acts on.

## Collection mode: from a set to work

A platform in **collection mode** monitors its whole set. Everything the set
wants and the library does not have becomes a row in `collection_targets` — the
gap list under Wanted → Collection.

The gap list is a **table**, not a derivation recomputed per cycle, because a
gap needs memory: how often it has been tried, what came back, and when to try
again. A title nothing indexes must stop being searched every hour. Backoff
doubles per attempt and caps at a week.

It is deliberately **not** the wishlist. The wishlist is what a person asked for
by name; these are what a policy implies, and one platform's set can imply
hundreds. Both feed **one** pipeline (`processWanted`), so a gap is acquired
under exactly the policy a wishlist row would have been — two queues, one set of
rules.

```
sync    reconcile the set → insert new gaps, keep existing ones' history,
        re-open a grab that did not fill its gap,
        delete gaps that are filled or no longer wanted
fill    take the due targets, oldest attempt first, up to the cycle budget
record  grabbed | unavailable (with the reason) | retired, if it turned out owned
```

🔴 **A grab is not a fill.** Seen on a live install the day this shipped: a gap
was grabbed, the release imported cleanly, and the set still wanted the game —
what landed was a differently-named dump the catalog does not carry. The row sat
in `grabbed`, which the due query excludes, so it was never retried and never
retired. A grab that is still wanted 12 hours later re-opens, keeping its
attempt count so its own backoff decides when to try again. Whether the gap is
filled is measured against the catalog, never against "a download succeeded".

Two switches, independent on purpose:

| switch | says |
|---|---|
| **collection mode** | what is wanted — the whole 1G1R set |
| **acquisition** | whether RomArr may go and get it |

With collection on and acquisition off, the gap list still builds and nothing is
searched: you can look before you leap. Leaving collection mode **drops that
platform's gap list immediately** — the rows derive from a policy that no longer
applies, and a stale queue would keep the scheduler busy with work nobody asked
for.

`collection_fill_per_cycle` (default 10) bounds what ONE cycle asks of the
indexers, across all platforms. It is a pace, not a switch: emptying a whole
catalog's gap list into a source in one pass is how an anonymous budget gets
spent.

🔴 An empty wishlist is not an empty cycle any more. The scheduler used to
return early when the wishlist was empty; collection gaps come from a platform's
set, so that early return would have meant the feature never ran on an install
that works this way.

## Declutter: the set read in the other direction

Collection mode asks *what does the set want that I do not have* and fills the
deficit. Declutter asks *what do I have that the set does not want* and offers
to prune the surplus. One policy object, two directions — which is why they
share the reconciliation rather than each carrying an opinion.

The preview classifies; the apply moves. Nothing moves without a human seeing
the diff first.

| verdict | what it is | applied? |
|---|---|---|
| `archive` | a catalogued dump the set does not keep, identified by hash or canonical name, in a group whose keeper is on disk | yes |
| `review` | matched only by a parsed title, **or** the keeper is still missing | never |
| `excluded-group` | an owned dump in a group policy leaves out of the set (hacks, prototypes, unlicensed) | opt-in per run |
| `uncatalogued-duplicate` | no catalogued dump matches it, but its TITLE is a game the set covers and whose catalogued dump is already on disk | opt-in per run |
| `uncatalogued-hack` | no catalogued dump matches it, and its NAME declares it a hack, alternate/bad dump, pirate release or fan translation | opt-in per run |
| `uncatalogued` | no catalogued dump matches this file at all | never |

Three rules it will not bend:

- 🔴 **Archive, never delete.** Every prune is a MOVE into the archive tree plus
  a manifest line naming where the file came from, where it went, and what
  replaced it. Nothing here removes bytes.
- 🔴 **Never prune the only copy.** A dump is surplus only when the dump the set
  actually keeps is already on disk. If the keeper is a gap, the copy you have
  *is* the collection, whatever its region.
- 🔴 **Never act on the catalog's silence alone.** A file no catalog has heard
  of is counted and listed, never archived by default. On one live platform that
  is 1,945 of 2,691 files, and "not in the DAT" is not evidence of redundancy.
  The one exception is explicit: a file whose *title* is a game the set covers
  **and whose catalogued dump is already on disk** is a spare copy of something
  owned — the hack and alt-dump pile that makes a shop list eighteen Asteroids.
  That is its own verdict with its own opt-in, and it never touches the
  standalone homebrew that exists nowhere else.

  A second exception covers what the first cannot see: a hack named for itself
  (`Asteroids SS (Asteroids Hack)`) never collides with the title it hacks, so
  no duplicate check can find it. The file's own name is the evidence —
  GoodTools-era markers (`[a1]`, `[h1]`, `[t1]`, `[b]`, `[o1]`, `[p1]`,
  `[T-Eng]`) and the explicit `(… Hack)` tag. `[!]` means verified good and
  never matches.

  🔴 Both exceptions stop at the same wall: if the set is still MISSING the
  catalogued dump of that game, the off-catalog file may be the only copy there
  is. It is review-only with every opt-in switched on — the lone-European-dump
  rule, one tier out.

The archive lives at `<roms>/.archive/<platform>/` by default
(`prune_archive_path` overrides it). Inside the ROM root and dot-prefixed on
purpose: the roms tree is what the cloud-sync exclusion covers, so an archive
beside it would start uploading exactly the files an operator just decided they
did not need online, and a hidden directory is skipped by the same library scans
that already ignore the renamer's scratch dir. A move is a rename, never a copy
— a cross-device archive path is refused loudly rather than silently copying
gigabytes.

## API

```
GET  /api/platforms/{slug}/set   ?status=owned|gap|out|all &q= &page= &page_size=
GET  /api/clonelists             (admin) locators, stored lists, fetch base, run status
POST /api/clonelists/refresh     (admin) re-fetch every assigned list; 409 while running
GET  /api/collection/targets     ?platform= &status= &q= &page= &page_size=
POST /api/collection/sync        (admin) ?platform= for one, omitted for every monitored platform
PUT  /api/platforms/{slug}       {"collection_mode": true|false}

GET  /api/library/prune/status
POST /api/library/prune/preview          {"platform_slug": "atari2600",
                                          "include_excluded": false,
                                          "include_uncatalogued_duplicates": false}
GET  /api/library/prune/preview/results  ?page= &page_size=
POST /api/library/prune/apply            {"exclude_ids": [...]}
POST /api/library/prune/stop
```

The fetch base is the setting `clonelist_fetch_base` — repointing at a mirror, a
fork or a test stub is an edit, not a deploy, exactly as with the DAT authorities.

## Layering

```
internal/collection     pure policy: grouping, keeper choice, reconciliation
internal/collectionsvc  rows <-> policy, plus clone-list fetching
internal/db             rows
```

`internal/collection` must never import `internal/db`, and `internal/db` must
never import `internal/collection` — the same constraint that made
`internal/datsvc` exist for the parser.
