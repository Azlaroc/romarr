package api

import (
	"testing"

	"gamarr/internal/db"
	"gamarr/internal/search"
)

// definitionFor pulls one platform out of the list response.
func definitionFor(t *testing.T, env *testEnv, slug string) map[string]interface{} {
	t.Helper()
	rr := env.do("GET", "/api/size-definitions", "")
	wantStatus(t, rr, 200)
	defs, _ := decodeMap(t, rr)["definitions"].([]interface{})
	for _, d := range defs {
		m, _ := d.(map[string]interface{})
		if m["platform_slug"] == slug {
			return m
		}
	}
	t.Fatalf("%q missing from the definitions list", slug)
	return nil
}

// Every platform with a catalog lane is listed even before anything has been
// imported for it. A screen that only showed platforms with stored rows would
// hide exactly the platforms an operator needs to reach.
func TestSizeDefinitionsListCoversEveryCatalogLane(t *testing.T) {
	env := newTestEnv(t, nil)

	rr := env.do("GET", "/api/size-definitions", "")
	wantStatus(t, rr, 200)
	defs, _ := decodeMap(t, rr)["definitions"].([]interface{})
	if len(defs) < 20 {
		t.Fatalf("got %d definitions, want one per shipped catalog lane", len(defs))
	}

	// atari2600 is the case that matters: it is absent from the download-side
	// platform list on purpose, so enumerating from there would drop it.
	got := definitionFor(t, env, "atari2600")
	if got["min_size"] != float64(0) || got["max_size"] != float64(0) {
		t.Errorf("unimported platform = %v/%v, want unbounded", got["min_size"], got["max_size"])
	}
	if got["has_catalog"] != false {
		t.Error("has_catalog should be false before anything is imported")
	}
}

func TestSizeDefinitionUpdateStoresManualBounds(t *testing.T) {
	env := newTestEnv(t, nil)
	t.Cleanup(func() { search.SetBandStore(nil) })

	rr := env.do("PUT", "/api/size-definitions/atari2600", `{"min_size":256,"max_size":65536}`)
	wantStatus(t, rr, 200)

	stored, ok := env.jobs.GetPlatformSize("atari2600")
	if !ok {
		t.Fatal("no definition stored")
	}
	if stored.MinSize != 256 || stored.MaxSize != 65536 {
		t.Errorf("stored = %d/%d, want 256/65536", stored.MinSize, stored.MaxSize)
	}
	if stored.Source != db.SizeSourceManual {
		t.Errorf("source = %q, want manual", stored.Source)
	}
}

// Omitting an end leaves it alone. Zeroing it would silently drop the other
// half of a band the operator did not mention.
func TestSizeDefinitionUpdateLeavesTheOmittedEndAlone(t *testing.T) {
	env := newTestEnv(t, nil)
	t.Cleanup(func() { search.SetBandStore(nil) })

	wantStatus(t, env.do("PUT", "/api/size-definitions/gb", `{"min_size":1000,"max_size":9000}`), 200)
	wantStatus(t, env.do("PUT", "/api/size-definitions/gb", `{"min_size":2000}`), 200)

	stored, _ := env.jobs.GetPlatformSize("gb")
	if stored.MinSize != 2000 || stored.MaxSize != 9000 {
		t.Errorf("stored = %d/%d, want 2000/9000", stored.MinSize, stored.MaxSize)
	}
}

func TestSizeDefinitionUpdateRejectsNonsense(t *testing.T) {
	env := newTestEnv(t, nil)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"inverted band", "/api/size-definitions/gb", `{"min_size":9000,"max_size":1000}`, 400},
		{"negative", "/api/size-definitions/gb", `{"min_size":-5}`, 400},
		{"no bounds at all", "/api/size-definitions/gb", `{}`, 400},
		// Uppercase and underscores are outside the slug charset; the value
		// stays URL-safe so the router, not the request builder, rejects it.
		{"bad slug", "/api/size-definitions/Atari_2600", `{"min_size":1}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantStatus(t, env.do("PUT", tc.path, tc.body), tc.want)
		})
	}
}

// Reset with no catalog to fall back on returns the platform to no limits
// rather than leaving the operator's numbers in place or storing zeroes.
func TestSizeDefinitionResetWithoutACatalogClearsTheRow(t *testing.T) {
	env := newTestEnv(t, nil)
	t.Cleanup(func() { search.SetBandStore(nil) })

	wantStatus(t, env.do("PUT", "/api/size-definitions/atari2600", `{"min_size":1024}`), 200)
	wantStatus(t, env.do("POST", "/api/size-definitions/atari2600/reset", ""), 200)

	if _, ok := env.jobs.GetPlatformSize("atari2600"); ok {
		t.Error("reset left a row behind with no catalog to justify it")
	}
}

// A manual override is retired by resetting to the authority's numbers.
func TestSizeDefinitionResetRestoresTheCatalogBand(t *testing.T) {
	env := newTestEnv(t, nil)
	t.Cleanup(func() { search.SetBandStore(nil) })

	if _, err := env.jobs.InsertDatSnapshot(db.DatSnapshotMeta{
		Authority: "no-intro", PlatformSlug: "gb", Version: "2026.08.01",
		SizeMin: 8192, SizeMax: 4194304, SizeP01: 32768, SizeP50: 131072, SizeP99: 2097152,
	}, nil); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	wantStatus(t, env.do("PUT", "/api/size-definitions/gb", `{"min_size":1,"max_size":2}`), 200)
	wantStatus(t, env.do("POST", "/api/size-definitions/gb/reset", ""), 200)

	stored, ok := env.jobs.GetPlatformSize("gb")
	if !ok {
		t.Fatal("reset stored nothing")
	}
	// 32768/8 and 2097152*2 — the allowances live in the derivation, so the
	// stored numbers are the ones that enforce.
	if stored.MinSize != 4096 || stored.MaxSize != 4194304 {
		t.Errorf("stored = %d/%d, want 4096/4194304", stored.MinSize, stored.MaxSize)
	}
	if stored.Source != db.SizeSourceCatalog || stored.SnapshotVersion != "2026.08.01" {
		t.Errorf("provenance = %q/%q", stored.Source, stored.SnapshotVersion)
	}
	if got := definitionFor(t, env, "gb"); got["has_catalog"] != true {
		t.Error("has_catalog should be true once a snapshot exists")
	}
}

// An edit has to reach the running selector, or the number on screen and the
// number enforcing drift apart until the next restart — the exact failure
// this whole design exists to prevent.
func TestSizeDefinitionEditIsVisibleToTheSelector(t *testing.T) {
	env := newTestEnv(t, nil)
	search.SetBandStore(env.jobs)
	t.Cleanup(func() { search.SetBandStore(nil) })

	if lo, hi := search.PlatformSizeRange("gb"); lo != 0 || hi != 0 {
		t.Fatalf("gb starts at [%d %d], want unbounded", lo, hi)
	}
	wantStatus(t, env.do("PUT", "/api/size-definitions/gb", `{"min_size":1000,"max_size":9000}`), 200)
	if lo, hi := search.PlatformSizeRange("gb"); lo != 1000 || hi != 9000 {
		t.Errorf("selector sees [%d %d] after the edit, want [1000 9000]", lo, hi)
	}

	wantStatus(t, env.do("POST", "/api/size-definitions/gb/reset", ""), 200)
	if lo, hi := search.PlatformSizeRange("gb"); lo != 0 || hi != 0 {
		t.Errorf("selector sees [%d %d] after the reset, want unbounded", lo, hi)
	}
}
