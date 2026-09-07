package api

import (
	"fmt"
	"testing"

	"gamarr/internal/db"
)

func addDetailItem(t *testing.T, env *testEnv, title, slug, path, metadata string) int64 {
	t.Helper()
	if metadata == "" {
		metadata = "{}"
	}
	id, err := env.jobs.AddLibraryItem(&db.LibraryItem{
		Title: title, Platform: slug, PlatformSlug: slug,
		FilePath: path, FileSize: 4096, Source: "scan", SourceType: "manual",
		SourceID: "manual:" + path, Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func TestLibraryDetail(t *testing.T) {
	env := newTestEnv(t, nil)

	// A catalog for gb: two same-bare_title variants, one of which the
	// stored hashes will land on.
	if _, err := env.jobs.InsertDatSnapshot(
		db.DatSnapshotMeta{Authority: "no-intro", PlatformSlug: "gb", Version: "2026.09.01"},
		[]db.DatGameRow{
			{Name: "Alleyway (World)", BareTitle: "Alleyway", TotalSize: 1024,
				Roms: []db.DatRomRow{{Name: "Alleyway (World).gb", Size: 1024, CRC: "deadbeef"}}},
			{Name: "Alleyway (Japan)", BareTitle: "Alleyway", TotalSize: 1024,
				Roms: []db.DatRomRow{{Name: "Alleyway (Japan).gb", Size: 1024, CRC: "cafef00d"}}},
		},
	); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}

	hashed := addDetailItem(t, env, "Alleyway", "gb", "/roms/gb/Alleyway (World).zip", "")
	if err := env.jobs.SaveLibraryHashes(hashed, db.LibraryHashes{CRC: "deadbeef"}); err != nil {
		t.Fatalf("SaveLibraryHashes: %v", err)
	}
	fresh := addDetailItem(t, env, "Fresh", "gb", "/roms/gb/Fresh (World).zip", "")

	rr := env.do("GET", "/api/library/99999", "")
	wantStatus(t, rr, 404)

	// The hashed row: canonical resolves through the catalog, the family
	// carries both variants, ours flagged current.
	rr = env.do("GET", fmt.Sprintf("/api/library/%d", hashed), "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	canonical := body["canonical"].(map[string]interface{})
	if canonical["outcome"] != "resolved" || canonical["name"] != "Alleyway (World).gb" {
		t.Fatalf("canonical=%v", canonical)
	}
	hashes := body["hashes"].(map[string]interface{})
	if hashes["domain"] != "gamarr" || hashes["crc"] != "deadbeef" {
		t.Fatalf("hashes=%v", hashes)
	}
	group := body["dat_group"].([]interface{})
	if len(group) != 2 {
		t.Fatalf("dat_group=%v, want both Alleyway variants", group)
	}
	currents := 0
	for _, raw := range group {
		g := raw.(map[string]interface{})
		if g["is_current"].(bool) {
			currents++
			if g["name"] != "Alleyway (World)" {
				t.Fatalf("is_current on %v", g["name"])
			}
		}
	}
	if currents != 1 {
		t.Fatalf("is_current count=%d, want exactly 1", currents)
	}
	profile := body["profile"].(map[string]interface{})
	if profile["id"].(float64) != 0 || profile["resolved_from"] != "platform" {
		t.Fatalf("profile=%v", profile)
	}

	// The unhashed row is honest about it: no verdict accusation, outcome
	// unhashed, family falls back to a title browse.
	rr = env.do("GET", fmt.Sprintf("/api/library/%d", fresh), "")
	wantStatus(t, rr, 200)
	body = decodeMap(t, rr)
	if body["canonical"].(map[string]interface{})["outcome"] != "unhashed" {
		t.Fatalf("fresh canonical=%v", body["canonical"])
	}
	item := body["item"].(map[string]interface{})
	if item["catalog_verdict"] != "" {
		t.Fatalf("fresh verdict=%v, want empty (never measured)", item["catalog_verdict"])
	}
}

func TestLibraryProfilePatch(t *testing.T) {
	env := newTestEnv(t, nil)
	id := addDetailItem(t, env, "Alpha", "nes", "/roms/nes/Alpha (USA).nes", "")

	// A template must be refused — same contract as the wishlist PATCH.
	var templateID int64
	env.jobs.DB().QueryRow("SELECT id FROM quality_profiles WHERE is_template = 1 LIMIT 1").Scan(&templateID)
	if templateID == 0 {
		t.Fatal("fixture: no seeded template profile found")
	}
	rr := env.do("PATCH", fmt.Sprintf("/api/library/%d", id), fmt.Sprintf(`{"profile_id": %d}`, templateID))
	wantStatus(t, rr, 400)

	var realID int64
	env.jobs.DB().QueryRow("SELECT id FROM quality_profiles WHERE is_template = 0 LIMIT 1").Scan(&realID)
	if realID == 0 {
		t.Fatal("fixture: no non-template profile found")
	}
	rr = env.do("PATCH", fmt.Sprintf("/api/library/%d", id), fmt.Sprintf(`{"profile_id": %d}`, realID))
	wantStatus(t, rr, 200)

	rr = env.do("GET", fmt.Sprintf("/api/library/%d", id), "")
	body := decodeMap(t, rr)
	profile := body["profile"].(map[string]interface{})
	if int64(profile["id"].(float64)) != realID || profile["resolved_from"] != "title" {
		t.Fatalf("profile after set=%v", profile)
	}

	// 0 clears.
	rr = env.do("PATCH", fmt.Sprintf("/api/library/%d", id), `{"profile_id": 0}`)
	wantStatus(t, rr, 200)
	rr = env.do("GET", fmt.Sprintf("/api/library/%d", id), "")
	if p := decodeMap(t, rr)["profile"].(map[string]interface{}); p["id"].(float64) != 0 {
		t.Fatalf("profile after clear=%v", p)
	}

	rr = env.do("PATCH", "/api/library/99999", `{"profile_id": 0}`)
	wantStatus(t, rr, 404)
}

func TestLibraryVerifyPreconditions(t *testing.T) {
	env := newTestEnv(t, nil)

	rr := env.do("POST", "/api/library/99999/verify", "")
	wantStatus(t, rr, 404)

	// A row already marked unhashable is refused with the reason up-front —
	// verify must not burn an extraction attempt on it.
	skipped := addDetailItem(t, env, "DiscDir", "psx", "/roms/psx/DiscDir (USA)",
		`{"gamarr":{"hash_skipped":"directory"}}`)
	rr = env.do("POST", fmt.Sprintf("/api/library/%d/verify", skipped), "")
	wantStatus(t, rr, 409)
}

func TestActivityFilteredEndpoint(t *testing.T) {
	env := newTestEnv(t, nil)
	a := addDetailItem(t, env, "Alpha", "nes", "/roms/nes/a.nes", "")
	env.jobs.LogActivity("verify", "Alpha", "catalog: verified", "", &a)
	env.jobs.LogActivity("system", "noise", "unrelated", "", nil)

	rr := env.do("GET", fmt.Sprintf("/api/activity?library_item_id=%d", a), "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if body["total"].(float64) != 1 {
		t.Fatalf("filtered activity total=%v, want 1", body["total"])
	}

	rr = env.do("GET", "/api/activity?library_item_id=bogus", "")
	wantStatus(t, rr, 400)
}
