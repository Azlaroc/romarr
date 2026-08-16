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

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/selection"
	"gamarr/internal/webhook"
)

// SearchFunc searches all sources for a query and platform, returning scored results.
type SearchFunc func(query, platformSlug string) []*models.SearchResult

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
	// The previous loop's channel was closed by StopLoop; every loop needs a
	// fresh one. In-flight cycles hold a snapshot of the OLD channel, which
	// stays closed — they wind down promptly, exactly like a shutdown.
	s.stopCh = make(chan struct{})
	s.loopLive = true
	go s.loop(s.stopCh)
	slog.Info("scheduler started", "interval_hours", s.cfg.SchedulerIntervalHrs())
	return true
}

// StopLoop halts the periodic loop AND interrupts any in-flight cycle
// (including a manual RunNow on a never-started loop — cycles snapshot the
// current channel, so closing it always reaches them). Status keeps working
// and cycle counters survive, so a re-enable doesn't zero the history.
func (s *Scheduler) StopLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stopCh:
		// Already closed by a previous stop.
	default:
		close(s.stopCh)
	}
	s.loopLive = false
}

// Stop halts the scheduler (shutdown path).
func (s *Scheduler) Stop() { s.StopLoop() }

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
	if len(wishlist) == 0 {
		slog.Info("scheduler: wishlist empty, nothing to do")
		s.mu.Lock()
		s.lastRun = time.Now()
		s.lastResults = 0
		s.autoDownloads += repairGrabs
		s.mu.Unlock()
		return
	}

	totalResults := 0
	autoDownloads := repairGrabs
	minScore := s.cfg.MinScore()

	var owned func(title, platformSlug string) *db.LibraryItem
	var ownedByHash func(md5, sha1 string) *db.LibraryItem
	if mode == "enforce" {
		// One library snapshot per cycle: parsing 20k+ titles per wishlist
		// item would be wasteful, and a cycle-stale index is fine — newly
		// imported titles are caught by the ActiveGrab jobs check instead.
		owned = s.buildOwnedIndex()
		ownedByHash = s.buildHashIndex()
	}

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

		results := s.searchFn(item.Title, item.PlatformSlug)
		if len(results) == 0 {
			continue
		}

		// Sort by score descending
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})

		totalResults += len(results)

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
		if mode == "off" {
			autoDownloads += s.legacyGrab(item, results, minScore)
			continue
		}

		opts := selection.SelectOpts{
			Query:        item.Title,
			PlatformSlug: item.PlatformSlug,
			MinScore:     minScore,
			Profile:      s.jobs.ResolveQualityProfile(item.PlatformSlug),
		}
		if mode == "enforce" {
			opts.Owned = owned
			opts.ActiveGrab = s.activeGrab
			opts.OwnedByHash = ownedByHash
		}
		dec := selection.Select(results, opts)

		chosen := ""
		if len(dec.Grabs) > 0 {
			chosen = dec.Grabs[0].Result.Title
		}
		legacy := ""
		if len(results) > 0 {
			legacy = results[0].Title
		}
		slog.Info("selector_decision",
			"mode", mode, "wishlist_title", item.Title,
			"action", dec.Action.String(), "chosen", chosen, "grabs", len(dec.Grabs),
			"reason", dec.Reason, "rejected", len(dec.Rejected), "legacy_pick", legacy)
		s.jobs.LogActivity("selector_decision", item.Title,
			fmt.Sprintf("[%s] %s: %s (grabs=%d, rejected=%d; legacy pick: %s)",
				mode, dec.Action.String(), dec.Reason, len(dec.Grabs), len(dec.Rejected), legacy), "", nil)

		if mode == "shadow" {
			autoDownloads += s.legacyGrab(item, results, minScore)
			continue
		}

		// enforce
		switch dec.Action {
		case selection.ActionSkip:
			if dec.Reason == "owned" {
				// Owned means fulfilled — this is where the wishlist row's
				// life ends under enforce (not at grab time).
				s.jobs.DeleteWishlistItem(item.ID)
				s.jobs.LogActivity("wishlist_fulfilled", item.Title,
					"In library — removed from wishlist", "", nil)
			}
		case selection.ActionGrab, selection.ActionGrabSet:
			if !s.cfg.AutoDownload() {
				continue
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
				continue
			}
			autoDownloads += grabbed
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
			// Wishlist row intentionally NOT deleted here — see mode comment.
		}
	}

	s.mu.Lock()
	s.lastRun = time.Now()
	s.lastResults = totalResults
	s.autoDownloads += autoDownloads
	s.mu.Unlock()

	slog.Info("scheduler: completed", "wishlist_items", len(wishlist),
		"results", totalResults, "auto_downloads", autoDownloads)
}

// legacyGrab is the pre-F4 top-pick block, kept verbatim for off mode and as
// shadow mode's executor: grab results[0] when it clears the score bar, log,
// webhook, and delete the wishlist row at grab time. Returns grabs made (0/1).
func (s *Scheduler) legacyGrab(item db.WishlistItem, results []*models.SearchResult, minScore int) int {
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

	// Remove from wishlist after successful download
	s.jobs.DeleteWishlistItem(item.ID)
	return 1
}

// ownershipKeys returns the lowered comparison keys a title matches under:
// the raw title, the parsed CleanTitle (classified tags stripped), and the
// BareTitle (ALL tags stripped). Raw and clean keep exact titles exact; bare
// is what lets a wishlist "Kirby's Dream Land 2" meet a library row
// "Kirby's Dream Land 2 (USA, Europe) (SGB Enhanced)" or a Vimm release
// carrying a trailing "(GB)" system tag.
func ownershipKeys(title string) []string {
	keys := make([]string, 0, 3)
	add := func(k string) {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			return
		}
		for _, have := range keys {
			if have == k {
				return
			}
		}
		keys = append(keys, k)
	}
	add(title)
	add(selection.Parse(title).CleanTitle)
	add(selection.BareTitle(title))
	return keys
}

// buildOwnedIndex snapshots the library into an ownership lookup. It indexes
// the GetAllLibraryTitles map KEYS — which carry both the stored titles and
// the RomM search_keys — under all ownershipKeys variants, so lookups match
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
		for _, k := range ownershipKeys(titleish) {
			idx[k+slugSuffix] = it
		}
	}
	return func(title, platformSlug string) *db.LibraryItem {
		for _, k := range ownershipKeys(title) {
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
	for _, k := range ownershipKeys(title) {
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
		for _, k := range ownershipKeys(jobTitle) {
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
