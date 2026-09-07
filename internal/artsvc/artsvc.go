// Package artsvc is the title-art plane: it resolves a library row to box
// art, caches the bytes under $DataDir/art/, and remembers the answer —
// including the negative one — in the art_cache table.
//
// The keying ladder is the #same one naming uses: stored content hashes →
// the active DAT catalog → the shared name resolver; the canonical DAT name
// keys libretro-thumbnails (art keyed by exactly the names we hold). Rows
// the catalog cannot answer fall to the metadata provider (IGDB). The
// library row's display title is deliberately never a libretro key — it is
// not DAT-canonical.
//
// Fetching is asynchronous: a cache miss answers 404 immediately and
// enqueues; the art appears on a later paint. A backfill campaign walks the
// whole library once, politely. Both paths share the same per-host pacing —
// GitHub raw gets a fixed politeness delay (the DAT fetcher's rule: a burst
// is what earns an egress ban), IGDB a 4-per-second budget.
package artsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/datname"
	"gamarr/internal/db"
	"gamarr/internal/metadata"
	"gamarr/internal/platform"
)

const (
	// negativeTTL is how long a notfound answer stands before a re-ask.
	negativeTTL = 7 * 24 * time.Hour
	// maxArtBytes caps a single downloaded image.
	maxArtBytes = 8 << 20
	// queueDepth bounds the on-demand mint queue; a full queue drops the
	// enqueue (the next visit re-asks) rather than blocking a request.
	queueDepth = 256
)

// Service resolves, fetches and serves title art.
type Service struct {
	cfg    *config.Config
	store  *db.JobStore
	meta   metadata.Provider
	client *http.Client

	// rawBase overrides the libretro-thumbnails host for tests.
	rawBase string
	// politeness is the gap between GitHub-raw fetches; igdbBudget paces
	// provider calls. Both are shared by the worker AND the campaign, so
	// running them together cannot double the traffic.
	politeness time.Duration
	rawMu      sync.Mutex
	rawLast    time.Time
	igdbMu     sync.Mutex
	igdbLast   time.Time

	queueMu sync.Mutex
	queued  map[int64]bool
	ch      chan int64

	campaignOn atomic.Bool
	statMu     sync.Mutex
	stat       Status

	stop chan struct{}
	once sync.Once
}

// Status is the poll surface for the UI.
type Status struct {
	QueueDepth      int    `json:"queue_depth"`
	CampaignRunning bool   `json:"campaign_running"`
	CampaignTotal   int    `json:"campaign_total"`
	CampaignDone    int    `json:"campaign_done"`
	OK              int    `json:"ok"`
	NotFound        int    `json:"notfound"`
	Errors          int    `json:"errors"`
	StartedAt       string `json:"started_at,omitempty"`
	LastError       string `json:"last_error,omitempty"`
}

// New builds the service and starts its single mint worker.
func New(cfg *config.Config, store *db.JobStore, meta metadata.Provider) *Service {
	s := &Service{
		cfg:        cfg,
		store:      store,
		meta:       meta,
		client:     &http.Client{Timeout: 30 * time.Second},
		rawBase:    "https://raw.githubusercontent.com/libretro-thumbnails",
		politeness: 2 * time.Second,
		queued:     map[int64]bool{},
		ch:         make(chan int64, queueDepth),
		stop:       make(chan struct{}),
	}
	go s.worker()
	return s
}

// Stop ends the worker at shutdown.
func (s *Service) Stop() { s.once.Do(func() { close(s.stop) }) }

// titleDir is where one platform's cached and custom art lives. A custom
// drop-in uses the same path scheme and simply wins by existing.
func (s *Service) titleDir(slug string) string {
	return filepath.Join(s.cfg.DataDir, "art", "titles", slug)
}

