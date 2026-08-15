package scheduler

import (
	"strings"
	"testing"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/selection"
)

func enforceCfg() *config.Config {
	return &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "enforce"}
}

// seedDegradedSet adds a degraded disc-set library row with its marker, the
// way finalizeDiscSet leaves one behind. Returns the row id.
func seedDegradedSet(t *testing.T, store *db.JobStore, setID string, total int, have []int, attempts int) int64 {
	t.Helper()
	id, err := store.AddLibraryItem(&db.LibraryItem{
		Title:        "Final Fantasy VII (USA)",
		Platform:     "PS1",
		PlatformSlug: "psx",
		FilePath:     "/roms/psx/Final Fantasy VII (USA)",
		Source:       "ddl",
		SourceType:   "ddl",
		SourceID:     "set:" + setID,
		Metadata:     "{}",
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	if err := store.SaveSetMarker(id, db.SetMarker{
		ID: setID, Total: total, Have: have, Degraded: true,
		RepairAttempts: attempts, DegradedAt: "2026-08-15T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveSetMarker: %v", err)
	}
	return id
}

func markerOf(t *testing.T, store *db.JobStore, id int64) db.SetMarker {
	t.Helper()
	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem: %v", err)
	}
	mk, ok := db.ParseSetMarker(item.Metadata)
	if !ok {
		t.Fatalf("marker missing: %q", item.Metadata)
	}
	return mk
}

func ff7Discs() []*models.SearchResult {
	return prep(90, "ddl",
		"Final Fantasy VII (USA) (Disc 1).zip",
		"Final Fantasy VII (USA) (Disc 2).zip",
		"Final Fantasy VII (USA) (Disc 3).zip")
}

func TestRepairGrabsOnlyMissingDiscIntoExistingSet(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	// Empty wishlist on purpose: the repair pass must run before the
	// empty-wishlist early return.
	rowID := seedDegradedSet(t, store, "set-r1", 3, []int{1, 3}, 0)

	var grabs []selection.Grab
	downloadFn := func(g selection.Grab) (string, error) {
		grabs = append(grabs, g)
		return "job-r1", nil
	}
	searched := 0
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		searched++
		if q != "Final Fantasy VII" || p != "psx" {
			t.Errorf("search args = %q/%q", q, p)
		}
		return ff7Discs()
	}, downloadFn, nil)
	s.rateLimit = 0
	s.run()

	if searched != 1 {
		t.Fatalf("searches = %d, want 1", searched)
	}
	if len(grabs) != 1 {
		t.Fatalf("grabs = %d, want only the missing disc", len(grabs))
	}
	g := grabs[0]
	if g.DiscSetID != "set-r1" || g.DiscIndex != 2 || g.DiscTotal != 3 ||
		g.SetDir != "Final Fantasy VII (USA)" {
		t.Errorf("grab stamps = %+v, want existing set identity", g)
	}
	// Phantom done-members for the purged imported discs, none for disc 2.
	for _, idx := range []int{1, 3} {
		job, ok := store.Get("repair-set-r1-d" + itoa(idx))
		if !ok {
			t.Fatalf("phantom for disc %d missing", idx)
		}
		if done, _ := job["set_member_done"].(bool); !done {
			t.Errorf("phantom disc %d not marked done", idx)
		}
		if fin, _ := job["set_finalized"].(bool); fin {
			t.Errorf("phantom disc %d finalized at reopen", idx)
		}
	}
	if _, ok := store.Get("repair-set-r1-d2"); ok {
		t.Error("phantom minted for the disc being re-grabbed")
	}
	mk := markerOf(t, store, rowID)
	if mk.RepairAttempts != 1 || mk.Exhausted || !mk.Degraded {
		t.Errorf("marker = %+v, want attempts=1 degraded", mk)
	}
	if s.Status()["auto_downloads"] != 1 {
		t.Errorf("auto_downloads = %v, want 1", s.Status()["auto_downloads"])
	}
}

