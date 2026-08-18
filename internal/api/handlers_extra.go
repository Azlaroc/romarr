package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/db"
	"gamarr/internal/romm"
	"gamarr/internal/search"
)

// ── Library ────────────────────────────────────────────────────────────────────

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 50
	}
	query := r.URL.Query().Get("q")
	platformSlug := r.URL.Query().Get("platform")
	tagFilter := r.URL.Query().Get("tag")

	result := s.mgr.Jobs().GetLibraryPage(page, pageSize, query, platformSlug)

	// Filter by tag if specified
	if tagFilter != "" {
		taggedIDs := s.mgr.Jobs().GetLibraryItemIDsByTag(tagFilter)
		idSet := make(map[int64]bool, len(taggedIDs))
		for _, id := range taggedIDs {
			idSet[id] = true
		}
		var filtered []db.LibraryItem
		for _, item := range result.Items {
			if idSet[item.ID] {
				filtered = append(filtered, item)
			}
		}
		if filtered == nil {
			filtered = []db.LibraryItem{}
		}
		result.Items = filtered
		result.Total = len(filtered)
		result.TotalPages = 1
	}

	writeJSON(w, 200, result)
}

func (s *Server) handleDeleteLibraryItem(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.mgr.Jobs().DeleteLibraryItem(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ── Wishlist ───────────────────────────────────────────────────────────────────

func (s *Server) handleWishlist(w http.ResponseWriter, r *http.Request) {
	items := s.mgr.Jobs().GetWishlist()
	if items == nil {
		items = []db.WishlistItem{}
	}
	writeJSON(w, 200, map[string]interface{}{"items": items})
}

func (s *Server) handleAddWishlist(w http.ResponseWriter, r *http.Request) {
	// Reject obviously-wrong content types so a misbehaving client gets a
	// proper 400 instead of having text/plain silently parsed as JSON.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		base := ct
		if idx := strings.Index(base, ";"); idx >= 0 {
			base = base[:idx]
		}
		base = strings.TrimSpace(strings.ToLower(base))
		if base != "application/json" {
			writeError(w, 415, "Content-Type must be application/json")
			return
		}
	}
	var req struct {
		Title        string `json:"title"`
		Platform     string `json:"platform"`
		PlatformSlug string `json:"platform_slug"`
		ProfileID    int64  `json:"profile_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeError(w, 400, "Title required")
		return
	}
	// Cap user-supplied strings so a misbehaving client can't bloat the DB.
	if len(req.Title) > 500 || len(req.Platform) > 100 || len(req.PlatformSlug) > 50 {
		writeError(w, 400, "Field exceeds maximum length")
		return
	}
	jobs := s.mgr.Jobs()
	if req.ProfileID > 0 {
		p, err := jobs.GetQualityProfile(req.ProfileID)
		if err != nil || p == nil {
			writeError(w, 400, "Unknown quality profile")
			return
		}
		if p.IsTemplate {
			writeError(w, 400, "That profile is a template — it is cloned for new platforms, not used directly")
			return
		}
	}

	// Adding the first title on a platform materializes its default profile
	// from the class template. That is the whole point of templates: adding a
	// platform stops being a setup step, and the operator is told once that a
	// profile now exists rather than being asked to create one first.
	var materialized *db.QualityProfile
	if req.ProfileID == 0 && req.PlatformSlug != "" && req.PlatformSlug != "all" {
		if p, created := jobs.EnsurePlatformProfile(req.PlatformSlug); created {
			materialized = p
		}
	}

	id, err := jobs.AddWishlistItemWithProfile(req.Title, req.Platform, req.PlatformSlug, req.ProfileID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	resp := map[string]interface{}{"success": true, "id": id}
	if materialized != nil {
		resp["materialized_profile"] = map[string]interface{}{
			"id": materialized.ID, "name": materialized.Name,
		}
	}
	writeJSON(w, 200, resp)
}

// handleUpdateWishlist changes one row's profile override. 0 clears it, which
// returns the row to "whatever this platform defaults to when the cycle runs".
func (s *Server) handleUpdateWishlist(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid wishlist id")
		return
	}
	var req struct {
		ProfileID *int64 `json:"profile_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ProfileID == nil {
		writeError(w, 400, "profile_id required")
		return
	}
	if *req.ProfileID > 0 {
		p, err := s.mgr.Jobs().GetQualityProfile(*req.ProfileID)
		if err != nil || p == nil || p.IsTemplate {
			writeError(w, 400, "Unknown quality profile")
			return
		}
	}
	ok, err := s.mgr.Jobs().SetWishlistProfile(id, *req.ProfileID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !ok {
		writeError(w, 404, "Wishlist item not found")
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id, "profile_id": *req.ProfileID})
}

func (s *Server) handleDeleteWishlist(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid wishlist id")
		return
	}
	s.mgr.Jobs().DeleteWishlistItem(id)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ── Activity ───────────────────────────────────────────────────────────────────

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	// page_size (limit accepted as an alias) was silently ignored before —
	// the window was pinned at 50 regardless of what the caller asked for.
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	entries, total := s.mgr.Jobs().GetActivity(page, pageSize)
	if entries == nil {
		entries = []db.ActivityEntry{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"entries": entries,
		"total":   total,
		"page":    page,
	})
}

// ── Retry ──────────────────────────────────────────────────────────────────────

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	ok, msg := s.mgr.RetryJob(jobID)
	writeJSON(w, 200, map[string]interface{}{"success": ok, "message": msg})
}

// ── Connection Tests ───────────────────────────────────────────────────────────

