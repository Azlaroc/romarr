package scheduler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/selection"
	"gamarr/internal/webhook"
)

func newTestStore(t *testing.T) *db.JobStore {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func noopSearch(query, platformSlug string) []*models.SearchResult { return nil }

func noopDownload(g selection.Grab) (string, error) { return "", nil }

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestNewAndStatusDefaults(t *testing.T) {
	cfg := &config.Config{
		SchedulerEnabled:       true,
		SchedulerIntervalHours: 12,
		SchedulerAutoDownload:  true,
		SchedulerMinScore:      80,
	}
	s := New(cfg, newTestStore(t), noopSearch, noopDownload, nil)

	status := s.Status()
	if status["enabled"] != true {
		t.Errorf("enabled = %v, want true", status["enabled"])
	}
	if status["interval_hours"] != 12 {
		t.Errorf("interval_hours = %v, want 12", status["interval_hours"])
	}
	if status["auto_download"] != true {
		t.Errorf("auto_download = %v, want true", status["auto_download"])
	}
	if status["min_score"] != 80 {
		t.Errorf("min_score = %v, want 80", status["min_score"])
	}
	if status["running"] != false {
		t.Errorf("running = %v, want false", status["running"])
	}
	if status["last_results"] != 0 {
		t.Errorf("last_results = %v, want 0", status["last_results"])
	}
	if status["auto_downloads"] != 0 {
		t.Errorf("auto_downloads = %v, want 0", status["auto_downloads"])
	}
}

func TestStartDisabled(t *testing.T) {
	cfg := &config.Config{SchedulerEnabled: false}
	s := New(cfg, newTestStore(t), noopSearch, noopDownload, nil)
	// Must return without launching the loop; nothing to observe beyond not hanging.
	s.Start()
	s.Stop()
}

func TestStartEnabledAndStop(t *testing.T) {
	// Interval 0 exercises the <1h fallback to 24h inside loop().
	cfg := &config.Config{SchedulerEnabled: true, SchedulerIntervalHours: 0}
	s := New(cfg, newTestStore(t), noopSearch, noopDownload, nil)
	s.Start()
	time.Sleep(20 * time.Millisecond) // let the loop goroutine start
	s.Stop()
	// Stop must be idempotent.
	s.Stop()
}

func TestRunEmptyWishlist(t *testing.T) {
	var searches int64
	searchFn := func(q, p string) []*models.SearchResult {
		atomic.AddInt64(&searches, 1)
		return nil
	}
	cfg := &config.Config{SchedulerEnabled: true, SchedulerAutoDownload: true}
	s := New(cfg, newTestStore(t), searchFn, noopDownload, nil)

	s.run()

	if got := atomic.LoadInt64(&searches); got != 0 {
		t.Errorf("searchFn called %d times for empty wishlist, want 0", got)
	}
	status := s.Status()
	if status["last_results"] != 0 {
		t.Errorf("last_results = %v, want 0", status["last_results"])
	}
	zeroTime := time.Time{}.Format(time.RFC3339)
	if status["last_run"] == zeroTime {
		t.Error("last_run was not updated")
	}
}

func TestRunNowAsync(t *testing.T) {
	cfg := &config.Config{}
	s := New(cfg, newTestStore(t), noopSearch, noopDownload, nil)

	before := time.Time{}.Format(time.RFC3339)
	s.RunNow()
	waitFor(t, 3*time.Second, func() bool {
		return s.Status()["last_run"] != before
	})
}

func TestRunAutoDownload(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Chrono Trigger", "SNES", "snes"); err != nil {
		t.Fatalf("AddWishlistItem: %v", err)
	}

	// Webhook receiver.
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case hit <- string(body):
		default:
		}
	}))
	t.Cleanup(srv.Close)

	var searchedQuery, searchedSlug string
	searchFn := func(q, p string) []*models.SearchResult {
		searchedQuery, searchedSlug = q, p
		// Deliberately unsorted: best match is second.
		return []*models.SearchResult{
			{Title: "Chrono Trigger [bad dump]", Platform: "SNES", Score: 40},
			{Title: "Chrono Trigger (USA)", Platform: "SNES", Score: 90},
		}
	}

	var downloaded *models.SearchResult
	downloadFn := func(g selection.Grab) (string, error) {
		downloaded = g.Result
		return "job-123", nil
	}

	webhookFn := func() []webhook.WebhookConfig {
		return []webhook.WebhookConfig{{
			Name: "test", URL: srv.URL, Type: "generic", Enabled: true, Events: "*",
		}}
	}

	cfg := &config.Config{
		SchedulerEnabled:      true,
		SchedulerAutoDownload: true,
		SchedulerMinScore:     0, // exercises the default-to-70 fallback
	}
	s := New(cfg, store, searchFn, downloadFn, webhookFn)

	s.run()

	if searchedQuery != "Chrono Trigger" || searchedSlug != "snes" {
		t.Errorf("searched (%q, %q), want (Chrono Trigger, snes)", searchedQuery, searchedSlug)
	}
	if downloaded == nil {
		t.Fatal("downloadFn was not called")
	}
	if downloaded.Title != "Chrono Trigger (USA)" || downloaded.Score != 90 {
		t.Errorf("downloaded %+v, want the highest-scored result", downloaded)
	}

	status := s.Status()
	if status["auto_downloads"] != 1 {
		t.Errorf("auto_downloads = %v, want 1", status["auto_downloads"])
	}
	if status["last_results"] != 2 {
		t.Errorf("last_results = %v, want 2", status["last_results"])
	}
	if status["running"] != false {
		t.Errorf("running = %v after run, want false", status["running"])
	}

	// Item removed from wishlist after successful download.
	if items := store.GetWishlist(); len(items) != 0 {
		t.Errorf("wishlist has %d items after auto-download, want 0", len(items))
	}

	// Webhook fired (async).
	select {
	case body := <-hit:
		if !strings.Contains(body, "scheduler_match") {
			t.Errorf("webhook body missing event: %s", body)
		}
		if !strings.Contains(body, "Chrono Trigger (USA)") {
			t.Errorf("webhook body missing matched title: %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Error("webhook was never delivered")
	}
}

func TestRunScoreBelowThreshold(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Obscure Game", "N64", "n64"); err != nil {
		t.Fatalf("AddWishlistItem: %v", err)
	}

	searchFn := func(q, p string) []*models.SearchResult {
		return []*models.SearchResult{{Title: "Obscure Game", Platform: "N64", Score: 90}}
	}
	var downloads int64
	downloadFn := func(g selection.Grab) (string, error) {
		atomic.AddInt64(&downloads, 1)
		return "job-x", nil
	}

	cfg := &config.Config{
		SchedulerAutoDownload: true,
		SchedulerMinScore:     95, // above the result's 90
	}
	s := New(cfg, store, searchFn, downloadFn, nil)

	s.run()

	if got := atomic.LoadInt64(&downloads); got != 0 {
		t.Errorf("downloadFn called %d times, want 0 (score below threshold)", got)
	}
	if items := store.GetWishlist(); len(items) != 1 {
		t.Errorf("wishlist has %d items, want 1 (item kept)", len(items))
	}
	status := s.Status()
	if status["last_results"] != 1 {
		t.Errorf("last_results = %v, want 1", status["last_results"])
	}
	if status["auto_downloads"] != 0 {
		t.Errorf("auto_downloads = %v, want 0", status["auto_downloads"])
	}
}

