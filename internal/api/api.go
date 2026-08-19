// Package api implements Gamarr's HTTP layer: the chi router, authentication
// (sessions, API keys, OIDC), rate limiting, and all REST and UI handlers.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/collectionsvc"
	"gamarr/internal/config"
	"gamarr/internal/datsvc"
	"gamarr/internal/db"
	"gamarr/internal/download"
	"gamarr/internal/metadata"
	"gamarr/internal/models"
	"gamarr/internal/platform"
	"gamarr/internal/prune"
	"gamarr/internal/qbit"
	"gamarr/internal/renamer"
	"gamarr/internal/sabnzbd"
	"gamarr/internal/scheduler"
	"gamarr/internal/search"
	"gamarr/internal/selection"
	"gamarr/internal/supervise"
	"gamarr/internal/torznab"
	"gamarr/web"
)

// Server holds all API dependencies.
type Server struct {
	cfg       *config.Config
	mgr       *download.Manager
	sab       *sabnzbd.Client
	sessions  *SessionStore
	scheduler *scheduler.Scheduler
	oidc      *OIDCHandler
	sup       *supervise.Supervisor
	renamer   *renamer.Runner
	// prune is the declutter half of the collection plane.
	prune *prune.Runner
	dat   *datsvc.Service
	// coll answers set questions. Built here rather than threaded through
	// NewRouter for the same reason meta is: it needs only the config and the
	// store, and it holds no state a caller has to own.
	coll *collectionsvc.Service
	// meta is the metadata authority (IGDB). Built from config here rather
	// than threaded through NewRouter: it needs nothing but credentials, and
	// an unconfigured one is a valid state the settings screen reports.
	meta metadata.Provider
}

