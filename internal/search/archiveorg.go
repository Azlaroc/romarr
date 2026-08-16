package search

import (
	"context"
	"log/slog"
	"time"

	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/sources"
	"gamarr/internal/sources/archiveorg"
	"gamarr/internal/sources/driver"
)

// ArchiveOrgPlatformSlugs returns the slugs the archive.org driver is
// configured to serve per the runtime registry.
func ArchiveOrgPlatformSlugs(reg *sources.Registry) []string {
	slugs := make([]string, 0, len(reg.ArchiveOrg.Items))
	for s := range reg.ArchiveOrg.Items {
		slugs = append(slugs, s)
	}
	return slugs
}

// SearchArchiveOrg is the fan-out adapter for the native Internet Archive
// driver. It exercises the sources/driver.SearchSource contract (the first
// driver to do so) and maps []driver.Release onto the existing
// []*models.SearchResult pipeline so it slots into the same post-processing
// (filter/score/library-dedup) as the other sources.
//
// It is inert unless the registry maps the platform to a collection item, so
// with the empty embedded defaults it is a no-op — enabling a platform is a
// registry edit, not a code change.
func SearchArchiveOrg(reg *sources.Registry, query, platformSlug string) []*models.SearchResult {
	if !reg.ArchiveOrgActive() {
		return nil
	}
	if IsCircuitOpen("archiveorg") {
		slog.Warn("archiveorg circuit open, skipping search")
		return nil
	}

	var src driver.SearchSource = archiveorg.New(
		reg.ArchiveOrg.Items,
		archiveorg.WithBaseURL(baseOrDefault(reg.ArchiveOrg.BaseURL, "https://archive.org")),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	releases, err := src.Search(ctx, driver.Query{Text: query, PlatformSlug: platformSlug})
	if err != nil {
		slog.Warn("archiveorg search error", "error", err)
		RecordSearchFail("archiveorg", err.Error())
		return nil
	}

	var results []*models.SearchResult
	for _, r := range releases {
		results = append(results, &models.SearchResult{
			Title:          r.Title,
			Size:           r.Size,
			SizeHuman:      HumanSize(r.Size),
			Indexer:        "Internet Archive",
			DownloadURL:    r.DownloadURL,
			GUID:           r.GUID,
			Platform:       platformNameForSlug(r.PlatformSlug),
			PlatformSlug:   r.PlatformSlug,
			SourceType:     "ddl",
			MD5:            r.MD5,
			SHA1:           r.SHA1,
			SafetyScore:    95,
			SafetyWarnings: []string{},
		})
	}
	if len(results) > 0 {
		RecordSearchSuccess("archiveorg")
	}
	return results
}

func baseOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func platformNameForSlug(slug string) string {
	for _, info := range platform.PlatformMap {
		if info.Slug == slug {
			return info.Name
		}
	}
	return "Unknown"
}
