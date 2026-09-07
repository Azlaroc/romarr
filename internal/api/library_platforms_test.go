package api

import (
	"testing"

	"gamarr/internal/db"
)

func TestLibraryPlatformsShelf(t *testing.T) {
	env := newTestEnv(t, nil)

	for i, path := range []string{"/roms/nes/a.nes", "/roms/nes/b.nes"} {
		if _, err := env.jobs.AddLibraryItem(&db.LibraryItem{
			Title: "G", Platform: "nes", PlatformSlug: "nes",
			FilePath: path, FileSize: 1000, Source: "scan", SourceType: "manual",
			SourceID: "manual:" + path, Metadata: "{}",
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := env.jobs.SavePlatformRollup(db.PlatformRollup{PlatformSlug: "nes", Owned: 2, Gaps: 7}); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	rr := env.do("GET", "/api/library/platforms", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)

	tiles := body["platforms"].([]interface{})
	var nes map[string]interface{}
	for _, raw := range tiles {
		tile := raw.(map[string]interface{})
		slug := tile["slug"].(string)
		if slug == "nes" {
			nes = tile
		}
		// The shelf shows inhabited platforms only: nothing here should have
		// zero owned unless collection mode holds it up.
		if tile["owned"].(float64) == 0 && tile["collection_mode"] != true {
			t.Fatalf("uninhabited tile on the shelf: %v", tile)
		}
	}
	if nes == nil {
		t.Fatalf("no nes tile in %v", tiles)
	}
	if nes["owned"].(float64) != 2 || nes["size_bytes"].(float64) != 2000 {
		t.Fatalf("nes live totals=%v", nes)
	}
	if nes["accent_color"] == "" || nes["art_source"] != "asset" {
		t.Fatalf("nes art identity missing: %v", nes)
	}
	sc := nes["set_counts"].(map[string]interface{})
	if sc["gaps"].(float64) != 7 || sc["computed_at"] == "" {
		t.Fatalf("nes set_counts=%v", sc)
	}

	totals := body["totals"].(map[string]interface{})
	if totals["owned"].(float64) != 2 || totals["size_bytes"].(float64) != 2000 {
		t.Fatalf("shelf totals=%v", totals)
	}

	// A platform with items but no rollup renders honest absence, not zeros.
	if _, err := env.jobs.AddLibraryItem(&db.LibraryItem{
		Title: "H", Platform: "gb", PlatformSlug: "gb",
		FilePath: "/roms/gb/h.gb", FileSize: 500, Source: "scan", SourceType: "manual",
		SourceID: "manual:h", Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	rr = env.do("GET", "/api/library/platforms", "")
	for _, raw := range decodeMap(t, rr)["platforms"].([]interface{}) {
		tile := raw.(map[string]interface{})
		if tile["slug"] == "gb" && tile["set_counts"] != nil {
			t.Fatalf("gb has set_counts without a rollup row: %v", tile)
		}
	}
}