// NewRouter creates a new chi router with all routes.
func NewRouter(cfg *config.Config, mgr *download.Manager, sab *sabnzbd.Client, sched *scheduler.Scheduler, sup *supervise.Supervisor, ren *renamer.Runner, dat *datsvc.Service) http.Handler {
	sessions := NewSessionStore()
	oidcHandler := NewOIDCHandler(cfg, mgr.Jobs(), sessions)
	s := &Server{cfg: cfg, mgr: mgr, sab: sab, sessions: sessions, scheduler: sched, oidc: oidcHandler, sup: sup, renamer: ren, dat: dat}
	var metaOpts []metadata.IGDBOption
	if cfg.IGDBAPIBase != "" || cfg.IGDBAuthBase != "" {
		metaOpts = append(metaOpts, metadata.WithIGDBBase(cfg.IGDBAPIBase, cfg.IGDBAuthBase))
	}
	s.meta = metadata.NewIGDB(cfg.IGDBClientID, cfg.IGDBClientSecret, metaOpts...)
	s.coll = collectionsvc.New(cfg, mgr.Jobs())
	// The same import notifier the renamer gets: a declutter changes the same
	// tree a rename does, so it owes RomM the same rescan.
	s.prune = prune.New(cfg, mgr.Jobs(), s.coll, mgr.NotifyImport)

	// Rate limiter: 60-second window.
	rl := NewRateLimiter(60, map[string]int{
		"login":    20,
		"search":   120,
		"download": 60,
		"api":      300,
		"default":  600,
	})

	r := chi.NewRouter()

	// Outermost middleware (applied first).
	r.Use(logMiddleware)
	r.Use(func(next http.Handler) http.Handler { return requestSizeLimitMiddleware(next) })
	r.Use(func(next http.Handler) http.Handler { return securityHeadersMiddleware(next) })
	r.Use(corsMiddleware)
	r.Use(func(next http.Handler) http.Handler { return rateLimitMiddleware(rl, next) })
	r.Use(func(next http.Handler) http.Handler { return authMiddleware(cfg, mgr.Jobs(), sessions, next) })

	// UI — the React SPA embedded from web/dist. Content-hashed assets are
	// cached hard; every other (non-API) path serves the SPA shell so client
	// side deep links resolve (see handleSPA + the r.NotFound fallback below).
	assets := http.FileServer(http.FS(mustDistSub()))
	r.Handle("/assets/*", immutableCache(assets))
	r.Get("/favicon.svg", serveDistFile("favicon.svg", "image/svg+xml"))
	r.Get("/", s.handleSPA)
	r.NotFound(s.handleSPA)

	// Auth routes (exempt from auth middleware).
	r.Post("/api/login", handleLogin(cfg, mgr.Jobs(), sessions))
	r.Post("/api/login/totp", handleLoginTOTP(mgr.Jobs(), sessions))
	r.Post("/api/logout", handleLogout(sessions, mgr.Jobs()))
	r.Post("/api/register", handleRegister(mgr.Jobs(), sessions))
	r.Get("/api/auth/status", handleAuthStatus(mgr.Jobs(), sessions, cfg))

	// OIDC/SSO routes (exempt from auth middleware).
	r.Get("/api/oidc/login", s.handleOIDCLogin)
	r.Get("/api/oidc/callback", s.handleOIDCCallback)
	r.Get("/api/oidc/status", s.handleOIDCStatus)

	// Search & browse
	r.Get("/api/search", s.handleSearch)
	r.Get("/api/platforms", s.handlePlatforms)
	r.Get("/api/platforms/{slug}", s.handlePlatform)
	r.Get("/api/sources", s.handleSources)

	// Torznab indexer endpoint — lets Prowlarr / Sonarr / other *arr apps
	// query Gamarr as if it were a Torznab indexer. /api alias is the path
	// Prowlarr probes during indexer discovery.
	tz := torznab.New(cfg.TorznabAPIKey, s.searchForTorznab)
	r.Get("/torznab/api", tz.ServeHTTP)
	r.Get("/api", tz.ServeHTTPAlias)

	// Bulk operations — accept a list of ids, or operate on all jobs
	// matching the natural filter (failed for retry, active for cancel).
	r.Post("/api/admin/bulk/retry", s.handleBulkRetry)
	r.Post("/api/admin/bulk/cancel", s.handleBulkCancel)
	r.Post("/api/wishlist/bulk-delete", s.handleBulkDeleteWishlist)

	// OpenAPI 3.1 spec — AI agents / tooling can introspect this to
	// discover endpoints, request shapes, and response shapes without
	// prior knowledge of the codebase.
	r.Get("/api/openapi.json", s.handleOpenAPI)

	// Downloads
	r.Post("/api/download", s.handleDownload)
	r.Get("/api/downloads", s.handleDownloads)
	r.Delete("/api/downloads/torrent/{hash}", s.handleDeleteTorrent)
	r.Delete("/api/downloads/{jobID}", s.handleDeleteJob)
	r.Post("/api/downloads/clear", s.handleClearFinished)
	r.Post("/api/downloads/organize/{hash}", s.handleOrganizeTorrent)
	r.Post("/api/downloads/{jobID}/retry", s.handleRetryJob)

	// Library
	r.Get("/api/library", s.handleLibrary)
	r.Delete("/api/library/{id}", s.handleDeleteLibraryItem)
	r.Get("/api/library/sync/status", s.handleLibrarySyncStatus)
	r.Post("/api/library/sync", requireAdmin(s.handleLibrarySync))
	r.Get("/api/library/normalize/status", s.handleNormalizeStatus)
	r.Get("/api/library/normalize/preview/results", requireAdmin(s.handleNormalizeResults))
	r.Post("/api/library/normalize/preview", requireAdmin(s.handleNormalizePreview))
	r.Post("/api/library/normalize/apply", requireAdmin(s.handleNormalizeApply))
	r.Post("/api/library/normalize/stop", requireAdmin(s.handleNormalizeStop))

	// Declutter: the same set, read in the prune direction. Nothing moves
	// without a preview a human has seen, so apply is admin-only and the
	// status read is not.
	r.Get("/api/library/prune/status", s.handlePruneStatus)
	r.Get("/api/library/prune/preview/results", requireAdmin(s.handlePruneResults))
	r.Post("/api/library/prune/preview", requireAdmin(s.handlePrunePreview))
	r.Post("/api/library/prune/apply", requireAdmin(s.handlePruneApply))
	r.Post("/api/library/prune/stop", requireAdmin(s.handlePruneStop))

	// Wishlist
	r.Get("/api/wishlist", s.handleWishlist)
	r.Post("/api/wishlist", s.handleAddWishlist)
	r.Patch("/api/wishlist/{id}", s.handleUpdateWishlist)
	r.Delete("/api/wishlist/{id}", s.handleDeleteWishlist)

	// Activity
	r.Get("/api/activity", s.handleActivity)

	// Source registry (built-in drivers) + custom DDL sources (admin only —
	// source curation is configuration, matching /api/settings)
	r.Get("/api/source-registry", requireAdmin(s.handleSourceRegistry))
	r.Put("/api/source-registry/{name}", requireAdmin(s.handleUpdateSourceRegistry))
	r.Get("/api/ddl-sources", requireAdmin(s.handleDDLSources))
	r.Post("/api/ddl-sources", requireAdmin(s.handleAddDDLSource))
	r.Delete("/api/ddl-sources/{id}", requireAdmin(s.handleDeleteDDLSource))

	// DAT catalogs (authorities, assignments, refresh, hand-upload). Writes
	// and configuration are admin-only like the source registry; the status
	// and coverage reads are not, matching the normalize status endpoint.
	r.Get("/api/dat/authorities", requireAdmin(s.handleDatAuthorities))
	r.Patch("/api/dat/authorities/{name}", requireAdmin(s.handleUpdateDatAuthority))
	r.Post("/api/dat/authorities/{name}/refresh", requireAdmin(s.handleDatRefresh))
	r.Post("/api/dat/authorities/{name}/upload", requireAdmin(s.handleDatUpload))
	r.Put("/api/dat/platforms/{slug}", requireAdmin(s.handleUpdateDatPlatform))
	r.Get("/api/dat/status", s.handleDatStatus)
	r.Get("/api/dat/coverage", s.handleDatCoverage)
	r.Get("/api/metadata/providers", s.handleMetadataProviders)
	r.Get("/api/metadata/search", s.handleMetadataSearch)
	r.Get("/api/dat/games", s.handleDatGames)

	// The 1G1R set and the clone lists that shape it. The set is a read like
	// coverage; the lists are configuration, so admin-only like authorities.
	r.Get("/api/platforms/{slug}/set", s.handlePlatformSet)
	r.Get("/api/clonelists", requireAdmin(s.handleCloneLists))
	r.Get("/api/collection/targets", s.handleCollectionTargets)
	r.Post("/api/collection/sync", requireAdmin(s.handleCollectionSync))
	r.Post("/api/clonelists/refresh", requireAdmin(s.handleRefreshCloneLists))
	r.Get("/api/dat/games/{id}/roms", s.handleDatGameRoms)

	// The platform registry. Reads are open — every picker in the app needs
	// the vocabulary — while edits are configuration, so admin-only.
	r.Put("/api/platforms/{slug}", requireAdmin(s.handleUpdatePlatform))

	// Per-platform size definitions: the band candidates are judged against.
	// Configuration, so admin-only throughout. Reset rather than delete, for
	// the same reason the DAT assignments have no delete.
	r.Get("/api/size-definitions", requireAdmin(s.handleSizeDefinitions))
	r.Put("/api/size-definitions/{slug}", requireAdmin(s.handleUpdateSizeDefinition))
	r.Post("/api/size-definitions/{slug}/reset", requireAdmin(s.handleResetSizeDefinition))

	// Settings & config (admin only)
	r.Get("/api/settings", requireAdmin(s.handleGetSettings))
	r.Put("/api/settings", requireAdmin(s.handleUpdateSettings))
	r.Get("/api/settings/env", requireAdmin(s.handleSettingsEnv))
	r.Get("/api/config", s.handleConfig)
	r.Get("/api/stats", s.handleStats)
	r.Get("/api/health", s.handleHealth)

	// Connection tests (admin only)
	r.Post("/api/test/prowlarr", requireAdmin(s.handleTestProwlarr))
	r.Post("/api/test/qbittorrent", requireAdmin(s.handleTestQBittorrent))
	r.Post("/api/test/sabnzbd", requireAdmin(s.handleTestSABnzbd))
	r.Post("/api/test/romm", requireAdmin(s.handleTestRomM))

	// Source health
	r.Get("/api/sources/health", s.handleSourcesHealth)
	r.Post("/api/sources/{name}/reset", s.handleSourceReset)

	// Admin dashboard & user management
	r.Get("/api/admin/dashboard", requireAdmin(s.handleAdminDashboard))
	r.Get("/api/users", requireAdmin(handleListUsers(mgr.Jobs())))
	r.Patch("/api/users/{id}", requireAdmin(handleUpdateUser(mgr.Jobs())))
	r.Delete("/api/users/{id}", requireAdmin(handleDeleteUser(mgr.Jobs())))

	// TOTP / 2FA
	r.Post("/api/totp/setup", handleTOTPSetup(mgr.Jobs()))
	r.Post("/api/totp/verify", handleTOTPVerify(mgr.Jobs()))
	r.Post("/api/totp/disable", handleTOTPDisable(mgr.Jobs()))
	r.Get("/api/totp/status", handleTOTPStatus(mgr.Jobs()))

	// Invite codes (admin only)
	r.Get("/api/invites", requireAdmin(handleListInvites(mgr.Jobs())))
	r.Post("/api/invites", requireAdmin(handleCreateInvite(mgr.Jobs())))
	r.Delete("/api/invites/{id}", requireAdmin(handleDeleteInvite(mgr.Jobs())))

	// Requests

	// Notifications
	r.Get("/api/notifications", s.handleGetNotifications)
	r.Get("/api/notifications/unread", s.handleUnreadCount)
	r.Post("/api/notifications/{id}/read", s.handleMarkRead)
	r.Post("/api/notifications/read-all", s.handleMarkAllRead)

	// Webhooks
	r.Get("/api/webhooks", s.handleGetWebhooks)
	r.Post("/api/webhooks", s.handleAddWebhook)
	r.Delete("/api/webhooks/{id}", s.handleDeleteWebhook)
	r.Post("/api/webhooks/test", s.handleTestWebhook)

	// Import/Export
	r.Get("/api/export/library", s.handleExportLibrary)
	r.Get("/api/export/wishlist", s.handleExportWishlist)
	r.Post("/api/import/library", s.handleImportLibrary)
	r.Post("/api/import/wishlist", s.handleImportWishlist)
	r.Post("/api/import/csv", s.handleImportCSV)

	// Scheduler
	r.Get("/api/scheduler/status", s.handleSchedulerStatus)
	r.Post("/api/scheduler/run", s.handleSchedulerRun)

	// Play History
	r.Post("/api/history", s.handleAddHistory)
	r.Get("/api/history", s.handleGetHistory)
	r.Get("/api/history/stats", s.handleHistoryStats)
	r.Patch("/api/history/{id}", s.handleUpdateHistory)
	r.Delete("/api/history/{id}", s.handleDeleteHistory)

	// Duplicate detection
	r.Get("/api/library/check", s.handleCheckLibrary)

	// Quality Profiles
	r.Get("/api/quality-profiles", s.handleGetQualityProfiles)
	r.Get("/api/quality-profiles/{id}", s.handleGetQualityProfile)
	r.Post("/api/quality-profiles", s.handleCreateQualityProfile)
	r.Put("/api/quality-profiles/{id}", s.handleUpdateQualityProfile)
	r.Delete("/api/quality-profiles/{id}", s.handleDeleteQualityProfile)

	// Blocklist
	r.Get("/api/blocklist", s.handleGetBlocklist)
	r.Post("/api/blocklist", s.handleAddBlocklistEntry)
	r.Delete("/api/blocklist/clear", s.handleClearBlocklist)
	r.Delete("/api/blocklist/{id}", s.handleDeleteBlocklistEntry)

	// Release Profiles
	r.Get("/api/release-profiles", s.handleGetReleaseProfiles)
	r.Post("/api/release-profiles", s.handleCreateReleaseProfile)
	r.Put("/api/release-profiles/{id}", s.handleUpdateReleaseProfile)
	r.Delete("/api/release-profiles/{id}", s.handleDeleteReleaseProfile)

	// Manual Import (scan + import files)
	r.Post("/api/import/scan", s.handleScanImport)
	r.Post("/api/import/files", s.handleImportFiles)

	// Tags
	r.Get("/api/tags", s.handleGetTags)
	r.Post("/api/tags", s.handleCreateTag)
	r.Delete("/api/tags/{id}", s.handleDeleteTag)
	r.Post("/api/library/{id}/tags", s.handleAddItemTag)
	r.Delete("/api/library/{id}/tags/{tagID}", s.handleRemoveItemTag)
	r.Get("/api/library/{id}/tags", s.handleGetItemTags)

	// Backup / Restore
	r.Get("/api/backup", requireAdmin(s.handleBackupDownload))
	r.Post("/api/backup/create", requireAdmin(s.handleBackupCreate))
	r.Get("/api/backup/list", requireAdmin(s.handleBackupList))
	r.Post("/api/restore", requireAdmin(s.handleRestore))

	// Release Calendar
	r.Get("/api/calendar", s.handleCalendar)
	r.Get("/api/calendar/recent", s.handleCalendarRecent)

	// Metrics
	r.Get("/metrics", s.handleMetrics)

	return r
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusNotFound, "OIDC not configured")
		return
	}
	s.oidc.HandleLogin(w, r)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusNotFound, "OIDC not configured")
		return
	}
	s.oidc.HandleCallback(w, r)
}

