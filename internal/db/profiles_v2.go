package db

import (
	"fmt"
	"log/slog"

	"gamarr/internal/platform"
)

// Profiles v2: a profile is a per-title choice, not a platform's property.
//
// Before this, a quality profile carried a platform_slug and a unique index
// allowed exactly one profile per platform — so "I want the Japanese revision
// of this one game" had nowhere to live, and adding a platform meant creating
// a profile for it first. Now:
//
//   - which profile a platform DEFAULTS to is a column on the platform row,
//   - a wishlist entry may carry its own profile id, overriding that default,
//   - and the first title added on a platform with no default silently
//     materializes one from a template, so adding a platform is not a step.
//
// Resolution is one function, ResolveProfileForItem, and it never returns nil.

// templateSeed ships the two classes every platform falls into. They are
// ordinary profile rows flagged is_template: editable, so an operator retunes
// what future platforms inherit rather than editing forty materialized copies
// — the TRaSH model, where the opinionated defaults are data.
var templateSeed = []QualityProfile{
	{
		Name: "Carts Default", IsTemplate: true, TemplateClass: "carts",
		FormatPreference: []string{"zip", "7z", "raw"},
		Prefer1G1R:       true,
	},
	{
		Name: "Discs Default", IsTemplate: true, TemplateClass: "discs",
		FormatPreference: defaultFormatPreference,
		Prefer1G1R:       true,
	},
}

// seedProfileTemplates inserts the templates once, on an install that has
// none. Guarded on the template rows themselves rather than on the table
// being empty, so an existing install gains them on upgrade.
func (s *JobStore) seedProfileTemplates() {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM quality_profiles WHERE is_template = 1").Scan(&count); err != nil || count > 0 {
		return
	}
	for i := range templateSeed {
		t := templateSeed[i]
		if _, err := s.AddQualityProfile(&t); err != nil {
			slog.Warn("seed profile template", "name", t.Name, "error", err)
		}
	}
}

// linkPlatformDefaults moves the legacy platform→profile mapping off the
// profile row and onto the platform registry, once. Guarded by a settings
// key rather than by "no platform has a default yet": an operator who clears
// a default deliberately must not have it resurrected on the next restart.
func (s *JobStore) linkPlatformDefaults() {
	if _, ok := s.GetSetting("profiles_v2_linked"); ok {
		return
	}
	linked := 0
	for _, p := range s.GetQualityProfiles() {
		if p.PlatformSlug == "" || p.IsTemplate {
			continue
		}
		row, ok := s.GetPlatformRow(p.PlatformSlug)
		if !ok || row.DefaultProfileID != 0 {
			continue
		}
		id := p.ID
		if err := s.PatchPlatform(p.PlatformSlug, PlatformPatch{DefaultProfileID: &id}); err != nil {
			slog.Warn("link platform default profile", "platform", p.PlatformSlug, "error", err)
			continue
		}
		linked++
	}
	if err := s.SetSetting("profiles_v2_linked", "1"); err != nil {
		slog.Warn("record profiles v2 link", "error", err)
		return
	}
	slog.Info("linked platform default profiles", "platforms", linked)
}

// ResolveProfileForItem returns the selection policy for one title:
//
//	the title's own profile → the platform's default → the legacy platform
//	column → the global default row → the lowest-id global row → the built-in
//
// The legacy step exists for an install whose link migration has not run yet;
// nothing writes that column any more. Never returns nil.
func (s *JobStore) ResolveProfileForItem(profileID int64, platformSlug string) *QualityProfile {
	if profileID > 0 {
		if p, err := s.GetQualityProfile(profileID); err == nil && p != nil && !p.IsTemplate {
			return p
		}
		// A profile deleted out from under a wishlist row falls through to the
		// platform default rather than failing the cycle.
		slog.Debug("wishlist profile missing, falling back", "profile_id", profileID, "platform", platformSlug)
	}
	if platformSlug != "" {
		if row, ok := s.GetPlatformRow(platformSlug); ok && row.DefaultProfileID != 0 {
			if p, err := s.GetQualityProfile(row.DefaultProfileID); err == nil && p != nil {
				return p
			}
		}
	}
	return s.ResolveQualityProfile(platformSlug)
}

