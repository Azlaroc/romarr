# Quality profiles

A profile is the selection policy for one title: which regions and formats to
prefer, what release classes to allow, how to rank sources, and what sizes are
plausible. This document is about *whose* policy applies to *what* — the part
that changed.

## What it used to be

A profile carried a `platform_slug`, and a unique index enforced one profile
per platform. That model has two holes:

- **"I want the Japanese revision of this one game" has nowhere to live.** The
  platform's profile is the only profile, so per-title intent cannot be
  expressed at all.
- **Adding a platform is a setup step.** Before a title on a new platform can
  be acquired sensibly, someone has to go make a profile for that platform.

## What it is now

Three moving parts, each in the place that owns it:

| Question | Where the answer lives |
|---|---|
| What policy applies to *this title*? | `wishlist.profile_id` — 0 means "the platform's default" |
| What does *this platform* default to? | `platforms.default_profile_id` on the registry row |
| What does a *brand-new* platform default to? | a **template**, cloned on first add |

Resolution is one function, `ResolveProfileForItem`, and it never returns nil:

```
the title's own profile
  → the platform's default
    → the legacy platform_slug column   (installs whose link migration has not run)
      → the global default row
        → the lowest-id global row
          → the built-in default
```

The legacy step is a migration courtesy. Nothing writes `platform_slug` any
more; a one-shot migration moves each mapping onto the platform row, guarded
by a settings key rather than by "no platform has a default yet" — an operator
who clears a default deliberately must not have it resurrected on restart.

## Templates and lazy materialization

Two profiles ship flagged `is_template`: **Carts Default** and **Discs
Default**. They are ordinary profile rows, editable, and never used directly.

The first title added on a platform with no default clones the template
matching that platform's class, names it `<Platform> Default`, and attaches it
to the platform row. The operator is told once, in the add response, that a
profile now exists — rather than being asked to create one first.

**The class comes from the platform's DAT authority** where it has a lane:
No-Intro → carts, Redump → discs. That is the same assignment the catalog
already makes, so it is not a second opinion about what a platform is. The
registry's `media_class` covers platforms with no lane, and carts is the last
resort — the conservative shape, whole-file dumps and no disc formats.

Editing a template retunes what *future* platforms inherit; it does not touch
the profiles already materialized from it. That is the TRaSH model: the
opinionated defaults are data, not code.

## Consequences worth knowing

- **Two profiles may target the same platform.** "PSX CHD" and "PSX raw" are
  both legitimate; a title picks one. The old unique index is gone.
- **Deleting a profile a platform defaults to is refused** (409), naming the
  platforms that would be orphaned. Point them elsewhere first.
- **A profile deleted out from under a wishlist row falls back** to the
  platform default rather than failing the cycle.
- **A template cannot be chosen for a title.** Picking one would couple that
  title to every future platform's defaults.
- **The profile is resolved once per item per cycle**, and the same profile
  drives both the sources' region policy and the tier sort. It used to be
  resolved twice, from the platform only, and neither resolution could see a
  title's own intent.

## Region policy reaches the sources

A profile's `region_priority` is passed to the search fan-out, not just used
for ranking afterwards. This matters because the archive.org driver applies a
coarse non-English pre-filter: without the profile reaching it, a profile that
ranks `japan` could not see a Japanese dump **at all**, because the drop
happened where the selector could never observe it.

The rule the driver follows: it may skip work the caller will clearly reject,
but it must never drop a region the caller asked for. A driver-level filter
the profile cannot see turns a wanted release into a gap that reads as a short
catalog rather than as a filter — which is exactly how it would fail under
collection mode.
