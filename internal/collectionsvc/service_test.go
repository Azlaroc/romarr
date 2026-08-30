package collectionsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gamarr/internal/collection"
	"gamarr/internal/config"
	"gamarr/internal/db"
)

func newTestService(t *testing.T) (*Service, *db.JobStore, *config.Config) {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{DataDir: t.TempDir()}
	cfg.AttachSettings(store)
	return New(cfg, store), store, cfg
}

// seedCatalog stores one snapshot for a platform and returns it, so a test can
// compute its expectations from the same rows the service will read.
func seedCatalog(t *testing.T, store *db.JobStore, slug string, games []db.DatGameRow) {
	t.Helper()
	if _, err := store.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: slug, Version: "v1",
	}, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
}

func catGame(name, region, md5 string) db.DatGameRow {
	return db.DatGameRow{
		Name: name, BareTitle: bareTitleOf(name), Region: region, TotalSize: 1024,
		Roms: []db.DatRomRow{{Name: name + ".a78", Size: 1024, MD5: md5}},
	}
}

func bareTitleOf(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}

func addLibraryItem(t *testing.T, store *db.JobStore, slug, title, path, md5 string) {
	t.Helper()
	meta := "{}"
	if md5 != "" {
		meta = fmt.Sprintf(`{"romm":{"md5":%q}}`, md5)
	}
	if _, err := store.AddLibraryItem(&db.LibraryItem{
		Title: title, PlatformSlug: slug, FilePath: path, Metadata: meta,
		Source: "scan", SourceID: path,
	}); err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
}

// narrowTo clears every clone-list locator except one platform's, so a refresh
// test walks one platform instead of the thirty the seed ships.
func narrowTo(t *testing.T, store *db.JobStore, keep string) {
	t.Helper()
	for _, p := range store.ListDatPlatforms() {
		if p.PlatformSlug == keep {
			continue
		}
		if err := store.SetCloneListName(p.PlatformSlug, ""); err != nil {
			t.Fatalf("clear locator for %s: %v", p.PlatformSlug, err)
		}
	}
}

func TestSetReconcilesAgainstTheLibrary(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "atari7800", []db.DatGameRow{
		catGame("Ace of Aces (USA)", "usa", "aaa"),
		catGame("Ace of Aces (Europe)", "europe", "bbb"),
		catGame("Ballblazer (USA)", "usa", "ccc"),
	})
	// Owned by hash: the RomM-sourced md5 of the USA dump and the European one.
	addLibraryItem(t, store, "atari7800", "Ace of Aces", "/roms/atari7800/Ace of Aces (USA).zip", "aaa")
	addLibraryItem(t, store, "atari7800", "Ace of Aces", "/roms/atari7800/Ace of Aces (Europe).zip", "bbb")

	res := svc.Set("atari7800")
	if res.Counts.Groups != 2 || res.Counts.Owned != 1 || res.Counts.Gaps != 1 {
		t.Errorf("counts = %+v, want 2 groups / 1 owned / 1 gap", res.Counts)
	}
	if res.Counts.Surplus != 1 {
		t.Errorf("surplus = %d, want the second Ace of Aces dump", res.Counts.Surplus)
	}
	if res.Grouping != "title" {
		t.Errorf("grouping = %q, want title-only with no clone list stored", res.Grouping)
	}
	if res.Policy.ProfileID == 0 || len(res.Policy.RegionPriority) == 0 {
		t.Errorf("policy summary is empty: %+v — the set must say what decided it", res.Policy)
	}
	for _, e := range res.Entries {
		if e.Status == collection.StatusOwned {
			k, _ := e.Keeper()
			if k.Owned == nil || k.Owned.MatchedBy != collection.MatchHash {
				t.Errorf("%s keeper matched by %+v, want a hash match", e.Title, k.Owned)
			}
		}
	}
}

func TestSetIsEmptyWithoutACatalog(t *testing.T) {
	svc, _, _ := newTestService(t)
	res := svc.Set("switch")
	if len(res.Entries) != 0 || res.Counts.Groups != 0 {
		t.Errorf("a platform with no catalog produced a set: %+v", res.Counts)
	}
	if res := svc.Set(""); len(res.Entries) != 0 {
		t.Error("an empty slug produced a set")
	}
}