func TestRunDownloadError(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Broken Game", "PC", ""); err != nil {
		t.Fatalf("AddWishlistItem: %v", err)
	}

	searchFn := func(q, p string) []*models.SearchResult {
		return []*models.SearchResult{{Title: "Broken Game", Platform: "PC", Score: 99}}
	}
	downloadFn := func(g selection.Grab) (string, error) {
		return "", errors.New("no seeders")
	}

	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70}
	// webhookFn nil covers the nil-webhook branch.
	s := New(cfg, store, searchFn, downloadFn, nil)

	s.run()

	status := s.Status()
	if status["auto_downloads"] != 0 {
		t.Errorf("auto_downloads = %v, want 0 after failed download", status["auto_downloads"])
	}
	if items := store.GetWishlist(); len(items) != 1 {
		t.Errorf("wishlist has %d items, want 1 (kept after failed download)", len(items))
	}
}

func TestRunAbortsAfterStop(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Some Game", "NES", "nes"); err != nil {
		t.Fatalf("AddWishlistItem: %v", err)
	}

	var searches int64
	searchFn := func(q, p string) []*models.SearchResult {
		atomic.AddInt64(&searches, 1)
		return nil
	}

	cfg := &config.Config{SchedulerAutoDownload: true}
	s := New(cfg, store, searchFn, noopDownload, nil)

	s.Stop() // stopCh closed before run
	s.run()

	if got := atomic.LoadInt64(&searches); got != 0 {
		t.Errorf("searchFn called %d times after Stop, want 0", got)
	}
}

