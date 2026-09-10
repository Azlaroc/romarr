package db

import (
	"strings"
	"testing"
)

// The breadcrumb keeps the ORIGINAL title across a double retitle, so a
// revert goes all the way back rather than to the intermediate name.
func TestRetitleBreadcrumbKeepsOriginalAcrossDoubleApply(t *testing.T) {
	store := newTestStore(t)
	id, err := store.AddLibraryItem(&LibraryItem{
		Title: "Original", PlatformSlug: "gb", FilePath: "/roms/gb/x.zip",
		Metadata: "{}", Source: "scan", SourceID: "x",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.ApplyRetitleBatch([]RetitleChange{
		{LibraryID: id, PlatformSlug: "gb", Old: "Original", New: "Middle"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRetitleBatch([]RetitleChange{
		{LibraryID: id, PlatformSlug: "gb", Old: "Middle", New: "Final"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	item, _ := store.GetLibraryItem(id)
	if item.Title != "Final" {
		t.Errorf("title = %q", item.Title)
	}
	n, err := store.RevertRetitles()
	if err != nil || n != 1 {
		t.Fatalf("revert = %d, %v", n, err)
	}
	item, _ = store.GetLibraryItem(id)
	if item.Title != "Original" {
		t.Errorf("revert landed on %q, want the ORIGINAL title", item.Title)
	}
	if strings.Contains(item.Metadata, "retitle") {
		t.Errorf("breadcrumb survived revert: %q", item.Metadata)
	}
}

// A malformed change aborts the WHOLE batch — one transaction, all or
// nothing, so a partial sweep can never masquerade as a finished one.
func TestApplyRetitleBatchIsAtomic(t *testing.T) {
	store := newTestStore(t)
	id, err := store.AddLibraryItem(&LibraryItem{
		Title: "Keep", PlatformSlug: "gb", FilePath: "/roms/gb/k.zip",
		Metadata: "{}", Source: "scan", SourceID: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.ApplyRetitleBatch([]RetitleChange{
		{LibraryID: id, PlatformSlug: "gb", Old: "Keep", New: "Changed"},
		{LibraryID: 0, PlatformSlug: "gb", Old: "x", New: "y"}, // malformed
	}, nil)
	if err == nil {
		t.Fatal("malformed change did not error")
	}
	item, _ := store.GetLibraryItem(id)
	if item.Title != "Keep" {
		t.Errorf("title = %q, want the batch rolled back whole", item.Title)
	}
}

// Metadata that is not valid JSON must not sink the batch: the breadcrumb
// lands on a fresh object, matching every other json_set site's guard.
func TestApplyRetitleBatchSurvivesInvalidMetadata(t *testing.T) {
	store := newTestStore(t)
	id, err := store.AddLibraryItem(&LibraryItem{
		Title: "Bad Meta", PlatformSlug: "gb", FilePath: "/roms/gb/b.zip",
		Metadata: "not-json", Source: "scan", SourceID: "b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRetitleBatch([]RetitleChange{
		{LibraryID: id, PlatformSlug: "gb", Old: "Bad Meta", New: "Good Title"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	item, _ := store.GetLibraryItem(id)
	if item.Title != "Good Title" || !strings.Contains(item.Metadata, "retitle") {
		t.Errorf("row = %q / %q", item.Title, item.Metadata)
	}
}