func TestSetAppliesAStoredCloneList(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "gb", []db.DatGameRow{
		catGame("Contra (Japan)", "japan", "c1"),
		catGame("Operation C (USA)", "usa", "c2"),
		catGame("Probotector (Europe)", "europe", "c3"),
	})
	before := svc.Set("gb")

	if err := store.ReplaceCloneList(
		db.CloneListRow{PlatformSlug: "gb", ListName: "Nintendo - Game Boy (No-Intro)"},
		[]db.CloneGroupRow{
			{PlatformSlug: "gb", GroupName: "Operation C", SearchTerm: "Contra", Priority: 1},
			{PlatformSlug: "gb", GroupName: "Operation C", SearchTerm: "Operation C", Priority: 1},
			{PlatformSlug: "gb", GroupName: "Operation C", SearchTerm: "Probotector", Priority: 1},
		}); err != nil {
		t.Fatalf("ReplaceCloneList: %v", err)
	}
	after := svc.Set("gb")

	if before.Counts.Groups != 3 {
		t.Fatalf("without a list, groups = %d, want 3 separate titles", before.Counts.Groups)
	}
	if after.Counts.Groups != 1 {
		t.Errorf("with the list, groups = %d, want the three retitles merged into 1", after.Counts.Groups)
	}
	if after.Grouping == before.Grouping {
		t.Errorf("grouping still reads %q — the set must say a clone list is in play", after.Grouping)
	}
	if after.CloneList == nil || after.CloneList.GroupCount != 1 {
		t.Errorf("clone list metadata not surfaced: %+v", after.CloneList)
	}
}

// The fetch side, against a stub standing in for the clone-list host.
func TestRefreshImportsThenShortCircuits(t *testing.T) {
	svc, store, _ := newTestService(t)

	payload, _ := json.Marshal(map[string]interface{}{
		"description": map[string]string{"name": "Atari - Atari 7800 (No-Intro)", "lastUpdated": "2026-01-02 11:07:19"},
		"variants": []map[string]interface{}{
			{"group": "1942", "titles": []map[string]interface{}{
				{"searchTerm": "1942 (Extended)"}, {"searchTerm": "1942", "priority": 2}}},
		},
	})
	var hits int
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.Contains(r.URL.Path, "Atari 7800") {
			http.NotFound(w, r)
			return
		}
		w.Write(payload)
	}))
	defer stub.Close()

	if err := store.SetSetting("clonelist_fetch_base", stub.URL+"/"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// Only atari7800 keeps a locator, so the walk is one platform wide.
	narrowTo(t, store, "atari7800")

	results := svc.RefreshCloneListsSync(context.Background())
	if len(results) != 1 || results[0].Status != StatusImported {
		t.Fatalf("first run = %+v, want one imported", results)
	}
	if results[0].Groups != 1 || results[0].Titles != 2 {
		t.Errorf("imported %d groups / %d titles, want 1 / 2", results[0].Groups, results[0].Titles)
	}

	// Same bytes on the second run: stored rows are left alone.
	results = svc.RefreshCloneListsSync(context.Background())
	if len(results) != 1 || results[0].Status != StatusUnchanged {
		t.Fatalf("second run = %+v, want unchanged", results)
	}
	if hits != 2 {
		t.Errorf("stub hits = %d, want 2 — unchanged is decided from the fetched bytes", hits)
	}
	if got := len(store.ListCloneGroups("atari7800")); got != 2 {
		t.Errorf("stored titles = %d, want 2 after an unchanged run", got)
	}
}

func TestRefreshReportsAFetchFailure(t *testing.T) {
	svc, store, _ := newTestService(t)
	stub := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer stub.Close()
	if err := store.SetSetting("clonelist_fetch_base", stub.URL+"/"); err != nil {
		t.Fatal(err)
	}
	narrowTo(t, store, "gb")
	results := svc.RefreshCloneListsSync(context.Background())
	if len(results) != 1 || results[0].Status != StatusError {
		t.Fatalf("results = %+v, want one error", results)
	}
	// A 404 is the locator being wrong, and the message has to say which one.
	if !strings.Contains(results[0].Error, "404") || !strings.Contains(results[0].List, "Game Boy") {
		t.Errorf("error = %q for list %q, want it to name the failure and the list", results[0].Error, results[0].List)
	}
	if st := svc.RefreshStatus(); st.LastError == "" || st.Running {
		t.Errorf("status after a failed run = %+v", st)
	}
}