func TestStopInterruptsRateLimitSleep(t *testing.T) {
	store := newTestStore(t)
	for _, title := range []string{"Game One", "Game Two", "Game Three"} {
		if _, err := store.AddWishlistItem(title, "NES", "nes"); err != nil {
			t.Fatalf("AddWishlistItem: %v", err)
		}
	}

	firstSearch := make(chan struct{})
	var searches int64
	searchFn := func(q, p string) []*models.SearchResult {
		if atomic.AddInt64(&searches, 1) == 1 {
			close(firstSearch)
		}
		// Non-empty results (below any threshold) so the iteration reaches
		// the inter-item rate limit rather than an early continue.
		return []*models.SearchResult{{Title: q, Platform: "NES", Score: 10}}
	}

	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 95}
	s := New(cfg, store, searchFn, noopDownload, nil)

	go func() {
		<-firstSearch
		s.Stop()
	}()

	start := time.Now()
	s.run()
	elapsed := time.Since(start)

	// The full rate-limit budget for 3 items is 2 sleeps x 2s = 4s. A stop
	// after the first item must interrupt the pending sleep, returning well
	// under a single 2s sleep.
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("run took %v after Stop, want well under the 2s rate-limit sleep", elapsed)
	}
	if got := atomic.LoadInt64(&searches); got != 1 {
		t.Errorf("searchFn called %d times, want 1 (stopped after first item)", got)
	}
}

func TestRateLimitAppliedOnContinuePaths(t *testing.T) {
	store := newTestStore(t)
	for _, title := range []string{"No Results One", "No Results Two", "No Results Three"} {
		if _, err := store.AddWishlistItem(title, "NES", "nes"); err != nil {
			t.Fatalf("AddWishlistItem: %v", err)
		}
	}

	var searches int64
	// Zero results exercises the continue branch, which previously skipped
	// the inter-item rate limit entirely.
	searchFn := func(q, p string) []*models.SearchResult {
		atomic.AddInt64(&searches, 1)
		return nil
	}

	cfg := &config.Config{SchedulerAutoDownload: true}
	s := New(cfg, store, searchFn, noopDownload, nil)
	s.rateLimit = 80 * time.Millisecond

	start := time.Now()
	s.run()
	elapsed := time.Since(start)

	if got := atomic.LoadInt64(&searches); got != 3 {
		t.Errorf("searchFn called %d times, want 3", got)
	}
	// Two inter-item waits must elapse even though every iteration continues.
	if want := 160 * time.Millisecond; elapsed < want {
		t.Errorf("run took %v, want >= %v (rate limit skipped on continue)", elapsed, want)
	}
}

