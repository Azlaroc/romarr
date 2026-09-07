package api

import (
	"fmt"
	"testing"

	"gamarr/internal/db"
)

// The wire contract the browse screens build on: filtered totals are the
// library's, not the page's, and the letters/facets envelopes hold shape.
func TestLibraryBrowseEndpoints(t *testing.T) {
	env := newTestEnv(t, nil)

	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		_, err := env.jobs.AddLibraryItem(&db.LibraryItem{
			Title: title, Platform: "nes", PlatformSlug: "nes",
			FilePath: fmt.Sprintf("/roms/nes/%s (USA).zip", title),
			FileSize: 1024, Source: "scan", SourceType: "manual",
			SourceID: "manual:" + title, Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("AddLibraryItem: %v", err)
		}
	}
	tagID, err := env.jobs.AddTag("shelf", "#123456")
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	ids := env.jobs.GetLibraryItemIDsByTag("shelf")
	if len(ids) != 0 {
		t.Fatalf("fresh tag already has items: %v", ids)
	}
	page := env.jobs.GetLibraryPage(db.LibraryQuery{Page: 1, PageSize: 50, Sort: "title"})
	if err := env.jobs.AddItemTag(page.Items[0].ID, tagID); err != nil {
		t.Fatalf("AddItemTag: %v", err)
	}
	if err := env.jobs.AddItemTag(page.Items[2].ID, tagID); err != nil {
		t.Fatalf("AddItemTag: %v", err)
	}

	// Tag totals describe the filtered library across pages (the regression).
	rr := env.do("GET", "/api/library?tag=shelf&page_size=1&page=2&sort=title", "")
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if got := body["total"].(float64); got != 2 {
		t.Fatalf("tag-filtered total=%v, want 2", got)
	}
	if got := body["total_pages"].(float64); got != 2 {
		t.Fatalf("tag-filtered total_pages=%v, want 2", got)
	}
	items := body["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("page 2 items=%d, want 1", len(items))
	}
	item := items[0].(map[string]interface{})
	if item["title"] != "Gamma" {
		t.Fatalf("page 2 serves %v, want Gamma", item["title"])
	}
	if _, ok := item["catalog_verdict"]; !ok {
		t.Fatal("items no longer carry catalog_verdict")
	}
	if item["fs_name"] != "Gamma (USA).zip" {
		t.Fatalf("fs_name=%v", item["fs_name"])
	}

	// Letters: totals agree with the unfiltered library.
	rr = env.do("GET", "/api/library/letters", "")
	wantStatus(t, rr, 200)
	body = decodeMap(t, rr)
	if got := body["total"].(float64); got != 3 {
		t.Fatalf("letters total=%v, want 3", got)
	}
	letters := body["letters"].([]interface{})
	first := letters[0].(map[string]interface{})
	if first["letter"] != "A" || first["offset"].(float64) != 0 {
		t.Fatalf("first letter=%v", first)
	}

	// Facets: envelope shape, and verdict/format params are ignored (they
	// are the chip dimensions, not base filters).
	rr = env.do("GET", "/api/library/facets?verdict=verified", "")
	wantStatus(t, rr, 200)
	facets := decodeMap(t, rr)["facets"].(map[string]interface{})
	verdicts := facets["verdict"].([]interface{})
	if len(verdicts) != 1 {
		t.Fatalf("verdict facets=%v", verdicts)
	}
	v := verdicts[0].(map[string]interface{})
	if v["value"] != "unmeasured" || v["count"].(float64) != 3 {
		t.Fatalf("verdict facet=%v, want unmeasured:3 despite ?verdict=verified", v)
	}
	tags := facets["tag"].([]interface{})
	if len(tags) != 1 || tags[0].(map[string]interface{})["count"].(float64) != 2 {
		t.Fatalf("tag facets=%v, want shelf:2", tags)
	}
}