// keyStem is the row's content stem: the canonical DAT name when the stored
// hashes resolve one, else the file basename's stem. Never the display title.
func (s *Service) keyStem(item *db.LibraryItem) string {
	if stem, ok := s.canonicalStem(item); ok {
		return stem
	}
	base := filepath.Base(item.FilePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (s *Service) canonicalStem(item *db.LibraryItem) (string, bool) {
	var matches []db.DatRomMatch
	if g, ok := db.ParseGamarrHashes(item.Metadata); ok {
		if g.CRC != "" || g.MD5 != "" || g.SHA1 != "" {
			matches = s.store.LookupDatRomsByHash(item.PlatformSlug, g.CRC, g.MD5, g.SHA1)
		}
		if len(matches) == 0 && g.Unh != nil {
			matches = s.store.LookupDatRomsByHash(item.PlatformSlug, g.Unh.CRC, g.Unh.MD5, g.Unh.SHA1)
		}
	}
	if len(matches) == 0 {
		if crc, md5, sha1, ok := db.ParseRommContentHashes(item.Metadata); ok && (crc != "" || md5 != "" || sha1 != "") {
			matches = s.store.LookupDatRomsByHash(item.PlatformSlug, crc, md5, sha1)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	cands := make([]datname.Candidate, 0, len(matches))
	for _, m := range matches {
		cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
	}
	if res := datname.Resolve(cands); res.Outcome == datname.Resolved {
		return res.Stem, true
	}
	return "", false
}

// CachedPath returns the servable file for a row when one exists on disk —
// fetched or hand-dropped, the mechanism does not care which.
func (s *Service) CachedPath(item *db.LibraryItem) (string, bool) {
	stem := SanitizeThumbName(s.keyStem(item))
	dir := s.titleDir(item.PlatformSlug)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		p := filepath.Join(dir, stem+ext)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Enqueue asks for a background mint unless one is queued or a fresh
// negative answer stands. Safe from a request handler: never blocks.
func (s *Service) Enqueue(id int64) {
	item, err := s.store.GetLibraryItem(id)
	if err != nil {
		return
	}
	if row, ok := s.store.GetArtCache(s.cacheKey(item)); ok && row.Status == "notfound" && !s.negativeExpired(row.FetchedAt) {
		return
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.queued[id] {
		return
	}
	select {
	case s.ch <- id:
		s.queued[id] = true
	default:
		// Full queue: drop. The next visit re-asks.
	}
}

func (s *Service) cacheKey(item *db.LibraryItem) string {
	return "title:" + item.PlatformSlug + ":" + SanitizeThumbName(s.keyStem(item))
}

func (s *Service) negativeExpired(fetchedAt string) bool {
	t, err := time.Parse("2006-01-02 15:04:05", fetchedAt)
	if err != nil {
		return true
	}
	return time.Since(t) > negativeTTL
}

func (s *Service) worker() {
	for {
		select {
		case <-s.stop:
			return
		case id := <-s.ch:
			s.queueMu.Lock()
			delete(s.queued, id)
			s.queueMu.Unlock()
			if item, err := s.store.GetLibraryItem(id); err == nil {
				if err := s.resolveOne(context.Background(), item); err != nil {
					s.noteError(err)
				}
			}
		}
	}
}

// resolveOne runs the fetch ladder for one row and records the answer.
func (s *Service) resolveOne(ctx context.Context, item *db.LibraryItem) error {
	if _, ok := s.CachedPath(item); ok {
		return nil // already on disk (fetched earlier, or hand-dropped)
	}
	stem := SanitizeThumbName(s.keyStem(item))
	key := s.cacheKey(item)

	// Tier 1: libretro-thumbnails, keyed by canonical DAT name. The stem is
	// worth asking about even when it is only the basename stem — a
	// post-rename library IS DAT-canonical on disk, which is the common
	// case for hashless rows.
	if repo := platform.ThumbRepoFor(item.PlatformSlug); repo != "" {
		url := s.rawBase + "/" + repo + "/master/Named_Boxarts/" + pathEscapeName(stem) + ".png"
		data, err := s.fetchRaw(ctx, url)
		if err == nil && len(data) > 0 {
			return s.bank(item, key, "libretro", stem+".png", data)
		}
		if err != nil && !errors.Is(err, errNotFound) {
			return err
		}
	}

	// Tier 2: the metadata provider's cover.
	if s.meta != nil && s.meta.Configured() {
		if data, ext, game, err := s.fetchIGDBCover(ctx, item); err != nil {
			return err
		} else if len(data) > 0 {
			if game != nil {
				s.bankIGDBMeta(item, game)
			}
			return s.bank(item, key, "igdb", stem+ext, data)
		}
	}

	return s.store.SaveArtCache(db.ArtCacheRow{Key: key, Status: "notfound"})
}

var errNotFound = errors.New("not found")

func (s *Service) fetchRaw(ctx context.Context, url string) ([]byte, error) {
	s.rawMu.Lock()
	if wait := s.politeness - time.Since(s.rawLast); wait > 0 {
		time.Sleep(wait)
	}
	s.rawLast = time.Now()
	s.rawMu.Unlock()
	return s.get(ctx, url)
}

func (s *Service) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxArtBytes))
}

// fetchIGDBCover searches the provider for the row's title on its platform
// and downloads the best match's cover. A miss is (nil, "", nil, nil).
func (s *Service) fetchIGDBCover(ctx context.Context, item *db.LibraryItem) ([]byte, string, *metadata.Game, error) {
	s.igdbMu.Lock()
	if wait := 250*time.Millisecond - time.Since(s.igdbLast); wait > 0 {
		time.Sleep(wait)
	}
	s.igdbLast = time.Now()
	s.igdbMu.Unlock()

	games, err := s.meta.Search(ctx, item.Title, 5)
	if err != nil {
		return nil, "", nil, err
	}
	for i := range games {
		g := &games[i]
		if g.CoverURL == "" {
			continue
		}
		onPlatform := len(g.Platforms) == 0
		for _, p := range g.Platforms {
			if p == item.PlatformSlug || (item.IsPC && p == "pc") {
				onPlatform = true
				break
			}
		}
		if !onPlatform {
			continue
		}
		data, err := s.get(ctx, g.CoverURL)
		if err != nil {
			if errors.Is(err, errNotFound) {
				continue
			}
			return nil, "", nil, err
		}
		ext := ".jpg"
		if strings.HasSuffix(strings.ToLower(g.CoverURL), ".png") {
			ext = ".png"
		}
		return data, ext, g, nil
	}
	return nil, "", nil, nil
}

// bankIGDBMeta merges year/genres into $.gamarr.igdb for the detail header —
// the shop-native lane's only source of either. json_set leaf writes only,
// per the library-identity contract.
func (s *Service) bankIGDBMeta(item *db.LibraryItem, g *metadata.Game) {
	if g.ReleaseYear == 0 && len(g.Genres) == 0 {
		return
	}
	if err := s.store.SaveLibraryIGDBMeta(item.ID, g.ReleaseYear, g.Genres); err != nil {
		slog.Warn("art: bank igdb meta", "library_id", item.ID, "error", err)
	}
}

// bank writes the image atomically and records the hit.
func (s *Service) bank(item *db.LibraryItem, key, source, filename string, data []byte) error {
	if !filepath.IsLocal(filename) {
		return fmt.Errorf("art: refusing non-local name %q", filename)
	}
	dir := s.titleDir(item.PlatformSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filename+".part")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	final := filepath.Join(dir, filename)
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	rel, _ := filepath.Rel(filepath.Join(s.cfg.DataDir, "art"), final)
	return s.store.SaveArtCache(db.ArtCacheRow{Key: key, Status: "ok", Source: source, RelPath: rel})
}

// PlatformOverridePath is the per-install platform-art escape hatch:
// $DataDir/art/platforms/<slug>.<ext>. Committed photos are served from the
// frontend bundle; this exists only so an operator can override one.
func (s *Service) PlatformOverridePath(slug string) (string, bool) {
	dir := filepath.Join(s.cfg.DataDir, "art", "platforms")
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		p := filepath.Join(dir, slug+ext)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// StartBackfill walks every library row once, resolving art politely.
// Single-flight; returns false when one is already running.
func (s *Service) StartBackfill() bool {
	if !s.campaignOn.CompareAndSwap(false, true) {
		return false
	}
	page := s.store.GetLibraryPage(db.LibraryQuery{Page: 1, PageSize: 1})
	s.statMu.Lock()
	s.stat = Status{CampaignRunning: true, CampaignTotal: page.Total, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	s.statMu.Unlock()
	go s.runBackfill()
	return true
}

func (s *Service) runBackfill() {
	defer s.campaignOn.Store(false)
	defer func() {
		s.statMu.Lock()
		s.stat.CampaignRunning = false
		s.statMu.Unlock()
	}()
	const pageSize = 200
	for pageN := 1; ; pageN++ {
		page := s.store.GetLibraryPage(db.LibraryQuery{Page: pageN, PageSize: pageSize, Sort: "title"})
		if len(page.Items) == 0 {
			return
		}
		for i := range page.Items {
			select {
			case <-s.stop:
				return
			default:
			}
			item := &page.Items[i]
			err := s.resolveOne(context.Background(), item)
			s.statMu.Lock()
			s.stat.CampaignDone++
			if err != nil {
				s.stat.Errors++
				s.stat.LastError = err.Error()
			}
			s.statMu.Unlock()
		}
		if pageN >= page.TotalPages {
			return
		}
	}
}

func (s *Service) noteError(err error) {
	s.statMu.Lock()
	s.stat.Errors++
	s.stat.LastError = err.Error()
	s.statMu.Unlock()
	slog.Warn("art: resolve", "error", err)
}

// GetStatus snapshots progress — a copy, never the live struct.
func (s *Service) GetStatus() Status {
	s.statMu.Lock()
	st := s.stat
	s.statMu.Unlock()
	st.CampaignRunning = s.campaignOn.Load()
	s.queueMu.Lock()
	st.QueueDepth = len(s.queued)
	s.queueMu.Unlock()
	st.OK, st.NotFound = s.store.CountArtCache()
	return st
}
