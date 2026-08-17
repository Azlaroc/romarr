package selection

import (
	"strings"
	"testing"

	"gamarr/internal/models"
	"gamarr/internal/search"
)

type bandTable map[string][2]int64

func (b bandTable) PlatformSizeBands() map[string][2]int64 { return b }

// armBands installs size definitions for one test. The resolver is
// process-global, so it must be undone.
func armBands(t *testing.T, bands bandTable) {
	t.Helper()
	search.SetBandStore(bands)
	t.Cleanup(func() { search.SetBandStore(nil) })
}

// TestBandDrivesRankingAndScoringIdentically is the cross-path guard. The
// defect this replaced had the size score reading one table and the ranking
// filter reading another, so the same candidate could score as ideally sized
// and still be rejected as implausible. Prepare and Select run here in the
// same order the scheduler runs them, against one armed definition.
func TestBandDrivesRankingAndScoringIdentically(t *testing.T) {
	armBands(t, bandTable{"psx": {1_000_000, 10_000_000}})

	cases := []struct {
		name      string
		size      int64
		wantKept  bool
		wantScore int
	}{
		{"below the floor", 500_000, false, 10},
		{"far below the floor", 1_000, false, 2},
		{"on the floor", 1_000_000, true, 15},
		{"mid band", 5_000_000, true, 15},
		{"on the ceiling", 10_000_000, true, 15},
		{"above the ceiling", 15_000_000, false, 10},
		{"far above the ceiling", 900_000_000, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			size := tc.size
			r := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = size })
			scored := search.ScoreResults([]*models.SearchResult{r}, "Game", "")
			if got := scored[0].ScoreBreakdown.SizeScore; got != tc.wantScore {
				t.Errorf("SizeScore=%d, want %d", got, tc.wantScore)
			}

			dec := Select([]*models.SearchResult{r}, SelectOpts{
				Query: "Game", MinScore: 0, Profile: romProfile(),
			})
			kept := dec.Action != ActionSkip
			if kept != tc.wantKept {
				t.Errorf("kept=%v, want %v (rejections: %v)", kept, tc.wantKept, dec.Rejected)
			}
		})
	}
}

// The band is applied exactly as stored, with no widening at the point of
// use. A stored floor that quietly rejected at half its value would mean the
// number on screen was not the number enforcing.
func TestBandEnforcedVerbatim(t *testing.T) {
	armBands(t, bandTable{"psx": {1_000_000, 10_000_000}})

	justUnder := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 999_999 })
	dec := Select([]*models.SearchResult{justUnder}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip {
		t.Error("a candidate one byte under the stored floor was kept")
	}
	if len(dec.Rejected) != 1 {
		t.Fatalf("rejections = %v, want one", dec.Rejected)
	}
	// The bound travels in the reason so an operator can see which number
	// did it without going to look the definition up.
	if !strings.Contains(dec.Rejected[0].Reason, "floor 1000000") {
		t.Errorf("reason %q does not name the bound that rejected it", dec.Rejected[0].Reason)
	}

	justOver := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 10_000_001 })
	dec = Select([]*models.SearchResult{justOver}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip {
		t.Error("a candidate one byte over the stored ceiling was kept")
	}
}

// A platform with no definition is not filtered on size at all. This is the
// case that broke before: a generic floor rejected every genuine dump for
// small-cartridge platforms, and the only fix available was a hand-added
// profile row per platform.
func TestUndefinedPlatformIsNotSizeFiltered(t *testing.T) {
	armBands(t, bandTable{"psx": {1_000_000, 10_000_000}})

	for _, size := range []int64{2_048, 2_987, 65_536, 40_000_000_000} {
		r := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) {
			r.Size = size
			r.PlatformSlug = "atari2600"
		})
		dec := Select([]*models.SearchResult{r}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
		if dec.Action == ActionSkip {
			t.Errorf("size %d rejected on an undefined platform: %v", size, dec.Rejected)
		}
	}
}

// An operator-typed bound is enforced exactly as typed, without the
// allowance a catalog-derived floor carries. The catalog measures
// uncompressed contents and needs headroom for archives; a number typed
// while looking at real files does not, and widening it would make every
// bound anyone has ever set mean something looser than it says.
func TestProfileBoundsAreNotWidened(t *testing.T) {
	armBands(t, bandTable{"psx": {1_000_000, 10_000_000}})

	prof := romProfile()
	prof.PreferredSizeMin = 300e6
	prof.PreferredSizeMax = 900e6

	under := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 299_999_999 })
	if dec := Select([]*models.SearchResult{under}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof}); dec.Action != ActionSkip {
		t.Error("candidate below an operator-set floor was kept")
	}
	over := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 900_000_001 })
	if dec := Select([]*models.SearchResult{over}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof}); dec.Action != ActionSkip {
		t.Error("candidate above an operator-set ceiling was kept")
	}
	ok := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 500e6 })
	if dec := Select([]*models.SearchResult{ok}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof}); dec.Action == ActionSkip {
		t.Errorf("candidate inside the operator-set band was rejected: %v", dec.Rejected)
	}
}

// A profile bound overrides only the end it sets. Zeroing the other end must
// not silently inherit the platform definition's opposite bound in a way the
// operator did not ask for — it inherits it deliberately, and this pins that.
func TestProfileBoundOverridesOneEndOnly(t *testing.T) {
	armBands(t, bandTable{"psx": {1_000_000, 10_000_000}})

	prof := romProfile()
	prof.PreferredSizeMin = 5_000_000 // ceiling still comes from the definition

	r := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 12_000_000 })
	if dec := Select([]*models.SearchResult{r}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof}); dec.Action != ActionSkip {
		t.Error("definition ceiling stopped applying once a profile floor was set")
	}
}
