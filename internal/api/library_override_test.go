package api

import (
	"fmt"
	"testing"

	"gamarr/internal/db"
)

func TestDumpOverrideEndpoints(t *testing.T) {
	env := newTestEnv(t, nil)

	if _, err := env.jobs.InsertDatSnapshot(
		db.DatSnapshotMeta{Authority: "no-intro", PlatformSlug: "nes", Version: "2026.09.01"},
		[]db.DatGameRow{
			{Name: "Contra (USA)", BareTitle: "Contra", TotalSize: 1024,
				Roms: []db.DatRomRow{{Name: "Contra (USA).nes", Size: 1024, CRC: "aaaa0001", MD5: "m1", SHA1: "s1"}}},
			{Name: "Contra (Europe)", BareTitle: "Contra", TotalSize: 1024,
				Roms: []db.DatRomRow{{Name: "Contra (Europe).nes", Size: 1024, CRC: "bbbb0002", MD5: "m2", SHA1: "s2"}}},
		},
	); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	id := addDetailItem(t, env, "Contra", "nes", "/roms/nes/Contra (USA).zip", "")
	if err := env.jobs.SaveLibraryHashes(id, db.LibraryHashes{CRC: "aaaa0001"}); err != nil {
		t.Fatal(err)
	}

	// Pin the OTHER dump.
	rr := env.do("PUT", fmt.Sprintf("/api/library/%d/override", id),
		`{"dump_name": "Contra (Europe)", "hashes": ["M2", "s2"]}`)
	wantStatus(t, rr, 200)
	body := decodeMap(t, rr)
	if body["wishlist_id"].(float64) <= 0 {
		t.Fatalf("no wishlist row minted: %v", body)
	}

	// The detail names the pin: the pinned family row flags is_override,
	// while the owned dump keeps is_current — two different facts.
	rr = env.do("GET", fmt.Sprintf("/api/library/%d", id), "")
	body = decodeMap(t, rr)
	if body["override"] == nil {
		t.Fatal("detail carries no override block")
	}
	flags := map[string][2]bool{}
	for _, raw := range body["dat_group"].([]interface{}) {
		g := raw.(map[string]interface{})
		flags[g["name"].(string)] = [2]bool{g["is_current"].(bool), g["is_override"].(bool)}
	}
	if f := flags["Contra (Europe)"]; !f[1] || f[0] {
		t.Fatalf("pinned dump must flag is_override and not is_current: %v", flags)
	}
	if f := flags["Contra (USA)"]; !f[0] || f[1] {
		t.Fatalf("owned dump must keep is_current only: %v", flags)
	}

	// The wishlist row now carries the enforced identity for the scheduler.
	ov, ok := env.jobs.GetWishlistOverrideForTitle("Contra", "nes")
	if !ok || len(ov.OverrideHashes) != 2 || ov.OverrideHashes[0] != "m2" {
		t.Fatalf("override row: %+v ok=%v", ov, ok)
	}

	// Clear → 200; second clear → 404; detail block gone.
	rr = env.do("DELETE", fmt.Sprintf("/api/library/%d/override", id), "")
	wantStatus(t, rr, 200)
	rr = env.do("DELETE", fmt.Sprintf("/api/library/%d/override", id), "")
	wantStatus(t, rr, 404)
	rr = env.do("GET", fmt.Sprintf("/api/library/%d", id), "")
	if decodeMap(t, rr)["override"] != nil {
		t.Fatal("override block survived the clear")
	}
}
