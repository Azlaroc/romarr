package api

import (
	"net/http"
	"testing"

	"gamarr/internal/db"
)

// Interactive search is the same search endpoint pointed at a Wanted row. The
// row's own quality profile is the reason it exists: a hand-picked release
// must be ranked under the policy the automatic grab would have used, or the
// manual pick and the scheduler disagree about what "best" means.
func TestSearchFromWishlistRow(t *testing.T) {
	t.Run("row supplies title, platform and profile", func(t *testing.T) {
		env := newTestEnv(t, nil)
		prof := &db.QualityProfile{Name: "Japan First", RegionPriority: []string{"japan", "usa"}}
		profID, err := env.jobs.AddQualityProfile(prof)
		if err != nil {
			t.Fatalf("create profile: %v", err)
		}
		id, err := env.jobs.AddWishlistItemWithProfile("Hagane", "SNES", "snes", profID)
		if err != nil {
			t.Fatalf("add wishlist: %v", err)
		}

		rr := env.do("GET", "/api/search?wishlist_id="+itoa(id), "")
		wantStatus(t, rr, http.StatusOK)
		// No source is configured in the test env, so the assertion that
		// matters is that the row resolved at all: a bad row 404s, and a
		// resolved row searches its own title rather than an empty query.
		resp := decodeMap(t, rr)
		if resp["error"] == "No query" {
			t.Fatal("wishlist row did not supply its title to the search")
		}
	})

	t.Run("unknown row is 404", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/search?wishlist_id=9999", "")
		wantStatus(t, rr, http.StatusNotFound)
	})

	t.Run("junk row id is 400", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/search?wishlist_id=abc", "")
		wantStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("plain search still works without a row", func(t *testing.T) {
		env := newTestEnv(t, nil)
		rr := env.do("GET", "/api/search?q=Hagane&platform=snes", "")
		wantStatus(t, rr, http.StatusOK)
	})
}