func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":  s.cfg.HasOIDC(),
		"provider": s.cfg.OIDCProviderName,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"success": false, "error": msg})
}

// decodeJSONBody decodes a JSON request body into v. An empty body is
// accepted and leaves v unchanged (handlers apply their own defaults);
// a body that exceeded the requestSizeLimitMiddleware cap writes 413;
// any other malformed JSON writes a 400 error response and returns false.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "Request body too large")
		return false
	}
	writeError(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
	return false
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		host := r.Host

		// Reflect origin only if it matches the Host header (same-origin protection).
		if origin != "" && strings.Contains(origin, host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// For API-key-only requests, allow any origin (external clients / PWA).
		if origin != "" && (r.Header.Get("X-Api-Key") != "" || r.URL.Query().Get("apikey") != "") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key")
		w.Header().Set("Vary", "Origin")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if !strings.HasPrefix(r.URL.Path, "/api/downloads") {
			slog.Debug("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration", time.Since(start).String())
		}
	})
}

// handleSPA serves the React shell (web.IndexHTML) for "/" and every
// unmatched non-API path, so client-side routes deep-link correctly. The API
// namespace keeps returning JSON 404s instead of the HTML shell.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/torznab/") {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if len(web.IndexHTML) == 0 {
		http.Error(w, "frontend not built — run `npm run build` in web/frontend", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(web.IndexHTML)
}

