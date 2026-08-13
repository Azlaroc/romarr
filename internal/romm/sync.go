package romm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/db"
	"gamarr/internal/platform"
	"gamarr/internal/search"
)

const (
	sourceName = "romm"
	// fullReconcileEvery bounds how stale the full picture may get: the
	// updated_after incremental cannot see hard-deleted ROMs, so a periodic
	// full pull reconciles removals.
	fullReconcileEvery = 24 * time.Hour
	// incrementalOverlap is subtracted from the last sync instant so clock
	// skew between Gamarr and RomM cannot drop updates; upserts are
	// idempotent so the overlap is free.
	incrementalOverlap = 5 * time.Minute
	syncTimeout        = 30 * time.Minute
)

// SyncOptions configures a Syncer.
type SyncOptions struct {
	RomsRoot         string        // cfg.GamesRomsPath — must be the directory RomM scans as its roms folder
	Interval         time.Duration // tick cadence; 0 disables the ticker (manual syncs only)
	ExcludePlatforms []string      // RomM fs_slugs to skip
	StateFile        string        // JSON state path, e.g. DataDir/romm_sync.json
}

type syncState struct {
	LastIncremental time.Time `json:"last_incremental"`
	LastFull        time.Time `json:"last_full"`
}

// Syncer mirrors RomM's catalog into library_items on a fixed cadence. RomM
// is the system of record: rows it owns carry source='romm' and are replaced
// wholesale by reconciliation, never edited locally.
type Syncer struct {
	client   *Client
	jobs     *db.JobStore
	romsRoot string
	interval time.Duration
	exclude  map[string]bool
	stateFN  string

	running atomic.Bool
	stopCh  chan struct{}

	mu       sync.Mutex
	state    syncState
	lastErr  string
	lastRun  time.Time
	stopOnce sync.Once
}

// NewSyncer builds a Syncer. client may not be nil; enable/disable decisions
// belong to the caller (main wires the Syncer only when RomM is configured).
func NewSyncer(client *Client, jobs *db.JobStore, opts SyncOptions) *Syncer {
	exclude := make(map[string]bool, len(opts.ExcludePlatforms))
	for _, slug := range opts.ExcludePlatforms {
		exclude[strings.TrimSpace(slug)] = true
	}
	s := &Syncer{
		client:   client,
		jobs:     jobs,
		romsRoot: opts.RomsRoot,
		interval: opts.Interval,
		exclude:  exclude,
		stateFN:  opts.StateFile,
		stopCh:   make(chan struct{}),
	}
	s.state = s.loadState()
	return s
}

// Start runs one sync immediately and then ticks on the configured interval.
// Nil-safe so callers can hold an unconfigured *Syncer.
func (s *Syncer) Start() {
	if s == nil {
		return
	}
	slog.Info("romm sync started", "interval", s.interval, "roms_root", s.romsRoot)
	s.TriggerSync(false)
	if s.interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.TriggerSync(false)
			case <-s.stopCh:
				slog.Info("romm sync stopped")
				return
			}
		}
	}()
}

// Stop signals the ticker to stop. Nil-safe.
func (s *Syncer) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// TriggerSync starts a sync in the background unless one is already running.
// full forces a full reconcile regardless of the schedule.
func (s *Syncer) TriggerSync(full bool) bool {
	if s == nil || s.client == nil {
		return false
	}
	if !s.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer s.running.Store(false)
		s.runOnce(full)
	}()
	return true
}

