// Package scheduler runs periodic wishlist searches, auto-grabbing matching
// releases and notifying webhooks on results.
package scheduler

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gamarr/internal/collectionsvc"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/selection"
	"gamarr/internal/webhook"
)

// SearchFunc searches all sources for a query and platform, returning scored
// results. The profile comes from the caller because it is a property of the
// TITLE being searched for, not of the platform: a wishlist row may carry its
// own, and the same profile has to drive both the sources' region policy and
// the tier sort. A nil profile means "resolve the platform default".
type SearchFunc func(query, platformSlug string, prof *db.QualityProfile) []*models.SearchResult

// DownloadFunc executes one selector grab and returns a job ID. Legacy
// (off/shadow) picks arrive as a bare Grab{Result: best}; enforce-mode grabs
// carry the selector's TargetFile and disc-set stamps.
type DownloadFunc func(g selection.Grab) (string, error)

// WebhookFunc returns enabled webhook configs for sending notifications.
type WebhookFunc func() []webhook.WebhookConfig

// Scheduler runs periodic wishlist searches.
type Scheduler struct {
	mu            sync.RWMutex
	cfg           *config.Config
	jobs          *db.JobStore
	searchFn      SearchFunc
	downloadFn    DownloadFunc
	webhookFn     WebhookFunc
	running       bool
	loopLive      bool // a loop() goroutine exists (distinct from a cycle running)
	lastRun       time.Time
	lastResults   int
	autoDownloads int
	stopCh        chan struct{}
	rateLimit     time.Duration // wait between wishlist searches
	// collections is the collection-mode plane. Nil leaves the fill half off.
	collections *collectionsvc.Service
}

// New creates a new Scheduler.
func New(cfg *config.Config, jobs *db.JobStore, searchFn SearchFunc, downloadFn DownloadFunc, webhookFn WebhookFunc) *Scheduler {
	return &Scheduler{
		cfg:        cfg,
		jobs:       jobs,
		searchFn:   searchFn,
		downloadFn: downloadFn,
		webhookFn:  webhookFn,
		stopCh:     make(chan struct{}),
		rateLimit:  2 * time.Second,
	}
}

// Start begins the scheduler loop if enabled (boot compatibility wrapper).
func (s *Scheduler) Start() {
	if !s.EnsureRunning() {
		slog.Info("scheduler disabled")
	}
}

// EnsureRunning starts the loop if the config wants it and none is live.
// Returns whether a loop is live afterwards. Safe to call repeatedly — this
// is the re-arm entry point for runtime settings changes.
func (s *Scheduler) EnsureRunning() bool {
	if !s.cfg.SchedulerOn() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loopLive {
		return true
	}
	// Reuse the current channel when it is open (the normal case — StopLoop
	// re-opens after a runtime disable, so pending RunNow cycles share the
	// same channel identity); replace it only if a shutdown Stop left it
	// closed.
	select {
	case <-s.stopCh:
		s.stopCh = make(chan struct{})
	default:
	}
	s.loopLive = true
	go s.loop(s.stopCh)
	slog.Info("scheduler started", "interval_hours", s.cfg.SchedulerIntervalHrs())
	return true
}

// StopLoop is the runtime disable: it halts the periodic loop, interrupts
// any in-flight cycle (cycles snapshot the current channel, so closing it
// always reaches them), then re-opens a fresh channel — manual RunNow keeps
// working while the loop is down, by design. Cycle counters survive.
func (s *Scheduler) StopLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeStopChLocked()
	s.stopCh = make(chan struct{})
	s.loopLive = false
}

// Stop is the shutdown path: like StopLoop but the channel STAYS closed, so
// any cycle started after shutdown begins aborts on entry.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeStopChLocked()
	s.loopLive = false
}

func (s *Scheduler) closeStopChLocked() {
	select {
	case <-s.stopCh:
		// Already closed.
	default:
		close(s.stopCh)
	}
}

// LoopRunning reports whether the periodic loop is live.
func (s *Scheduler) LoopRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loopLive
}

// stopChan returns the current loop's stop channel. Cycles snapshot it once
// at entry so a re-arm (which swaps the channel) reads never race.
func (s *Scheduler) stopChan() chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopCh
}

// RunNow triggers an immediate search cycle.
func (s *Scheduler) RunNow() {
	go s.run()
}

