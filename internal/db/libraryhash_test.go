package db

import (
	"encoding/json"
	"testing"
)

func addRow(t *testing.T, s *JobStore, title, slug, path, meta string) int64 {
	t.Helper()
	id, err := s.AddLibraryItem(&LibraryItem{
		Title: title, Platform: slug, PlatformSlug: slug,
		FilePath: path, FileSize: 1, Source: "romm",
		SourceID: "romm:" + title, Metadata: meta,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem(%s): %v", title, err)
	}
	return id
}

// meta reads a row's metadata back as a generic tree, so an assertion can
// name a path without a struct that quietly ignores unexpected keys.
func meta(t *testing.T, s *JobStore, id int64) map[string]interface{} {
	t.Helper()
	item, err := s.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem(%d): %v", id, err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &out); err != nil {
		t.Fatalf("metadata not JSON: %v (%s)", err, item.Metadata)
	}
	return out
}

func at(t *testing.T, tree map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = tree
	for _, k := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func TestSaveLibraryHashesPreservesSiblings(t *testing.T) {
	s := newTestStore(t)
	// The row carries RomM's identity AND a disc-set marker: the hash writer
	// shares the $.gamarr object with SaveSetMarker, so clobbering a sibling
	// is the failure this test exists for.
	id := addRow(t, s, "Set Game", "psx", "/roms/psx/set",
		`{"romm":{"rom_id":7,"fs_name":"set"},"gamarr":{"set":{"id":"s1","total":2},"catalog":"verified"}}`)

	if err := s.SaveLibraryHashes(id, LibraryHashes{
		CRC: "AABBCCDD", MD5: "D3BFF827B6BE076D969FC1DC01602082", SHA1: "FFEE",
	}); err != nil {
		t.Fatalf("SaveLibraryHashes: %v", err)
	}

	tree := meta(t, s, id)
	if got := at(t, tree, "romm", "rom_id"); got != float64(7) {
		t.Errorf("$.romm.rom_id = %v, want RomM's identity untouched", got)
	}
	if got := at(t, tree, "gamarr", "set", "id"); got != "s1" {
		t.Errorf("$.gamarr.set.id = %v, want the disc-set marker untouched", got)
	}
	if got := at(t, tree, "gamarr", "catalog"); got != "verified" {
		t.Errorf("$.gamarr.catalog = %v, want it untouched", got)
	}
	if got := at(t, tree, "gamarr", "md5"); got != "d3bff827b6be076d969fc1dc01602082" {
		t.Errorf("$.gamarr.md5 = %v, want the lowercased hash", got)
	}
	if got := at(t, tree, "gamarr", "crc"); got != "aabbccdd" {
		t.Errorf("$.gamarr.crc = %v", got)
	}
	if at(t, tree, "gamarr", "hashed_at") == nil {
		t.Error("$.gamarr.hashed_at not stamped")
	}
}

func TestSaveLibraryHashesOnEmptyAndBrokenMetadata(t *testing.T) {
	for _, start := range []string{"", "{}", "not json at all", `{"romm":`} {
		s := newTestStore(t)
		id := addRow(t, s, "Game", "gb", "/roms/gb/g.gb", start)
		if err := s.SaveLibraryHashes(id, LibraryHashes{MD5: "abc"}); err != nil {
			t.Fatalf("SaveLibraryHashes(start=%q): %v", start, err)
		}
		if got := at(t, meta(t, s, id), "gamarr", "md5"); got != "abc" {
			t.Errorf("start=%q → $.gamarr.md5 = %v, want abc", start, got)
		}
	}
}

func TestSaveLibraryHashesUnheadered(t *testing.T) {
	s := newTestStore(t)
	id := addRow(t, s, "NES Game", "nes", "/roms/nes/g.7z", "{}")
	if err := s.SaveLibraryHashes(id, LibraryHashes{
		MD5: "whole", SHA1: "wholesha",
		Unh: &UnheaderedHashes{CRC: "PCRC", MD5: "PMD5", SHA1: "PSHA1", Header: "ines"},
	}); err != nil {
		t.Fatalf("SaveLibraryHashes: %v", err)
	}
	tree := meta(t, s, id)
	if got := at(t, tree, "gamarr", "unh", "md5"); got != "pmd5" {
		t.Errorf("$.gamarr.unh.md5 = %v", got)
	}
	if got := at(t, tree, "gamarr", "unh", "header"); got != "ines" {
		t.Errorf("$.gamarr.unh.header = %v", got)
	}
	// A nested object must survive as an object, not as an escaped string.
	if _, ok := at(t, tree, "gamarr", "unh").(map[string]interface{}); !ok {
		t.Errorf("$.gamarr.unh is not an object: %#v", at(t, tree, "gamarr", "unh"))
	}

	// No header → no unh key at all, rather than an empty one that reads as
	// "we looked and the payload hashes to nothing".
	id2 := addRow(t, s, "GB Game", "gb", "/roms/gb/g.gb", "{}")
	if err := s.SaveLibraryHashes(id2, LibraryHashes{MD5: "plain"}); err != nil {
		t.Fatalf("SaveLibraryHashes: %v", err)
	}
	if got := at(t, meta(t, s, id2), "gamarr", "unh"); got != nil {
		t.Errorf("$.gamarr.unh = %v on an unheadered row, want absent", got)
	}
}

func TestSaveLibraryHashesPairsPathsWithValues(t *testing.T) {
	// Regression guard for the clause/arg ordering trap: with all families
	// present, every value must land on its own path. A map-driven builder
	// pairs them wrongly and only when more than one is set.
	s := newTestStore(t)
	id := addRow(t, s, "All", "snes", "/roms/snes/a.sfc", "{}")
	if err := s.SaveLibraryHashes(id, LibraryHashes{
		CRC: "11111111", MD5: "22222222", SHA1: "33333333",
		Unh: &UnheaderedHashes{CRC: "44444444", MD5: "55555555", SHA1: "66666666", Header: "ines"},
	}); err != nil {
		t.Fatalf("SaveLibraryHashes: %v", err)
	}
	tree := meta(t, s, id)
	for _, c := range []struct {
		path []string
		want string
	}{
		{[]string{"gamarr", "crc"}, "11111111"},
		{[]string{"gamarr", "md5"}, "22222222"},
		{[]string{"gamarr", "sha1"}, "33333333"},
		{[]string{"gamarr", "unh", "crc"}, "44444444"},
		{[]string{"gamarr", "unh", "md5"}, "55555555"},
		{[]string{"gamarr", "unh", "sha1"}, "66666666"},
	} {
		if got := at(t, tree, c.path...); got != c.want {
			t.Errorf("%v = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestSaveLibraryHashesBySourceID(t *testing.T) {
	s := newTestStore(t)
	id := addRow(t, s, "By Source", "gba", "/roms/gba/g.gba", "{}")
	if err := s.SaveLibraryHashesBySourceID("romm:By Source", LibraryHashes{MD5: "abc"}); err != nil {
		t.Fatalf("SaveLibraryHashesBySourceID: %v", err)
	}
	if got := at(t, meta(t, s, id), "gamarr", "md5"); got != "abc" {
		t.Errorf("$.gamarr.md5 = %v", got)
	}
	if err := s.SaveLibraryHashesBySourceID("", LibraryHashes{MD5: "x"}); err != nil {
		t.Errorf("empty source_id should be a no-op, got %v", err)
	}
}

func TestListLibraryItemsNeedingHash(t *testing.T) {
	s := newTestStore(t)
	hashless := addRow(t, s, "Hashless", "nes", "/roms/nes/a.7z", "{}")
	empty := addRow(t, s, "EmptyMeta", "nes", "/roms/nes/b.7z", "")
	broken := addRow(t, s, "Broken", "nes", "/roms/nes/c.7z", "{oops")
	blank := addRow(t, s, "BlankHash", "nes", "/roms/nes/d.7z", `{"romm":{"md5":""}}`)
	addRow(t, s, "RommHashed", "nes", "/roms/nes/e.7z", `{"romm":{"md5":"aa"}}`)
	addRow(t, s, "RommSha1Only", "nes", "/roms/nes/f.7z", `{"romm":{"sha1":"bb"}}`)
	addRow(t, s, "OurHashed", "nes", "/roms/nes/g.7z", `{"gamarr":{"md5":"cc"}}`)
	addRow(t, s, "OurSha1Only", "nes", "/roms/nes/h.7z", `{"gamarr":{"sha1":"dd"}}`)
	other := addRow(t, s, "OtherPlatform", "gb", "/roms/gb/i.gb", "{}")
	// A release hash is NOT a content hash: the row still needs hashing.
	release := addRow(t, s, "ReleaseOnly", "nes", "/roms/nes/j.7z", `{"gamarr":{"release":{"md5":"ee"}}}`)

	want := map[int64]bool{hashless: true, empty: true, broken: true, blank: true, release: true}
	got := map[int64]bool{}
	for _, it := range s.ListLibraryItemsNeedingHash("nes", false) {
		got[it.ID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("nes scope returned %d rows, want %d (%v vs %v)", len(got), len(want), got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("row %d missing from the work list", id)
		}
	}

	all := s.ListLibraryItemsNeedingHash("", false)
	if len(all) != len(want)+1 {
		t.Errorf(`scope "" returned %d rows, want %d`, len(all), len(want)+1)
	}
	var sawOther bool
	for _, it := range all {
		if it.ID == other {
			sawOther = true
		}
	}
	if !sawOther {
		t.Error(`scope "" dropped the other platform's row`)
	}

	if n := len(s.ListLibraryItemsNeedingHash("nes", true)); n != 9 {
		t.Errorf("force returned %d rows, want every nes row (9)", n)
	}

	counts := s.CountLibraryItemsNeedingHash()
	if counts["nes"] != len(want) || counts["gb"] != 1 {
		t.Errorf("counts = %v, want nes=%d gb=1", counts, len(want))
	}
}

func TestListLibraryItemsNeedingHashSkipsPCAndMarkedRows(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AddLibraryItem(&LibraryItem{
		Title: "A PC Game", IsPC: true, PlatformSlug: "pc",
		FilePath: "/games/pc/g", Metadata: "{}", SourceID: "pc:1",
	}); err != nil {
		t.Fatal(err)
	}
	marked := addRow(t, s, "A Directory", "switch", "/roms/switch/dir", "{}")
	if err := s.MarkLibraryHashSkipped(marked, HashSkipDirectory); err != nil {
		t.Fatalf("MarkLibraryHashSkipped: %v", err)
	}

	if got := s.ListLibraryItemsNeedingHash("", false); len(got) != 0 {
		t.Errorf("work list = %+v, want empty (PC rows and marked rows excluded)", got)
	}
	if got := at(t, meta(t, s, marked), "gamarr", "hash_skipped"); got != HashSkipDirectory {
		t.Errorf("$.gamarr.hash_skipped = %v", got)
	}
	// force is the way back: a marked row is a candidate again.
	if got := s.ListLibraryItemsNeedingHash("switch", true); len(got) != 1 {
		t.Errorf("force returned %d rows, want the marked row back", len(got))
	}
	if n := s.CountLibraryItemsNeedingHash()["switch"]; n != 0 {
		t.Errorf("pending switch = %d, want 0 — a permanent skip must not park a remainder", n)
	}
}

func TestFindLibraryByHashAcrossFamilies(t *testing.T) {
	s := newTestStore(t)
	addRow(t, s, "Romm", "gb", "/roms/gb/a.gb", `{"romm":{"md5":"aaa","sha1":"aaas"}}`)
	addRow(t, s, "Ours", "gb", "/roms/gb/b.gb", `{"gamarr":{"md5":"bbb","sha1":"bbbs"}}`)
	addRow(t, s, "Headered", "nes", "/roms/nes/c.7z", `{"gamarr":{"md5":"ccc","unh":{"md5":"ddd","sha1":"ddds"}}}`)
	addRow(t, s, "Released", "gba", "/roms/gba/d.gba", `{"gamarr":{"release":{"md5":"eee","sha1":"eees"}}}`)

	for _, c := range []struct{ md5, sha1, want string }{
		{"aaa", "", "Romm"},
		{"", "AAAS", "Romm"},
		{"bbb", "", "Ours"},
		{"ccc", "", "Headered"},
		{"ddd", "", "Headered"},
		{"", "ddds", "Headered"},
		{"eee", "", "Released"},
		{"", "eees", "Released"},
	} {
		got := s.FindLibraryByHash(c.md5, c.sha1)
		if got == nil {
			t.Errorf("FindLibraryByHash(%q,%q) = nil, want %s", c.md5, c.sha1, c.want)
			continue
		}
		if got.Title != c.want {
			t.Errorf("FindLibraryByHash(%q,%q) = %s, want %s", c.md5, c.sha1, got.Title, c.want)
		}
	}
	if got := s.FindLibraryByHash("", ""); got != nil {
		t.Errorf("empty inputs matched %v", got)
	}
	if got := s.FindLibraryByHash("nothing", ""); got != nil {
		t.Errorf("unknown hash matched %v", got)
	}
}

func TestLibraryHashIndexCoversEveryFamilyDeterministically(t *testing.T) {
	s := newTestStore(t)
	first := addRow(t, s, "First", "nes", "/roms/nes/a.7z", `{"gamarr":{"md5":"shared","unh":{"md5":"unh1"}}}`)
	addRow(t, s, "Second", "nes", "/roms/nes/b.7z", `{"gamarr":{"md5":"shared"}}`)
	addRow(t, s, "Romm", "gb", "/roms/gb/c.gb", `{"romm":{"sha1":"rsha"}}`)
	addRow(t, s, "Release", "gb", "/roms/gb/d.gb", `{"gamarr":{"release":{"md5":"rel"}}}`)

	idx := s.LibraryHashIndex()
	for key, want := range map[string]string{
		"md5:shared": "First", "md5:unh1": "First",
		"sha1:rsha": "Romm", "md5:rel": "Release",
	} {
		got, ok := idx[key]
		if !ok {
			t.Errorf("index missing %q", key)
			continue
		}
		if got.Title != want {
			t.Errorf("index[%q] = %s, want %s", key, got.Title, want)
		}
	}
	// Two rows share md5:shared; the lower id must win every time, or which
	// row owns an ownership match varies between runs.
	if got := idx["md5:shared"]; got != nil && got.ID != first {
		t.Errorf("index[md5:shared].ID = %d, want the lower id %d", got.ID, first)
	}
}

func TestMigrateLibraryReleaseHashes(t *testing.T) {
	s := newTestStore(t)
	legacy := addRow(t, s, "Legacy", "snes", "/roms/snes/a.sfc",
		`{"romm":{"rom_id":3},"gamarr":{"md5":"legacymd5","sha1":"legacysha","catalog":"unknown"}}`)
	backfilled := addRow(t, s, "Backfilled", "snes", "/roms/snes/b.sfc",
		`{"gamarr":{"md5":"realmd5","hashed_at":"2026-08-20T00:00:00Z"}}`)
	untouched := addRow(t, s, "NoHash", "snes", "/roms/snes/c.sfc", `{"romm":{"md5":"rm"}}`)

	s.migrateLibraryReleaseHashes()

	tree := meta(t, s, legacy)
	if got := at(t, tree, "gamarr", "release", "md5"); got != "legacymd5" {
		t.Errorf("$.gamarr.release.md5 = %v, want the moved value", got)
	}
	if got := at(t, tree, "gamarr", "md5"); got != nil {
		t.Errorf("$.gamarr.md5 = %v, want it vacated for content hashes", got)
	}
	if got := at(t, tree, "gamarr", "catalog"); got != "unknown" {
		t.Errorf("sibling $.gamarr.catalog = %v", got)
	}
	if got := at(t, tree, "romm", "rom_id"); got != float64(3) {
		t.Errorf("sibling $.romm.rom_id = %v", got)
	}

	// A backfilled row is stamped, so the migration must leave it alone —
	// this is what makes a second run a no-op.
	s.migrateLibraryReleaseHashes()
	if got := at(t, meta(t, s, backfilled), "gamarr", "md5"); got != "realmd5" {
		t.Errorf("backfilled row's content hash = %v, want it untouched", got)
	}
	if got := at(t, meta(t, s, backfilled), "gamarr", "release"); got != nil {
		t.Errorf("backfilled row grew a release object: %v", got)
	}
	if got := at(t, meta(t, s, untouched), "romm", "md5"); got != "rm" {
		t.Errorf("hashless row changed: %v", got)
	}
}
