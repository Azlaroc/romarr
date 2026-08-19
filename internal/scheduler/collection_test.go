package scheduler

import (
	"testing"
	"time"

	"gamarr/internal/collectionsvc"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/selection"
)

// collectionEnv wires a scheduler with the collection plane attached and one
// platform in collection mode whose catalog has a gap.
func collectionEnv(t *testing.T, cfg *config.Config, searchFn SearchFunc, downloadFn DownloadFunc) (*Scheduler, *db.JobStore) {
	t.Helper()
	store := newTestStore(t)
	cfg.AttachSettings(store)
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })

	// A catalog with two dumps of one game and one of another, and a library
	// holding neither: two gaps.
	if _, err := store.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "atari7800", Version: "v1",
	}, []db.DatGameRow{
		{Name: "Xevious (USA)", BareTitle: "Xevious", Region: "usa", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Xevious (USA).a78", Size: 1024, MD5: "x1"}}},
		{Name: "Ballblazer (USA)", BareTitle: "Ballblazer", Region: "usa", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ballblazer (USA).a78", Size: 1024, MD5: "b1"}}},
	}); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
	on := true
	if err := store.PatchPlatform("atari7800", db.PlatformPatch{CollectionMode: &on}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}

	s := New(cfg, store, searchFn, downloadFn, nil)
	s.SetCollections(collectionsvc.New(cfg, store))
	s.rateLimit = time.Millisecond
	return s, store
}

func baseCfg() *config.Config {
	return &config.Config{
		SchedulerAutoDownload:  true,
		SchedulerMinScore:      1,
		SelectorMode:           "enforce",
		CollectionFillPerCycle: 10,
	}
}

// 🔴 The install this was built for runs with an EMPTY wishlist. An early
// return on "wishlist empty" would mean collection mode never runs at all.
func TestCollectionFillRunsWithAnEmptyWishlist(t *testing.T) {
	var searched []string
	search := func(query, slug string, _ *db.QualityProfile) []*models.SearchResult {
		searched = append(searched, query)
		return nil
	}
	s, store := collectionEnv(t, baseCfg(), search, noopDownload)

	if items := store.GetWishlist(); len(items) != 0 {
		t.Fatalf("fixture has %d wishlist rows, want an empty wishlist", len(items))
	}
	s.run()

	if len(searched) != 2 {
		t.Fatalf("searched %v, want both catalogued gaps", searched)
	}
	targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10})
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2 gaps recorded", len(targets))
	}
	for _, tg := range targets {
		if tg.Attempts != 1 || tg.Status != db.TargetUnavailable {
			t.Errorf("%s: attempts=%d status=%q, want one attempt recorded as unavailable", tg.Title, tg.Attempts, tg.Status)
		}
		if tg.LastReason == "" {
			t.Errorf("%s: no reason recorded — a backed-off gap must say why", tg.Title)
		}
	}
}

func TestCollectionFillGrabsAndMarksTheTarget(t *testing.T) {
	search := func(query, slug string, _ *db.QualityProfile) []*models.SearchResult {
		if query != "Xevious" {
			return nil
		}
		return []*models.SearchResult{{
			Title: "Xevious (USA)", Platform: "atari7800", PlatformSlug: "atari7800",
			Score: 90, Size: 1024, SourceType: "ddl", DownloadURL: "http://stub/x.zip",
		}}
	}
	var grabbed []string
	download := func(g selection.Grab) (string, error) {
		grabbed = append(grabbed, g.Result.Title)
		return "job-1", nil
	}
	s, store := collectionEnv(t, baseCfg(), search, download)
	s.run()

	if len(grabbed) != 1 || grabbed[0] != "Xevious (USA)" {
		t.Fatalf("grabbed = %v, want the one candidate", grabbed)
	}
	targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Status: db.TargetGrabbed, Limit: 10})
	if len(targets) != 1 || targets[0].Title != "Xevious" {
		t.Fatalf("grabbed targets = %+v, want Xevious marked", targets)
	}
	// The other gap is untouched by the grab and still wanted.
	wanted, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Status: db.TargetUnavailable, Limit: 10})
	if len(wanted) != 1 || wanted[0].Title != "Ballblazer" {
		t.Errorf("other target = %+v, want Ballblazer recorded as unavailable", wanted)
	}
}

// The budget bounds what ONE cycle asks of the indexers. Without it, switching
// a platform on empties its whole gap list into a source in a single pass.
func TestCollectionFillHonoursTheBudget(t *testing.T) {
	var searched int
	search := func(string, string, *db.QualityProfile) []*models.SearchResult {
		searched++
		return nil
	}
	cfg := baseCfg()
	cfg.CollectionFillPerCycle = 1
	s, _ := collectionEnv(t, cfg, search, noopDownload)
	s.run()

	if searched != 1 {
		t.Errorf("searched %d gaps, want the budget of 1", searched)
	}
}