// EnsurePlatformProfile returns a platform's default profile, materializing
// one from its class template if it has none. The second return reports
// whether this call created it — the caller surfaces that once, as a banner,
// so an operator learns a profile now exists without being asked to make one.
func (s *JobStore) EnsurePlatformProfile(platformSlug string) (*QualityProfile, bool) {
	if platformSlug == "" {
		return s.ResolveQualityProfile(""), false
	}
	row, ok := s.GetPlatformRow(platformSlug)
	if !ok {
		return s.ResolveQualityProfile(platformSlug), false
	}
	if row.DefaultProfileID != 0 {
		if p, err := s.GetQualityProfile(row.DefaultProfileID); err == nil && p != nil {
			return p, false
		}
	}

	tmpl := s.templateForPlatform(row)
	if tmpl == nil {
		return s.ResolveQualityProfile(platformSlug), false
	}

	clone := *tmpl
	clone.ID = 0
	clone.IsTemplate = false
	clone.TemplateClass = ""
	clone.IsDefault = false
	clone.PlatformSlug = ""
	clone.Name = s.uniqueProfileName(row.DisplayName + " Default")
	id, err := s.AddQualityProfile(&clone)
	if err != nil {
		slog.Warn("materialize platform profile", "platform", platformSlug, "error", err)
		return s.ResolveQualityProfile(platformSlug), false
	}
	if err := s.PatchPlatform(platformSlug, PlatformPatch{DefaultProfileID: &id}); err != nil {
		slog.Warn("attach materialized profile", "platform", platformSlug, "error", err)
	}
	clone.ID = id
	slog.Info("materialized default profile", "platform", platformSlug, "profile", clone.Name, "template", tmpl.Name)
	return &clone, true
}

// templateForPlatform picks the class template. The platform's DAT authority
// decides the class where it has a lane — carts from No-Intro, discs from
// Redump — which is the same assignment the catalog already makes, rather
// than a second opinion about what a platform is. The registry's media_class
// covers platforms with no lane, and carts is the last resort.
func (s *JobStore) templateForPlatform(row platform.Row) *QualityProfile {
	class := row.MediaClass
	for _, lane := range s.ListDatPlatforms() {
		if lane.PlatformSlug != row.Slug {
			continue
		}
		for _, a := range s.ListDatAuthorities() {
			if a.Name == lane.Authority && a.Kind != "" {
				class = a.Kind
			}
		}
	}

	profiles := s.GetQualityProfiles()
	for i := range profiles {
		if profiles[i].IsTemplate && profiles[i].TemplateClass == class {
			return &profiles[i]
		}
	}
	// pc has its own shipped profile rather than a template; anything else
	// without a class match (arcade, computer) inherits the cart shape, which
	// is the conservative one: whole-file dumps, no disc formats.
	for i := range profiles {
		if profiles[i].IsTemplate && profiles[i].TemplateClass == "carts" {
			return &profiles[i]
		}
	}
	return nil
}

// uniqueProfileName respects the table's UNIQUE(name): a second platform
// whose display name collides gets a numbered suffix rather than an insert
// error that would look like a materialization failure.
func (s *JobStore) uniqueProfileName(base string) string {
	taken := map[string]bool{}
	for _, p := range s.GetQualityProfiles() {
		taken[p.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
	return base + " " + fmt.Sprint(len(taken)+1)
}

// PlatformsUsingProfile lists the platforms that default to a profile, so a
// screen can say what deleting it would orphan.
func (s *JobStore) PlatformsUsingProfile(profileID int64) []string {
	var out []string
	for _, p := range s.PlatformRows() {
		if p.DefaultProfileID == profileID {
			out = append(out, p.Slug)
		}
	}
	return out
}
