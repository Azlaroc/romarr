package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gamarr/internal/api"
	"gamarr/internal/config"
	"gamarr/internal/converto"
	"gamarr/internal/datsvc"
	"gamarr/internal/db"
	"gamarr/internal/download"
	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/qbit"
	"gamarr/internal/renamer"
	"gamarr/internal/romm"
	"gamarr/internal/rommconnect"
	"gamarr/internal/sabnzbd"
	"gamarr/internal/scheduler"
	"gamarr/internal/search"
	"gamarr/internal/selection"
	"gamarr/internal/supervise"
	"gamarr/internal/webhook"
)

var Version = "1.0.0"

// enabledWebhooks returns the enabled webhook configs stored in the database,
// plus the env-configured default webhook (WEBHOOK_URL) if one is set.
func enabledWebhooks(database *db.JobStore, cfg *config.Config) []webhook.WebhookConfig {
	configs := database.GetEnabledWebhooks()
	if cfg.WebhookURL != "" {
		configs = append(configs, webhook.WebhookConfig{
			Name:    "default",
			URL:     cfg.WebhookURL,
			Type:    cfg.WebhookType,
			Enabled: true,
			Events:  "*",
		})
	}
	return configs
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("gamarr starting", "version", Version)

	// Load config
	cfg := config.Load()

	// Ensure directories exist. DataDir holds the database, so the binary
	// cannot run without it. The download/library paths default to /data/*
	// volume mounts that only exist in the Docker deployment — downloads and
	// organizing fail loudly later if they're missing, so a warning suffices.
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		slog.Error("failed to create data directory", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}
	for _, dir := range []string{cfg.QBSavePath, cfg.GamesVaultPath, cfg.GamesRomsPath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			slog.Warn("could not create directory (downloads/organizing to it will fail)", "dir", dir, "error", err)
		}
	}

	// Probe the baked-in rom-converto engine at boot so a missing or broken
	// binary is visible immediately, not only when a conversion is first
	// attempted. Non-fatal: conversion features degrade gracefully without it.
	if cv := converto.New(cfg); cv.Available() {
		if v, err := cv.Version(context.Background()); err == nil {
			slog.Info("rom-converto ready", "version", v)
		} else {
			slog.Warn("rom-converto present but not runnable", "error", err)
		}
	} else {
		slog.Warn("rom-converto not found; ROM conversion unavailable", "bin", cfg.ConvertoBin)
	}

	// Initialize database
	database, err := db.New(fmt.Sprintf("%s/gamarr.db", cfg.DataDir))
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Runtime settings live in the DB (row wins over env, env is the
	// fresh-install default); legacy settings.json imports once.
	cfg.AttachSettings(database)
	download.ImportLegacySettings(cfg, database)

	// The source registry is DB-backed too: the env-resolved registry
	// (GAMARR_SOURCES_PATH/URL, VIMM_URL, ARCHIVEORG_URL) seeds it on first
	// boot only; afterwards the DB is authoritative and edits hot-apply.
	if os.Getenv("GAMARR_SOURCES_PATH") != "" || os.Getenv("GAMARR_SOURCES_URL") != "" {
		slog.Info("sources registry is database-managed; GAMARR_SOURCES_PATH/URL seed it on first boot only")
	}
	cfg.SetSourcesRegistry(database.LoadSourceRegistry(cfg.Sources))
	download.ImportLegacyDDLSources(cfg, database)

	// The platform registry is the one canonical vocabulary — display names,
	// category mappings, the RomM fs_slug and the CHD policy all resolve
	// through it. Attached before anything can look a platform up.
	platform.SetRegistry(database)
	database.BackfillLibraryPlatformNames()

	// The IA driver's item-metadata cache persists in the DB: restarts and
	// registry edits warm from stored listings instead of refetching against
	// archive.org's anonymous rate budget.
	search.SetIACache(database)

	// Candidate sizes are judged against per-platform definitions stored in
	// the DB. A platform without one is not size-filtered at all, so an
	// unlisted platform degrades to "unjudged" rather than "rejected".
	search.SetBandStore(database)

	// Initialize qBittorrent client
	qb := qbit.New(cfg.QBURL, cfg.QBUser, cfg.QBPass)

	// Initialize SABnzbd client (optional)
	var sab *sabnzbd.Client
	if cfg.HasSABnzbd() {
		sab = sabnzbd.New(cfg.SABnzbdURL, cfg.SABnzbdAPIKey)
		slog.Info("SABnzbd configured", "url", cfg.SABnzbdURL)
	}

	// Initialize download manager
	mgr := download.New(cfg, database, qb)

	// Set up webhook notification callback
	mgr.NotifyFunc = func(userID, notifType, title, message string) {
		configs := enabledWebhooks(database, cfg)
		if len(configs) > 0 {
			status := "info"
			if notifType == models.NotifTypeDownloadComplete || notifType == models.NotifTypeRequestCompleted {
				status = "completed"
			} else if notifType == models.NotifTypeRequestFailed {
				status = "failed"
			} else if notifType == models.NotifTypeRequestApproved {
				status = "approved"
			}
			webhook.Send(configs, webhook.Payload{
				Event:   notifType,
				Title:   title,
				Status:  status,
				Message: message,
			})
		}
	}

	// Initialize scheduler
	searchFn := func(query, platformSlug string) []*models.SearchResult {
		slug := platformSlug
		if slug == "all" {
			slug = ""
		}
		allResults := search.FanOut(context.Background(), search.BuildSources(cfg), query, slug)
		// Shared F4 preparation — the scheduler path finally gets the same
		// blocklist / release-profile / tier-sort treatment as /api/search.
		pl := &selection.Pipeline{
			Blocklisted:     database.IsBlocklisted,
			ReleaseProfiles: database.ApplyReleaseProfiles,
		}
		prof := database.ResolveQualityProfile(slug)
		results := pl.Prepare(allResults, query, platformSlug, prof)
		if results == nil {
			results = []*models.SearchResult{}
		}
		return results
	}

	downloadFn := func(g selection.Grab) (string, error) {
		result := g.Result
		// Zero-value DiscSet for single grabs — the job plumbing ignores it.
		set := download.DiscSet{ID: g.DiscSetID, Index: g.DiscIndex, Total: g.DiscTotal, Dir: g.SetDir}
		if result.SourceType == "ddl" {
			jobID := mgr.DownloadDDL(result.DownloadURL, result.VimmID, result.Title, result.Platform, result.PlatformSlug, result.IsPC, result.MD5, result.SHA1, set)
			return jobID, nil
		}
		if result.DownloadProtocol == "nzb" {
			return mgr.DownloadNZB(sab, result.DownloadURL, result.Title, result.Platform, result.PlatformSlug, result.IsPC, set)
		}
		url := result.DownloadURL
		if url == "" {
			url = result.MagnetURL
		}
		if url == "" && result.InfoHash != "" {
			url = fmt.Sprintf("magnet:?xt=urn:btih:%s", result.InfoHash)
		}
		return mgr.DownloadTorrent(download.TorrentSpec{
			URL:          url,
			InfoHash:     result.InfoHash,
			Title:        result.Title,
			Platform:     result.Platform,
			PlatformSlug: result.PlatformSlug,
			IsPC:         result.IsPC,
			TargetFile:   g.TargetFile,
			DiscSet:      set,
		})
	}

	webhookFn := func() []webhook.WebhookConfig {
		return enabledWebhooks(database, cfg)
	}

	sched := scheduler.New(cfg, database, searchFn, downloadFn, webhookFn)

	// DAT catalogs: fetch drivers, hand uploads, and the optional refresh
	// cadence. Built unconditionally — it is the handle the API uses for
	// manual refreshes and uploads even while the cadence stays off.
	datSvc := datsvc.New(cfg, database, datsvc.WithOnSnapshot(func(string) { search.RefreshBands() }))
	// Catalogs imported before size definitions existed would otherwise never
	// produce one: an unchanged refresh short-circuits before the import
	// path, so upgrading an installation whose catalogs are current would
	// leave every platform unbounded for good.
	datSvc.BackfillSizeDefinitions()

	// Configure circuit breaker
	search.InitHealthConfig(cfg.CircuitBreakerThreshold, cfg.CircuitBreakerTimeoutS)

	// Recover orphaned torrents and scan library in background
	if cfg.HasQBittorrent() {
		go mgr.RecoverOrphanedTorrents()
	}
	go mgr.RecoverOrphanedNZBDownloads()
	go mgr.ScanLibraryDirs()

	// Background loops (scheduler, torrent watcher, RomM sync, RomM Connect
	// notifier) live under one supervisor: it boots them here and re-arms
	// them when their runtime settings change. RomM notes: when sync is
	// configured RomM owns the ROM side of the library and the fs scanner
	// only walks the PC vault; the Connect notifier tells RomM which platform
	// folders changed after imports (ROMM_API_USER needs the tasks.run scope
	// — admin role in RomM 5.x).
	sup := supervise.New(cfg, sched, datSvc, supervise.Builders{
		Watcher: func() *download.Watcher { return download.NewWatcher(cfg, mgr) },
		RomMSync: func() *romm.Syncer {
			return romm.NewSyncer(
				romm.New(cfg.RomMURL, cfg.RomMAPIUser, cfg.RomMAPIPass),
				database,
				romm.SyncOptions{
					RomsRoot:         cfg.GamesRomsPath,
					Interval:         time.Duration(cfg.RomMSyncIntervalSeconds()) * time.Second,
					ExcludePlatforms: cfg.RomMExcludeList(),
					StateFile:        filepath.Join(cfg.DataDir, "romm_sync.json"),
				},
			)
		},
		Connect: func() (*rommconnect.Notifier, func(string)) {
			n := rommconnect.NewNotifier(
				rommconnect.New(cfg.RomMURL, cfg.RomMAPIUser, cfg.RomMAPIPass),
				rommconnect.NotifierOptions{
					FlushIdle: time.Duration(cfg.RomMConnectFlushIdleS) * time.Second,
					Tick:      time.Duration(cfg.RomMConnectTickS) * time.Second,
					OnEvent: func(event, detail string) {
						database.LogActivity(event, "RomM Connect", detail, "", nil)
					},
				},
			)
			return n, n.Enqueue
		},
	}, mgr.SetImportNotify)
	sup.StartAll()

	// Periodic cleanup: remove stale downloading jobs and old finished jobs
	// (>7d). The stale horizon tracks the disc-set timeout — deleting a
	// still-pending set member early would fire the sweep's degrade arm before
	// SELECTOR_SET_TIMEOUT_HOURS elapses.
	go func() {
		// Initial cleanup on startup. The disc-set sweep runs after job
		// recovery: it re-finalizes sets whose barrier a restart interrupted
		// and degrades sets that can no longer complete (F4).
		database.CleanupStaleDownloads(cfg.StaleJobHorizonHours())
		database.Cleanup(7)
		mgr.SweepDiscSets()

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			stale := database.CleanupStaleDownloads(cfg.StaleJobHorizonHours())
			old := database.Cleanup(7)
			if stale > 0 || old > 0 {
				slog.Info("periodic cleanup", "stale_downloads", stale, "old_jobs", old)
			}
			mgr.SweepDiscSets()
		}
	}()

	// Create HTTP router
	// On-demand bulk library rename (preview/apply). NotifyImport is
	// nil-safe, so this works whether or not RomM Connect is attached.
	ren := renamer.New(cfg, database, mgr.NotifyImport)

	router := api.NewRouter(cfg, mgr, sab, sched, sup, ren, datSvc)

	// Start HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sup.StopAll()
	ren.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}
