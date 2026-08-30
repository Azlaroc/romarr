package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/collection"
	"gamarr/internal/collectionsvc"
	"gamarr/internal/db"
)

// The 1G1R set: what a platform's catalog says the collection should be, what
// of it is on disk, and what is surplus. One read serves both directions of
// collection mode — the gap list to fill and the prune's work list.

// handlePlatformSet returns a platform's reconciled set, paged.
//
// The counts are over the WHOLE set, never the page: "84 gaps" is the number
// an operator acts on, and a per-page count would silently mean something else
// on page two.
func (s *Server) handlePlatformSet(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "platform is required")
		return
	}
	if s.coll == nil {
		writeError(w, http.StatusServiceUnavailable, "collection service unavailable")
		return
	}

	res := s.coll.Set(slug)
	entries := filterEntries(res.Entries, r.URL.Query().Get("status"), r.URL.Query().Get("q"))

	pageSize := clampInt(intParam(r, "page_size", 50), 1, 200)
	page := intParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	total := len(entries)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"platform_slug": slug,
		"entries":       entries[start:end],
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"counts":        res.Counts,
		"uncatalogued":  res.Uncatalogued,
		"policy":        res.Policy,
		"clone_list":    res.CloneList,
		"grouping":      res.Grouping,
	})
}

// filterEntries narrows a set by status and free text. Filtering here rather
// than in SQL is deliberate: the set is derived, not stored, and a filtered
// derivation is still the same derivation.
func filterEntries(entries []collection.Entry, status, text string) []collection.Entry {
	status = strings.TrimSpace(strings.ToLower(status))
	text = strings.ToLower(strings.TrimSpace(text))
	if status == "" && text == "" {
		if entries == nil {
			return []collection.Entry{}
		}
		return entries
	}
	out := make([]collection.Entry, 0, len(entries))
	for _, e := range entries {
		if status != "" && status != "all" && e.Status != status {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(e.Title), text) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// handleCloneLists reports the clone-list plane: which platforms have a
// locator, what is stored for each, and whether a refresh is running.
func (s *Server) handleCloneLists(w http.ResponseWriter, r *http.Request) {
	jobs := s.mgr.Jobs()
	platforms := jobs.ListCloneListPlatforms()
	if platforms == nil {
		platforms = []db.CloneListPlatform{}
	}
	lists := jobs.ListCloneLists()
	if lists == nil {
		lists = []db.CloneListRow{}
	}
	status := collectionsvc.RefreshState{}
	if s.coll != nil {
		status = s.coll.RefreshStatus()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"platforms": platforms,
		"lists":     lists,
		"status":    status,
		"base":      s.cfg.CloneListFetchBase(),
	})
}

// handleRefreshCloneLists re-fetches every assigned list. Asynchronous like
// the DAT refresh, and for the same reason: thirty sequential fetches is not a
// request handler's work.
func (s *Server) handleRefreshCloneLists(w http.ResponseWriter, r *http.Request) {
	if s.coll == nil {
		writeError(w, http.StatusServiceUnavailable, "collection service unavailable")
		return
	}
	if !s.coll.RefreshCloneLists() {
		writeError(w, http.StatusConflict, collectionsvc.ErrBusy.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"success": true, "status": s.coll.RefreshStatus()})
}

// The gap list: what collection mode is trying to acquire, and how it is
// getting on. A derived set answers "what should exist"; these rows answer
// "what have we actually been doing about it".

func (s *Server) handleCollectionTargets(w http.ResponseWriter, r *http.Request) {
	pageSize := clampInt(intParam(r, "page_size", 50), 1, 200)
	page := intParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	jobs := s.mgr.Jobs()
	targets, total := jobs.ListCollectionTargets(db.CollectionTargetQuery{
		PlatformSlug: strings.TrimSpace(r.URL.Query().Get("platform")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
		Text:         r.URL.Query().Get("q"),
		Limit:        pageSize,
		Offset:       (page - 1) * pageSize,
	})
	if targets == nil {
		targets = []db.CollectionTarget{}
	}
	platforms := []string{}
	if s.coll != nil {
		platforms = s.coll.CollectionPlatforms()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"targets":        targets,
		"total":          total,
		"page":           page,
		"page_size":      pageSize,
		"counts":         jobs.CollectionTargetCounts(),
		"platforms":      platforms,
		"fill_per_cycle": s.cfg.CollectionFill(),
	})
}

// handleCollectionSync rebuilds the gap list now rather than at the next
// scheduler cycle. Synchronous: it is one reconciliation per platform against
// an in-memory snapshot, and an operator who just switched a platform on wants
// the answer, not a job id.
func (s *Server) handleCollectionSync(w http.ResponseWriter, r *http.Request) {
	if s.coll == nil {
		writeError(w, http.StatusServiceUnavailable, "collection service unavailable")
		return
	}
	slug := strings.TrimSpace(r.URL.Query().Get("platform"))
	if slug != "" {
		res := s.coll.NewCycle().SyncTargets(slug)
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "results": []interface{}{res}})
		return
	}
	results := s.coll.SyncAll()
	if results == nil {
		results = []collectionsvc.SyncResult{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "results": results})
}
