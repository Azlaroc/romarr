package selection

import (
	"strings"
	"testing"

	"gamarr/internal/db"
	"gamarr/internal/models"
)

func romProfile() *db.QualityProfile {
	return db.DefaultQualityProfile() // usa/world/europe/japan + chd-first + 1G1R
}

// mk builds an annotated result the way Prepare would (Attrs parsed).
func mk(title string, score int, mut ...func(*models.SearchResult)) *models.SearchResult {
	a := Parse(title)
	r := &models.SearchResult{Title: title, Score: score, Attrs: &a, PlatformSlug: "psx"}
	for _, m := range mut {
		m(r)
	}
	return r
}

func withHash(r *models.SearchResult) { r.MD5 = "d41d8cd98f00b204e9800998ecf8427e" }
func withIndexer(name string) func(*models.SearchResult) {
	return func(r *models.SearchResult) { r.Indexer = name }
}

// The live finding pinned on #235: a hash-carrying release must outrank a
// hashless one with a higher quality score (archive.org 76 vs Vimm 80).
func TestHashOutranksScore(t *testing.T) {
	ia := mk("American Pool (USA).zip", 76, withHash, withIndexer("Internet Archive"))
	vimm := mk("American Pool (USA)", 80, withIndexer("Vimm's Lair"))
	dec := Select([]*models.SearchResult{vimm, ia}, SelectOpts{Query: "American Pool", PlatformSlug: "psx", MinScore: 70, Profile: romProfile()})
	if dec.Action != ActionGrab {
		t.Fatalf("action = %v, want grab", dec.Action)
	}
	if dec.Grabs[0].Result != ia {
		t.Fatalf("winner = %q, want the hash-carrying archive.org release", dec.Grabs[0].Result.Title)
	}
}

func TestRegionTier(t *testing.T) {
	usa := mk("Game (USA).zip", 70, withHash)
	eur := mk("Game (Europe).zip", 90, withHash)
	jap := mk("Game (Japan).zip", 95, withHash)
	dec := Select([]*models.SearchResult{jap, eur, usa}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Grabs[0].Result != usa {
		t.Fatalf("winner = %q, want USA over Europe/Japan despite lower score", dec.Grabs[0].Result.Title)
	}
}

func TestFormatTier(t *testing.T) {
	chd := mk("Game (USA).chd", 70, withHash)
	zip := mk("Game (USA).zip", 90, withHash)
	dec := Select([]*models.SearchResult{zip, chd}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Grabs[0].Result != chd {
		t.Fatalf("winner = %q, want chd over zip", dec.Grabs[0].Result.Title)
	}
}

func TestEmptyRegionPriorityMeansNoFilter(t *testing.T) {
	prof := romProfile()
	prof.RegionPriority = nil // the seeded PC profile shape
	jap := mk("Game (Japan).zip", 80, withHash)
	dec := Select([]*models.SearchResult{jap}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof})
	if dec.Action != ActionGrab {
		t.Fatalf("empty region priority must not reject region-tagged results: %v (%s)", dec.Action, dec.Reason)
	}
}

func TestUnprofiledRegionRejected(t *testing.T) {
	kor := mk("Game (Korea).zip", 80, withHash)
	dec := Select([]*models.SearchResult{kor}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip || len(dec.Rejected) != 1 {
		t.Fatalf("Korea release under usa/world/europe/japan profile: action=%v rejected=%d", dec.Action, len(dec.Rejected))
	}
}

func TestClassFilters(t *testing.T) {
	prof := romProfile()
	cases := []struct {
		title string
		allow func()
	}{
		{"Game (USA) [b].zip", nil},
		{"[BIOS] Console (USA).zip", func() { prof.AllowBIOS = true }},
		{"Game (Proto).zip", func() { prof.AllowProto = true }},
		{"Game (Demo).zip", func() { prof.AllowDemo = true }},
	}
	for _, c := range cases {
		prof = romProfile()
		dec := Select([]*models.SearchResult{mk(c.title, 80, withHash)}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof})
		if dec.Action != ActionSkip {
			t.Errorf("%q: expected rejection under default profile", c.title)
		}
		if c.allow != nil {
			c.allow()
			dec = Select([]*models.SearchResult{mk(c.title, 80, withHash)}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof})
			if dec.Action != ActionGrab {
				t.Errorf("%q: expected grab with allow flag set, got %v (%s)", c.title, dec.Action, dec.Reason)
			}
		}
	}
}

