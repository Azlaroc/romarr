package collectionsvc

import (
	"log/slog"

	"gamarr/internal/collection"
	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// Turning a set's gaps into a work queue.
//
// The set is derived; the queue is stored. That split is the whole point: a
// gap needs memory (how often it has been tried, what came back, when to try
// again) while the set itself must never be a second copy of the catalog.

// SyncResult is what one platform's sync did.
type SyncResult struct {
	Platform string            `json:"platform"`
	Added    int               `json:"added"`
	Removed  int               `json:"removed"`
	Counts   collection.Counts `json:"counts"`
}

// SyncTargets reconciles one platform and writes its gap list.
//
// It syncs whatever platform it is asked about, including one with collection
// mode off — the caller decides who is in scope, and an operator asking for a
// preview of a platform they have not switched on yet is a reasonable thing to
// want.
func (c *Cycle) SyncTargets(slug string) SyncResult {
	res := c.Set(slug)
	gaps := make([]db.CollectionGap, 0, res.Counts.Gaps)
	for _, e := range res.Entries {
		if e.Status != collection.StatusGap {
			continue
		}
		keeper, ok := e.Keeper()
		if !ok {
			continue
		}
		gaps = append(gaps, db.CollectionGap{
			SetKey: e.Key, Title: e.Title, DumpName: keeper.Name,
		})
	}
	added, removed := c.svc.store.SyncCollectionTargets(slug, gaps)
	return SyncResult{Platform: slug, Added: added, Removed: removed, Counts: res.Counts}
}

// SyncAll refreshes the gap list for every platform in collection mode, and
// clears the list of any platform that has left it.
//
// Clearing matters as much as syncing: targets are derived from a policy, so a
// platform whose switch went off must stop generating work immediately rather
// than at whatever future point someone notices.
func (s *Service) SyncAll() []SyncResult {
	rows := s.store.PlatformRows()
	if len(rows) == 0 {
		return nil
	}
	var cycle *Cycle
	var out []SyncResult
	for _, p := range rows {
		if !p.CollectionMode {
			if n := s.store.ClearCollectionTargets(p.Slug); n > 0 {
				slog.Info("collection: cleared targets for a platform no longer in collection mode",
					"platform", p.Slug, "targets", n)
			}
			continue
		}
		// Built lazily: an install with collection mode off everywhere must not
		// pay for a library snapshot every cycle.
		if cycle == nil {
			cycle = s.NewCycle()
		}
		res := cycle.SyncTargets(p.Slug)
		out = append(out, res)
		if res.Added > 0 || res.Removed > 0 {
			slog.Info("collection: gap list synced", "platform", p.Slug,
				"added", res.Added, "removed", res.Removed,
				"gaps", res.Counts.Gaps, "owned", res.Counts.Owned, "set", res.Counts.Groups)
		}
	}
	return out
}

// CollectionPlatforms lists the platforms collection mode is on for, in the
// order the scheduler should work through them.
func (s *Service) CollectionPlatforms() []string {
	var out []string
	for _, p := range s.store.PlatformRows() {
		if p.CollectionMode && !p.IsSystem {
			out = append(out, p.Slug)
		}
	}
	return out
}

// AcquirablePlatforms narrows that to the ones whose acquisition switch is also
// on. Collection mode says WHAT is wanted; acquisition says whether RomArr may
// go and get it, and the two switches are deliberately independent — an
// operator can watch a platform's gaps fill up on screen without RomArr acting.
func (s *Service) AcquirablePlatforms() []string {
	var out []string
	for _, slug := range s.CollectionPlatforms() {
		if platform.AcquisitionEnabled(slug) {
			out = append(out, slug)
		}
	}
	return out
}
