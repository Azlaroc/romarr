package search

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/sources"
	"gamarr/internal/sources/archiveorg"
	"gamarr/internal/sources/driver"
)

// The archive.org driver is memoized per registry instance so its 1h item
// metadata cache actually lives between searches. Constructing a fresh driver
// per call (the old behavior) refetched the mapped item's full metadata
// (~1MB for a 3.5k-file item) on EVERY query — an 87-row wishlist cycle burned
// 87 fetches against IA's ~60/hr anonymous rate limit and could trip the
// circuit breaker mid-cycle. The registry loads once per process (restart to
// reload), so pointer identity is the correct cache key: a new registry —
// including every hand-built one in tests — gets a fresh driver. The driver's
// internal cache is mutex-guarded, so sharing one instance across concurrent
// searches is safe.
var (
	iaDriverMu  sync.Mutex
	iaDriverReg *sources.Registry
	iaDriver    *archiveorg.Driver
	iaCache     archiveorg.CacheStore
)

// SetIACache attaches the persistent metadata store (the DB) to every driver
// the memo builds from here on, and drops the current memo so an already-built
// driver rebuilds with the cache. Called once at boot; also means a sources
// edit's intentional memo drop rebuilds a driver that warms from the DB
// instead of refetching.
func SetIACache(store archiveorg.CacheStore) {
	iaDriverMu.Lock()
	defer iaDriverMu.Unlock()
	iaCache = store
	iaDriver = nil
	iaDriverReg = nil
}

func archiveOrgDriver(reg *sources.Registry) *archiveorg.Driver {
	iaDriverMu.Lock()
	defer iaDriverMu.Unlock()
	if iaDriver == nil || iaDriverReg != reg {
		opts := []archiveorg.Option{
			archiveorg.WithBaseURL(baseOrDefault(reg.ArchiveOrg.BaseURL, "https://archive.org")),
		}
		if iaCache != nil {
			opts = append(opts, archiveorg.WithCache(iaCache))
		}
		iaDriver = archiveorg.New(reg.ArchiveOrg.Items, opts...)
		iaDriverReg = reg
	}
	return iaDriver
}

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
// It searches every platform. A registry mapping makes a platform's curated
// collection the preferred place to look; without one the driver searches
// archive.org itself. Enabling a platform is no longer a registry edit.
func SearchArchiveOrg(reg *sources.Registry, query, platformSlug string, regions []string) []*models.SearchResult {
	if !reg.ArchiveOrgActive() {
		return nil
	}
	if IsCircuitOpen("archiveorg") {
		slog.Warn("archiveorg circuit open, skipping search")
		return nil
	}

	var src driver.SearchSource = archiveOrgDriver(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	releases, err := src.Search(ctx, driver.Query{
		Text:         query,
		PlatformSlug: platformSlug,
		// The display name steers open-search relevance. It comes from the
		// platform registry here so the driver never holds a second copy of
		// the vocabulary.
		PlatformName: platform.DisplayName(platformSlug),
		Regions:      regions,
	})
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

// platformNameForSlug resolves the display name through the registry. It used
// to scan the Prowlarr category map, which has never carried atari2600, gbc,
// sms or gg — so every result on those platforms was labelled "Unknown", a
// string that reads as a failure when the slug was right all along.
func platformNameForSlug(slug string) string {
	return platform.DisplayName(slug)
}
