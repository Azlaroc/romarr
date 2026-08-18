package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestWishlistAddMaterializesAPlatformProfile is the acceptance clause
// "adding a title on a never-acquired platform requires zero prior setup",
// asserted at the door an operator actually uses.
func TestWishlistAddMaterializesAPlatformProfile(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("POST", "/api/wishlist", `{"title":"Dracula X","platform":"TurboGrafx-16","platform_slug":"tg16"}`)
	wantStatus(t, rr, http.StatusOK)
	body := decodeMap(t, rr)
	mat, _ := body["materialized_profile"].(map[string]interface{})
	if mat == nil {
		t.Fatal("first add on an untouched platform should report the profile it created")
	}
	if mat["name"] != "TurboGrafx-16 Default" {
		t.Errorf("materialized profile = %v", mat["name"])
	}

	// Told once, not every time.
	rr = e.do("POST", "/api/wishlist", `{"title":"Ys Book I&II","platform":"TurboGrafx-16","platform_slug":"tg16"}`)
	wantStatus(t, rr, http.StatusOK)
	if _, again := decodeMap(t, rr)["materialized_profile"]; again {
		t.Error("second add re-announced the profile")
	}
}

func TestWishlistPerTitleProfile(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("POST", "/api/quality-profiles", `{"name":"PSX Japan","region_priority":["japan","usa"],"format_preference":["chd"]}`)
	wantStatus(t, rr, http.StatusCreated)
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &created)
	if created.ID == 0 {
		t.Fatalf("could not read the new profile id from %s", rr.Body.String())
	}
	profileID := created.ID

	rr = e.do("POST", "/api/wishlist", `{"title":"Rondo","platform":"PS1","platform_slug":"psx","profile_id":`+itoa(profileID)+`}`)
	wantStatus(t, rr, http.StatusOK)
	id := int64(decodeMap(t, rr)["id"].(float64))

	var found bool
	for _, w := range e.jobs.GetWishlist() {
		if w.ID == id {
			found = true
			if w.ProfileID != profileID {
				t.Errorf("row profile_id = %d, want %d", w.ProfileID, profileID)
			}
		}
	}
	if !found {
		t.Fatal("wishlist row missing")
	}

	// Changing your mind, and clearing it again.
	rr = e.do("PATCH", "/api/wishlist/"+itoa(id), `{"profile_id":0}`)
	wantStatus(t, rr, http.StatusOK)
	for _, w := range e.jobs.GetWishlist() {
		if w.ID == id && w.ProfileID != 0 {
			t.Errorf("override survived the clear: %d", w.ProfileID)
		}
	}

	// Garbage is refused rather than stored.
	rr = e.do("POST", "/api/wishlist", `{"title":"X","platform_slug":"psx","profile_id":99999}`)
	wantStatus(t, rr, http.StatusBadRequest)
	rr = e.do("PATCH", "/api/wishlist/999999", `{"profile_id":0}`)
	wantStatus(t, rr, http.StatusNotFound)
}

// TestTemplatesAreNotSelectable: a template is cloned, never used directly —
// picking one for a title would silently couple that title to every future
// platform's defaults.
func TestTemplatesAreNotSelectable(t *testing.T) {
	e := newTestEnv(t, nil)

	var templateID int64
	for _, p := range e.jobs.GetQualityProfiles() {
		if p.IsTemplate {
			templateID = p.ID
		}
	}
	if templateID == 0 {
		t.Fatal("no templates seeded")
	}
	rr := e.do("POST", "/api/wishlist", `{"title":"X","platform_slug":"psx","profile_id":`+itoa(templateID)+`}`)
	wantStatus(t, rr, http.StatusBadRequest)
}

func TestQualityProfileGetAndInUseDelete(t *testing.T) {
	e := newTestEnv(t, nil)

	// Materialize one by adding a title, then try to delete it.
	rr := e.do("POST", "/api/wishlist", `{"title":"Alex Kidd","platform":"Sega Master System","platform_slug":"sms"}`)
	wantStatus(t, rr, http.StatusOK)
	mat := decodeMap(t, rr)["materialized_profile"].(map[string]interface{})
	id := int64(mat["id"].(float64))

	rr = e.do("GET", "/api/quality-profiles/"+itoa(id), "")
	wantStatus(t, rr, http.StatusOK)
	got := decodeMap(t, rr)
	if got["profile"] == nil {
		t.Error("GET by id returned no profile")
	}
	plats, _ := got["platforms"].([]interface{})
	if len(plats) != 1 || plats[0] != "sms" {
		t.Errorf("platforms using the profile = %v, want [sms]", plats)
	}

	rr = e.do("DELETE", "/api/quality-profiles/"+itoa(id), "")
	wantStatus(t, rr, http.StatusConflict)
	if !contains(rr.Body.String(), "sms") {
		t.Errorf("the refusal should name what would be orphaned: %s", rr.Body.String())
	}

	rr = e.do("GET", "/api/quality-profiles/999999", "")
	wantStatus(t, rr, http.StatusNotFound)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