func TestSizeSanity(t *testing.T) {
	// psx band floor is 1MB (platformSizeRange) — a 100KB "release" is a
	// placeholder; profile override tightens further.
	small := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 100e3 })
	dec := Select([]*models.SearchResult{small}, SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip {
		t.Fatalf("implausibly small release passed the filter")
	}
	prof := romProfile()
	prof.PreferredSizeMin = 300e6
	mid := mk("Game (USA).zip", 80, withHash, func(r *models.SearchResult) { r.Size = 100e6 })
	dec = Select([]*models.SearchResult{mid}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof})
	if dec.Action != ActionSkip {
		t.Fatalf("profile size floor not honored")
	}
}

func TestSourceTrustSubstringMatch(t *testing.T) {
	prof := romProfile()
	prof.RegionPriority = nil
	prof.FormatPreference = nil
	prof.Prefer1G1R = false
	prof.SourceRanking = []string{"Vimm"}
	vimm := mk("Game.zip", 70, withIndexer("Vimm's Lair"))
	other := mk("Game.zip", 70, withIndexer("SomeTracker"))
	dec := Select([]*models.SearchResult{other, vimm}, SelectOpts{Query: "Game", MinScore: 0, Profile: prof})
	if dec.Grabs[0].Result != vimm {
		t.Fatalf("SourceRanking entry 'Vimm' must match indexer \"Vimm's Lair\"")
	}
}

func TestDiscSetComplete(t *testing.T) {
	d1 := mk("Final Fantasy VII (USA) (Disc 1).cue", 80, withHash, withIndexer("Internet Archive"))
	d2 := mk("Final Fantasy VII (USA) (Disc 2).cue", 80, withHash, withIndexer("Internet Archive"))
	d3 := mk("Final Fantasy VII (USA) (Disc 3).cue", 80, withHash, withIndexer("Internet Archive"))
	dec := Select([]*models.SearchResult{d2, d3, d1}, SelectOpts{Query: "Final Fantasy VII", MinScore: 70, Profile: romProfile()})
	if dec.Action != ActionGrabSet {
		t.Fatalf("action = %v (%s), want grab_set", dec.Action, dec.Reason)
	}
	if len(dec.Grabs) != 3 {
		t.Fatalf("grabs = %d, want 3", len(dec.Grabs))
	}
	for i, g := range dec.Grabs {
		if g.DiscIndex != i+1 || g.DiscTotal != 3 {
			t.Errorf("grab %d: index/total = %d/%d", i, g.DiscIndex, g.DiscTotal)
		}
		if g.DiscSetID == "" || g.DiscSetID != dec.Grabs[0].DiscSetID {
			t.Errorf("grab %d: inconsistent DiscSetID", i)
		}
		if g.SetDir != "Final Fantasy VII (USA)" {
			t.Errorf("grab %d: SetDir = %q", i, g.SetDir)
		}
	}
}

