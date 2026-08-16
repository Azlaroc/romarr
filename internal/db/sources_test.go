package db

import (
	"path/filepath"
	"testing"

	"gamarr/internal/sources"
)

func newSourcesStore(t *testing.T) *JobStore {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedRegistry() *sources.Registry {
	off := false
	return &sources.Registry{
		Version: 1,
		Vimm: sources.VimmSpec{
			BaseURL:         "https://vimm.example/",
			Enabled:         &off,
			PlatformSystems: map[string]string{"gba": "GBA"},
		},
		ArchiveOrg: sources.ArchiveOrgSpec{
			BaseURL: "https://archive.example",
			Items:   map[string]string{"gb": "item-gb"},
		},
	}
}

func TestSourceRegistrySeedAndHydrate(t *testing.T) {
	store := newSourcesStore(t)

	reg := store.LoadSourceRegistry(seedRegistry())
	if reg.Vimm.IsEnabled() {
		t.Error("vimm enabled=false must survive the seed")
	}
	if reg.Vimm.BaseURL != "https://vimm.example/" || reg.Vimm.PlatformSystems["gba"] != "GBA" {
		t.Errorf("vimm spec = %+v", reg.Vimm)
	}
	if !reg.ArchiveOrg.IsEnabled() || reg.ArchiveOrg.Items["gb"] != "item-gb" {
		t.Errorf("archiveorg spec = %+v", reg.ArchiveOrg)
	}
	if !reg.ArchiveOrgActive() || reg.VimmActive() {
		t.Errorf("active predicates: vimm=%v archiveorg=%v", reg.VimmActive(), reg.ArchiveOrgActive())
	}

	// Seed is once: a different seed must NOT overwrite existing rows.
	other := seedRegistry()
	other.Vimm.BaseURL = "https://other.example/"
	reg2 := store.LoadSourceRegistry(other)
	if reg2.Vimm.BaseURL != "https://vimm.example/" {
		t.Errorf("reseed overwrote rows: %q", reg2.Vimm.BaseURL)
	}
}

func TestUpdateSourceSpec(t *testing.T) {
	store := newSourcesStore(t)
	store.LoadSourceRegistry(seedRegistry())

	on := true
	newURL := "https://mirror.example/"
	if err := store.UpdateSourceSpec("vimm", SourcePatch{Enabled: &on, BaseURL: &newURL}); err != nil {
		t.Fatalf("UpdateSourceSpec: %v", err)
	}
	reg := store.HydrateSourceRegistry()
	if !reg.Vimm.IsEnabled() || reg.Vimm.BaseURL != newURL {
		t.Errorf("patched vimm = %+v", reg.Vimm)
	}
	// Sparse: mapping untouched.
	if reg.Vimm.PlatformSystems["gba"] != "GBA" {
		t.Errorf("mapping lost on sparse patch: %+v", reg.Vimm.PlatformSystems)
	}

	if err := store.UpdateSourceSpec("archiveorg", SourcePatch{Mapping: map[string]string{"snes": "item-snes"}}); err != nil {
		t.Fatalf("mapping patch: %v", err)
	}
	reg = store.HydrateSourceRegistry()
	if reg.ArchiveOrg.Items["snes"] != "item-snes" || len(reg.ArchiveOrg.Items) != 1 {
		t.Errorf("mapping replace = %+v", reg.ArchiveOrg.Items)
	}

	if err := store.UpdateSourceSpec("nope", SourcePatch{}); err == nil {
		t.Error("unknown source must error")
	}
}

func TestDDLSourcesCRUD(t *testing.T) {
	store := newSourcesStore(t)

	if rows := store.ListDDLSources(); len(rows) != 0 {
		t.Fatalf("fresh table rows = %v", rows)
	}
	id1, err := store.AddDDLSource("A", "https://a.example")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	id2, _ := store.AddDDLSource("B", "https://b.example")
	if id2 <= id1 {
		t.Fatalf("ids not increasing: %d then %d", id1, id2)
	}
	if n := store.CountDDLSources(); n != 2 {
		t.Fatalf("count = %d", n)
	}

	ok, err := store.DeleteDDLSource(id1)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if ok, _ := store.DeleteDDLSource(id1); ok {
		t.Error("double delete reported ok")
	}
	rows := store.ListDDLSources()
	if len(rows) != 1 || rows[0].ID != id2 || rows[0].Name != "B" || !rows[0].Enabled {
		t.Errorf("rows after delete = %+v", rows)
	}
}