func TestRunGuardPreventsConcurrentRuns(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Guarded Game", "GBA", "gba"); err != nil {
		t.Fatalf("AddWishlistItem: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var searches int64
	searchFn := func(q, p string) []*models.SearchResult {
		atomic.AddInt64(&searches, 1)
		once.Do(func() { close(started) })
		<-release
		return nil // zero results: run skips the inter-item sleep
	}

	cfg := &config.Config{SchedulerAutoDownload: false}
	s := New(cfg, store, searchFn, noopDownload, nil)

	firstDone := make(chan struct{})
	go func() {
		s.run()
		close(firstDone)
	}()
	<-started

	if s.Status()["running"] != true {
		t.Error("running should be true while a run is in flight")
	}

	// A second run must bail out immediately while the first holds the guard.
	secondDone := make(chan struct{})
	go func() {
		s.run()
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second run did not return immediately; guard failed")
	}

	close(release)
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first run never finished")
	}

	if got := atomic.LoadInt64(&searches); got != 1 {
		t.Errorf("searchFn called %d times, want 1 (second run blocked by guard)", got)
	}
	if s.Status()["running"] != false {
		t.Error("running should be false after run completes")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{5, "5"},
		{42, "42"},
		{100, "100"},
		{987654, "987654"},
		{-7, "-7"},
		{-120, "-120"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := itoa(tt.in); got != tt.want {
				t.Errorf("itoa(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// prep mirrors the real searchFn contract: results arrive parsed + scored.
func prep(score int, sourceType string, titles ...string) []*models.SearchResult {
	var out []*models.SearchResult
	for _, title := range titles {
		attrs := selection.Parse(title)
		out = append(out, &models.SearchResult{
			Title: title, Platform: "PS1", PlatformSlug: "psx",
			SourceType: sourceType, Score: score, MD5: "d41d8cd98f00b204e9800998ecf8427e",
			Attrs: &attrs,
		})
	}
	return out
}

func TestOffModeKeepsDeleteAtGrab(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Wipeout XL", "PS1", "psx"); err != nil {
		t.Fatal(err)
	}
	var grabs []selection.Grab
	downloadFn := func(g selection.Grab) (string, error) {
		grabs = append(grabs, g)
		return "job-1", nil
	}
	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "off"}
	s := New(cfg, store, func(q, p string) []*models.SearchResult {
		return prep(90, "ddl", "Wipeout XL (USA).zip")
	}, downloadFn, nil)
	s.run()

	if len(grabs) != 1 || grabs[0].Result.Title != "Wipeout XL (USA).zip" || grabs[0].DiscSetID != "" {
		t.Fatalf("off mode grabs = %+v, want one bare legacy grab", grabs)
	}
	if items := store.GetWishlist(); len(items) != 0 {
		t.Errorf("wishlist has %d items, want 0 (off mode deletes at grab)", len(items))
	}
}

func TestEnforceGrabSetDispatchesAllMembersAndKeepsWishlistRow(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Final Fantasy VII", "PS1", "psx"); err != nil {
		t.Fatal(err)
	}
	var grabs []selection.Grab
	downloadFn := func(g selection.Grab) (string, error) {
		grabs = append(grabs, g)
		return "job-" + itoa(len(grabs)), nil
	}
	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "enforce"}
	s := New(cfg, store, func(q, p string) []*models.SearchResult {
		return prep(85, "ddl",
			"Final Fantasy VII (USA) (Disc 1).zip",
			"Final Fantasy VII (USA) (Disc 2).zip",
			"Final Fantasy VII (USA) (Disc 3).zip")
	}, downloadFn, nil)
	s.run()

	if len(grabs) != 3 {
		t.Fatalf("dispatched %d grabs, want 3 set members", len(grabs))
	}
	setID := grabs[0].DiscSetID
	if setID == "" {
		t.Fatal("set members carry no DiscSetID")
	}
	for i, g := range grabs {
		if g.DiscSetID != setID || g.DiscIndex != i+1 || g.DiscTotal != 3 ||
			g.SetDir != "Final Fantasy VII (USA)" {
			t.Errorf("grab %d = %+v, want shared set stamps", i, g)
		}
	}
	if items := store.GetWishlist(); len(items) != 1 {
		t.Errorf("wishlist has %d items, want 1 (enforce keeps the row until owned)", len(items))
	}
	if status := s.Status(); status["auto_downloads"] != 3 {
		t.Errorf("auto_downloads = %v, want 3", status["auto_downloads"])
	}
}

func TestEnforceOwnedSkipDeletesWishlistRow(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	// The live Kirby finding: tagged library title, bare wishlist title —
	// neither exact-title nor extension-stripping matching hits; the bare
	// ownership key must.
	if _, err := store.AddLibraryItem(&db.LibraryItem{
		Title: "Kirby's Dream Land 2 (USA, Europe) (SGB Enhanced)", Platform: "GB",
		PlatformSlug: "gb", FilePath: "/roms/gb/kdl2", Source: "romm", SourceType: "romm",
		SourceID: "romm:999", Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddWishlistItem("Kirby's Dream Land 2", "GB", "gb"); err != nil {
		t.Fatal(err)
	}
	var grabs int
	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "enforce"}
	s := New(cfg, store, func(q, p string) []*models.SearchResult {
		return prep(90, "ddl", "Kirby's Dream Land 2 (USA, Europe).zip")
	}, func(g selection.Grab) (string, error) { grabs++; return "job", nil }, nil)
	s.run()

	if grabs != 0 {
		t.Errorf("downloadFn called %d times, want 0 (owned)", grabs)
	}
	if items := store.GetWishlist(); len(items) != 0 {
		t.Errorf("wishlist has %d items, want 0 (owned deletes the row)", len(items))
	}
}

func TestEnforceActiveGrabSkipsWhileSetInFlight(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	if _, err := store.AddWishlistItem("Final Fantasy VII", "PS1", "psx"); err != nil {
		t.Fatal(err)
	}
	// A completed-but-unfinalized set member counts as in flight; so would a
	// plain downloading job.
	store.Set("job-d1", map[string]interface{}{
		"status": "completed", "title": "Final Fantasy VII (USA) (Disc 1)",
		"platform_slug": "psx", "disc_set_id": "set-abc", "set_member_done": true,
	})
	var grabs int
	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "enforce"}
	s := New(cfg, store, func(q, p string) []*models.SearchResult {
		return prep(90, "ddl", "Final Fantasy VII (USA) (Disc 1).zip",
			"Final Fantasy VII (USA) (Disc 2).zip", "Final Fantasy VII (USA) (Disc 3).zip")
	}, func(g selection.Grab) (string, error) { grabs++; return "job", nil }, nil)
	s.run()

	if grabs != 0 {
		t.Errorf("downloadFn called %d times, want 0 (set in flight)", grabs)
	}
	if items := store.GetWishlist(); len(items) != 1 {
		t.Errorf("wishlist has %d items, want 1 (kept while in flight)", len(items))
	}
}

func TestOwnershipKeys(t *testing.T) {
	t.Parallel()
	keys := ownershipKeys("Kirby's Dream Land 2 (USA, Europe) (SGB Enhanced)")
	found := false
	for _, k := range keys {
		if k == "kirby's dream land 2" {
			found = true
		}
	}
	if !found {
		t.Errorf("ownershipKeys missing bare key, got %v", keys)
	}
	// Vimm titles carry a trailing "(SystemName)" parenthetical.
	keys = ownershipKeys("Kirby's Dream Land 2 (GB)")
	found = false
	for _, k := range keys {
		if k == "kirby's dream land 2" {
			found = true
		}
	}
	if !found {
		t.Errorf("ownershipKeys missing bare key for Vimm system tag, got %v", keys)
	}
	if got := ownershipKeys("(Weird) [Tags]"); len(got) == 0 {
		t.Errorf("all-tag title produced no keys at all: %v", got)
	}
}

func TestEnforceHashOwnedSkipDeletesWishlistRow(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	// The live Hagane finding: the library title does NOT title-match the
	// wishlist ("Hagane" vs "Hagane - The Final Conflict"), but the stored
	// $.romm hash matches the winning release byte-for-byte — hash identity
	// must mark the item fulfilled where title matching cannot.
	if _, err := store.AddLibraryItem(&db.LibraryItem{
		Title: "Hagane - The Final Conflict (U).zip", Platform: "SNES",
		PlatformSlug: "snes", FilePath: "/roms/snes/hagane", Source: "romm", SourceType: "romm",
		SourceID: "romm:777", Metadata: `{"romm":{"md5":"d41d8cd98f00b204e9800998ecf8427e"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddWishlistItem("Hagane", "SNES", "snes"); err != nil {
		t.Fatal(err)
	}
	var grabs int
	cfg := &config.Config{SchedulerAutoDownload: true, SchedulerMinScore: 70, SelectorMode: "enforce"}
	s := New(cfg, store, func(q, p string) []*models.SearchResult {
		return prep(90, "ddl", "Hagane - The Final Conflict (USA).zip")
	}, func(g selection.Grab) (string, error) { grabs++; return "job", nil }, nil)
	s.run()

	if grabs != 0 {
		t.Errorf("downloadFn called %d times, want 0 (hash-owned)", grabs)
	}
	if items := store.GetWishlist(); len(items) != 0 {
		t.Errorf("wishlist has %d items, want 0 (hash-owned deletes the row)", len(items))
	}
}
