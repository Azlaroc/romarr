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
	"gamarr/internal/db"
	"gamarr/internal/download"
	"gamarr/internal/models"
	"gamarr/internal/monitor"
	"gamarr/internal/qbit"
	"gamarr/internal/renamer"
	"gamarr/internal/romm"
	"gamarr/internal/rommconnect"
	"gamarr/internal/sabnzbd"
	"gamarr/internal/scheduler"
	"gamarr/internal/search"
	"gamarr/internal/selection"
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

	// Initialize AI monitor
	mon := monitor.New(cfg, monitor.Callbacks{
		GetJobs: func() []struct {
			ID   string
			Data map[string]interface{}
		} {
			items := database.Items()
			result := make([]struct {
				ID   string
				Data map[string]interface{}
			}, len(items))
			for i, item := range items {
				result[i].ID = item.ID
				result[i].Data = item.Data
			}
			return result
		},
		QBReauth: func() bool {
			return cfg.HasQBittorrent() && qb.Login()
		},
		RunOrphanRecovery: func() { go mgr.RecoverOrphanedTorrents() },
	})
	mon.Start()

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
	sched.Start()

	// Configure circuit breaker
	search.InitHealthConfig(cfg.CircuitBreakerThreshold, cfg.CircuitBreakerTimeoutS)

	// Recover orphaned torrents and scan library in background
	if cfg.HasQBittorrent() {
		go mgr.RecoverOrphanedTorrents()
	}
	go mgr.RecoverOrphanedNZBDownloads()
	go mgr.ScanLibraryDirs()

	// Start torrent completion watcher
	watcher := download.NewWatcher(cfg, mgr)
	watcher.Start()

	// RomM library sync — when configured, RomM owns the ROM side of the
	// library and the fs scanner above only walks the PC vault.
	// Connect plane: tell RomM which platform folders changed after imports,
	// instead of leaving new files invisible until a manual scan. The account
	// behind ROMM_API_USER needs the tasks.run scope (admin role in RomM 5.x).
	if cfg.HasRomMAPI() && cfg.RomMConnectEnabled {
		connectNotifier := rommconnect.NewNotifier(
			rommconnect.New(cfg.RomMURL, cfg.RomMAPIUser, cfg.RomMAPIPass),
			rommconnect.NotifierOptions{
				FlushIdle: time.Duration(cfg.RomMConnectFlushIdleS) * time.Second,
				Tick:      time.Duration(cfg.RomMConnectTickS) * time.Second,
				OnEvent: func(event, detail string) {
					database.LogActivity(event, "RomM Connect", detail, "", nil)
				},
			},
		)
		connectNotifier.Start()
		mgr.ImportNotify = connectNotifier.Enqueue
	}

	var rommSync *romm.Syncer
	if cfg.HasRomMAPI() && cfg.RomMSyncEnabled {
		rommSync = romm.NewSyncer(
			romm.New(cfg.RomMURL, cfg.RomMAPIUser, cfg.RomMAPIPass),
			database,
			romm.SyncOptions{
				RomsRoot:         cfg.GamesRomsPath,
				Interval:         time.Duration(cfg.RomMSyncIntervalS) * time.Second,
				ExcludePlatforms: cfg.RomMExcludePlatforms,
				StateFile:        filepath.Join(cfg.DataDir, "romm_sync.json"),
			},
		)
		rommSync.Start()
	}

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
	// On-demand bulk library rename (preview/apply). The notify closure
	// defers the nil-check to call time: ImportNotify is only assigned when
	// RomM Connect is enabled.
	ren := renamer.New(cfg, database, func(fsSlug string) {
		if mgr.ImportNotify != nil {
			mgr.ImportNotify(fsSlug)
		}
	})

	router := api.NewRouter(cfg, mgr, mon, sab, sched, rommSync, ren)

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

	watcher.Stop()
	rommSync.Stop()
	ren.Stop()
	sched.Stop()
	mon.Stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}
