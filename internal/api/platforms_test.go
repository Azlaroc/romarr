package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gamarr/internal/db"
)

// TestPlatformsEnumerateBeyondTheLibrary is the acceptance canary for the
// registry: before it, /api/platforms could only offer a platform that was
// already in the library, which is why a game could not be added for a
// platform RomArr had never acquired for.
func TestPlatformsEnumerateBeyondTheLibrary(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("GET", "/api/platforms", "")
	wantStatus(t, rr, http.StatusOK)
	body := decodeMap(t, rr)
	list, _ := body["platforms"].([]interface{})

	byID := map[string]string{}
	for _, raw := range list {
		p, _ := raw.(map[string]interface{})
		id, _ := p["id"].(string)
		name, _ := p["name"].(string)
		byID[id] = name
	}

	if byID["all"] != "All Platforms" {
		t.Error("the all-platforms sentinel that filter UIs depend on is gone")
	}
	// An empty library still enumerates the whole vocabulary.
	for slug, want := range map[string]string{
		"atari2600": "Atari 2600",
		"gbc":       "Game Boy Color",
		"psx":       "PS1",
		"pc":        "PC",
	} {
		if got := byID[slug]; got != want {
			t.Errorf("platform %q = %q, want %q", slug, got, want)
		}
	}
	if len(byID) < 30 {
		t.Errorf("enumerated %d platforms, want the whole registry", len(byID))
	}
	for slug, name := range byID {
		if name == "Unknown" {
			t.Errorf("platform %q is still labelled Unknown", slug)
		}
	}
}

func TestPlatformFullRowsAndPatch(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("GET", "/api/platforms?full=1", "")
	wantStatus(t, rr, http.StatusOK)
	var full struct {
		Platforms []struct {
			Slug          string `json:"slug"`
			IGDBSlug      string `json:"igdb_slug"`
			IGDBID        int    `json:"igdb_id"`
			RommFSSlug    string `json:"romm_fs_slug"`
			MediaClass    string `json:"media_class"`
			ConvertsToCHD bool   `json:"converts_to_chd"`
			DatAuthority  string `json:"dat_authority"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rows := map[string]int{}
	for i, p := range full.Platforms {
		rows[p.Slug] = i
	}
	genesis := full.Platforms[rows["genesis"]]
	if genesis.RommFSSlug != "genesis-slash-megadrive" {
		t.Errorf("genesis fs_slug = %q — that value decides an on-disk path", genesis.RommFSSlug)
	}
	if genesis.IGDBID == 0 {
		t.Error("genesis has no IGDB id")
	}
	// The DAT lane rides along so a platforms screen and the coverage table
	// can never disagree about which authority owns a platform.
	if got := full.Platforms[rows["psx"]].DatAuthority; got != "redump" {
		t.Errorf("psx dat_authority = %q, want redump", got)
	}
	if got := full.Platforms[rows["nes"]].DatAuthority; got != "no-intro" {
		t.Errorf("nes dat_authority = %q, want no-intro", got)
	}
	if !full.Platforms[rows["psx"]].ConvertsToCHD {
		t.Error("psx should convert to CHD")
	}

	// Sparse patch: one field moves, the identity fields do not.
	rr = e.do("PUT", "/api/platforms/atari2600", `{"acquisition_enabled":false}`)
	wantStatus(t, rr, http.StatusOK)
	after := decodeMap(t, rr)
	if after["acquisition_enabled"] != false {
		t.Errorf("acquisition_enabled = %v, want false", after["acquisition_enabled"])
	}
	if after["igdb_slug"] != "atari2600" || after["display_name"] != "Atari 2600" {
		t.Errorf("sparse patch disturbed identity fields: %v", after)
	}

	rr = e.do("PUT", "/api/platforms/atari2600", `{"media_class":"laserdisc"}`)
	wantStatus(t, rr, http.StatusBadRequest)
	rr = e.do("PUT", "/api/platforms/nonesuch", `{"display_name":"X"}`)
	wantStatus(t, rr, http.StatusNotFound)
	rr = e.do("GET", "/api/platforms/nonesuch", "")
	wantStatus(t, rr, http.StatusNotFound)
}

// TestPickerExcludesSystemDirectories keeps nonsense out of add dialogs: a
// directory that is not a platform is still reachable by the Library filter
// (it is merged back in once rows exist under it), but it is never offered as
// something to acquire for.
func TestPickerExcludesSystemDirectories(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("GET", "/api/platforms", "")
	wantStatus(t, rr, http.StatusOK)
	body := decodeMap(t, rr)
	list, _ := body["platforms"].([]interface{})
	for _, raw := range list {
		p, _ := raw.(map[string]interface{})
		if id, _ := p["id"].(string); id == "supporting_files" || id == "forwarders" {
			t.Errorf("picker offers %q, which is a directory, not a platform", id)
		}
	}

	// A library row under one still surfaces it, so the filter can reach it.
	if _, err := e.jobs.AddLibraryItem(&db.LibraryItem{
		Title: "Zelda forwarder", Platform: "Forwarders", PlatformSlug: "forwarders",
		FilePath: "/roms/forwarders/zelda.nsp",
	}); err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	rr = e.do("GET", "/api/platforms", "")
	if !strings.Contains(rr.Body.String(), "forwarders") {
		t.Error("a platform the library holds rows for must stay filterable")
	}
}

// TestPlatformDefaultProfileAssignment covers the control the Platforms page
// actually drives: which profile new titles on a platform inherit.
func TestPlatformDefaultProfileAssignment(t *testing.T) {
	e := newTestEnv(t, nil)

	rr := e.do("POST", "/api/quality-profiles", `{"name":"PSX CHD","format_preference":["chd"]}`)
	wantStatus(t, rr, http.StatusCreated)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("could not read profile id: %s", rr.Body.String())
	}

	rr = e.do("PUT", "/api/platforms/psx", `{"default_profile_id":`+itoa(created.ID)+`}`)
	wantStatus(t, rr, http.StatusOK)
	if got := decodeMap(t, rr)["default_profile_id"]; got != float64(created.ID) {
		t.Errorf("default_profile_id = %v, want %d", got, created.ID)
	}
	// It is the profile a title added for that platform now resolves to.
	if got := e.jobs.ResolveProfileForItem(0, "psx"); got.ID != created.ID {
		t.Errorf("psx resolves to %q, want the assigned profile", got.Name)
	}

	// 0 clears it: titles fall through to the global default again.
	rr = e.do("PUT", "/api/platforms/psx", `{"default_profile_id":0}`)
	wantStatus(t, rr, http.StatusOK)
	if got := e.jobs.ResolveProfileForItem(0, "psx"); got.ID == created.ID {
		t.Error("clearing the default left it assigned")
	}

	// A template cannot be a platform's default — it is cloned, not applied.
	var templateID int64
	for _, p := range e.jobs.GetQualityProfiles() {
		if p.IsTemplate {
			templateID = p.ID
		}
	}
	rr = e.do("PUT", "/api/platforms/psx", `{"default_profile_id":`+itoa(templateID)+`}`)
	wantStatus(t, rr, http.StatusBadRequest)
	rr = e.do("PUT", "/api/platforms/psx", `{"default_profile_id":999999}`)
	wantStatus(t, rr, http.StatusBadRequest)
}