// Status returns the current scheduler status. "enabled" reports actual
// loop liveness, not the config flag — a runtime disable is visible here
// immediately.
func (s *Scheduler) Status() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]interface{}{
		"enabled":        s.loopLive,
		"interval_hours": s.cfg.SchedulerIntervalHrs(),
		"auto_download":  s.cfg.AutoDownload(),
		"min_score":      s.cfg.MinScore(),
		"selector_mode":  s.cfg.SelectorModeEffective(),
		"running":        s.running,
		"last_run":       s.lastRun.Format(time.RFC3339),
		"last_results":   s.lastResults,
		"auto_downloads": s.autoDownloads,
	}
}

// loop ticks at the interval read when the loop started. An interval change
// re-arms via StopLoop+EnsureRunning (a fresh goroutine, not a fresh
// Scheduler — cycle counters survive): with ticks this long, waiting for the
// next tick to re-read would delay the change by up to a full interval.
func (s *Scheduler) loop(stop chan struct{}) {
	ticker := time.NewTicker(time.Duration(s.cfg.SchedulerIntervalHrs()) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.run()
		case <-stop:
			return
		}
	}
}

func (s *Scheduler) run() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	slog.Info("scheduler: starting wishlist search")

	// Snapshot the stop channel once: a re-arm swaps s.stopCh, and an
	// in-flight cycle should keep honoring the channel it started under
	// (which StopLoop closed) — it winds down exactly like a shutdown.
	stop := s.stopChan()

	mode := s.cfg.SelectorModeEffective()

	// Degraded disc-set repair runs ahead of the wishlist loop: its work list
	// comes from library markers, not the wishlist — the degraded finalize
	// consumed the wishlist row, so by the time a set needs repair the
	// wishlist is typically empty.
	repairGrabs := s.repairDegradedSets(mode)

	wishlist := s.jobs.GetWishlist()
	// 🔴 An empty wishlist is NOT an empty cycle any more. Collection mode's
	// gaps come from a platform's set, not from the wishlist, and the install
	// this was built for runs with an empty wishlist most of the time — an
	// early return here would have meant the whole feature never ran.
	if len(wishlist) == 0 {
		slog.Info("scheduler: wishlist empty")
	}

	totalResults := 0
	autoDownloads := repairGrabs
	minScore := s.cfg.MinScore()
	skippedPlatforms := map[string]int{}

	var owned func(title, platformSlug string) *db.LibraryItem
	var ownedByHash func(md5, sha1 string) *db.LibraryItem
	// Guarded by "is there anything to measure": removing the empty-wishlist
	// early return would otherwise parse a 20k-title library every cycle on an
	// install with nothing wanted at all.
	if mode == "enforce" && (len(wishlist) > 0 || s.hasCollectionWork()) {
		// One library snapshot per cycle: parsing 20k+ titles per wishlist
		// item would be wasteful, and a cycle-stale index is fine — newly
		// imported titles are caught by the ActiveGrab jobs check instead.
		owned = s.buildOwnedIndex()
		ownedByHash = s.buildHashIndex()
	}

	cx := &cycleCtx{mode: mode, minScore: minScore, owned: owned, ownedByHash: ownedByHash}

	for i, item := range wishlist {
		// Check for stop signal between items, and rate-limit before every
		// item after the first. Waiting at the top of the loop (rather than
		// sleeping at the bottom) applies the rate limit on every iteration,
		// including ones that end early via continue, and a Stop() call
		// interrupts the wait immediately.
		if i == 0 {
			select {
			case <-stop:
				return
			default:
			}
		} else {
			select {
			case <-stop:
				return
			case <-time.After(s.rateLimit):
			}
		}

		// A platform with acquisition turned off keeps its wishlist rows —
		// they are still wanted — but nothing is searched or grabbed for it.
		// The row survives so flipping the switch back resumes where it left
		// off rather than losing the request.
		if item.PlatformSlug != "" && !platform.AcquisitionEnabled(item.PlatformSlug) {
			skippedPlatforms[item.PlatformSlug]++
			continue
		}

		out := s.processWanted(wantedOf(item), cx)
		totalResults += out.Results
		autoDownloads += out.Grabs
		if out.Fulfilled {
			// Owned means fulfilled — this is where a wishlist row's life ends
			// under enforce (not at grab time).
			s.jobs.DeleteWishlistItem(item.ID)
			s.jobs.LogActivity("wishlist_fulfilled", item.Title,
				"In library — removed from wishlist", "", nil)
		}
	}

	// Collection mode runs after the wishlist: a title a person asked for by
	// name outranks one a policy implies, and both share the cycle's budget.
	autoDownloads += s.fillCollections(stop, cx)

	s.mu.Lock()
	s.lastRun = time.Now()
	s.lastResults = totalResults
	s.autoDownloads += autoDownloads
	s.mu.Unlock()

	if len(skippedPlatforms) > 0 {
		// Named, not silent: "nothing happened for this title" must be
		// traceable to the switch that caused it.
		slog.Info("scheduler: skipped platforms with acquisition off", "platforms", skippedPlatforms)
	}

	slog.Info("scheduler: completed", "wishlist_items", len(wishlist),
		"results", totalResults, "auto_downloads", autoDownloads)
}