// 🔴 Ownership claims must be identical on every rebuild. Two library titles
// that expand to the same ownership key ("Great Escape" / "Great Escape
// (Europe)" both bare to "great escape") used to race for the title-index
// slot in map-iteration order, so owned/gap/surplus counts wobbled between
// two requests on the same process — live prod read colecovision owned as
// 127, 128 and 129 in three consecutive calls. The rule: the lowest library
// id wins a contested key, every time.
func TestCycleOwnershipIsDeterministic(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedCatalog(t, store, "atari7800", []db.DatGameRow{
		catGame("Great Escape (USA)", "usa", "ge1"),
	})
	// No hashes: force the title tier, where the collision lives.
	addLibraryItem(t, store, "atari7800", "Great Escape", "/roms/atari7800/Great Escape.a78", "")
	addLibraryItem(t, store, "atari7800", "Great Escape (Europe)", "/roms/atari7800/Great Escape (Europe).a78", "")

	const wantPath = "/roms/atari7800/Great Escape.a78" // the lower library id
	for i := 0; i < 40; i++ {
		res := svc.NewCycle().Set("atari7800")
		if len(res.Entries) != 1 {
			t.Fatalf("rebuild %d: entries = %d, want 1", i, len(res.Entries))
		}
		keeper := res.Entries[0].Members[res.Entries[0].KeeperIndex]
		if keeper.Owned == nil {
			t.Fatalf("rebuild %d: keeper unowned", i)
		}
		if keeper.Owned.FilePath != wantPath {
			t.Fatalf("rebuild %d: owner = %q, want the lowest library id %q",
				i, keeper.Owned.FilePath, wantPath)
		}
	}
}

// Golden equivalence for the profile rewire: under the shipped Standard
// profile, the set must match what the OLD policyFor (scraping the quality
// profile + the hardcoded category list) produced — with exactly ONE declared
// delta: pirate-tagged dumps now leave the set. That delta is the deliberate
// half of parser v3 (they used to pollute gap lists because (Pirate) was
// unparsed); everything else is byte-equal membership.
func TestPolicyRewireGoldenEquivalence(t *testing.T) {
	svc, store, _ := newTestService(t)
	games := []db.DatGameRow{
		catGame("Solar Fox (USA)", "usa", "s1"),
		catGame("Solar Fox (Europe)", "europe", "s2"),
		catGame("Mother 3 (Japan)", "japan", "m1"),
		catGame("Proto Thing (USA) (Proto)", "usa", "p1"),
		catGame("Homebrew Blast (World)", "world", "h1"),
		catGame("Bootleg 52-in-1 (Taiwan)", "taiwan", "b1"),
	}
	games[3].Flags = "proto"
	games[4].Flags = "aftermarket"
	games[5].Flags = "pirate"
	seedCatalog(t, store, "atari7800", games)
	addLibraryItem(t, store, "atari7800", "Solar Fox", "/roms/atari7800/Solar Fox (USA).a78", "s1")

	// The old effective policy, expressed in today's Policy vocabulary:
	// quality-profile region + three gates, aftermarket folded into the
	// unlicensed gate (both false), pirate NEVER excluded, Applications out.
	prof := store.ResolveProfileForItem(0, "atari7800")
	oldPolicy := collection.Policy{
		RegionPriority:    prof.RegionPriority,
		AllowProto:        prof.AllowProto,
		AllowDemo:         prof.AllowDemo,
		AllowBIOS:         prof.AllowBIOS,
		AllowPirate:       true,
		ExcludeCategories: []string{"Applications"},
	}
	oldGroups := collection.Build(membersOf(t, store, "atari7800"), nil, oldPolicy)

	res := svc.NewCycle().Set("atari7800")
	if len(res.Entries) != len(oldGroups) {
		t.Fatalf("group count drifted: new %d vs old %d", len(res.Entries), len(oldGroups))
	}
	for i, e := range res.Entries {
		old := oldGroups[i]
		if e.Key != old.Key {
			t.Fatalf("group order drifted at %d: %q vs %q", i, e.Key, old.Key)
		}
		newKeeper, newOK := e.Keeper()
		oldKeeper, oldOK := old.Keeper()
		pirate := strings.Contains(e.Key, "bootleg")
		if pirate {
			// The declared delta, and nothing more.
			if newOK {
				t.Errorf("pirate group %q still has a keeper under Standard", e.Key)
			}
			continue
		}
		if newOK != oldOK {
			t.Errorf("group %q keeper presence drifted: new %v old %v", e.Key, newOK, oldOK)
			continue
		}
		if newOK && newKeeper.GameID != oldKeeper.GameID {
			t.Errorf("group %q keeper drifted: new %d old %d", e.Key, newKeeper.GameID, oldKeeper.GameID)
		}
		for mi := range e.Members {
			if e.Members[mi].Excluded != old.Members[mi].Excluded {
				t.Errorf("group %q member %d exclusion drifted", e.Key, mi)
			}
		}
	}
}

// membersOf loads a platform's catalog the way the service does, so the
// golden comparison feeds both sides identical members.
func membersOf(t *testing.T, store *db.JobStore, slug string) []collection.Member {
	t.Helper()
	members := mapMembers(store.DatSetMembers(slug))
	if len(members) == 0 {
		t.Fatalf("no catalog rows for %s", slug)
	}
	return members
}
