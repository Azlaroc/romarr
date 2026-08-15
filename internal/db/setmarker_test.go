package db

import (
	"encoding/json"
	"testing"
)

func addSetRow(t *testing.T, store *JobStore, sourceID, metadata string) int64 {
	t.Helper()
	id, err := store.AddLibraryItem(&LibraryItem{
		Title:        "Test Game (USA)",
		Platform:     "PS1",
		PlatformSlug: "psx",
		FilePath:     "/roms/psx/Test Game (USA)/" + sourceID,
		Source:       "ddl",
		SourceType:   "ddl",
		SourceID:     sourceID,
		Metadata:     metadata,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func TestSetMarkerRoundTrip(t *testing.T) {
	store := newTestStore(t)
	id := addSetRow(t, store, "set:set-rt1", "{}")

	mk := SetMarker{ID: "set-rt1", Total: 3, Have: []int{3, 1}, Degraded: true,
		RepairAttempts: 2, DegradedAt: "2026-08-15T09:00:00Z"}
	if err := store.SaveSetMarker(id, mk); err != nil {
		t.Fatalf("SaveSetMarker: %v", err)
	}

	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseSetMarker(item.Metadata)
	if !ok {
		t.Fatalf("marker missing after save: %q", item.Metadata)
	}
	if got.ID != "set-rt1" || got.Total != 3 || !got.Degraded || got.RepairAttempts != 2 {
		t.Errorf("marker = %+v", got)
	}
	if len(got.Have) != 2 || got.Have[0] != 1 || got.Have[1] != 3 {
		t.Errorf("Have = %v, want ascending [1 3]", got.Have)
	}
	if got.DegradedAt != "2026-08-15T09:00:00Z" || got.Exhausted || got.RepairedAt != "" {
		t.Errorf("marker = %+v", got)
	}
}

func TestSetMarkerMergePreservesForeignKeys(t *testing.T) {
	store := newTestStore(t)
	id := addSetRow(t, store, "set:set-mg1",
		`{"romm":{"search_key":"test game","md5":"aabb"},"gamarr":{"md5":"ccdd"}}`)

	if err := store.SaveSetMarker(id, SetMarker{ID: "set-mg1", Total: 2, Have: []int{1}, Degraded: true}); err != nil {
		t.Fatalf("SaveSetMarker: %v", err)
	}

	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &meta); err != nil {
		t.Fatalf("metadata unparseable after merge: %v", err)
	}
	romm, _ := meta["romm"].(map[string]interface{})
	if romm == nil || romm["search_key"] != "test game" || romm["md5"] != "aabb" {
		t.Errorf("$.romm clobbered: %v", meta["romm"])
	}
	gamarr, _ := meta["gamarr"].(map[string]interface{})
	if gamarr == nil || gamarr["md5"] != "ccdd" {
		t.Errorf("$.gamarr.md5 clobbered: %v", meta["gamarr"])
	}
	if _, ok := ParseSetMarker(item.Metadata); !ok {
		t.Error("set marker missing after merge")
	}
}

func TestSetMarkerToleratesInvalidMetadata(t *testing.T) {
	store := newTestStore(t)
	id := addSetRow(t, store, "set:set-iv1", "not-json{")

	if err := store.SaveSetMarker(id, SetMarker{ID: "set-iv1", Total: 2, Have: []int{1}, Degraded: true}); err != nil {
		t.Fatalf("SaveSetMarker over invalid metadata: %v", err)
	}
	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if mk, ok := ParseSetMarker(item.Metadata); !ok || !mk.Degraded {
		t.Errorf("marker not written over invalid metadata: %q", item.Metadata)
	}

	if mk, ok := ParseSetMarker("also-not-json"); ok {
		t.Errorf("ParseSetMarker on garbage = %+v, ok=true", mk)
	}
	if mk, ok := ParseSetMarker("{}"); ok {
		t.Errorf("ParseSetMarker on empty blob = %+v, ok=true", mk)
	}
}

func TestListDegradedSets(t *testing.T) {
	store := newTestStore(t)

	degradedID := addSetRow(t, store, "set:set-ls1", "{}")
	if err := store.SaveSetMarker(degradedID, SetMarker{ID: "set-ls1", Total: 2, Have: []int{1}, Degraded: true}); err != nil {
		t.Fatal(err)
	}
	completeID := addSetRow(t, store, "set:set-ls2", "{}")
	if err := store.SaveSetMarker(completeID, SetMarker{ID: "set-ls2", Total: 2, Have: []int{1, 2}, Degraded: false}); err != nil {
		t.Fatal(err)
	}
	addSetRow(t, store, "set:set-ls3", "{}")          // set row, no marker
	addSetRow(t, store, "set:set-ls4", "broken-json") // malformed blob must not error the query
	addSetRow(t, store, "ddl:/roms/psx/single.bin",   // non-set row with a degraded-looking blob
		`{"gamarr":{"set":{"id":"x","total":2,"have":[1],"degraded":true}}}`)

	got := store.ListDegradedSets()
	if len(got) != 1 {
		t.Fatalf("ListDegradedSets = %d rows, want 1", len(got))
	}
	if got[0].SourceID != "set:set-ls1" {
		t.Errorf("row = %q", got[0].SourceID)
	}
}

func TestFindLibraryBySourceID(t *testing.T) {
	store := newTestStore(t)
	id := addSetRow(t, store, "set:set-fs1", "{}")

	item := store.FindLibraryBySourceID("set:set-fs1")
	if item == nil || item.ID != id {
		t.Fatalf("FindLibraryBySourceID hit = %+v", item)
	}
	if store.FindLibraryBySourceID("set:nope") != nil {
		t.Error("miss should return nil")
	}
	if store.FindLibraryBySourceID("") != nil {
		t.Error("empty source_id should return nil")
	}
}

func TestUpdateLibraryItemFileSize(t *testing.T) {
	store := newTestStore(t)
	id := addSetRow(t, store, "set:set-fz1", "{}")

	if err := store.UpdateLibraryItemFileSize(id, 12345); err != nil {
		t.Fatalf("UpdateLibraryItemFileSize: %v", err)
	}
	item, err := store.GetLibraryItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if item.FileSize != 12345 {
		t.Errorf("FileSize = %d, want 12345", item.FileSize)
	}
}