// wantedItem is one thing the scheduler is trying to acquire, whoever asked
// for it: a wishlist row a person added by name, or a gap a platform's 1G1R
// set implies. Both feed ONE pipeline — search, select, grab — because the
// moment there are two they drift, which is the lesson the requests plane's
// duplicate pipeline already taught.
type wantedItem struct {
	Title        string
	PlatformSlug string
	ProfileID    int64
	// WishlistID is the row a person added, or 0 when the title is wanted
	// because a set implies it. Only legacy ("off") mode reads it: that mode
	// deletes the wishlist row at grab time, and a collection target has no
	// row to delete.
	WishlistID int64
}

func wantedOf(item db.WishlistItem) wantedItem {
	return wantedItem{
		Title: item.Title, PlatformSlug: item.PlatformSlug,
		ProfileID: item.ProfileID, WishlistID: item.ID,
	}
}

// wantedOutcome is what one pass over one wanted title did.
//
// The CALLER owns the bookkeeping: a wishlist row is deleted when fulfilled, a
// collection target is retired or backed off. Deciding that here would mean
// this function knowing which queue it was called from, which is exactly what
// it must not know.
type wantedOutcome struct {
	Results int
	Grabs   int
	// Fulfilled is true when the title turned out to be owned already.
	Fulfilled bool
	// Reason is the selector's verdict, or why nothing happened. It is what a
	// collection target records as its last attempt's result.
	Reason string
}

// cycleCtx is the per-cycle state every wanted title is measured against.
type cycleCtx struct {
	mode        string
	minScore    int
	owned       func(title, platformSlug string) *db.LibraryItem
	ownedByHash func(md5, sha1 string) *db.LibraryItem
}

