package datsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/dat"
	"gamarr/internal/db"
)

// Per-platform outcomes.
const (
	StatusImported  = "imported"
	StatusUnchanged = "unchanged"
	StatusPinned    = "pinned"
	StatusSkipped   = "skipped"
	StatusError     = "error"
)

// Authority roll-ups, stored in dat_authorities.last_status.
const (
	RunOK      = "ok"
	RunPartial = "partial"
	RunError   = "error"
)

// fetchTimeout bounds one catalog fetch. Redump's PSX datfile is ~10MB, so
// this is generous for a slow link and still finite.
const fetchTimeout = 5 * time.Minute

// politenessDelay spaces sequential fetches of the same authority. The
// generator feeding the libretro mirror runs its Redump pass with
// CONCURRENCY = 1 and the comment "Kept low to be polite to redump.org";
// eleven files in a burst is exactly what earns an egress ban. A var so
// tests need not sleep through it.
var politenessDelay = 2 * time.Second

// ErrBusy is returned when a refresh or import is already in flight. One
// writer at a time is deliberate: InsertDatSnapshot reads its diff baseline
// before opening its transaction, so two writers on one platform would race
// into phantom added/removed counts.
var ErrBusy = errors.New("a DAT refresh is already running")

// PlatformResult is what one platform's refresh or import did.
type PlatformResult struct {
	Platform string `json:"platform"`
	Status   string `json:"status"`
	Version  string `json:"version,omitempty"`
	Games    int    `json:"games,omitempty"`
	Roms     int    `json:"roms,omitempty"`
	Added    int    `json:"added,omitempty"`
	Removed  int    `json:"removed,omitempty"`
	Changed  int    `json:"changed,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SkippedMember is a pack member no assigned platform claimed.
type SkippedMember struct {
	Member string `json:"member"`
	Reason string `json:"reason"`
}

// UploadResult reports what an upload imported and what it passed over. A
// daily pack carries ~90 catalogs and we assign a fraction of them, so
// "skipped" is the normal case, not a failure.
type UploadResult struct {
	Authority string           `json:"authority"`
	Imported  []PlatformResult `json:"imported"`
	Skipped   []SkippedMember  `json:"skipped"`
}

// Service owns catalog acquisition: fetching, importing uploads, and the
// optional refresh cadence.
type Service struct {
	cfg   *config.Config
	store *db.JobStore
	http  *http.Client

	// onSnapshot fires after a platform's catalog is replaced, so consumers
	// holding a cached view of the derived bands can drop it. A callback
	// rather than a direct call outward: the catalog plane should not need to
	// know who is reading its output.
	onSnapshot func(platformSlug string)

	// running is the single-flight gate shared by refreshes and uploads.
	running atomic.Bool

	mu         sync.Mutex
	cancel     context.CancelFunc
	phase      string // idle | refreshing | importing
	authority  string
	total      int
	done       int
	results    []PlatformResult
	lastErr    string
	startedAt  time.Time
	finishedAt time.Time

	loopMu   sync.Mutex
	stopCh   chan struct{}
	loopLive bool
}

// Option customises a Service.
type Option func(*Service)

// WithHTTPClient swaps the fetch client (tests point it at a stub).
func WithHTTPClient(c *http.Client) Option { return func(s *Service) { s.http = c } }

// WithOnSnapshot registers a callback fired once per platform whose catalog
// was actually replaced. It does not fire when a fetch turns out to be
// unchanged or is held back by a pin, because in both cases the stored bands
// are still current.
func WithOnSnapshot(fn func(platformSlug string)) Option {
	return func(s *Service) { s.onSnapshot = fn }
}

// New builds a Service. It starts no goroutine: the cadence loop arms only
// through EnsureRunning, which the supervisor calls when the toggle is on.
func New(cfg *config.Config, store *db.JobStore, opts ...Option) *Service {
	s := &Service{
		cfg:    cfg,
		store:  store,
		http:   &http.Client{Timeout: fetchTimeout},
		phase:  "idle",
		stopCh: make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ---------- refresh ----------

// RefreshAuthority starts an asynchronous refresh of every platform assigned
// to an authority. The error reports why a refresh is impossible (unknown,
// disabled, or hand-fed); the bool reports whether one was already running.
func (s *Service) RefreshAuthority(name string) (bool, error) {
	if s == nil {
		return false, errors.New("DAT service unavailable")
	}
	auth, err := s.store.GetDatAuthority(name)
	if err != nil {
		return false, err
	}
	// Driver first: a hand-fed authority can never be refreshed, so that is
	// the useful answer whether or not it is also disabled (MAME ships both).
	if auth.FetchDriver == DriverUpload {
		return false, ErrUploadOnly
	}
	if !auth.Enabled {
		return false, fmt.Errorf("authority %q is disabled", auth.Name)
	}
	if !s.running.CompareAndSwap(false, true) {
		return false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.beginRun("refreshing", auth.Name, cancel)
	go func() {
		defer s.running.Store(false)
		defer cancel()
		s.runRefresh(ctx, auth, false)
		s.endRun()
	}()
	return true, nil
}

// runRefresh walks an authority's platforms one at a time. A platform that
// fails keeps its previous active snapshot: stale beats absent, and the
// failure is visible in last_status rather than as a hole in coverage.
func (s *Service) runRefresh(ctx context.Context, auth db.DatAuthorityRow, auto bool) {
	plats := assignedPlatforms(s.store.ListDatPlatforms(), auth.Name)
	s.setTotal(len(plats))

	fetcher, err := NewFetcher(auth.FetchDriver, s.http)
	if err != nil {
		s.failRun(auth, err)
		return
	}

	var ok, failed int
	for i, p := range plats {
		if ctx.Err() != nil {
			break
		}
		if i > 0 && !wait(ctx, politeDelay(auth.FetchBase)) {
			break
		}
		res := s.refreshPlatform(ctx, fetcher, auth, p, auto)
		if res.Status == StatusError {
			failed++
		} else {
			ok++
		}
		s.recordResult(res)
	}

	status := RunOK
	switch {
	case ok == 0 && failed > 0:
		status = RunError
	case failed > 0:
		status = RunPartial
	}
	detail := s.runDetail()
	s.store.SetDatRefreshResult(auth.Name, status, failureDetail(s.snapshotResults()), time.Now())
	// One durable row per run: per-platform entries would be noise at eleven
	// platforms a pass, and the per-platform truth already lives in
	// dat_snapshots.
	s.store.LogActivity("dat_refreshed", "DAT refresh ("+authorityLabel(auth)+")", detail, "", nil)
	slog.Info("dat refresh finished", "authority", auth.Name, "status", status, "ok", ok, "failed", failed)
}

func (s *Service) refreshPlatform(ctx context.Context, f Fetcher, auth db.DatAuthorityRow, p db.DatPlatformRow, auto bool) PlatformResult {
	if !p.Enabled {
		return PlatformResult{Platform: p.PlatformSlug, Status: StatusSkipped, Error: "assignment disabled"}
	}
	payload, err := f.Fetch(ctx, auth.FetchBase, p.DatCode)
	if err != nil {
		return PlatformResult{Platform: p.PlatformSlug, Status: StatusError, Error: err.Error()}
	}
	res, err := s.importCatalog(auth, p.PlatformSlug, payload.Body, auto)
	if err != nil {
		return PlatformResult{Platform: p.PlatformSlug, Status: StatusError, Error: err.Error()}
	}
	return res
}

// importCatalog is the single path from bytes to a stored snapshot, shared by
// fetches and uploads so a hand-uploaded DAT replaces a fetched one through
// exactly the same code.
func (s *Service) importCatalog(auth db.DatAuthorityRow, slug string, transport []byte, auto bool) (PlatformResult, error) {
	// Hash what the source actually sent, before any unwrapping: that is the
	// provenance stamp, and comparing it against the active snapshot turns a
	// re-fetch of unchanged bytes into a no-op instead of a churned snapshot
	// with a phantom diff.
	sum := sha256.Sum256(transport)
	digest := hex.EncodeToString(sum[:])
	// The raw copy has to be there too: a snapshot whose on-disk catalog was
	// lost must be able to re-materialize it rather than short-circuit forever.
	//
	// And the PARSER version has to match: identical bytes imported by an older
	// derivation are stale rows, not current ones. Without this an improvement
	// to what the parser derives — regions, flags, titles — would never reach a
	// catalog whose source has not changed, which is most of them.
	if active, ok := s.store.ActiveDatSnapshot(slug); ok && active.SourceSHA256 == digest &&
		active.ParserVersion == dat.ParserVersion && rawExists(active.RawPath) {
		return PlatformResult{
			Platform: slug, Status: StatusUnchanged, Version: active.Version,
			Games: active.GameCount, Roms: active.RomCount,
		}, nil
	}

	raw, err := unwrap(transport)
	if err != nil {
		return PlatformResult{}, err
	}
	file, err := dat.Parse(raw)
	if err != nil {
		return PlatformResult{}, err
	}
	if len(file.Games) == 0 {
		return PlatformResult{}, fmt.Errorf("catalog for %s is empty", slug)
	}

	// Pinning freezes automatic advance only. A manual refresh is an
	// operator saying "advance it", which is the whole point of leaving the
	// button live while the cadence is pinned.
	if auto && auth.PinnedVersion != "" && file.Header.Version != auth.PinnedVersion {
		return PlatformResult{
			Platform: slug, Status: StatusPinned, Version: file.Header.Version,
			Error: "pinned to " + auth.PinnedVersion,
		}, nil
	}

	// A missing on-disk copy is a diagnostics loss, not a data loss: the
	// catalog itself lives in SQLite, so the import proceeds. raw_path stays
	// empty rather than pointing at a file that is not there.
	rawPath, err := s.writeRaw(auth.Name, slug, raw)
	if err != nil {
		slog.Warn("dat raw copy failed", "authority", auth.Name, "platform", slug, "error", err)
		rawPath = ""
	}

	meta := db.DatSnapshotMeta{
		Authority:     auth.Name,
		PlatformSlug:  slug,
		Version:       file.Header.Version,
		SourceSHA256:  digest,
		RawPath:       rawPath,
		ParserVersion: dat.ParserVersion,
	}
	if stats, ok := file.SizeStats(); ok {
		meta.SizeMin, meta.SizeMax = stats.Min, stats.Max
		meta.SizeP01, meta.SizeP50, meta.SizeP99 = stats.P01, stats.P50, stats.P99
	}
	// A sizeless catalog imports with zero size columns on purpose: the
	// derived definition then has nothing to say about either end, and the
	// platform is left unbounded rather than inheriting a zero floor.

	row, err := s.store.InsertDatSnapshot(meta, mapGames(file.Games))
	if err != nil {
		return PlatformResult{}, err
	}
	// The catalog moved, so the platform's enforced band moves with it and
	// anyone caching that band is told to drop it. Both happen here, on the
	// single import path shared by fetches and hand-uploaded files.
	s.applySizeDefinition(row)
	if s.onSnapshot != nil {
		s.onSnapshot(slug)
	}
	return PlatformResult{
		Platform: slug, Status: StatusImported, Version: row.Version,
		Games: row.GameCount, Roms: row.RomCount,
		Added: row.DiffAdded, Removed: row.DiffRemoved, Changed: row.DiffChanged,
	}, nil
}

// mapGames converts the parser's model into store rows. This mapping is the
// reason the package exists: internal/db cannot import internal/dat.
func mapGames(games []dat.Game) []db.DatGameRow {
	out := make([]db.DatGameRow, 0, len(games))
	for _, g := range games {
		row := db.DatGameRow{
			Name:      g.Name,
			BareTitle: g.BareTitle,
			Region:    g.Region,
			Languages: g.Languages,
			Revision:  g.Revision,
			CloneOf:   g.CloneOf,
			Flags:     g.Flags,
			// TotalSize is a method on the parser's type and a column here;
			// summing every track is what makes disc bands correct.
			TotalSize: g.TotalSize(),
		}
		for _, r := range g.Roms {
			row.Roms = append(row.Roms, db.DatRomRow{
				Name: r.Name, Size: r.Size, CRC: r.CRC, MD5: r.MD5, SHA1: r.SHA1, Serial: r.Serial,
			})
		}
		out = append(out, row)
	}
	return out
}

// writeRaw keeps the decompressed catalog beside the database so an operator
// can diff or re-upload it without going back to the source.
func (s *Service) writeRaw(authority, slug string, raw []byte) (string, error) {
	if s.cfg == nil || s.cfg.DataDir == "" {
		return "", nil
	}
	if !filepath.IsLocal(authority) || !filepath.IsLocal(slug) {
		return "", fmt.Errorf("unsafe path component in %q/%q", authority, slug)
	}
	dir := filepath.Join(s.cfg.DataDir, "dat", authority)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, slug+".dat")
	tmp := final + ".part"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// ---------- upload ----------

// ImportUpload stores a hand-supplied catalog. With a platform slug it
// imports one DAT (bare, or a zip holding a single catalog); without one it
// treats a zip as a pack and fans it out across the authority's assignments.
//
// This is the escape hatch for every authority, not just the hand-fed ones:
// if a mirror lags or goes away, an operator with the file stays unblocked.
func (s *Service) ImportUpload(authorityName, platformSlug string, body []byte) (UploadResult, error) {
	if s == nil {
		return UploadResult{}, errors.New("DAT service unavailable")
	}
	auth, err := s.store.GetDatAuthority(authorityName)
	if err != nil {
		return UploadResult{}, err
	}
	if len(body) == 0 {
		return UploadResult{}, errors.New("uploaded file is empty")
	}
	if !s.running.CompareAndSwap(false, true) {
		return UploadResult{}, ErrBusy
	}
	defer s.running.Store(false)
	s.beginRun("importing", auth.Name, nil)
	defer s.endRun()

	plats := assignedPlatforms(s.store.ListDatPlatforms(), auth.Name)
	out := UploadResult{Authority: auth.Name}

	if slug := strings.TrimSpace(platformSlug); slug != "" {
		p, ok := findPlatform(plats, slug)
		if !ok {
			return UploadResult{}, fmt.Errorf("platform %q is not assigned to authority %q", slug, auth.Name)
		}
		s.setTotal(1)
		res, err := s.importCatalog(auth, p.PlatformSlug, body, false)
		if err != nil {
			return UploadResult{}, err
		}
		s.recordResult(res)
		out.Imported = append(out.Imported, res)
		return out, nil
	}

	if !isZip(body) {
		return UploadResult{}, errors.New("a single DAT needs ?platform=<slug>; upload a zip to fan out across a lane")
	}
	entries, err := zipDats(body)
	if err != nil {
		return UploadResult{}, err
	}
	s.setTotal(len(entries))
	for _, e := range entries {
		p, ok := matchMember(e.Name, plats)
		if !ok {
			out.Skipped = append(out.Skipped, SkippedMember{
				Member: e.Name,
				Reason: "no platform assigned to " + auth.Name + " matches this catalog",
			})
			s.bumpDone()
			continue
		}
		res, err := s.importCatalog(auth, p.PlatformSlug, e.Body, false)
		if err != nil {
			res = PlatformResult{Platform: p.PlatformSlug, Status: StatusError, Error: err.Error()}
		}
		s.recordResult(res)
		out.Imported = append(out.Imported, res)
	}
	if len(out.Imported) == 0 {
		return out, fmt.Errorf("no member of this archive matches a platform assigned to %q", auth.Name)
	}
	return out, nil
}

// matchMember maps a pack member's filename onto an assignment. Daily packs
// stamp their members ("Atari - 2600 (20260801-063015).dat"), so a member
// matches when its stem is the code, or the code followed by a parenthesised
// stamp. Longest code wins, so "Nintendo - Game Boy" cannot swallow a
// "Nintendo - Game Boy Color" file.
func matchMember(member string, plats []db.DatPlatformRow) (db.DatPlatformRow, bool) {
	stem := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(member, filepath.Ext(member)), " "))
	var best db.DatPlatformRow
	found := false
	for _, p := range plats {
		code := strings.TrimSpace(p.DatCode)
		if code == "" {
			continue
		}
		if !strings.EqualFold(stem, code) && !strings.HasPrefix(strings.ToLower(stem), strings.ToLower(code)+" (") {
			continue
		}
		if !found || len(code) > len(best.DatCode) {
			best, found = p, true
		}
	}
	return best, found
}

func findPlatform(plats []db.DatPlatformRow, slug string) (db.DatPlatformRow, bool) {
	for _, p := range plats {
		if p.PlatformSlug == slug {
			return p, true
		}
	}
	return db.DatPlatformRow{}, false
}

func assignedPlatforms(all []db.DatPlatformRow, authority string) []db.DatPlatformRow {
	var out []db.DatPlatformRow
	for _, p := range all {
		if p.Authority == authority {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlatformSlug < out[j].PlatformSlug })
	return out
}

func authorityLabel(a db.DatAuthorityRow) string {
	if a.Label != "" {
		return a.Label
	}
	return a.Name
}

// rawExists reports whether a snapshot's on-disk catalog is still present.
func rawExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// politeDelay is the gap before the next fetch of the same authority. The
// delay exists to be a good citizen to a third-party mirror, so a loopback
// base (a test stub) gets none — which keeps the e2e journey's eleven
// sequential fetches fast without a test-only knob.
func politeDelay(base string) time.Duration {
	if u, err := url.Parse(base); err == nil {
		host := u.Hostname()
		if host == "localhost" || net.ParseIP(host).IsLoopback() {
			return 0
		}
	}
	return politenessDelay
}

// wait sleeps unless the context ends first; false means "stop".
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// ---------- run state ----------

func (s *Service) beginRun(phase, authority string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.authority, s.cancel = phase, authority, cancel
	s.total, s.done, s.results, s.lastErr = 0, 0, nil, ""
	s.startedAt, s.finishedAt = time.Now(), time.Time{}
}

func (s *Service) endRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase, s.cancel = "idle", nil
	s.finishedAt = time.Now()
}

func (s *Service) setTotal(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total = n
}

func (s *Service) recordResult(res PlatformResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, res)
	s.done++
	if res.Status == StatusError {
		s.lastErr = res.Platform + ": " + res.Error
	}
}

func (s *Service) bumpDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done++
}

func (s *Service) snapshotResults() []PlatformResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PlatformResult(nil), s.results...)
}

func (s *Service) failRun(auth db.DatAuthorityRow, err error) {
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
	s.store.SetDatRefreshResult(auth.Name, RunError, err.Error(), time.Now())
	slog.Warn("dat refresh failed", "authority", auth.Name, "error", err)
}

// runDetail summarises a run for the activity log.
func (s *Service) runDetail() string {
	counts := map[string]int{}
	for _, r := range s.snapshotResults() {
		counts[r.Status]++
	}
	return fmt.Sprintf("%d imported, %d unchanged, %d pinned, %d failed",
		counts[StatusImported], counts[StatusUnchanged], counts[StatusPinned], counts[StatusError])
}

// failureDetail keeps per-platform errors in last_error, since one status
// word cannot describe seven-of-eleven-succeeded.
func failureDetail(results []PlatformResult) string {
	var parts []string
	for _, r := range results {
		if r.Status == StatusError {
			parts = append(parts, r.Platform+": "+r.Error)
		}
	}
	return strings.Join(parts, "; ")
}

// Status reports the service state for the API poller. Nil-receiver safe.
func (s *Service) Status() map[string]interface{} {
	if s == nil {
		return map[string]interface{}{"enabled": false, "running": false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{
		"enabled":       s.cfg.DatAutoRefreshOn(),
		"interval_days": s.cfg.DatRefreshIntervalDays(),
		"loop_running":  s.LoopRunning(),
		"running":       s.running.Load(),
		"phase":         s.phase,
		"authority":     s.authority,
		"total":         s.total,
		"done":          s.done,
		"results":       s.results,
		"last_error":    s.lastErr,
		"started_at":    timeOrEmpty(s.startedAt),
		"finished_at":   timeOrEmpty(s.finishedAt),
	}
}

func timeOrEmpty(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

// ---------- cadence loop ----------

// EnsureRunning arms the refresh cadence when the toggle is on. It is a
// no-op while the toggle is off, which is how it ships.
func (s *Service) EnsureRunning() {
	if s == nil || s.cfg == nil || !s.cfg.DatAutoRefreshOn() {
		return
	}
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	if s.loopLive {
		return
	}
	// A shutdown Stop leaves the channel closed; re-open before arming or
	// the new loop exits on its first select.
	select {
	case <-s.stopCh:
		s.stopCh = make(chan struct{})
	default:
	}
	s.loopLive = true
	go s.loop(s.stopCh, time.Duration(s.cfg.DatRefreshIntervalDays())*24*time.Hour)
}

// StopLoop is the runtime disable: it halts the cadence and immediately
// re-opens the stop channel, so a later EnsureRunning (or the manual refresh
// button, which never consults it) keeps working. Any in-flight refresh runs
// to completion — turning the cadence off should not abort work an operator
// started by hand.
func (s *Service) StopLoop() {
	if s == nil {
		return
	}
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	s.closeStopLocked()
	s.stopCh = make(chan struct{})
	s.loopLive = false
}

// Stop is the shutdown path: the channel stays closed and the in-flight
// refresh is cancelled.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.loopMu.Lock()
	s.closeStopLocked()
	s.loopLive = false
	s.loopMu.Unlock()

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

func (s *Service) closeStopLocked() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// LoopRunning reports whether the cadence is armed.
func (s *Service) LoopRunning() bool {
	if s == nil {
		return false
	}
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	return s.loopLive
}

func (s *Service) loop(stop chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.refreshAllAuto(stop)
		}
	}
}

// refreshAllAuto walks every enabled, automatable authority. It honours the
// single-flight gate: a cadence tick that lands during a manual refresh is
// dropped rather than queued.
func (s *Service) refreshAllAuto(stop chan struct{}) {
	for _, auth := range s.store.ListDatAuthorities() {
		select {
		case <-stop:
			return
		default:
		}
		if !auth.Enabled || auth.FetchDriver == DriverUpload {
			continue
		}
		if !s.running.CompareAndSwap(false, true) {
			slog.Info("dat cadence skipped: a refresh is already running", "authority", auth.Name)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.beginRun("refreshing", auth.Name, cancel)
		s.runRefresh(ctx, auth, true)
		s.endRun()
		cancel()
		s.running.Store(false)
	}
}