func TestRepairSkipsWhenSetInFlight(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	rowID := seedDegradedSet(t, store, "set-r2", 2, []int{1}, 0)
	// A member still downloading (e.g. the timeout degrade arm fired over it).
	store.Set("member-slow", map[string]interface{}{
		"status": "downloading", "title": "Final Fantasy VII (USA) (Disc 2)",
		"platform_slug": "psx", "disc_set_id": "set-r2", "disc_index": 2,
		"disc_total": 2, "set_dir": "Final Fantasy VII (USA)",
	})

	searched := 0
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		searched++
		return ff7Discs()
	}, noopDownload, nil)
	s.rateLimit = 0
	s.run()

	if searched != 0 {
		t.Fatalf("searched %d times, want 0 while a member is in flight", searched)
	}
	if mk := markerOf(t, store, rowID); mk.RepairAttempts != 0 {
		t.Errorf("attempts = %d, want unchanged 0", mk.RepairAttempts)
	}
}

func TestRepairReopensSurvivorsWithoutPhantomDuplicates(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	seedDegradedSet(t, store, "set-r3", 2, []int{1}, 0)
	// Surviving finalized members from the degraded finalize: disc 1 done,
	// disc 2 terminal.
	store.Set("survivor-d1", map[string]interface{}{
		"status": "completed", "title": "Final Fantasy VII (USA) (Disc 1)",
		"platform_slug": "psx", "disc_set_id": "set-r3", "disc_index": 1,
		"disc_total": 2, "set_dir": "Final Fantasy VII (USA)",
		"set_member_done": true, "set_finalized": true, "set_started_at": 1000,
	})
	store.Set("dead-d2", map[string]interface{}{
		"status": "error", "title": "Final Fantasy VII (USA) (Disc 2)",
		"platform_slug": "psx", "disc_set_id": "set-r3", "disc_index": 2,
		"disc_total": 2, "set_dir": "Final Fantasy VII (USA)",
		"set_finalized": true, "set_started_at": 1000,
	})

	var grabs []selection.Grab
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		return ff7Discs()
	}, func(g selection.Grab) (string, error) {
		grabs = append(grabs, g)
		return "job-r3", nil
	}, nil)
	s.rateLimit = 0
	s.run()

	if len(grabs) != 1 || grabs[0].DiscIndex != 2 {
		t.Fatalf("grabs = %+v, want disc 2 only", grabs)
	}
	if _, ok := store.Get("repair-set-r3-d1"); ok {
		t.Error("phantom minted although a surviving done member exists")
	}
	for _, jobID := range []string{"survivor-d1", "dead-d2"} {
		job, _ := store.Get(jobID)
		if fin, _ := job["set_finalized"].(bool); fin {
			t.Errorf("%s latch not cleared", jobID)
		}
		if jobInt(job["set_started_at"]) <= 1000 {
			t.Errorf("%s set_started_at not refreshed", jobID)
		}
	}
}

func TestRepairNoCandidatesBumpsAttemptsKeepsSetClosed(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	rowID := seedDegradedSet(t, store, "set-r4", 2, []int{1}, 0)
	store.Set("survivor-d1", map[string]interface{}{
		"status": "completed", "title": "Final Fantasy VII (USA) (Disc 1)",
		"platform_slug": "psx", "disc_set_id": "set-r4", "disc_index": 1,
		"disc_total": 2, "set_dir": "Final Fantasy VII (USA)",
		"set_member_done": true, "set_finalized": true, "set_started_at": 1000,
	})

	s := New(enforceCfg(), store, noopSearch, func(g selection.Grab) (string, error) {
		t.Error("download dispatched with no candidates")
		return "", nil
	}, nil)
	s.rateLimit = 0
	s.run()

	if mk := markerOf(t, store, rowID); mk.RepairAttempts != 1 {
		t.Errorf("attempts = %d, want 1 (no-candidate cycles cost an attempt)", mk.RepairAttempts)
	}
	job, _ := store.Get("survivor-d1")
	if fin, _ := job["set_finalized"].(bool); !fin {
		t.Error("set reopened although nothing was dispatched")
	}
	if _, ok := store.Get("repair-set-r4-d1"); ok {
		t.Error("phantom minted although nothing was dispatched")
	}
}