func (s *Server) handleTestProwlarr(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasProwlarr() {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": "Not configured"})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", s.cfg.ProwlarrURL+"/api/v1/indexer", nil)
	req.Header.Set("X-Api-Key", s.cfg.ProwlarrAPIKey)
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	resp.Body.Close()
	writeJSON(w, 200, map[string]interface{}{"success": resp.StatusCode == 200, "status": resp.StatusCode})
}

func (s *Server) handleTestQBittorrent(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasQBittorrent() {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": "Not configured"})
		return
	}
	ok := s.mgr.QB().Login()
	writeJSON(w, 200, map[string]interface{}{"success": ok})
}

func (s *Server) handleTestSABnzbd(w http.ResponseWriter, r *http.Request) {
	if s.sab == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": "Not configured"})
		return
	}
	err := s.sab.TestConnection()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

func (s *Server) handleTestRomM(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasRomMAPI() {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": "Not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := romm.New(s.cfg.RomMURL, s.cfg.RomMAPIUser, s.cfg.RomMAPIPass)
	count, err := client.TestConnection(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "platforms": count})
}

// ── Source Health ─────────────────────────────────────────────────────────────

func (s *Server) handleSourcesHealth(w http.ResponseWriter, r *http.Request) {
	healthData := search.GetAllSourceHealth()
	writeJSON(w, 200, map[string]interface{}{"sources": healthData})
}

func (s *Server) handleSourceReset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ok := search.ResetCircuit(name)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": "Source not found"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ── Config ─────────────────────────────────────────────────────────────────────

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"prowlarr": map[string]interface{}{
			"configured": s.cfg.HasProwlarr(),
			"url":        s.cfg.ProwlarrURL,
		},
		"qbittorrent": map[string]interface{}{
			"configured": s.cfg.HasQBittorrent(),
			"url":        s.cfg.QBURL,
		},
		"sabnzbd": map[string]interface{}{
			"configured": s.cfg.HasSABnzbd(),
			"url":        s.cfg.SABnzbdURL,
		},
		"romm": map[string]interface{}{
			"configured": s.cfg.HasRomMAPI(),
			"url":        s.cfg.RomMURL,
		},
		"romm_url": s.cfg.RomMURL,
		"version":  "1.0.0",
	})
}

// ── Metrics (Prometheus) ───────────────────────────────────────────────────────

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.MetricsEnabled {
		http.Error(w, "Metrics disabled", http.StatusForbidden)
		return
	}

	// Job status counts
	statusCounts := make(map[string]int)
	for _, item := range s.mgr.Jobs().Items() {
		status, _ := item.Data["status"].(string)
		statusCounts[status]++
	}

	// Library stats
	libStats := s.mgr.Jobs().LibraryStats()
	libTotal := s.mgr.Jobs().LibraryTotal()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP gamarr_jobs_total Number of download jobs by status\n")
	fmt.Fprintf(w, "# TYPE gamarr_jobs_total gauge\n")
	for status, count := range statusCounts {
		fmt.Fprintf(w, "gamarr_jobs_total{status=%q} %d\n", status, count)
	}

	fmt.Fprintf(w, "# HELP gamarr_library_total Total items in library\n")
	fmt.Fprintf(w, "# TYPE gamarr_library_total gauge\n")
	fmt.Fprintf(w, "gamarr_library_total %d\n", libTotal)

	fmt.Fprintf(w, "# HELP gamarr_library_by_platform Library items by platform\n")
	fmt.Fprintf(w, "# TYPE gamarr_library_by_platform gauge\n")
	for plat, count := range libStats {
		fmt.Fprintf(w, "gamarr_library_by_platform{platform=%q} %d\n", plat, count)
	}

	// Activity event count
	activityCount := s.mgr.Jobs().ActivityCount()
	fmt.Fprintf(w, "# HELP gamarr_activity_events_total Total activity log events\n")
	fmt.Fprintf(w, "# TYPE gamarr_activity_events_total gauge\n")
	fmt.Fprintf(w, "gamarr_activity_events_total %d\n", activityCount)

	// Source health metrics
	healthData := search.GetAllSourceHealth()
	fmt.Fprintf(w, "# HELP gamarr_source_health_score Source health score (0-100)\n")
	fmt.Fprintf(w, "# TYPE gamarr_source_health_score gauge\n")
	for name, h := range healthData {
		fmt.Fprintf(w, "gamarr_source_health_score{source=%q} %d\n", name, h.Score)
	}
	fmt.Fprintf(w, "# HELP gamarr_source_circuit_open Whether source circuit breaker is open (1=open)\n")
	fmt.Fprintf(w, "# TYPE gamarr_source_circuit_open gauge\n")
	for name, h := range healthData {
		val := 0
		if h.CircuitOpen {
			val = 1
		}
		fmt.Fprintf(w, "gamarr_source_circuit_open{source=%q} %d\n", name, val)
	}
	fmt.Fprintf(w, "# HELP gamarr_source_search_total Source search counts\n")
	fmt.Fprintf(w, "# TYPE gamarr_source_search_total counter\n")
	for name, h := range healthData {
		fmt.Fprintf(w, "gamarr_source_search_total{source=%q,result=\"ok\"} %d\n", name, h.SearchOK)
		fmt.Fprintf(w, "gamarr_source_search_total{source=%q,result=\"fail\"} %d\n", name, h.SearchFail)
	}
	fmt.Fprintf(w, "# HELP gamarr_source_download_total Source download counts\n")
	fmt.Fprintf(w, "# TYPE gamarr_source_download_total counter\n")
	for name, h := range healthData {
		fmt.Fprintf(w, "gamarr_source_download_total{source=%q,result=\"ok\"} %d\n", name, h.DownloadOK)
		fmt.Fprintf(w, "gamarr_source_download_total{source=%q,result=\"fail\"} %d\n", name, h.DownloadFail)
	}
}
