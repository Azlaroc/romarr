package selection

import (
	"strings"
	"testing"

	"gamarr/internal/db"
	"gamarr/internal/models"
)

// The "That!" contract: an ENFORCED want is a gate, not a boost. Unmet means
// wait — the policy pick must never be silently substituted — and the skip
// reason names the pinned dump so the decision log is legible.
func TestWantEnforceWaits(t *testing.T) {
	right := mk("Game (Europe).zip", 60, func(r *models.SearchResult) { r.MD5 = "AABB01" })
	wrong := mk("Game (USA).zip", 95, withHash)

	// Unmet: only non-matching candidates → skip, reason names the pin.
	dec := Select([]*models.SearchResult{wrong}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Want: Want{DumpName: "Game (Europe)", Hashes: []string{"aabb01"}, Enforce: true},
	})
	if dec.Action != ActionSkip {
		t.Fatalf("unmet enforced want must wait, got %v grabbing %d", dec.Action, len(dec.Grabs))
	}
	if !strings.Contains(dec.Reason, "Game (Europe)") || !strings.Contains(dec.Reason, "waiting") {
		t.Fatalf("skip reason must name the pin and the wait: %q", dec.Reason)
	}
	if len(dec.Rejected) != 1 || !strings.Contains(dec.Rejected[0].Reason, "pinned dump") {
		t.Fatalf("the policy candidate must be visibly rejected: %+v", dec.Rejected)
	}

	// Met: the matching candidate wins even against a higher score.
	dec = Select([]*models.SearchResult{wrong, right}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Want: Want{DumpName: "Game (Europe)", Hashes: []string{"aabb01"}, Enforce: true},
	})
	if len(dec.Grabs) != 1 || dec.Grabs[0].Result != right {
		t.Fatalf("enforced want must grab exactly the pinned dump, got %+v", dec.Grabs)
	}

	// Enforce off: unchanged boost semantics (TestWantHashBoost's law).
	dec = Select([]*models.SearchResult{wrong}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Want: Want{DumpName: "Game (Europe)", Hashes: []string{"aabb01"}},
	})
	if dec.Action != ActionGrab {
		t.Fatalf("un-enforced want stays a boost: %v (%s)", dec.Action, dec.Reason)
	}
}

// The Tetris regression (blaster#385): a pinned row whose TITLE is owned must
// not be consumed by the title check — the pin row exists precisely because a
// variant of the title is owned (an upgrade request). On 1225e1a this
// returned {Skip, "owned"} before the enforce gate ever ran, and the
// scheduler deleted the row.
func TestEnforcePinSurvivesTitleOwnership(t *testing.T) {
	ownedVariant := &db.LibraryItem{ID: 42, Title: "テトリス"}
	wrong := mk("Game (Japan).zip", 95, withHash)
	dec := Select([]*models.SearchResult{wrong}, SelectOpts{
		Query: "Tetris", MinScore: 0, Profile: romProfile(),
		Want:  Want{DumpName: "Tetris (World) (Rev 1)", Hashes: []string{"aabb01"}, Enforce: true},
		Owned: func(title, slug string) *db.LibraryItem { return ownedVariant },
	})
	if dec.Action != ActionSkip || dec.OwnedBy != nil {
		t.Fatalf("pinned row must not fulfill on title ownership: %v OwnedBy=%v (%s)",
			dec.Action, dec.OwnedBy, dec.Reason)
	}
	if !strings.Contains(dec.Reason, "waiting") {
		t.Fatalf("unmet pin over an owned title must wait: %q", dec.Reason)
	}
}

// A pinned upgrade proceeds despite title ownership when a candidate IS the
// pinned dump — grabbing the better copy is the pin's whole purpose.
func TestEnforcePinGrabsUpgradeOverOwnedTitle(t *testing.T) {
	right := mk("Tetris (World) (Rev 1).7z", 60, func(r *models.SearchResult) { r.MD5 = "AABB01" })
	dec := Select([]*models.SearchResult{right}, SelectOpts{
		Query: "Tetris", MinScore: 0, Profile: romProfile(),
		Want:  Want{DumpName: "Tetris (World) (Rev 1)", Hashes: []string{"aabb01"}, Enforce: true},
		Owned: func(title, slug string) *db.LibraryItem { return &db.LibraryItem{ID: 42, Title: "テトリス"} },
	})
	if dec.Action != ActionGrab || len(dec.Grabs) != 1 || dec.Grabs[0].Result != right {
		t.Fatalf("pinned upgrade must grab despite owned title: %v (%s)", dec.Action, dec.Reason)
	}
}

// Owned-by-pin: the pin's hash already in the library = genuinely fulfilled,
// and the verdict names the satisfying item.
func TestEnforcePinOwnedByHashFulfills(t *testing.T) {
	lib := &db.LibraryItem{ID: 7, Title: "Tetris (World) (Rev 1)"}
	cand := mk("Game (USA).zip", 95, withHash)
	for _, probe := range []string{"md5", "sha1"} {
		dec := Select([]*models.SearchResult{cand}, SelectOpts{
			Query: "Tetris", MinScore: 0, Profile: romProfile(),
			Want: Want{DumpName: "Tetris (World) (Rev 1)", Hashes: []string{"aabb01"}, Enforce: true},
			OwnedByHash: func(md5, sha1 string) *db.LibraryItem {
				if (probe == "md5" && md5 == "aabb01") || (probe == "sha1" && sha1 == "aabb01") {
					return lib
				}
				return nil
			},
		})
		if dec.Action != ActionSkip || dec.OwnedBy != lib {
			t.Fatalf("[%s] pin-hash-owned must fulfill: %v OwnedBy=%v (%s)",
				probe, dec.Action, dec.OwnedBy, dec.Reason)
		}
		if !strings.Contains(dec.Reason, "Tetris (World) (Rev 1)") {
			t.Fatalf("[%s] owned reason must name the item: %q", probe, dec.Reason)
		}
	}
}

// A hashless pin resolves fulfilled-vs-waiting by on-disk name.
func TestEnforceHashlessPinOwnedByName(t *testing.T) {
	lib := &db.LibraryItem{ID: 9, Title: "Game (Europe)"}
	cand := mk("Game (USA).zip", 95, withHash)
	byName := func(hit bool) func(string, string) *db.LibraryItem {
		return func(name, slug string) *db.LibraryItem {
			if hit && name == "Game (Europe)" {
				return lib
			}
			return nil
		}
	}
	dec := Select([]*models.SearchResult{cand}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Want:        Want{DumpName: "Game (Europe)", Enforce: true},
		OwnedByName: byName(true),
	})
	if dec.Action != ActionSkip || dec.OwnedBy != lib {
		t.Fatalf("hashless pin with the dump on disk must fulfill: %v OwnedBy=%v (%s)",
			dec.Action, dec.OwnedBy, dec.Reason)
	}
	dec = Select([]*models.SearchResult{cand}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Want:        Want{DumpName: "Game (Europe)", Enforce: true},
		OwnedByName: byName(false),
	})
	if dec.Action != ActionSkip || dec.OwnedBy != nil || !strings.Contains(dec.Reason, "waiting") {
		t.Fatalf("hashless pin unmet must wait: %v OwnedBy=%v (%s)", dec.Action, dec.OwnedBy, dec.Reason)
	}
}
