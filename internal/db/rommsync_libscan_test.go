package db

import (
	"strings"
	"testing"
)

// 🔴 The composition contract between the two library producers, pinned:
// the RomM sync's full reconcile purges source='scan' (legacy) and deletes
// absent source='romm' rows — it must treat the scanner's rows as neither.
// A libscan-created row survives a full reconcile untouched, a scanner-
// adopted romm row keeps the $.gamarr identity the scanner banked on it,
// and a RomM pull arriving over a libscan-tracked path merges into that row
// (the adopt clause) instead of minting a duplicate.
func TestSyncRommItemsComposesWithLibscanRows(t *testing.T) {
	store := newTestStore(t)

	// A row the scanner created for an out-of-band arrival, hashes banked.
	libscanID, err := store.AddLibraryItem(&LibraryItem{
		Title: "Out Of Band", Platform: "NES", PlatformSlug: "nes",
		FilePath: "/roms/nes/Out Of Band (USA).nes",
		Source:   "libscan", SourceType: "libscan",
		SourceID: "libscan:/roms/nes/Out Of Band (USA).nes", Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLibraryHashes(libscanID, LibraryHashes{CRC: "aabbccdd", MD5: "11", SHA1: "22"}); err != nil {
		t.Fatal(err)
	}

	// A romm row the scanner adopted: verdict + hashes live under $.gamarr.
	adoptedID, err := store.AddLibraryItem(&LibraryItem{
		Title: "Synced", Platform: "NES", PlatformSlug: "nes",
		FilePath: "/roms/nes/Synced (USA).nes",
		Source:   "romm", SourceType: "romm", SourceID: "romm:1",
		Metadata: `{"romm":{"rom_id":1}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLibraryHashes(adoptedID, LibraryHashes{CRC: "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	store.SetLibraryCatalogStatusByID(adoptedID, CatalogVerified)

	// The pull: the synced row again, plus a RomM discovery of the very file
	// libscan already tracks.
	items := []RommSyncItem{
		{Item: LibraryItem{
			Title: "Synced", Platform: "NES", PlatformSlug: "nes",
			FilePath: "/roms/nes/Synced (USA).nes", SourceID: "romm:1",
			Metadata: `{"romm":{"rom_id":1,"crc":"deadbeef"}}`,
		}},
		{Item: LibraryItem{
			Title: "Out Of Band", Platform: "NES", PlatformSlug: "nes",
			FilePath: "/roms/nes/Out Of Band (USA).nes", SourceID: "romm:2",
			Metadata: `{"romm":{"rom_id":2}}`,
		}},
	}
	added, _, removed, err := store.SyncRommItems(items, true)
	if err != nil {
		t.Fatalf("SyncRommItems: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0 — the libscan row must be adopted, not duplicated", added)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 — no scanner row is the reconcile's to delete", removed)
	}

	ls, err := store.GetLibraryItem(libscanID)
	if err != nil {
		t.Fatal("libscan row deleted by the full reconcile")
	}
	if ls.Source != "libscan" {
		t.Errorf("libscan row source = %q, want libscan", ls.Source)
	}
	if !strings.Contains(ls.Metadata, `"crc":"aabbccdd"`) {
		t.Errorf("libscan row lost $.gamarr on adopt-merge: %s", ls.Metadata)
	}
	if !strings.Contains(ls.Metadata, `"rom_id":2`) {
		t.Errorf("adopt-merge did not bring $.romm identity: %s", ls.Metadata)
	}

	ad, err := store.GetLibraryItem(adoptedID)
	if err != nil {
		t.Fatal("adopted romm row deleted")
	}
	if !strings.Contains(ad.Metadata, `"crc":"deadbeef"`) || !strings.Contains(ad.Metadata, `"catalog":"verified"`) {
		t.Errorf("adopted romm row lost scanner-banked $.gamarr across a sync update: %s", ad.Metadata)
	}
}
