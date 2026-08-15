package db

import "testing"

func TestTokensContainTerm(t *testing.T) {
	cases := []struct {
		title, term string
		want        bool
	}{
		// The #287 class: banned term inside a longer word must NOT match.
		{"Incredibles, The - Rise of the Underminer (USA, Europe).zip", "miner", false},
		{"Harvest Moon - Friends of Mineral Town (USA).zip", "miner", false},
		{"Dora the Explorer - The Search for the Pirate Pig's Treasure (USA).zip", "RAT", false},
		{"Rugrats - Castle Capers (USA).zip", "rat", false},
		{"Operation Armored Liberty (USA).zip", "rat", false},

		// Whole tokens still match, case-insensitive, punctuation-adjacent.
		{"Totally Legit Game [KEYGEN].zip", "keygen", true},
		{"Game.Pack.RAT.edition", "rat", true},
		{"lab rat simulator", "RAT", true},
		{"Bitcoin Miner Deluxe", "miner", true},

		// Multi-word terms = contiguous token sequence.
		{"Cool.Crypto.Miner.2024", "crypto miner", true},
		{"Crypto Fans Love This Miner", "crypto miner", false},

		// Edge behavior.
		{"anything", "", true},
		{"", "rat", false},
	}
	for _, c := range cases {
		if got := tokensContainTerm(termTokens(c.title), c.term); got != c.want {
			t.Errorf("tokensContainTerm(%q, %q) = %v, want %v", c.title, c.term, got, c.want)
		}
	}
}

func TestApplyReleaseProfilesTokenSemantics(t *testing.T) {
	store := newTestStore(t) // migrateReleaseProfiles seeds the default profile

	// The two real titles #287 could never surface.
	for _, title := range []string{
		"Dora the Explorer - The Search for the Pirate Pig's Treasure (USA).zip",
		"Incredibles, The - Rise of the Underminer (USA, Europe).zip",
	} {
		if _, excluded := store.ApplyReleaseProfiles(title); excluded {
			t.Errorf("%q excluded by seeded profile, want pass", title)
		}
	}

	// Genuine junk with a standalone banned token still blocks.
	for _, title := range []string{
		"Game (USA) [keygen].zip",
		"Totally.Real.ROM.Pack.RAT.Free.Download",
	} {
		if _, excluded := store.ApplyReleaseProfiles(title); !excluded {
			t.Errorf("%q not excluded, want blocked", title)
		}
	}

	// Preferred-word scoring still adjusts, on token semantics.
	if _, err := store.AddReleaseProfile(&ReleaseProfile{
		Name: "prefer verified", Enabled: true,
		Preferred: []PreferredWord{{Word: "verified", Score: 7}},
	}); err != nil {
		t.Fatal(err)
	}
	if score, excluded := store.ApplyReleaseProfiles("Some Game (USA) [verified].zip"); excluded || score != 7 {
		t.Errorf("preferred scoring = (%d, %v), want (7, false)", score, excluded)
	}
	if score, _ := store.ApplyReleaseProfiles("Unverified Game (USA).zip"); score != 0 {
		t.Errorf("substring must not score: got %d, want 0", score)
	}
}