// Two independent switches: collection mode says what is wanted, acquisition
// says whether RomArr may go and get it. Gaps still get listed.
func TestAcquisitionOffListsGapsWithoutSearching(t *testing.T) {
	var searched int
	search := func(string, string, *db.QualityProfile) []*models.SearchResult {
		searched++
		return nil
	}
	s, store := collectionEnv(t, baseCfg(), search, noopDownload)
	off := false
	if err := store.PatchPlatform("atari7800", db.PlatformPatch{AcquisitionEnabled: &off}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}
	s.run()

	if searched != 0 {
		t.Errorf("searched %d gaps with acquisition off, want 0", searched)
	}
	targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10})
	if len(targets) != 2 {
		t.Errorf("targets = %d, want the gap list still built", len(targets))
	}
}

// Turning collection mode off must stop generating work at once.
func TestCollectionModeOffClearsTheGapList(t *testing.T) {
	s, store := collectionEnv(t, baseCfg(), noopSearch, noopDownload)
	s.run()
	if targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10}); len(targets) != 2 {
		t.Fatalf("targets = %d, want 2 before the switch flips", len(targets))
	}

	off := false
	if err := store.PatchPlatform("atari7800", db.PlatformPatch{CollectionMode: &off}); err != nil {
		t.Fatalf("PatchPlatform: %v", err)
	}
	s.run()

	if targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10}); len(targets) != 0 {
		t.Errorf("targets = %d, want the gap list cleared", len(targets))
	}
}

// An owned title never becomes a gap in the first place: reconciliation sees
// the hash before the queue is written.
//
// This is NOT the mid-cycle retirement path — the sync removes it before the
// fill ever looks. That path is exercised directly in
// TestRecordTargetRetiresAFulfilledGap, because a test that cannot distinguish
// the two proves neither.
func TestOwnedTitleNeverBecomesAGap(t *testing.T) {
	search := func(query, slug string, _ *db.QualityProfile) []*models.SearchResult {
		return []*models.SearchResult{{
			Title: "Xevious (USA)", Platform: "atari7800", PlatformSlug: "atari7800",
			Score: 90, Size: 1024, SourceType: "ddl", DownloadURL: "http://stub/x.zip",
		}}
	}
	s, store := collectionEnv(t, baseCfg(), search, noopDownload)
	if _, err := store.AddLibraryItem(&db.LibraryItem{
		Title: "Xevious", PlatformSlug: "atari7800", FilePath: "/roms/atari7800/Xevious (USA).zip",
		Metadata: `{"romm":{"md5":"x1"}}`, Source: "scan", SourceID: "own-1",
	}); err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	s.run()

	targets, _ := store.ListCollectionTargets(db.CollectionTargetQuery{Limit: 10})
	for _, tg := range targets {
		if tg.Title == "Xevious" {
			t.Fatalf("an owned title is still in the gap list: %+v", tg)
		}
	}
	if len(targets) != 1 {
		t.Errorf("targets = %d, want only the genuinely missing one", len(targets))
	}
}

// The bookkeeping half of the fill loop, exercised directly: the caller — not
// processWanted — decides what an outcome means for a queue.
func TestRecordTargetRetiresAFulfilledGap(t *testing.T) {
	s, store := collectionEnv(t, baseCfg(), noopSearch, noopDownload)
	store.SyncCollectionTargets("atari7800", []db.CollectionGap{
		{SetKey: "title:xevious", Title: "Xevious", DumpName: "Xevious (USA)"},
	})
	rows, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "atari7800", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("fixture rows = %d, want 1", len(rows))
	}

	s.recordTarget(rows[0], wantedOutcome{Fulfilled: true})

	after, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "atari7800", Limit: 10})
	if len(after) != 0 {
		t.Errorf("target survived a fulfilled outcome: %+v", after)
	}
}

func TestRecordTargetKeepsTheReasonForABackoff(t *testing.T) {
	s, store := collectionEnv(t, baseCfg(), noopSearch, noopDownload)
	store.SyncCollectionTargets("gb", []db.CollectionGap{{SetKey: "k", Title: "Some Game"}})
	rows, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "gb", Limit: 10})

	s.recordTarget(rows[0], wantedOutcome{Reason: "no results"})

	after, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "gb", Limit: 10})
	if len(after) != 1 {
		t.Fatalf("rows = %d, want the target kept", len(after))
	}
	if after[0].Status != db.TargetUnavailable || after[0].LastReason != "no results" || after[0].Attempts != 1 {
		t.Errorf("recorded %+v, want unavailable / reason preserved / one attempt", after[0])
	}
	// An outcome with no reason still has to say something readable.
	s.recordTarget(after[0], wantedOutcome{})
	final, _ := store.ListCollectionTargets(db.CollectionTargetQuery{PlatformSlug: "gb", Limit: 10})
	if final[0].LastReason == "" {
		t.Error("an empty reason was stored verbatim — the row must still explain itself")
	}
}
