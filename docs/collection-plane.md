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

## API

```
GET  /api/platforms/{slug}/set   ?status=owned|gap|out|all &q= &page= &page_size=
GET  /api/clonelists             (admin) locators, stored lists, fetch base, run status
POST /api/clonelists/refresh     (admin) re-fetch every assigned list; 409 while running
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