// mustDistSub returns the embedded dist/ subtree. Fails loudly at startup if
// the embed is malformed (impossible with a well-formed build).
func mustDistSub() fs.FS {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		panic("web dist assets missing from binary: " + err.Error())
	}
	return sub
}

// immutableCache marks content-hashed /assets/* responses cacheable forever.
func immutableCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// serveDistFile serves a single file from the embedded dist/ root (e.g. the
// favicon, which Vite copies from public/).
func serveDistFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(web.DistFS, "dist/"+name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	platformFilter := r.URL.Query().Get("platform")

	// Interactive search from a Wanted row (the arrs' manual search): the row
	// supplies the title, the platform, and — the reason this is more than a
	// prefilled search box — the profile that title was added under.
	var profileID int64
	if raw := r.URL.Query().Get("wishlist_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "Invalid wishlist_id")
			return
		}
		item, ok := s.mgr.Jobs().GetWishlistItem(id)
		if !ok {
			writeError(w, http.StatusNotFound, "Wishlist item not found")
			return
		}
		if query == "" {
			query = item.Title
		}
		if platformFilter == "" {
			platformFilter = item.PlatformSlug
		}
		profileID = item.ProfileID
	}

	if query == "" {
		writeJSON(w, 200, map[string]interface{}{"results": []interface{}{}, "error": "No query"})
		return
	}

	start := time.Now()
	slug := platformFilter
	if slug == "all" {
		slug = ""
	}
	// The profile is resolved BEFORE the fan-out, not after: its region
	// priority is what tells a source not to pre-drop a region the user
	// actually wants.
	// The same ladder the scheduler resolves with, so a manual search ranks
	// under the policy the automatic grab would have used. Without a row this
	// is the platform default -> global chain; ResolveQualityProfile alone
	// skipped the platform default a platform row now carries.
	prof := s.mgr.Jobs().ResolveProfileForItem(profileID, slug)
	allResults := search.FanOut(r.Context(), search.BuildSources(s.cfg), query, slug,
		search.Opts{Regions: prof.RegionPriority})

	// One shared preparation stage (F4): torrent gates, blocklist, release
	// profiles, attrs parse, unified score, profile-tiered sort.
	pl := &selection.Pipeline{
		Blocklisted:     s.mgr.Jobs().IsBlocklisted,
		ReleaseProfiles: s.mgr.Jobs().ApplyReleaseProfiles,
	}
	results := pl.Prepare(allResults, query, platformFilter, prof)
	if results == nil {
		results = []*models.SearchResult{}
	}

	// Cross-reference with library for duplicate detection
	libraryMap := s.mgr.Jobs().GetAllLibraryTitles()
	if libraryMap != nil {
		for _, r := range results {
			key := strings.ToLower(strings.TrimSpace(r.Title)) + "|" + r.PlatformSlug
			if _, found := libraryMap[key]; found {
				r.InLibrary = true
				continue
			}
			// Release names usually carry a file extension the library
			// titles/search keys do not — retry with it stripped.
			if stripped := db.NormalizeTitleKey(r.Title); stripped != "" {
				if _, found := libraryMap[stripped+"|"+r.PlatformSlug]; found {
					r.InLibrary = true
				}
			}
		}
	}

	elapsed := int(time.Since(start).Milliseconds())

	// Source metadata
	sourceMeta := []map[string]interface{}{
		{"name": "prowlarr", "label": "Prowlarr", "color": "#f97316", "source_type": "torrent", "enabled": s.cfg.HasProwlarr()},
		{"name": "archiveorg", "label": "Internet Archive", "color": "#0ea5e9", "source_type": "ddl", "enabled": s.cfg.SourcesRegistry().ArchiveOrgActive()},
		{"name": "vimm", "label": "Vimm's Lair", "color": "#6366f1", "source_type": "ddl", "enabled": s.cfg.SourcesRegistry().VimmActive()},
	}

	writeJSON(w, 200, map[string]interface{}{
		"results":        results,
		"search_time_ms": elapsed,
		"sources":        sourceMeta,
	})
}

