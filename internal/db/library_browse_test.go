package db

import (
	"fmt"
	"testing"
)

func addBrowseItem(t *testing.T, store *JobStore, title, slug, path, metadata string) int64 {
	t.Helper()
	if metadata == "" {
		metadata = "{}"
	}
	id, err := store.AddLibraryItem(&LibraryItem{
		Title:        title,
		Platform:     slug,
		PlatformSlug: slug,
		FilePath:     path,
		FileSize:     1024,
		Source:       "scan",
		SourceType:   "manual",
		SourceID:     "manual:" + path,
		Metadata:     metadata,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem(%s): %v", title, err)
	}
	return id
}

// The regression this file exists for: the tag filter used to run AFTER
// pagination and rewrite Total/TotalPages from the served page, so page 2
// of a tag filter claimed the library held only what page 1 showed.
func TestLibraryTagFilterTotals(t *testing.T) {
	store := newTestStore(t)
	a := addBrowseItem(t, store, "Alpha", "nes", "/roms/nes/Alpha (USA).nes", "")
	addBrowseItem(t, store, "Beta", "nes", "/roms/nes/Beta (USA).nes", "")
	c := addBrowseItem(t, store, "Gamma", "nes", "/roms/nes/Gamma (USA).nes", "")

	tagID, err := store.AddTag("shelf", "#123456")
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := store.AddItemTag(a, tagID); err != nil {
		t.Fatalf("AddItemTag: %v", err)
	}
	if err := store.AddItemTag(c, tagID); err != nil {
		t.Fatalf("AddItemTag: %v", err)
	}

	page1 := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 1, Tag: "shelf", Sort: "title"})
	if page1.Total != 2 || page1.TotalPages != 2 {
		t.Fatalf("page1 total=%d pages=%d, want 2/2", page1.Total, page1.TotalPages)
	}
	if len(page1.Items) != 1 || page1.Items[0].Title != "Alpha" {
		t.Fatalf("page1 items=%v", page1.Items)
	}
	page2 := store.GetLibraryPage(LibraryQuery{Page: 2, PageSize: 1, Tag: "shelf", Sort: "title"})
	if page2.Total != 2 || len(page2.Items) != 1 || page2.Items[0].Title != "Gamma" {
		t.Fatalf("page2 total=%d items=%v, want Gamma", page2.Total, page2.Items)
	}
}

// A non-Latin display name must still answer to its on-disk filename — and
// a q matching only a directory segment must not match everything in it.
func TestLibraryBasenameSearch(t *testing.T) {
	store := newTestStore(t)
	addBrowseItem(t, store, "テトリス", "gb", "/roms/gb/Tetris (World) (Rev 1).zip", "")
	addBrowseItem(t, store, "Alleyway", "gb", "/roms/gb/Alleyway (World).zip", "")

	byName := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Q: "tetris"})
	if byName.Total != 1 || byName.Items[0].Title != "テトリス" {
		t.Fatalf("q=tetris total=%d, want the non-Latin row", byName.Total)
	}
	if got := byName.Items[0].FsName; got != "Tetris (World) (Rev 1).zip" {
		t.Fatalf("fs_name=%q", got)
	}

	// "gb" appears in every row's directory; only Alleyway has no "gb" in
	// title or basename... nor does Tetris. A dir-only hit must be zero.
	byDir := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Q: "roms/gb"})
	if byDir.Total != 0 {
		t.Fatalf("q=roms/gb total=%d, want 0 (directory segments must not match)", byDir.Total)
	}

	// No-slash path: the whole value is the basename.
	addBrowseItem(t, store, "Bare", "gb", "Bare (World).gb", "")
	bare := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Q: "bare ("})
	if bare.Total != 1 || bare.Items[0].FsName != "Bare (World).gb" {
		t.Fatalf("no-slash basename: total=%d fs_name=%q", bare.Total, bare.Items[0].FsName)
	}
}

func TestLibraryVerdictFilter(t *testing.T) {
	store := newTestStore(t)
	addBrowseItem(t, store, "Verified", "nes", "/roms/nes/v.nes", `{"gamarr":{"catalog":"verified"}}`)
	addBrowseItem(t, store, "Mismatch", "nes", "/roms/nes/m.nes", `{"gamarr":{"catalog":"mismatch"}}`)
	addBrowseItem(t, store, "Fresh", "nes", "/roms/nes/f.nes", "")
	// Pre-contract blob: not JSON at all. Must count as unmeasured, not error.
	addBrowseItem(t, store, "Legacy", "nes", "/roms/nes/l.nes", "not json")

	got := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Verdict: "verified"})
	if got.Total != 1 || got.Items[0].Title != "Verified" {
		t.Fatalf("verdict=verified total=%d", got.Total)
	}
	if got.Items[0].CatalogVerdict != "verified" {
		t.Fatalf("catalog_verdict=%q", got.Items[0].CatalogVerdict)
	}

	un := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Verdict: "unmeasured", Sort: "title"})
	if un.Total != 2 {
		t.Fatalf("verdict=unmeasured total=%d, want 2 (fresh + legacy blob)", un.Total)
	}
	for _, item := range un.Items {
		if item.CatalogVerdict != "" {
			t.Fatalf("unmeasured row carries verdict %q", item.CatalogVerdict)
		}
	}
}

