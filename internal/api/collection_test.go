package api

import (
	"testing"

	"gamarr/internal/db"
)

// seedSet gives a platform a catalog and one owned file, so the set endpoint
// has something real to reconcile.
func seedSet(t *testing.T, env *testEnv) {
	t.Helper()
	games := []db.DatGameRow{
		{Name: "Ace of Aces (USA)", BareTitle: "Ace of Aces", Region: "usa", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ace of Aces (USA).a78", Size: 1024, MD5: "aaa"}}},
		{Name: "Ace of Aces (Europe)", BareTitle: "Ace of Aces", Region: "europe", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ace of Aces (Europe).a78", Size: 1024, MD5: "bbb"}}},
		{Name: "Ballblazer (USA)", BareTitle: "Ballblazer", Region: "usa", TotalSize: 1024,
			Roms: []db.DatRomRow{{Name: "Ballblazer (USA).a78", Size: 1024, MD5: "ccc"}}},
	}
	if _, err := env.jobs.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "atari7800", Version: "v1",
	}, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
	if _, err := env.jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Ace of Aces", PlatformSlug: "atari7800",
		FilePath: "/roms/atari7800/Ace of Aces (USA).zip",
		Metadata: `{"romm":{"md5":"aaa"}}`, Source: "scan", SourceID: "seed-1",
	}); err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
}

func TestPlatformSetReportsGapsAndPolicy(t *testing.T) {
	env := newTestEnv(t, nil)
	seedSet(t, env)

	rr := env.do("GET", "/api/platforms/atari7800/set", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)

	counts, _ := body["counts"].(map[string]interface{})
	if counts["groups"] != float64(2) || counts["owned"] != float64(1) || counts["gaps"] != float64(1) {
		t.Errorf("counts = %v, want 2 groups / 1 owned / 1 gap", counts)
	}
	if counts["surplus"] != float64(0) {
		t.Errorf("surplus = %v, want 0 — the European dump is catalogued but not owned", counts["surplus"])
	}
	// The set must always say what decided it.
	policy, _ := body["policy"].(map[string]interface{})
	if policy["profile_name"] == "" || policy["region_priority"] == nil {
		t.Errorf("policy = %v, want the profile and region order that chose the keepers", policy)
	}
	entries, _ := body["entries"].([]interface{})
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// 🔴 Field PRESENCE, not emptiness. A missing key decodes to a nil
	// interface, which compares unequal to "" — so an emptiness check passes
	// happily against a payload that carries no such field at all. This
	// assertion exists because exactly that hid Go field names (Title,
	// Members) leaking into the JSON.
	for _, e := range entries {
		m, _ := e.(map[string]interface{})
		for _, key := range []string{"key", "title", "source", "members", "status"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("entry is missing %q — got keys %v", key, mapKeys(m))
			}
		}
		members, _ := m["members"].([]interface{})
		if len(members) == 0 {
			t.Fatalf("%v has no members", m["title"])
		}
		for _, mem := range members {
			mm, _ := mem.(map[string]interface{})
			if _, ok := mm["name"]; !ok {
				t.Fatalf("member is missing %q — got keys %v", "name", mapKeys(mm))
			}
			reason, ok := mm["reason"].(string)
			if !ok || reason == "" {
				t.Errorf("%v/%v: a member with no reason — every verdict must be readable", m["title"], mm["name"])
			}
		}
	}
}

func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Counts describe the whole set, never the filtered page: "1 gap" has to mean
// the same thing on every view of it.
func TestPlatformSetFilterKeepsWholeSetCounts(t *testing.T) {
	env := newTestEnv(t, nil)
	seedSet(t, env)

	rr := env.do("GET", "/api/platforms/atari7800/set?status=gap", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	entries, _ := body["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("filtered entries = %d, want 1", len(entries))
	}
	if got := body["total"]; got != float64(1) {
		t.Errorf("total = %v, want the filtered count 1", got)
	}
	counts, _ := body["counts"].(map[string]interface{})
	if counts["groups"] != float64(2) {
		t.Errorf("counts.groups = %v under a filter, want the whole set's 2", counts["groups"])
	}
}

func TestPlatformSetIsEmptyWithoutACatalog(t *testing.T) {
	env := newTestEnv(t, nil)
	rr := env.do("GET", "/api/platforms/switch/set", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if entries, _ := body["entries"].([]interface{}); len(entries) != 0 {
		t.Errorf("entries = %d, want none for a platform with no catalog lane", len(entries))
	}
	if body["grouping"] != "title" {
		t.Errorf("grouping = %v, want the honest default", body["grouping"])
	}
}

func TestCloneListsReportPlatformsAndBase(t *testing.T) {
	env := newTestEnv(t, nil)

	rr := env.do("GET", "/api/clonelists", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)

	platforms, _ := body["platforms"].([]interface{})
	if len(platforms) < 20 {
		t.Fatalf("platforms with a locator = %d, want one per shipped catalog lane", len(platforms))
	}
	// 🔴 The locator vocabulary is not the DAT-code vocabulary. atari7800 is
	// one of the five that disagree, and getting it wrong is a silent 404.
	var found string
	for _, p := range platforms {
		m, _ := p.(map[string]interface{})
		if m["platform_slug"] == "atari7800" {
			found, _ = m["clonelist_name"].(string)
		}
	}
	if found != "Atari - Atari 7800 (No-Intro)" {
		t.Errorf("atari7800 locator = %q, want the clone-list spelling", found)
	}
	if lists, ok := body["lists"].([]interface{}); !ok || len(lists) != 0 {
		t.Errorf("lists = %v, want an empty list before any refresh", body["lists"])
	}
	if body["base"] == "" {
		t.Error("the fetch base must be visible — it is an editable setting")
	}
}

func TestCollectionTargetsAndSync(t *testing.T) {
	env := newTestEnv(t, nil)
	seedSet(t, env)

	// Nothing is in collection mode yet, so a sync produces nothing.
	rr := env.do("POST", "/api/collection/sync", "")
	wantStatus(t, rr, 200)
	if results, _ := decodeMap(t, rr)["results"].([]interface{}); len(results) != 0 {
		t.Errorf("results = %v, want none before any platform opts in", results)
	}

	rr = env.do("PUT", "/api/platforms/atari7800", `{"collection_mode":true}`)
	wantStatus(t, rr, 200)
	if got := decodeMap(t, rr)["collection_mode"]; got != true {
		t.Fatalf("collection_mode = %v after the patch", got)
	}

	rr = env.do("POST", "/api/collection/sync", "")
	wantStatus(t, rr, 200)
	results, _ := decodeMap(t, rr)["results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("results = %v, want the one platform", results)
	}

	rr = env.do("GET", "/api/collection/targets", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	targets, _ := body["targets"].([]interface{})
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want the single gap (Ballblazer)", len(targets))
	}
	first, _ := targets[0].(map[string]interface{})
	if first["title"] != "Ballblazer" || first["status"] != "wanted" {
		t.Errorf("target = %v, want Ballblazer wanted", first)
	}
	if body["fill_per_cycle"] == nil {
		t.Error("the pace has to be visible next to the queue it paces")
	}

	// 🔴 Leaving collection mode drops the queue with it.
	rr = env.do("PUT", "/api/platforms/atari7800", `{"collection_mode":false}`)
	wantStatus(t, rr, 200)
	rr = env.do("GET", "/api/collection/targets", "")
	wantStatus(t, rr, 200)
	if targets, _ := decodeMap(t, rr)["targets"].([]interface{}); len(targets) != 0 {
		t.Errorf("targets = %d after collection mode went off, want 0", len(targets))
	}
}