// handlePlatforms enumerates every platform RomArr knows about, not only the
// ones already sitting in the library — which is the difference between being
// able to add a game for a platform you have never acquired for and not.
//
// The response keeps its {id, name} shape by default: it fills every picker
// in the app and is fetched on most screens. ?full=1 serves the whole registry
// row instead, for the screens that manage platforms rather than pick one.
func (s *Server) handlePlatforms(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("full") == "1" {
		writeJSON(w, 200, map[string]interface{}{"platforms": s.platformViews()})
		return
	}
	platforms := []map[string]string{
		{"id": "all", "name": "All Platforms"},
	}
	seenSlugs := map[string]bool{"all": true}
	for _, p := range platform.Rows() {
		// System rows are directories, not platforms — offering "Supporting
		// Files" in an add dialog is nonsense. They are still enumerable
		// below once the library actually holds rows under one, which is how
		// the Library filter has always reached them.
		if p.IsSystem {
			continue
		}
		seenSlugs[p.Slug] = true
		platforms = append(platforms, map[string]string{"id": p.Slug, "name": p.DisplayName})
	}

	// Merge platforms present in the library but not (yet) registered, so the
	// Library filter can always reach every row it holds.
	for _, lp := range s.mgr.Jobs().LibraryPlatforms() {
		if seenSlugs[lp.Slug] {
			continue
		}
		seenSlugs[lp.Slug] = true
		name := lp.Name
		if name == "" {
			name = strings.ToUpper(lp.Slug)
		}
		platforms = append(platforms, map[string]string{"id": lp.Slug, "name": name})
	}
	writeJSON(w, 200, map[string]interface{}{"platforms": platforms})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	healthData := search.GetAllSourceHealth()
	sourceMeta := []map[string]interface{}{
		{"name": "prowlarr", "label": "Prowlarr", "color": "#f97316", "source_type": "torrent", "enabled": s.cfg.HasProwlarr()},
		{"name": "archiveorg", "label": "Internet Archive", "color": "#0ea5e9", "source_type": "ddl", "enabled": s.cfg.SourcesRegistry().ArchiveOrgActive()},
		{"name": "vimm", "label": "Vimm's Lair", "color": "#6366f1", "source_type": "ddl", "enabled": s.cfg.SourcesRegistry().VimmActive()},
	}
	// Attach health data to each source
	for _, src := range sourceMeta {
		name, _ := src["name"].(string)
		if h, ok := healthData[name]; ok {
			src["health"] = h
		}
	}
	writeJSON(w, 200, map[string]interface{}{"sources": sourceMeta})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req models.DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "Invalid request body")
		return
	}

	// A release hash proving a byte-identical copy is owned blocks (409,
	// force overrides); a title match merely warns below.
	if s.hashOwnedConflict(w, &req) {
		return
	}

	// Check for duplicate in library (warn but don't block)
	var duplicateWarning string
	if existing := s.mgr.Jobs().FindLibraryByTitle(req.Title, req.PlatformSlug); existing != nil {
		duplicateWarning = fmt.Sprintf("Game already exists in library: %s (%s)", existing.Title, existing.Platform)
	}

	set, err := discSetFromRequest(&req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	if req.SourceType == "ddl" {
		if req.DownloadURL == "" && req.VimmID == "" {
			writeError(w, 400, "No download URL")
			return
		}
		jobID := s.mgr.DownloadDDL(req.DownloadURL, req.VimmID, req.Title, req.Platform, req.PlatformSlug, req.IsPC, req.MD5, req.SHA1, set)
		resp := map[string]interface{}{"success": true, "job_id": jobID}
		if duplicateWarning != "" {
			resp["warning"] = duplicateWarning
		}
		writeJSON(w, 200, resp)
		return
	}

	// NZB / Usenet route
	if req.DownloadProtocol == "nzb" {
		nzbURL := req.DownloadURL
		if nzbURL == "" {
			writeError(w, 400, "No NZB URL")
			return
		}
		jobID, err := s.mgr.DownloadNZB(s.sab, nzbURL, req.Title, req.Platform, req.PlatformSlug, req.IsPC, set)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		resp := map[string]interface{}{"success": true, "job_id": jobID}
		if duplicateWarning != "" {
			resp["warning"] = duplicateWarning
		}
		writeJSON(w, 200, resp)
		return
	}

	// Torrent download
	url := req.DownloadURL
	if url == "" {
		url = req.MagnetURL
	}
	if url == "" && req.InfoHash != "" {
		url = fmt.Sprintf("magnet:?xt=urn:btih:%s", req.InfoHash)
	}

	jobID, err := s.mgr.DownloadTorrent(download.TorrentSpec{
		URL:          url,
		InfoHash:     req.InfoHash,
		Title:        req.Title,
		Platform:     req.Platform,
		PlatformSlug: req.PlatformSlug,
		IsPC:         req.IsPC,
		TargetFile:   req.TargetFile,
		DiscSet:      set,
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	resp := map[string]interface{}{"success": true, "job_id": jobID}
	if duplicateWarning != "" {
		resp["warning"] = duplicateWarning
	}
	writeJSON(w, 200, resp)
}

// jobIntValue coerces a numeric job-blob value that arrives as int/int64 on a
// fresh write but float64 after the blob round-trips through JSON on restart
// (mirrors internal/download's int64Value).
func jobIntValue(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	downloads := make([]models.DownloadEntry, 0)

	statusMap := map[string]string{
		"downloading": "downloading", "stalledDL": "stalled",
		"metaDL": "metadata", "forcedDL": "downloading",
		"pausedDL": "paused", "queuedDL": "queued",
		"uploading": "seeding", "stalledUP": "seeding",
		"forcedUP": "seeding", "stoppedUP": "completed",
		"pausedUP": "completed", "queuedUP": "completed",
		"checkingDL": "checking", "checkingUP": "checking",
		"stoppedDL": "paused",
		"error":     "error", "missingFiles": "error",
	}

	matchedJobIDs := make(map[string]bool)

	// Active torrents from qBit, when configured. Jobs from other download
	// clients are still returned below.
	var torrents []qbit.Torrent
	if s.cfg.HasQBittorrent() {
		torrents = s.mgr.QB().GetTorrents(s.cfg.QBCategory)
	}
	jobs := s.mgr.Jobs()

	for _, t := range torrents {
		progress := float64(int(t.Progress*1000)) / 10.0
		speed := search.HumanSize(t.DLSpeed) + "/s"

		// Match to a job: by persisted infohash first (authoritative), then
		// the legacy fuzzy title match for pre-migration jobs.
		var matchedJob struct {
			ID   string
			Data map[string]interface{}
		}
		found := false
		for _, item := range jobs.Items() {
			if jh, _ := item.Data["torrent_hash"].(string); jh != "" && strings.EqualFold(jh, t.Hash) {
				matchedJob = item
				found = true
				break
			}
		}
		if !found {
			for _, item := range jobs.Items() {
				jTitle, _ := item.Data["title"].(string)
				if jTitle == "" {
					continue
				}
				if strings.Contains(strings.ToLower(t.Name), strings.ToLower(jTitle)) ||
					strings.Contains(strings.ToLower(jTitle), strings.ToLower(t.Name)) {
					matchedJob = item
					found = true
					break
				}
			}
		}

		if found {
			matchedJobIDs[matchedJob.ID] = true
			jStatus, _ := matchedJob.Data["status"].(string)
			displayStatus := jStatus
			if jStatus == "downloading" {
				if mapped, ok := statusMap[t.State]; ok {
					displayStatus = mapped
				}
			}
			platf, _ := matchedJob.Data["platform"].(string)
			errMsg, _ := matchedJob.Data["error"].(string)
			detail, _ := matchedJob.Data["detail"].(string)
			setID, _ := matchedJob.Data["disc_set_id"].(string)

			downloads = append(downloads, models.DownloadEntry{
				Type:      "job",
				Title:     jTitle(matchedJob.Data),
				Platform:  platf,
				Status:    displayStatus,
				JobID:     matchedJob.ID,
				Error:     errMsg,
				Detail:    detail,
				Progress:  progress,
				Size:      search.HumanSize(t.TotalSize),
				Speed:     speed,
				ETA:       t.ETA,
				Hash:      t.Hash,
				DiscSetID: setID,
				DiscIndex: jobIntValue(matchedJob.Data["disc_index"]),
				DiscTotal: jobIntValue(matchedJob.Data["disc_total"]),
			})
		} else {
			status := t.State
			if mapped, ok := statusMap[t.State]; ok {
				status = mapped
			}
			downloads = append(downloads, models.DownloadEntry{
				Type:     "torrent",
				Title:    t.Name,
				Progress: progress,
				Status:   status,
				Size:     search.HumanSize(t.TotalSize),
				Speed:    speed,
				ETA:      t.ETA,
				Hash:     t.Hash,
			})
		}
	}

	// Unmatched jobs. Items() iterates a map (random order per call), so
	// sort deterministically — disc-set members by index, everything else by
	// title then id — or the queue reshuffles on every poll.
	var unmatched []models.DownloadEntry
	for _, item := range jobs.Items() {
		if matchedJobIDs[item.ID] {
			continue
		}
		platf, _ := item.Data["platform"].(string)
		status, _ := item.Data["status"].(string)
		errMsg, _ := item.Data["error"].(string)
		detail, _ := item.Data["detail"].(string)
		setID, _ := item.Data["disc_set_id"].(string)

		unmatched = append(unmatched, models.DownloadEntry{
			Type:      "job",
			Title:     jTitle(item.Data),
			Platform:  platf,
			Status:    status,
			JobID:     item.ID,
			Error:     errMsg,
			Detail:    detail,
			DiscSetID: setID,
			DiscIndex: jobIntValue(item.Data["disc_index"]),
			DiscTotal: jobIntValue(item.Data["disc_total"]),
		})
	}
	sort.SliceStable(unmatched, func(i, j int) bool {
		a, b := unmatched[i], unmatched[j]
		if a.DiscSetID != b.DiscSetID {
			return a.DiscSetID < b.DiscSetID
		}
		if a.DiscIndex != b.DiscIndex {
			return a.DiscIndex < b.DiscIndex
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.JobID < b.JobID
	})
	downloads = append(downloads, unmatched...)

	writeJSON(w, 200, map[string]interface{}{"downloads": downloads})
}

func jTitle(data map[string]interface{}) string {
	t, _ := data["title"].(string)
	return t
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasQBittorrent() {
		writeError(w, 400, "qBittorrent is not configured")
		return
	}
	hash := chi.URLParam(r, "hash")
	ok := s.mgr.QB().DeleteTorrent(hash, true)
	writeJSON(w, 200, map[string]interface{}{"success": ok})
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if s.mgr.Jobs().Contains(jobID) {
		s.mgr.Jobs().Delete(jobID)
		writeJSON(w, 200, map[string]interface{}{"success": true})
		return
	}
	writeError(w, 404, "Not found")
}

func (s *Server) handleClearFinished(w http.ResponseWriter, r *http.Request) {
	cleared := 0
	for _, item := range s.mgr.Jobs().Items() {
		status, _ := item.Data["status"].(string)
		if status == "completed" || status == "error" {
			s.mgr.Jobs().Delete(item.ID)
			cleared++
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "cleared": cleared})
}

func (s *Server) handleOrganizeTorrent(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	var req struct {
		PlatformSlug string `json:"platform_slug"`
		IsPC         bool   `json:"is_pc"`
		Platform     string `json:"platform"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}

	jobID, err := s.mgr.OrganizeTorrent(hash, req.Platform, req.PlatformSlug, req.IsPC)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "job_id": jobID})
}

func (s *Server) handleDDLSources(w http.ResponseWriter, r *http.Request) {
	builtIn := []map[string]interface{}{
		{"name": "Vimm's Lair", "url": s.cfg.SourcesRegistry().Vimm.BaseURL, "type": "vimm", "builtin": true,
			"platforms": search.VimmPlatformSlugs(s.cfg.SourcesRegistry())},
	}
	all := builtIn
	for _, row := range s.mgr.Jobs().ListDDLSources() {
		all = append(all, map[string]interface{}{
			"id": row.ID, "name": row.Name, "url": row.URL, "type": "custom",
			"builtin": false, "enabled": row.Enabled,
		})
	}
	writeJSON(w, 200, map[string]interface{}{"sources": all})
}

func (s *Server) handleAddDDLSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Name == "" || req.URL == "" {
		writeError(w, 400, "Name and URL required")
		return
	}
	id, err := s.mgr.Jobs().AddDDLSource(req.Name, req.URL)
	if err != nil {
		writeError(w, 500, "Failed to add source")
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

func (s *Server) handleDeleteDDLSource(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "Invalid id")
		return
	}
	ok, err := s.mgr.Jobs().DeleteDDLSource(id)
	if err != nil || !ok {
		writeError(w, 404, "Not found")
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	// Job status counts
	items := s.mgr.Jobs().Items()
	byStatus := make(map[string]int)
	for _, item := range items {
		status, _ := item.Data["status"].(string)
		byStatus[status]++
	}

	// Library stats (from library_items table)
	libStats := s.mgr.Jobs().LibraryStats()
	libTotal := s.mgr.Jobs().LibraryTotal()
	recentItems := s.mgr.Jobs().RecentLibraryItems(10)

	var recent []map[string]interface{}
	for _, item := range recentItems {
		recent = append(recent, map[string]interface{}{
			"title":         item.Title,
			"platform":      item.Platform,
			"platform_slug": item.PlatformSlug,
			"added_at":      item.AddedAt,
			"file_size":     item.FileSize,
		})
	}

	writeJSON(w, 200, map[string]interface{}{
		"platforms":     libStats,
		"by_status":     byStatus,
		"library_total": libTotal,
		"total_jobs":    len(items),
		"recent":        recent,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "version": "1.0.0"})
}
