package db

import (
	"path/filepath"
	"testing"

	"gamarr/internal/platform"
)

// The plane is INERT in this PR — nothing reads it for policy yet — so these
// tests pin the data layer: seeding disciplines, the fold-in migration, and
// the resolution chain the later rewire will stand on.

func TestCollectionProfileSeedAndResolveDefaults(t *testing.T) {
	store := registryStore(t)

	profiles := store.GetCollectionProfiles()
	if len(profiles) != 4 {
		// Standard / Everything / Verified Only, plus the fold-in's
		// "Migrated: PC Default" (see the fresh-install test below).
		t.Fatalf("seeded profiles = %d, want 4", len(profiles))
	}
	std := profiles[0]
	if std.Name != "Standard — Licensed Retail" || !std.KeepWithoutEnglish || std.AllowProto {
		t.Fatalf("standard row wrong: %+v", std)
	}

	// An unassigned platform resolves to the built-in, which must be
	// indistinguishable from the shipped Standard row (minus the id).
	got := store.ResolveCollectionProfile("snes")
	builtin := DefaultCollectionProfile()
	if got.Name != std.Name || !equalStringSlices(got.RegionPriority, builtin.RegionPriority) ||
		got.AllowProto != builtin.AllowProto || got.VerifiedOnly != builtin.VerifiedOnly {
		t.Fatalf("unassigned platform resolves to %+v, want the Standard policy", got)
	}

	// A dangling id degrades to the built-in, never nil.
	bogus := int64(9999)
	if err := store.PatchPlatform("snes", PlatformPatch{CollectionProfileID: &bogus}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if got := store.ResolveCollectionProfile("snes"); got == nil || got.ID != 0 {
		t.Fatalf("dangling id resolves to %+v, want the built-in", got)
	}
}

// 🔴 The fold-in exists for exactly one live hazard: PC Default ships an
// EMPTY region_priority (no region filtering), and without a migrated
// profile the search rewire would silently start filtering pc under
// Standard's usa-first chain. A fresh install must fold pc and nothing else.
func TestFoldInMigratesPCAndOnlyPC(t *testing.T) {
	store := registryStore(t)

	migrated := 0
	for _, row := range store.PlatformRows() {
		if row.CollectionProfileID == 0 {
			continue
		}
		migrated++
		if row.Slug != "pc" {
			t.Errorf("platform %s folded to profile %d; only pc should differ from Standard", row.Slug, row.CollectionProfileID)
			continue
		}
		p, err := store.GetCollectionProfile(row.CollectionProfileID)
		if err != nil {
			t.Fatalf("pc profile: %v", err)
		}
		if p.Name != "Migrated: PC Default" || len(p.RegionPriority) != 0 {
			t.Errorf("pc folded to %+v, want empty region priority preserved", p)
		}
	}
	if migrated != 1 {
		t.Errorf("folded platforms = %d, want just pc", migrated)
	}
}

func TestFoldInIsGuardedAndFindOrCreates(t *testing.T) {
	store := registryStore(t)

	// A platform re-pointed at Standard must STAY there across restarts:
	// the guard key, not the data, decides whether the fold runs.
	zero := int64(0)
	if err := store.PatchPlatform("pc", PlatformPatch{CollectionProfileID: &zero}); err != nil {
		t.Fatalf("clear pc: %v", err)
	}
	store.foldInCollectionProfiles()
	if row, _ := store.GetPlatformRow("pc"); row.CollectionProfileID != 0 {
		t.Fatalf("guarded fold re-ran and re-linked pc to %d", row.CollectionProfileID)
	}

	// Dropping the guard re-runs the fold — and find-or-create means the
	// existing "Migrated: PC Default" row is reused, not duplicated.
	before := len(store.GetCollectionProfiles())
	store.DeleteSetting("collection_profiles_folded")
	store.foldInCollectionProfiles()
	if after := len(store.GetCollectionProfiles()); after != before {
		t.Fatalf("re-run minted profiles: %d -> %d", before, after)
	}
	if row, _ := store.GetPlatformRow("pc"); row.CollectionProfileID == 0 {
		t.Fatalf("unguarded fold did not re-link pc")
	}
}

// A customized platform default (a quality profile with its own region chain
// or category flags) folds into a migrated collection profile carrying the
// exact tuple.
func TestFoldInCarriesCustomTuple(t *testing.T) {
	store := registryStore(t)

	qp := &QualityProfile{
		Name:           "SNES Proto Hunting",
		RegionPriority: []string{"japan", "usa"},
		AllowProto:     true,
	}
	id, err := store.AddQualityProfile(qp)
	if err != nil {
		t.Fatalf("add qp: %v", err)
	}
	if err := store.PatchPlatform("snes", PlatformPatch{DefaultProfileID: &id}); err != nil {
		t.Fatalf("link qp: %v", err)
	}
	store.DeleteSetting("collection_profiles_folded")
	store.foldInCollectionProfiles()

	row, _ := store.GetPlatformRow("snes")
	if row.CollectionProfileID == 0 {
		t.Fatalf("snes did not fold")
	}
	p, err := store.GetCollectionProfile(row.CollectionProfileID)
	if err != nil {
		t.Fatalf("folded profile: %v", err)
	}
	if p.Name != "Migrated: SNES Proto Hunting" || !p.AllowProto || p.AllowDemo ||
		!equalStringSlices(p.RegionPriority, []string{"japan", "usa"}) {
		t.Fatalf("folded tuple wrong: %+v", p)
	}
	// Untouched fields carry Standard's semantics.
	if !p.KeepWithoutEnglish || p.VerifiedOnly || len(p.ExcludeCategories) != 1 {
		t.Fatalf("folded profile lost Standard defaults: %+v", p)
	}
}

func TestCollectionProfileCRUDAndDeleteRefusal(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	platform.SetRegistry(store)
	t.Cleanup(func() { platform.SetRegistry(nil) })

	p := &CollectionProfile{
		Name:             "USA Verified Carts",
		RegionPriority:   []string{"usa"},
		EnglishPreferred: true, KeepWithoutEnglish: false,
		VerifiedOnly:      true,
		ExcludeCategories: []string{"Applications", "Educational"},
	}
	id, err := store.AddCollectionProfile(p)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := store.GetCollectionProfile(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != p.Name || !got.VerifiedOnly || got.KeepWithoutEnglish ||
		!equalStringSlices(got.ExcludeCategories, p.ExcludeCategories) {
		t.Fatalf("round-trip lost fields: %+v", got)
	}

	got.AllowAftermarket = true
	if err := store.UpdateCollectionProfile(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if again, _ := store.GetCollectionProfile(id); !again.AllowAftermarket {
		t.Fatalf("update did not persist")
	}

	// Referenced profiles refuse deletion; re-pointing frees them.
	if err := store.PatchPlatform("gb", PlatformPatch{CollectionProfileID: &id}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := store.DeleteCollectionProfile(id); err == nil {
		t.Fatalf("delete succeeded on a referenced profile")
	}
	zero := int64(0)
	if err := store.PatchPlatform("gb", PlatformPatch{CollectionProfileID: &zero}); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if err := store.DeleteCollectionProfile(id); err != nil {
		t.Fatalf("delete after unassign: %v", err)
	}
}