func TestLibraryFormatFilter(t *testing.T) {
	store := newTestStore(t)
	addBrowseItem(t, store, "Zipped", "nes", "/roms/nes/Zipped (USA).zip", "")
	addBrowseItem(t, store, "Seven", "nes", "/roms/nes/Seven (USA).7z", "")
	addBrowseItem(t, store, "Gzip", "nes", "/roms/nes/Gzip (USA).nes.gz", "")
	addBrowseItem(t, store, "DiscDir", "psx", "/roms/psx/DiscDir (USA)", "")

	if got := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Format: "zip"}); got.Total != 1 || got.Items[0].Title != "Zipped" {
		t.Fatalf("format=zip total=%d (\".gz\" must not match \"z\", \".nes.gz\" is gz)", got.Total)
	}
	if got := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Format: ".7z"}); got.Total != 1 || got.Items[0].Title != "Seven" {
		t.Fatalf("format=.7z (leading dot tolerated) total=%d", got.Total)
	}
	if got := store.GetLibraryPage(LibraryQuery{Page: 1, PageSize: 50, Format: "gz"}); got.Total != 1 || got.Items[0].Title != "Gzip" {
		t.Fatalf("format=gz total=%d", got.Total)
	}
}

// The letter map's offsets are only useful if paging to an offset under
// Sort:"title" really lands on that bucket's first row — assert alignment
// by walking every letter, not by restating the fixture.
func TestLibraryLettersOffsetAlignment(t *testing.T) {
	store := newTestStore(t)
	titles := []string{
		"1942",               // digit → '#'
		"[Aladdin] Big Nose", // bracket → '#', and '[' > 'Z' in raw ASCII: the trap
		"Alpha Mission",
		"alter Ego", // lowercase must fold into 'A'
		"Batman",
		"Zelda II",
	}
	for i, title := range titles {
		addBrowseItem(t, store, title, "nes", fmt.Sprintf("/roms/nes/t%d.nes", i), "")
	}

	letters, total := store.LibraryLetters(LibraryQuery{})
	if total != len(titles) {
		t.Fatalf("letters total=%d, want %d", total, len(titles))
	}
	wantBuckets := map[string]int{"#": 2, "A": 2, "B": 1, "Z": 1}
	if len(letters) != len(wantBuckets) {
		t.Fatalf("letters=%v", letters)
	}
	if letters[0].Letter != "#" || letters[0].Offset != 0 {
		t.Fatalf("first bucket=%v, want # at offset 0", letters[0])
	}
	sum := 0
	for _, l := range letters {
		if wantBuckets[l.Letter] != l.Count {
			t.Fatalf("bucket %s count=%d, want %d", l.Letter, l.Count, wantBuckets[l.Letter])
		}
		if l.Offset != sum {
			t.Fatalf("bucket %s offset=%d, want %d", l.Letter, l.Offset, sum)
		}
		sum += l.Count

		// The alignment property itself: page in at the offset, the first
		// row must belong to this bucket.
		page := store.GetLibraryPage(LibraryQuery{Page: l.Offset + 1, PageSize: 1, Sort: "title"})
		if len(page.Items) != 1 {
			t.Fatalf("bucket %s: no row at offset %d", l.Letter, l.Offset)
		}
		first := page.Items[0].Title
		bucket := "#"
		if c := first[0]; (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			bucket = string(c &^ 0x20)
		}
		if bucket != l.Letter {
			t.Fatalf("offset %d serves %q (bucket %s), want bucket %s", l.Offset, first, bucket, l.Letter)
		}
	}

	// Letters respect the same filters as the page read.
	nesOnly, nesTotal := store.LibraryLetters(LibraryQuery{Q: "alpha"})
	if nesTotal != 1 || len(nesOnly) != 1 || nesOnly[0].Letter != "A" {
		t.Fatalf("filtered letters=%v total=%d", nesOnly, nesTotal)
	}
}

func TestLibraryFacets(t *testing.T) {
	store := newTestStore(t)
	a := addBrowseItem(t, store, "Verified Zip", "nes", "/roms/nes/a.zip", `{"gamarr":{"catalog":"verified"}}`)
	addBrowseItem(t, store, "Fresh Zip", "nes", "/roms/nes/b.zip", "")
	addBrowseItem(t, store, "Fresh Seven", "gb", "/roms/gb/c.7z", "")
	addBrowseItem(t, store, "Disc Dir", "psx", "/roms/psx/d", "")

	tagID, err := store.AddTag("shelf", "#123456")
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := store.AddItemTag(a, tagID); err != nil {
		t.Fatalf("AddItemTag: %v", err)
	}

	facets := store.LibraryFacets(LibraryQuery{})

	want := map[string]map[string]int{
		"platform":    {"nes": 2, "gb": 1, "psx": 1},
		"verdict":     {"verified": 1, "unmeasured": 3},
		"format":      {"zip": 2, "7z": 1}, // extension-less dir row absent, not ""
		"source_type": {"manual": 4},
		"tag":         {"shelf": 1},
	}
	for dim, buckets := range want {
		got := map[string]int{}
		for _, f := range facets[dim] {
			got[f.Value] = f.Count
		}
		if len(got) != len(buckets) {
			t.Fatalf("%s facets=%v, want %v", dim, got, buckets)
		}
		for v, n := range buckets {
			if got[v] != n {
				t.Fatalf("%s[%s]=%d, want %d", dim, v, got[v], n)
			}
		}
	}

	// Base filters narrow every dimension: under platform=nes the gb format
	// bucket must vanish (zero-count values are absent by construction).
	nes := store.LibraryFacets(LibraryQuery{PlatformSlug: "nes"})
	for _, f := range nes["format"] {
		if f.Value == "7z" {
			t.Fatalf("platform=nes facets still list 7z: %v", nes["format"])
		}
	}
	if len(nes["verdict"]) != 2 {
		t.Fatalf("platform=nes verdict facets=%v", nes["verdict"])
	}
}