func TestDiscSetIncompleteRejected(t *testing.T) {
	d1 := mk("Final Fantasy VII (USA) (Disc 1 of 3).cue", 80, withHash)
	d2 := mk("Final Fantasy VII (USA) (Disc 2 of 3).cue", 80, withHash)
	dec := Select([]*models.SearchResult{d1, d2}, SelectOpts{Query: "Final Fantasy VII", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip {
		t.Fatalf("incomplete 2-of-3 set must not be grabbed: %v", dec.Action)
	}
	found := false
	for _, rej := range dec.Rejected {
		if strings.Contains(rej.Reason, "incomplete disc set (2 of 3)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected incomplete-set rejection, got %+v", dec.Rejected)
	}
}

func TestDiscSetRegionNotMixed(t *testing.T) {
	// A Europe disc must not complete a USA set: different SetKey.
	d1 := mk("Final Fantasy VII (USA) (Disc 1 of 2).cue", 80, withHash)
	d2e := mk("Final Fantasy VII (Europe) (Disc 2 of 2).cue", 80, withHash)
	dec := Select([]*models.SearchResult{d1, d2e}, SelectOpts{Query: "Final Fantasy VII", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionSkip {
		t.Fatalf("cross-region discs must not merge into one set: %v (%s)", dec.Action, dec.Reason)
	}
}

func TestMinScoreGate(t *testing.T) {
	low := mk("Game (USA).zip", 40, withHash)
	dec := Select([]*models.SearchResult{low}, SelectOpts{Query: "Game", MinScore: 70, Profile: romProfile()})
	if dec.Action != ActionSkip || !strings.Contains(dec.Reason, "below min score") {
		t.Fatalf("min-score gate: %v (%s)", dec.Action, dec.Reason)
	}
}

func TestOwnedAndActiveGrabSkip(t *testing.T) {
	r := mk("Game (USA).zip", 90, withHash)
	dec := Select([]*models.SearchResult{r}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		Owned: func(title, slug string) *db.LibraryItem { return &db.LibraryItem{} },
	})
	if dec.Action != ActionSkip || dec.Reason != "owned" {
		t.Fatalf("owned skip: %v (%s)", dec.Action, dec.Reason)
	}
	dec = Select([]*models.SearchResult{r}, SelectOpts{
		Query: "Game", MinScore: 0, Profile: romProfile(),
		ActiveGrab: func(title, slug string) bool { return true },
	})
	if dec.Action != ActionSkip || dec.Reason != "already downloading" {
		t.Fatalf("active-grab skip: %v (%s)", dec.Action, dec.Reason)
	}
}

func TestDeterminism(t *testing.T) {
	in := func() []*models.SearchResult {
		return []*models.SearchResult{
			mk("Game (Europe).zip", 90, withHash),
			mk("Game (USA).zip", 70, withHash),
			mk("Game (USA) (Rev 1).zip", 70, withHash),
			mk("Game (Japan).zip", 95, withHash),
		}
	}
	first := Select(in(), SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
	for i := 0; i < 10; i++ {
		dec := Select(in(), SelectOpts{Query: "Game", MinScore: 0, Profile: romProfile()})
		if dec.Action != first.Action || dec.Grabs[0].Result.Title != first.Grabs[0].Result.Title {
			t.Fatalf("non-deterministic selection on run %d", i)
		}
	}
	// 1G1R: Rev 1 beats the base dump.
	if first.Grabs[0].Result.Title != "Game (USA) (Rev 1).zip" {
		t.Fatalf("winner = %q, want the Rev 1 USA dump", first.Grabs[0].Result.Title)
	}
}

func TestPrepareInvariantScoreEqualsTotal(t *testing.T) {
	pl := &Pipeline{
		ReleaseProfiles: func(title string) (int, bool) {
			if strings.Contains(title, "PREF") {
				return 15, false
			}
			return 0, false
		},
	}
	rs := []*models.SearchResult{
		{Title: "Game PREF (USA).zip", SourceType: "ddl", PlatformSlug: "psx"},
		{Title: "Game (USA).zip", SourceType: "ddl", PlatformSlug: "psx"},
	}
	out := pl.Prepare(rs, "Game", "psx", romProfile())
	for _, r := range out {
		if r.ScoreBreakdown == nil {
			t.Fatalf("%q: missing breakdown", r.Title)
		}
		if r.Score != r.ScoreBreakdown.Total {
			t.Errorf("%q: Score %d != Breakdown.Total %d", r.Title, r.Score, r.ScoreBreakdown.Total)
		}
		if r.Attrs == nil {
			t.Errorf("%q: missing attrs", r.Title)
		}
	}
	// The preferred-word adjustment must actually count now.
	var pref, plain *models.SearchResult
	for _, r := range out {
		if strings.Contains(r.Title, "PREF") {
			pref = r
		} else {
			plain = r
		}
	}
	if pref.ScoreBreakdown.ProfileAdjust != 15 || pref.Score <= plain.Score {
		t.Errorf("release-profile adjustment lost: adjust=%d pref=%d plain=%d",
			pref.ScoreBreakdown.ProfileAdjust, pref.Score, plain.Score)
	}
}

func TestPrepareBlocklistAndExclude(t *testing.T) {
	pl := &Pipeline{
		Blocklisted: func(url, hash string) bool { return url == "http://bad" },
		ReleaseProfiles: func(title string) (int, bool) {
			return 0, strings.Contains(title, "BANNED")
		},
	}
	rs := []*models.SearchResult{
		{Title: "Game (USA).zip", SourceType: "ddl", DownloadURL: "http://bad", PlatformSlug: "psx"},
		{Title: "Game BANNED (USA).zip", SourceType: "ddl", PlatformSlug: "psx"},
		{Title: "Game (USA).zip", SourceType: "ddl", PlatformSlug: "psx"},
	}
	out := pl.Prepare(rs, "Game", "psx", romProfile())
	if len(out) != 1 {
		t.Fatalf("prepared = %d, want 1 (blocklist + exclude drops)", len(out))
	}
}

// #281 live-fire: with Prefer1G1R, the revision tier grabbed the sequel over
// the queried game ("Spyro 2" → "Spyro - Year of the Dragon (Rev 1)"). Title
// identity must outrank revision.
func TestTitleRelevanceOutranksRevision(t *testing.T) {
	right := mk("Spyro 2 - Ripto's Rage! (USA).zip", 90, withHash)
	wrong := mk("Spyro - Year of the Dragon (USA) (Rev 1).zip", 90, withHash)
	dec := Select([]*models.SearchResult{wrong, right}, SelectOpts{Query: "Spyro 2", MinScore: 0, Profile: romProfile()})
	if dec.Grabs[0].Result != right {
		t.Fatalf("winner = %q, want the queried game over the Rev 1 sequel", dec.Grabs[0].Result.Title)
	}
}

// A token-exact clean title beats a covering superset even when the superset
// carries a later revision and a higher score.
func TestTitleExactBeatsSupersetRevision(t *testing.T) {
	exact := mk("Spyro the Dragon (USA).zip", 80, withHash)
	super := mk("Spyro - Year of the Dragon (USA) (Rev 1).zip", 95, withHash)
	dec := Select([]*models.SearchResult{super, exact}, SelectOpts{Query: "Spyro the Dragon", MinScore: 0, Profile: romProfile()})
	if dec.Grabs[0].Result != exact {
		t.Fatalf("winner = %q, want the exact-title release", dec.Grabs[0].Result.Title)
	}
}

// Identity sits above the hash tier: a hashless release of the queried game
// beats a hash-carrying release of a different game.
func TestTitleIdentityOutranksHash(t *testing.T) {
	right := mk("Spyro the Dragon (USA)", 80)
	wrong := mk("Spyro - Year of the Dragon (USA) (Rev 1).zip", 90, withHash)
	dec := Select([]*models.SearchResult{wrong, right}, SelectOpts{Query: "Spyro the Dragon", MinScore: 0, Profile: romProfile()})
	if dec.Grabs[0].Result != right {
		t.Fatalf("winner = %q, want the queried game despite missing hashes", dec.Grabs[0].Result.Title)
	}
}

// A query no candidate covers (numeral vs roman numeral) is neutral — every
// candidate ties at the no-cover sentinel and ranking degrades to the quality
// tiers; it must never reject.
func TestTitleNoCoverNeutral(t *testing.T) {
	ff7 := mk("Final Fantasy VII (USA).zip", 90, withHash)
	dec := Select([]*models.SearchResult{ff7}, SelectOpts{Query: "Final Fantasy 7", MinScore: 0, Profile: romProfile()})
	if dec.Action != ActionGrab || dec.Grabs[0].Result != ff7 {
		t.Fatalf("action = %v, want grab of the sole candidate", dec.Action)
	}
}
