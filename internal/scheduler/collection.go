package scheduler

import (
	"log/slog"
	"time"

	"gamarr/internal/collectionsvc"
	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// Collection mode's half of a scheduler cycle.
//
// The wishlist is what a person asked for by name; a collection target is what
// a platform's 1G1R set implies. They are different queues on purpose — one
// platform's set can imply hundreds of rows and would drown a hand-made list —
// but they run through ONE pipeline, processWanted, so a gap is acquired under
// exactly the policy a wishlist row would have been.

// SetCollections attaches the collection plane. A nil service leaves the fill
// half switched off, which is what a test scheduler and a pre-migration
// install both want.
func (s *Scheduler) SetCollections(svc *collectionsvc.Service) {
	s.collections = svc
}

// hasCollectionWork reports whether any platform is monitored AND acquirable.
// It is what keeps an idle cycle idle now that an empty wishlist no longer
// ends one.
func (s *Scheduler) hasCollectionWork() bool {
	return s.collections != nil && len(s.collections.AcquirablePlatforms()) > 0
}

// fillCollections refreshes every collection-mode platform's gap list, then
// searches as many due gaps as the cycle's budget allows.
//
// The budget is global rather than per platform: it bounds what one cycle asks
// of the indexers, and splitting it per platform would multiply that ask by
// however many platforms are switched on.
func (s *Scheduler) fillCollections(stop chan struct{}, cx *cycleCtx) int {
	if s.collections == nil {
		return 0
	}
	s.collections.SyncAll()

	budget := s.cfg.CollectionFill()
	if budget <= 0 {
		return 0
	}
	platforms := s.collections.AcquirablePlatforms()
	if len(platforms) == 0 {
		return 0
	}

	grabs, searched := 0, 0
	for _, slug := range platforms {
		if budget <= 0 {
			break
		}
		// Re-checked per platform rather than trusted from the list: a cycle
		// can be long, and a switch flipped mid-cycle should take effect.
		if !platform.AcquisitionEnabled(slug) {
			continue
		}
		targets := s.jobs.DueCollectionTargets(slug, budget, time.Now().UTC())
		for _, t := range targets {
			select {
			case <-stop:
				return grabs
			case <-time.After(s.rateLimit):
			}
			out := s.processWanted(wantedItem{
				Title: t.Title, PlatformSlug: t.PlatformSlug,
				DumpName: t.DumpName, DumpHashes: t.DumpHashes,
			}, cx)
			searched++
			budget--
			s.recordTarget(t, out)
			grabs += out.Grabs
			if budget <= 0 {
				break
			}
		}
	}
	if searched > 0 {
		slog.Info("collection: fill pass", "platforms", len(platforms),
			"searched", searched, "grabs", grabs)
	}
	return grabs
}

// recordTarget writes what one attempt did.
//
// A gap that turned out to be owned is deleted immediately rather than left
// for the next sync: the sync would drop it anyway, and leaving it lets one
// more cycle search for something already on disk. Everything else records the
// attempt, which is what backs the queue off — a title no source carries must
// stop being searched every hour, and the reason it stopped has to be readable.
func (s *Scheduler) recordTarget(t db.CollectionTarget, out wantedOutcome) {
	switch {
	case out.Fulfilled:
		s.jobs.DeleteCollectionTarget(t.ID)
		s.jobs.LogActivity("collection_owned", t.Title,
			"Already in library — removed from "+t.PlatformSlug+" gap list", "", nil)
	case out.Grabs > 0:
		s.jobs.RecordCollectionAttempt(t.ID, db.TargetGrabbed, reasonOr(out.Reason, "grabbed"))
		s.jobs.LogActivity("collection_grab", t.Title,
			"Collection mode grabbed a release for "+t.PlatformSlug, "", nil)
	default:
		s.jobs.RecordCollectionAttempt(t.ID, db.TargetUnavailable, reasonOr(out.Reason, "nothing usable found"))
	}
}

func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}