// Status reports sync state for the API.
func (s *Syncer) Status() map[string]interface{} {
	if s == nil {
		return map[string]interface{}{"enabled": false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{
		"enabled":          true,
		"running":          s.running.Load(),
		"interval_seconds": int(s.interval / time.Second),
		"last_incremental": timeOrNil(s.state.LastIncremental),
		"last_full":        timeOrNil(s.state.LastFull),
		"last_run":         timeOrNil(s.lastRun),
		"last_error":       s.lastErr,
	}
}

func (s *Syncer) runOnce(forceFull bool) {
	if search.IsCircuitOpen(sourceName) {
		slog.Debug("romm sync skipped: circuit open")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	s.mu.Lock()
	state := s.state
	s.mu.Unlock()

	full := forceFull || state.LastFull.IsZero() || time.Since(state.LastFull) > fullReconcileEvery
	var updatedAfter time.Time
	if !full && !state.LastIncremental.IsZero() {
		updatedAfter = state.LastIncremental.Add(-incrementalOverlap)
	}

	runStart := time.Now()
	added, updated, removed, total, err := s.pull(ctx, full, updatedAfter)
	if err != nil {
		search.RecordSearchFail(sourceName, err.Error())
		s.mu.Lock()
		s.lastErr = err.Error()
		s.lastRun = runStart
		s.mu.Unlock()
		slog.Warn("romm sync failed", "error", err, "full", full)
		return
	}

	search.RecordSearchSuccess(sourceName)
	s.mu.Lock()
	s.lastErr = ""
	s.lastRun = runStart
	s.state.LastIncremental = runStart
	if full {
		s.state.LastFull = runStart
	}
	state = s.state
	s.mu.Unlock()
	s.saveState(state)

	slog.Info("romm sync complete",
		"full", full, "roms", total, "added", added, "updated", updated, "removed", removed)
}

// pull fetches the catalog and applies it. Any fetch error aborts the whole
// run before a single row is written — the last-good library view survives a
// RomM outage untouched.
func (s *Syncer) pull(ctx context.Context, full bool, updatedAfter time.Time) (added, updated, removed, total int, err error) {
	if err = s.client.Heartbeat(ctx); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("romm heartbeat: %w", err)
	}
	platforms, err := s.client.ListPlatforms(ctx)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("romm platforms: %w", err)
	}

	var items []db.RommSyncItem
	for _, p := range platforms {
		if s.exclude[p.FSSlug] {
			continue
		}
		roms, rerr := s.client.ListRoms(ctx, p.ID, updatedAfter)
		if rerr != nil {
			return 0, 0, 0, 0, fmt.Errorf("romm roms for %s: %w", p.FSSlug, rerr)
		}
		for i := range roms {
			items = append(items, s.mapRom(&roms[i], &p))
		}
	}
	total = len(items)

	added, updated, removed, err = s.jobs.SyncRommItems(items, full)
	if err != nil {
		return 0, 0, 0, total, fmt.Errorf("apply romm sync: %w", err)
	}
	return added, updated, removed, total, nil
}

// rommMetadata is the identity blob stashed under metadata $.romm.
type rommMetadata struct {
	RomID      int64  `json:"rom_id"`
	IGDBID     *int64 `json:"igdb_id,omitempty"`
	PlatformID int    `json:"platform_id"`
	FSSlug     string `json:"fs_slug"`
	FSName     string `json:"fs_name"`
	SearchKey  string `json:"search_key"`
	CRC        string `json:"crc,omitempty"`
	MD5        string `json:"md5,omitempty"`
	SHA1       string `json:"sha1,omitempty"`
	SyncedAt   string `json:"synced_at"`
}

func (s *Syncer) mapRom(r *Rom, p *Platform) db.RommSyncItem {
	fsSlug := r.PlatformFSSlug
	if fsSlug == "" {
		fsSlug = p.FSSlug
	}

	title := r.Name
	if title == "" {
		title = r.FSNameNoTags
	}
	if title == "" {
		title = r.FSNameNoExt
	}

	platName := p.Name
	if platName == "" {
		platName = strings.ToUpper(fsSlug)
	}

	meta, _ := json.Marshal(map[string]rommMetadata{"romm": {
		RomID:      r.ID,
		IGDBID:     r.IGDBID,
		PlatformID: r.PlatformID,
		FSSlug:     fsSlug,
		FSName:     r.FSName,
		SearchKey:  strings.ToLower(strings.TrimSpace(r.FSNameNoExt)),
		CRC:        r.CRCHash,
		MD5:        r.MD5Hash,
		SHA1:       r.SHA1Hash,
		SyncedAt:   time.Now().UTC().Format(time.RFC3339),
	}})

	return db.RommSyncItem{
		MissingFromFS: r.MissingFromFS,
		Item: db.LibraryItem{
			Title:        title,
			Platform:     platName,
			PlatformSlug: platform.FromRommFSSlug(fsSlug),
			FilePath:     LocalPath(s.romsRoot, r),
			FileSize:     r.FSSizeBytes,
			Source:       sourceName,
			SourceType:   sourceName,
			SourceID:     fmt.Sprintf("romm:%d", r.ID),
			Metadata:     string(meta),
		},
	}
}

// LocalPath maps a RomM rom onto Gamarr's filesystem view. RomM's fs_path is
// relative to its own library root (e.g. "roms/psx" or "roms/psx/discs")
// while romsRoot is Gamarr's path for that same "roms" directory — the
// deployment contract is that GAMES_ROMS_PATH points at the directory RomM
// scans for ROMs. The path is anchored on the platform fs_slug segment so
// platform-internal subdirectories are preserved; fs_name may name a file or
// a game directory (multi-file ROMs).
func LocalPath(romsRoot string, r *Rom) string {
	fsSlug := r.PlatformFSSlug
	segs := strings.Split(strings.Trim(filepath.ToSlash(r.FSPath), "/"), "/")
	for i, seg := range segs {
		if seg == fsSlug {
			parts := append([]string{romsRoot}, segs[i:]...)
			return filepath.Join(append(parts, r.FSName)...)
		}
	}
	return filepath.Join(romsRoot, fsSlug, r.FSName)
}

func (s *Syncer) loadState() syncState {
	var st syncState
	if s.stateFN == "" {
		return st
	}
	data, err := os.ReadFile(s.stateFN)
	if err != nil {
		return st
	}
	json.Unmarshal(data, &st)
	return st
}

func (s *Syncer) saveState(st syncState) {
	if s.stateFN == "" {
		return
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(s.stateFN, data, 0644); err != nil {
		slog.Warn("romm sync state not saved", "error", err)
	}
}

func timeOrNil(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