// processWanted runs one title through search, selection and grabbing.
func (s *Scheduler) processWanted(item wantedItem, cx *cycleCtx) wantedOutcome {
	var out wantedOutcome

	// One resolution per item, honouring the row's own profile override.
	prof := s.jobs.ResolveProfileForItem(item.ProfileID, item.PlatformSlug)

	results := s.searchFn(item.Title, item.PlatformSlug, prof)
	if len(results) == 0 {
		out.Reason = "no results"
		return out
	}

	// Sort by score descending
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	out.Results = len(results)

	// F4 selector (SELECTOR_MODE):
	//   off     — pre-F4 behavior exactly: top score >= min grabs, and the
	//             wishlist row is deleted at grab time.
	//   shadow  — legacy pick still drives the grab; the selector runs
	//             alongside and its decision is logged next to the legacy
	//             pick (the live diff channel).
	//   enforce — the selector's Decision drives the grab: skip reasons
	//             honored, disc sets dispatched as stamped members, and
	//             the wishlist row survives until a later cycle's Owned
	//             check sees the title in the library (delete-at-grab
	//             lost rows when an async torrent submit failed).
	if cx.mode == "off" {
		out.Grabs = s.legacyGrab(item, results, cx.minScore)
		return out
	}

	opts := selection.SelectOpts{
		Query:        item.Title,
		PlatformSlug: item.PlatformSlug,
		MinScore:     cx.minScore,
		Profile:      prof,
		Collection:   s.jobs.ResolveCollectionProfile(item.PlatformSlug),
	}
	if cx.mode == "enforce" {
		opts.Owned = cx.owned
		opts.ActiveGrab = s.activeGrab
		opts.OwnedByHash = cx.ownedByHash
	}
	dec := selection.Select(results, opts)
	out.Reason = dec.Reason

	chosen := ""
	if len(dec.Grabs) > 0 {
		chosen = dec.Grabs[0].Result.Title
	}
	legacy := ""
	if len(results) > 0 {
		legacy = results[0].Title
	}
	slog.Info("selector_decision",
		"mode", cx.mode, "wishlist_title", item.Title,
		"action", dec.Action.String(), "chosen", chosen, "grabs", len(dec.Grabs),
		"reason", dec.Reason, "rejected", len(dec.Rejected), "legacy_pick", legacy)
	s.jobs.LogActivity("selector_decision", item.Title,
		fmt.Sprintf("[%s] %s: %s (grabs=%d, rejected=%d; legacy pick: %s)",
			cx.mode, dec.Action.String(), dec.Reason, len(dec.Grabs), len(dec.Rejected), legacy), "", nil)
	// One line per discarded candidate. The count alone says a search
	// found nothing usable without saying why, which is how a filter
	// rejecting an entire platform stayed invisible for months. The
	// activity feed keeps the summary — per-candidate rows would drown
	// it — so this lives in the logs.
	for _, rej := range dec.Rejected {
		slog.Info("selector_rejected", "mode", cx.mode, "wishlist_title", item.Title,
			"platform", item.PlatformSlug, "candidate", rej.Title, "reason", rej.Reason)
	}

	if cx.mode == "shadow" {
		out.Grabs = s.legacyGrab(item, results, cx.minScore)
		return out
	}

	// enforce
	switch dec.Action {
	case selection.ActionSkip:
		// Owned is the one skip the caller acts on: a wishlist row is
		// fulfilled, a collection target is no longer a gap.
		out.Fulfilled = dec.Reason == "owned"
	case selection.ActionGrab, selection.ActionGrabSet:
		if !s.cfg.AutoDownload() {
			out.Reason = "auto-download off"
			return out
		}
		grabbed := 0
		for _, g := range dec.Grabs {
			jobID, err := s.downloadFn(g)
			if err != nil {
				// A partially-dispatched set is tolerable: the disc-set
				// sweep degrades it if the missing member never lands.
				slog.Warn("scheduler: download failed", "title", g.Result.Title, "error", err)
				continue
			}
			grabbed++
			s.jobs.LogActivity("scheduler_download", item.Title,
				"Auto-downloaded from wishlist search: "+g.Result.Title, jobID, nil)
		}
		if grabbed == 0 {
			out.Reason = "every dispatch failed"
			return out
		}
		out.Grabs = grabbed
		if s.webhookFn != nil {
			msg := "Selector grabbed: " + chosen + " (score: " + itoa(dec.Grabs[0].Result.Score) + ")"
			if dec.Action == selection.ActionGrabSet {
				msg = fmt.Sprintf("Selector grabbed disc set: %s (%d discs)", dec.Grabs[0].SetDir, grabbed)
			}
			webhook.Send(s.webhookFn(), webhook.Payload{
				Event:    webhook.EventSchedulerMatch,
				Title:    item.Title,
				Platform: dec.Grabs[0].Result.Platform,
				Status:   "downloading",
				Message:  msg,
			})
		}
		// A wishlist row is intentionally NOT deleted on a grab — see the
		// mode comment above.
	}
	return out
}

// legacyGrab is the pre-F4 top-pick block, kept verbatim for off mode and as
// shadow mode's executor: grab results[0] when it clears the score bar, log,
// webhook, and delete the wishlist row at grab time. Returns grabs made (0/1).
func (s *Scheduler) legacyGrab(item wantedItem, results []*models.SearchResult, minScore int) int {
	if !s.cfg.AutoDownload() || len(results) == 0 || results[0].Score < minScore {
		return 0
	}
	best := results[0]
	slog.Info("scheduler: auto-downloading", "title", best.Title, "score", best.Score,
		"wishlist_title", item.Title, "platform", best.Platform)

	jobID, err := s.downloadFn(selection.Grab{Result: best})
	if err != nil {
		slog.Warn("scheduler: download failed", "title", best.Title, "error", err)
		return 0
	}

	s.jobs.LogActivity("scheduler_download", item.Title,
		"Auto-downloaded from wishlist search: "+best.Title, jobID, nil)

	if s.webhookFn != nil {
		webhook.Send(s.webhookFn(), webhook.Payload{
			Event:    webhook.EventSchedulerMatch,
			Title:    item.Title,
			Platform: best.Platform,
			Status:   "downloading",
			Message:  "Scheduler found match: " + best.Title + " (score: " + itoa(best.Score) + ")",
		})
	}

	// Remove from wishlist after successful download. A title wanted because
	// a set implies it has no wishlist row: its bookkeeping is the caller's.
	if item.WishlistID > 0 {
		s.jobs.DeleteWishlistItem(item.WishlistID)
	}
	return 1
}

