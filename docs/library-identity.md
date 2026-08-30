# Library identity

Every library row can carry hashes. This page says which ones, what each means,
and why they are not interchangeable — because they answer different questions
about *different bytes*, and treating any two as the same is how a library
silently stops matching its catalog.

## The bytes in question

A single library entry can be four different byte sequences depending on what
you ask:

```
Sonic.7z                     the file on disk           ← what a source published
  └─ Sonic (USA).nes         the ROM inside it          ← what the entry IS
       ├─ 16-byte header     a container descriptor     ← not part of the dump
       └─ payload            the cartridge's bytes      ← what the catalog knows
```

The archive's hash changes when you recompress the identical ROM. The header's
bytes differ between dump groups for the identical cartridge. Only the payload
is the dump — but only the whole ROM is *this file*.

## The keys

```jsonc
{
  "romm": {                       // RomM's, written while the retired sync
    "crc": "…",                   // ran (through 2026-08). Frozen data now —
    "md5": "…",                   // never write here, still read: the hashes
    "sha1": "…"                   // are inner-content, same domain as ours.
  },
  "gamarr": {                     // Ours.
    "crc": "…",                   // THE ROM'S OWN BYTES — an archive's inner
    "md5": "…",                   // file, or the file itself when raw.
    "sha1": "…",                  // The entry's identity.
    "hashed_at": "2026-08-20T…Z",

    "unh": {                      // The same bytes minus a container header.
      "crc": "…",                 // Present only when one was recognised.
      "md5": "…",                 // What a headered platform's catalog knows.
      "sha1": "…",
      "header": "ines"
    },

    "release": {                  // The source's PUBLISHED hash of the file it
      "md5": "…",                 // served — the OUTER bytes. Answers "have I
      "sha1": "…"                 // downloaded this exact release before?" and
    },                            // nothing else.

    "hash_skipped": "directory"   // Why this row can never carry a hash.
  }
}
```

`$.romm.*` and `$.gamarr.{crc,md5,sha1}` mean the same thing measured by
different parties; they agree when both exist. Everything else is a different
object, and none of them ever agree.

## Why `unh` exists

No-Intro publishes NES twice — `<game>.nes` with a 16-byte iNES header it
generates, and `<game>.unh` without one. Our NES library is a headered
GoodNES-era set whose header bytes are *not* No-Intro's. Measured on the live
library:

| our nes files hashed as | rows matching the catalog |
|---|---|
| the whole file | 0 of 25 sampled |
| header stripped | 713 of 762 |

So on nes the whole-file hash is the entry's identity and matches nothing, while
the payload hash matches everything and is not a file that exists anywhere.
Both are needed, so both are stored.

Only iNES gets a strip rule. Atari 7800 and Lynx carry container headers too,
but No-Intro hashes those *with* the header — stripping them would break a
match that works today. `nes` is the only platform in the catalog shipping
`.unh` rows.

## Who reads them

`FindLibraryByHash` and `LibraryHashIndex` (`internal/db/library.go`) check
every family independently and key each one separately, so a caller holding any
one kind of hash finds the row. Through them the families reach:

- the collection plane's ownership tier — hash beats name beats parsed title
- the selector's owned-check, once per scheduler cycle
- the request-side duplicate gate (HTTP 409 `duplicate_hash`)
- the declutter's verdict ladder, which will not archive on a title guess

`$.gamarr.crc` is stored and not yet matched on: the read paths take md5 and
sha1. It is free to compute in the same pass and No-Intro leads with crc32, so
it is kept for the join that will want it.

## Who writes them

| writer | keys |
|---|---|
| `internal/download/library.go` `importMetadata` | `$.gamarr.release.*` at import |
| `internal/db/libraryhash.go` `SaveLibraryHashes` | `$.gamarr.{crc,md5,sha1,unh,hashed_at}` |
| `internal/db/setmarker.go` `SaveSetMarker` | `$.gamarr.set` |
| `internal/db/dat.go` `SetLibraryCatalogStatus[ByID]` | `$.gamarr.catalog` |

The writers share the `$.gamarr` object, so every one of them patches the
leaves it owns rather than replacing the object.

## One key, one meaning

`$.gamarr.{md5,sha1}` used to hold the release hash. `migrateLibraryReleaseHashes`
moves the handful of rows that predate the split to `$.gamarr.release`, guarded
on the absence of `hashed_at` so it runs once and never touches a backfilled
row. The alternative — a discriminator field saying which sense a value is in —
would have made every reader ask a question the data should answer by itself.

## Skips

A row whose entry can never have a single-ROM identity is marked
`hash_skipped` and stops being enumerated: a directory (a disc set has no one
ROM), a multi-file archive, a `.rar` (no extractor in the image). Transient
trouble — a missing file, a full disk, an unreadable mount — is deliberately
*not* marked, because it is worth retrying.

That distinction is what lets "rows still needing a hash" reach zero. A count
that parks a permanent remainder reads as a stuck job.

## Who owns "what is held" (reversed 2026-08)

For its first year the library's ROM side was **mirrored from RomM**: a sync
pulled RomM's catalog into `library_items`, and "RomM = ownership truth" was
the working doctrine. That was a bootstrap-era convenience — the app had no
scanner and RomM had already identified the back catalog — and it quietly
made a sibling app this one's oracle.

The doctrine is **reversed**. An arr owns its own inventory: *what is held*
comes from the library scanner (`internal/libscan`) walking this app's own
root folders into this app's own DB. RomM remains a library **peer** — it
scans the same tree for its own consumers (shops, launchers, humans), imports
still notify it to rescan (the Connect plane), and the `$.romm` hashes it
wrote while the sync lived remain valid frozen data — but neither app is the
other's system of record. The analogy that settled it: Radarr does not ask
Plex what movies exist.
