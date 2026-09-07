package selection

import (
	"strings"
	"testing"

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