// buildOwnedIndex snapshots the library into an ownership lookup. It indexes
// the GetAllLibraryTitles map KEYS — which carry both the stored titles and
// the RomM search_keys — under all OwnershipKeys variants, so lookups match
// regardless of which side carries the No-Intro/Vimm tags.
func (s *Scheduler) buildOwnedIndex() func(title, platformSlug string) *db.LibraryItem {
	all := s.jobs.GetAllLibraryTitles()
	idx := make(map[string]*db.LibraryItem, len(all)*2)
	for key, it := range all {
		cut := strings.LastIndex(key, "|")
		if cut < 0 {
			continue
		}
		titleish, slugSuffix := key[:cut], key[cut:]
		for _, k := range selection.OwnershipKeys(titleish) {
			idx[k+slugSuffix] = it
		}
	}
	return func(title, platformSlug string) *db.LibraryItem {
		for _, k := range selection.OwnershipKeys(title) {
			if it := idx[k+"|"+platformSlug]; it != nil {
				return it
			}
		}
		return nil
	}
}

// buildHashIndex snapshots the library's stored hash identities once per
// cycle for the selector's OwnedByHash seam. The Decision reason stays the
// title-check's "owned" (same wishlist-fulfilled handling); the log line is
// what distinguishes a hash skip from a title skip.
func (s *Scheduler) buildHashIndex() func(md5, sha1 string) *db.LibraryItem {
	idx := s.jobs.LibraryHashIndex()
	return func(md5, sha1 string) *db.LibraryItem {
		if v := strings.ToLower(strings.TrimSpace(md5)); v != "" {
			if it := idx["md5:"+v]; it != nil {
				slog.Info("scheduler: winner hash-owned", "md5", v, "library_id", it.ID, "owned_title", it.Title)
				return it
			}
		}
		if v := strings.ToLower(strings.TrimSpace(sha1)); v != "" {
			if it := idx["sha1:"+v]; it != nil {
				slog.Info("scheduler: winner hash-owned", "sha1", v, "library_id", it.ID, "owned_title", it.Title)
				return it
			}
		}
		return nil
	}
}

// activeGrab reports an in-flight grab for the title: any non-terminal job —
// including a completed disc-set member whose set has not finalized — whose
// release title resolves to the same game on the same platform. This is what
// keeps enforce mode's surviving wishlist rows from re-grabbing every cycle
// while a download or set is still converging.
func (s *Scheduler) activeGrab(title, platformSlug string) bool {
	want := map[string]bool{}
	for _, k := range selection.OwnershipKeys(title) {
		want[k] = true
	}
	for _, item := range s.jobs.Items() {
		slug, _ := item.Data["platform_slug"].(string)
		if slug != platformSlug {
			continue
		}
		jobTitle, _ := item.Data["title"].(string)
		if jobTitle == "" || !jobInFlight(item.Data) {
			continue
		}
		for _, k := range selection.OwnershipKeys(jobTitle) {
			if want[k] {
				return true
			}
		}
	}
	return false
}

// jobInFlight reports whether a job blob still represents work in progress.
// Terminal failures don't block a re-grab; a completed disc-set member does
// until its set finalizes (the set's library row doesn't exist yet).
func jobInFlight(data map[string]interface{}) bool {
	status, _ := data["status"].(string)
	switch status {
	case "", "error", "interrupted", "completed_unorganized":
		return false
	case "completed":
		if id, _ := data["disc_set_id"].(string); id != "" {
			fin, _ := data["set_finalized"].(bool)
			return !fin
		}
		return false
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