func TestRepairAttemptCapLatchesExhaustedOnce(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	rowID := seedDegradedSet(t, store, "set-r5", 2, []int{1}, maxSetRepairAttempts)

	searched := 0
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		searched++
		return nil
	}, noopDownload, nil)
	s.rateLimit = 0
	s.run()
	s.run()

	if searched != 0 {
		t.Fatalf("searched %d times, want 0 at the attempt cap", searched)
	}
	mk := markerOf(t, store, rowID)
	if !mk.Exhausted || mk.RepairAttempts != maxSetRepairAttempts {
		t.Errorf("marker = %+v, want exhausted at %d attempts", mk, maxSetRepairAttempts)
	}
	exhaustedEvents := 0
	activity, _ := store.GetActivity(1, 50)
	for _, a := range activity {
		if a.EventType == "repair_exhausted" {
			exhaustedEvents++
		}
	}
	if exhaustedEvents != 1 {
		t.Errorf("repair_exhausted events = %d, want exactly 1 across two runs", exhaustedEvents)
	}
}

func TestRepairWholeSetHealsByReopenOnly(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	seedDegradedSet(t, store, "set-r6", 2, []int{1}, 0)
	// The straggler landed AFTER the timeout degrade: done member on disk,
	// not in the marker. Repair must reopen for re-finalize, not re-grab.
	store.Set("straggler-d2", map[string]interface{}{
		"status": "completed", "title": "Final Fantasy VII (USA) (Disc 2)",
		"platform_slug": "psx", "disc_set_id": "set-r6", "disc_index": 2,
		"disc_total": 2, "set_dir": "Final Fantasy VII (USA)",
		"set_member_done": true, "set_finalized": true, "set_started_at": 1000,
	})

	searched := 0
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		searched++
		return ff7Discs()
	}, func(g selection.Grab) (string, error) {
		t.Errorf("re-grabbed disc %d although it already landed", g.DiscIndex)
		return "", nil
	}, nil)
	s.rateLimit = 0
	s.run()

	if searched != 0 {
		t.Errorf("searched %d times, want 0 for a whole set", searched)
	}
	job, _ := store.Get("straggler-d2")
	if fin, _ := job["set_finalized"].(bool); fin {
		t.Error("whole set not reopened for re-finalize")
	}
	if _, ok := store.Get("repair-set-r6-d1"); !ok {
		t.Error("phantom for the marker-recorded disc 1 missing")
	}
}

func TestRepairNoopOutsideEnforceOrWithoutAutoDownload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{"shadow", &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "shadow"}},
		{"no-auto-download", &config.Config{SchedulerAutoDownload: false, SchedulerMinScore: 70, SelectorMode: "enforce"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			rowID := seedDegradedSet(t, store, "set-r7", 2, []int{1}, 0)
			searched := 0
			s := New(tc.cfg, store, func(q, p string) []*models.SearchResult {
				searched++
				return nil
			}, noopDownload, nil)
			s.rateLimit = 0
			s.run()
			if searched != 0 {
				t.Errorf("searched %d times, want 0", searched)
			}
			if mk := markerOf(t, store, rowID); mk.RepairAttempts != 0 {
				t.Errorf("attempts = %d, want untouched", mk.RepairAttempts)
			}
		})
	}
}

func TestRepairDecisionLogged(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	seedDegradedSet(t, store, "set-r8", 3, []int{1, 3}, 0)
	s := New(enforceCfg(), store, func(q, p string) []*models.SearchResult {
		return ff7Discs()
	}, func(g selection.Grab) (string, error) { return "job-r8", nil }, nil)
	s.rateLimit = 0
	s.run()

	var decision, download string
	activity, _ := store.GetActivity(1, 50)
	for _, a := range activity {
		switch a.EventType {
		case "selector_decision":
			decision = a.Detail
		case "scheduler_download":
			download = a.Detail
		}
	}
	if !strings.Contains(decision, "[repair] grab_set") || !strings.Contains(decision, "want [2]") ||
		!strings.Contains(decision, "attempt 1/5") {
		t.Errorf("decision activity = %q", decision)
	}
	if !strings.Contains(download, "Repair re-grab:") {
		t.Errorf("download activity = %q", download)
	}
}
