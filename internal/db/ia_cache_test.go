package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestIAItemMetadataRoundTrip(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "ia.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	if _, _, ok := store.GetItemMetadata("itemA"); ok {
		t.Fatal("missing item reported ok")
	}

	at := time.Now().UTC().Truncate(time.Second)
	store.PutItemMetadata("itemA", []byte(`[{"name":"Game (USA).zip"}]`), at)
	raw, got, ok := store.GetItemMetadata("itemA")
	if !ok || string(raw) != `[{"name":"Game (USA).zip"}]` {
		t.Fatalf("round trip: ok=%v raw=%s", ok, raw)
	}
	if !got.Equal(at) {
		t.Fatalf("fetched_at = %v, want %v", got, at)
	}

	// Upsert overwrites.
	at2 := at.Add(time.Minute)
	store.PutItemMetadata("itemA", []byte(`[]`), at2)
	raw, got, _ = store.GetItemMetadata("itemA")
	if string(raw) != `[]` || !got.Equal(at2) {
		t.Fatalf("upsert: raw=%s at=%v", raw, got)
	}
}
