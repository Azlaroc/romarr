// Package libscan is the library's root-folder scanner: it walks the ROM
// tree and reconciles what is on disk with what library_items believes,
// so RomArr's inventory is owned by RomArr — from its own root folders —
// rather than borrowed from whatever catalogued the files first.
//
// Doctrine (D1, reversed 2026-08): "what is held" belongs to this app's own
// DB over its own folders. Downstream catalogs hold their own views for
// their own consumers; neither side is the other's oracle.
//
// Three verbs, none of them delete:
//
//   - adopt: a row already tracks the file (any source — import, sync, a
//     prior scan). The row is kept, annotated where it is silent (hashes,
//     catalog verdict, a registry-unknown slug repaired from the directory),
//     and its source is NEVER changed: sources mark provenance, and other
//     planes' reconcile sweeps filter on them.
//   - create: no row tracks the file — an out-of-band arrival. A row is
//     minted with source 'libscan'.
//   - report: a row whose file is gone is reported as missing and left
//     alone. This scanner has no authority over rows it did not create,
//     and none over files at all.
//
// Platform identity comes from the top-level directory name alone, checked
// against the registry (FromRommFSSlug → Lookup). No extension guessing: an
// unattended scanner must not guess, and the vocabulary that guesses lives
// in manual import where an operator confirms every row. An unmapped
// directory is reported and skipped; a root-level file is reported as
// unsorted.
//
// Shape: the hash backfill's skeleton — single-flight, one phase, DryRun for
// free (same pass, no writes), results held and paged in memory, one
// activity summary per run, an error breaker for systemic failures.
package libscan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/platform"
	"gamarr/internal/romfile"
)

// Row statuses.
const (
	StatusCreated         = "created"
	StatusAdopted         = "adopted"
	StatusMissing         = "missing"          // row's file is gone; reported, never deleted
	StatusUnvisited       = "unvisited"        // row's file exists but the walk never enumerated it
	StatusUnknownPlatform = "unknown-platform" // top-level dir names no registered platform
	StatusUnsorted        = "unsorted"         // file at the tree root, outside any platform dir
	StatusError           = "error"
)

// Detail strings for entries that could not be measured. Stable: displayed,
// counted and asserted on.
const (
	DetailDirectory = "directory — a multi-file entry has no single ROM identity"
	DetailMultiFile = "archive holds more than one file"
	DetailRar       = "rar unsupported (no extractor in image)"
	DetailNoSpace   = "not enough free space to extract"
	DetailNested    = "inside an entry the scan already tracks"
	DetailUnscanned = "under a directory the scan did not walk"
	// DetailUnmeasured marks an adopted row with no stored measurement: the
	// routine scan refuses to pay file I/O for it (a first scan would read
	// hundreds of GB). Force, scoped to a platform, is the way to fill it.
	DetailUnmeasured = "no stored measurement — force a re-measure to fill the verdict"
)

// maxConsecutiveErrors aborts a run failing for a systemic reason — a
// vanished mount, a broken extractor — rather than grinding through
// thousands of files to report the same failure N times. Only StatusError
// increments it: a platform of multi-file packs is legitimate work.
const maxConsecutiveErrors = 25

// workDirName is this runner's scratch dir for archive extraction. It must
// be its own: the renamer's is os.RemoveAll'd at every preview start and the
// hash backfill's at every run start, and either would delete a concurrent
// scan's in-flight extraction. Dot-prefixed so the walk skips it.
const workDirName = ".gamarr-scan-tmp"

// Row is one scan observation: a walked file, or a library row the
// reconciliation pass had something to say about.
type Row struct {
	LibraryID    int64  `json:"library_id,omitempty"`
	PlatformSlug string `json:"platform_slug,omitempty"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	Status       string `json:"status"`
	// Detail carries the sub-classification: a measurement skip reason, an
	// error, a slug repair note.
	Detail string `json:"detail,omitempty"`
	// Catalog is the verdict recorded (or, dry-run, that would be recorded)
	// for this entry during this run. Empty when the row already carried one.
	Catalog string `json:"catalog,omitempty"`
}

// Opts are one run's switches.
type Opts struct {
	// DryRun walks, measures and reports everything, and writes nothing.
	DryRun bool
	// Force re-measures entries whose rows already carry hashes or a
	// verdict, for re-deriving after a rule change. It still never
	// downgrades a banked verdict to unknown: converted formats (CHD) were
	// verified before conversion and re-measuring them can only say unknown.
	Force bool
}

// Runner scans one platform directory, or the whole tree, once at a time.
type Runner struct {
	cfg   *config.Config
	store *db.JobStore

	running atomic.Bool

	mu          sync.Mutex
	cancel      context.CancelFunc
	phase       string // idle | enumerating | scanning | reconciling
	scope       string
	opts        Opts
	total       int
	done        int
	created     int
	adopted     int
	missing     int
	unvisited   int
	errCount    int
	bytesHashed int64
	lastErr     string
	startedAt   time.Time
	finishedAt  time.Time
	rows        []Row
	counts      map[string]int
}

// New builds a Runner.
func New(cfg *config.Config, store *db.JobStore) *Runner {
	return &Runner{cfg: cfg, store: store, phase: "idle"}
}

// Trigger starts an async scan over scope ("all" or a platform slug).
// Returns false when a run is already in flight.
func (r *Runner) Trigger(scope string, opts Opts) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.phase = "enumerating"
	r.scope = scope
	r.opts = opts
	r.total, r.done, r.created, r.adopted, r.missing, r.unvisited, r.errCount = 0, 0, 0, 0, 0, 0, 0
	r.bytesHashed = 0
	r.lastErr = ""
	r.startedAt = time.Now()
	r.finishedAt = time.Time{}
	r.rows = nil
	r.counts = map[string]int{}
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		defer cancel()
		r.run(ctx, scope, opts)
		r.mu.Lock()
		r.phase = "idle"
		r.finishedAt = time.Now()
		created, adopted, missing, unvisited, errs, dry := r.created, r.adopted, r.missing, r.unvisited, r.errCount, r.opts.DryRun
		label := r.scope
		r.mu.Unlock()
		if dry {
			return // a dry run changed nothing; the activity log records changes
		}
		if label == "" || label == "all" {
			label = "all platforms"
		}
		// One summary row per run: per-file entries would be noise at library
		// scale, and the runner's own state is in-memory — the activity log is
		// the durable record that the run happened.
		r.store.LogActivity("library_scanned", "Library scan ("+label+")",
			fmt.Sprintf("%d created, %d adopted, %d missing, %d unvisited, %d errors",
				created, adopted, missing, unvisited, errs), "", nil)
	}()
	return true
}

// Stop cancels an in-flight run; the Runner stays usable.
func (r *Runner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Status reports the current or last run. Nil-receiver safe.
func (r *Runner) Status() map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"phase": "idle", "running": false, "configured": false}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]interface{}{
		"configured": true,
		"running":    r.running.Load(),
		"phase":      r.phase,
		"scope":      r.scope,
		"dry_run":    r.opts.DryRun,
		"force":      r.opts.Force,
		"total":      r.total,
		"done":       r.done,
		"created":    r.created,
		"adopted":    r.adopted,
		"missing":    r.missing,
		"unvisited":  r.unvisited,
		"errors":     r.errCount,
		// Bytes, not just rows: work concentrated in a few multi-gigabyte
		// files leaves `done` sitting still, and this is the sign of life.
		"bytes_hashed": r.bytesHashed,
		"last_error":   r.lastErr,
		"counts":       copyCounts(r.counts),
		"started_at":   timeOrEmpty(r.startedAt),
		"finished_at":  timeOrEmpty(r.finishedAt),
	}
}

func timeOrEmpty(t time.Time) interface{} {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ResultsPage returns a page of the held rows plus the total.
func (r *Runner) ResultsPage(page, pageSize int) ([]Row, int) {
	if r == nil {
		return nil, 0
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total := len(r.rows)
	start := (page - 1) * pageSize
	if start >= total {
		return []Row{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]Row, end-start)
	copy(out, r.rows[start:end])
	return out, total
}

// entry is one walked filesystem object that should be a library row.
type entry struct {
	path   string
	topDir string // the platform directory's name
	slug   string // registry slug resolved from topDir
	isDir  bool
	size   int64
}

func (r *Runner) run(ctx context.Context, scope string, opts Opts) {
	root := r.cfg.GamesRomsPath
	if root == "" {
		r.fail("no ROM library path configured")
		return
	}

	workRoot := filepath.Join(root, workDirName)
	os.RemoveAll(workRoot) // reap leftovers from a crashed run
	defer os.RemoveAll(workRoot)

	entries, scanned := r.enumerate(scope, root)

	r.mu.Lock()
	r.total = len(entries)
	r.phase = "scanning"
	r.mu.Unlock()

	seen := make(map[string]bool, len(entries))
	consecutive := 0
	for i := range entries {
		select {
		case <-ctx.Done():
			r.fail("stopped")
			return
		default:
		}

		row := r.visit(ctx, entries[i], workRoot, opts)
		seen[entries[i].path] = true
		r.mu.Lock()
		r.done++
		r.rows = append(r.rows, row)
		switch row.Status {
		case StatusCreated:
			r.created++
		case StatusAdopted:
			r.adopted++
		case StatusError:
			r.errCount++
			r.lastErr = row.Detail
		}
		if row.Detail != "" && row.Status != StatusError {
			r.counts[row.Detail]++
		}
		r.mu.Unlock()

		if row.Status == StatusError {
			consecutive++
			if consecutive >= maxConsecutiveErrors {
				r.fail(fmt.Sprintf("aborted after %d consecutive errors: %s", consecutive, row.Detail))
				slog.Error("library scan aborted", "consecutive_errors", consecutive, "last", row.Detail)
				return
			}
			continue
		}
		consecutive = 0
	}

	r.mu.Lock()
	r.phase = "reconciling"
	r.mu.Unlock()
	r.reconcile(scope, root, seen, scanned)
}

// fail records a terminal message on the run.
func (r *Runner) fail(msg string) {
	r.mu.Lock()
	r.lastErr = msg
	r.mu.Unlock()
}

// enumerate walks the tree and returns the entries to visit plus the set of
// top-level directory names that were actually scanned. It also emits the
// report-only rows for unmapped directories and unsorted root files.
func (r *Runner) enumerate(scope, root string) ([]entry, map[string]bool) {
	scanned := map[string]bool{}
	var out []entry

	var tops []string
	if scope != "" && scope != "all" {
		tops = []string{platform.ToRommFSSlug(scope)}
	} else {
		dirents, err := os.ReadDir(root)
		if err != nil {
			r.fail("cannot read library root: " + err.Error())
			return nil, scanned
		}
		for _, e := range dirents {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !e.IsDir() {
				// A file at the tree root belongs to no platform. Report it;
				// guessing a platform for it is exactly what this scanner
				// refuses to do.
				r.report(Row{Path: filepath.Join(root, name), Name: name, Status: StatusUnsorted})
				continue
			}
			tops = append(tops, name)
		}
	}
	sort.Strings(tops)

	for _, top := range tops {
		dir := filepath.Join(root, top)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			r.report(Row{Path: dir, Name: top, Status: StatusError, Detail: "platform directory missing"})
			continue
		}
		slug := platform.FromRommFSSlug(top)
		if _, known := platform.Lookup(slug); !known {
			// The registry is the whole vocabulary. A directory it cannot
			// name gets reported once and its contents left alone — not
			// imported under a minted slug.
			r.report(Row{Path: dir, Name: top, Status: StatusUnknownPlatform,
				Detail: "directory does not name a registered platform"})
			continue
		}
		scanned[top] = true
		r.walkPlatform(dir, top, slug, &out)
	}
	return out, scanned
}

// walkPlatform collects entries under one platform dir — ONE level, never
// deeper. The library model (RomM's, and now ours) is platform/entry: every
// depth-1 item is one entry, a directory being a multi-file game. Verified
// against the real library before this shipped: all 21K rows sit at exactly
// depth 1, including whole directories-of-files as single rows. A recursion
// heuristic ("does this dir hold game files?") was tried and rejected — it
// keyed on an extension list that no cart platform's DAT-canonical names
// (.a26, .pce, .lnx, ...) stay inside, and misreading a game dir as
// organizational would mint depth-2 rows no other plane expects.
//
// Files have no extension allowlist either: the sidecar exclusion is the
// honest filter, for the same reason.
func (r *Runner) walkPlatform(dir, top, slug string, out *[]entry) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		r.report(Row{Path: dir, Name: filepath.Base(dir), PlatformSlug: slug,
			Status: StatusError, Detail: "unreadable: " + err.Error()})
		return
	}
	for _, e := range dirents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasSuffix(name, ".gamarr.json") || strings.HasSuffix(name, ".extracted") {
			continue
		}
		fp := filepath.Join(dir, name)
		if e.IsDir() {
			*out = append(*out, entry{path: fp, topDir: top, slug: slug, isDir: true})
			continue
		}
		if romfile.IsSidecarExtension(name) {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		*out = append(*out, entry{path: fp, topDir: top, slug: slug, size: size})
	}
}

// report appends a bookkeeping row (no visit counter — these are not work
// items) and tallies it.
func (r *Runner) report(row Row) {
	r.mu.Lock()
	r.rows = append(r.rows, row)
	r.counts[row.Status]++
	if row.Status == StatusError {
		r.errCount++
		r.lastErr = row.Detail
	}
	r.mu.Unlock()
}

// visit reconciles one walked entry with the library.
func (r *Runner) visit(ctx context.Context, e entry, workRoot string, opts Opts) Row {
	row := Row{
		PlatformSlug: e.slug,
		Path:         e.path,
		Name:         filepath.Base(e.path),
		Size:         e.size,
	}

	if existing := r.store.LibraryItemByFilePath(e.path); existing != nil {
		return r.adopt(ctx, existing, e, workRoot, opts, row)
	}
	return r.create(ctx, e, workRoot, opts, row)
}

// adopt annotates an existing row: repair a registry-unknown slug from the
// directory, and fill an absent verdict — from stored hashes when the row
// has them (zero file I/O; this is what makes the first full scan over a
// large synced library operable), by measuring otherwise. The row's source
// is never touched: other planes' reconcile sweeps filter on source, and
// flipping it would hand this row to their delete clauses.
func (r *Runner) adopt(ctx context.Context, item *db.LibraryItem, e entry, workRoot string, opts Opts, row Row) Row {
	row.Status = StatusAdopted
	row.LibraryID = item.ID

	// Slug repair: only when the stored slug is registry-unknown AND the
	// directory names a slug the registry knows. A registry-known stored
	// slug is someone's decision; the scanner does not second-guess it.
	if _, known := platform.Lookup(item.PlatformSlug); !known {
		row.Detail = fmt.Sprintf("platform repaired: %q → %q", item.PlatformSlug, e.slug)
		if !opts.DryRun {
			if err := r.store.UpdateLibraryItemPlatform(item.ID, platform.DisplayName(e.slug), e.slug); err != nil {
				row.Status, row.Detail = StatusError, "slug repair: "+err.Error()
				return row
			}
		}
		item.PlatformSlug = e.slug
	}

	catalog, detail, errDetail := r.ensureVerdict(ctx, item, e, workRoot, opts)
	if errDetail != "" {
		row.Status, row.Detail = StatusError, errDetail
		return row
	}
	row.Catalog = catalog
	if row.Detail == "" {
		row.Detail = detail
	}
	return row
}

// create mints a library row for an out-of-band arrival.
//
// 🔴 source is 'libscan', not 'scan': the RomM sync's full reconcile purges
// source='scan' AND is_pc=0 rows as legacy, while its adopt clause merges
// any source it does not own — so 'libscan' rows compose with that plane
// with zero changes to it, and 'scan' rows would be deleted nightly.
func (r *Runner) create(ctx context.Context, e entry, workRoot string, opts Opts, row Row) Row {
	row.Status = StatusCreated

	var hashes *db.LibraryHashes
	catalog := db.CatalogUnknown
	var measureErr error
	if e.isDir {
		row.Detail = DetailDirectory
		row.Size = dirSize(e.path)
	} else {
		res, err := romfile.Measure(ctx, e.path, workRoot)
		if err != nil {
			detail, hard := classifyMeasure(err)
			if hard {
				row.Status, row.Detail = StatusError, detail
				return row
			}
			row.Detail = detail
			measureErr = err
		} else {
			r.addBytes(res.Size)
			hashes = measuredHashes(res)
			catalog = r.doubleAsk(e.slug, filepath.Base(e.path), res.Hashes, payloadOf(res))
			if e.size == 0 {
				row.Size = res.Size
			}
		}
	}
	row.Catalog = catalog
	if opts.DryRun {
		return row
	}

	title := filepath.Base(e.path)
	if !e.isDir {
		title = strings.TrimSuffix(title, filepath.Ext(title))
	}
	id, created, err := r.store.AddLibraryItemUnlessPathTracked(&db.LibraryItem{
		Title:        title,
		Platform:     platform.DisplayName(e.slug),
		PlatformSlug: e.slug,
		FilePath:     e.path,
		FileSize:     row.Size,
		Source:       "libscan",
		SourceType:   "libscan",
		SourceID:     "libscan:" + e.path,
		Metadata:     "{}",
	})
	if err != nil {
		row.Status, row.Detail = StatusError, "create: "+err.Error()
		return row
	}
	if !created {
		// Raced by a concurrent writer (the RomM sync inserting the same
		// arriving file): the row exists now, which is the outcome we wanted.
		row.Status = StatusAdopted
		if item := r.store.LibraryItemByFilePath(e.path); item != nil {
			row.LibraryID = item.ID
		}
		return row
	}
	row.LibraryID = id
	if hashes != nil {
		if err := r.store.SaveLibraryHashes(id, *hashes); err != nil {
			row.Status, row.Detail = StatusError, "save hashes: "+err.Error()
			return row
		}
	}
	r.store.SetLibraryCatalogStatusByID(id, catalog)
	// Permanent classifications are recorded exactly as the hash backfill
	// records them, so neither plane ever re-extracts this entry to re-learn
	// what it is.
	if e.isDir {
		r.markSkipReason(id, db.HashSkipDirectory, opts)
	} else if measureErr != nil {
		r.markSkip(id, measureErr, opts)
	}
	return row
}

// markSkip records a permanent skip marker for a measurement classification;
// transient ones (no space) are deliberately not recorded — worth retrying.
func (r *Runner) markSkip(id int64, err error, opts Opts) {
	var multi *romfile.MultiFileError
	switch {
	case errors.Is(err, romfile.ErrRarArchive):
		r.markSkipReason(id, db.HashSkipRar, opts)
	case errors.As(err, &multi):
		r.markSkipReason(id, db.HashSkipMultiFile, opts)
	}
}

func (r *Runner) markSkipReason(id int64, reason string, opts Opts) {
	if opts.DryRun {
		return // a dry run leaves no trace, and a marker is a write
	}
	if err := r.store.MarkLibraryHashSkipped(id, reason); err != nil {
		slog.Warn("mark hash skip", "library_id", id, "error", err)
	}
}

// detailForMarker maps a stored skip marker back onto this runner's detail
// vocabulary.
func detailForMarker(marker string) string {
	switch marker {
	case db.HashSkipDirectory:
		return DetailDirectory
	case db.HashSkipMultiFile:
		return DetailMultiFile
	case db.HashSkipRar:
		return DetailRar
	}
	return marker
}

// ensureVerdict fills a row's catalog verdict from what is already known.
// Returns the verdict recorded ("" when the banked one was kept), a detail
// note, and an error detail ("" when none).
//
// A verdict means MEASURED: it comes from $.gamarr hashes (stored, or
// computed under Force), never from $.romm — that namespace is rewritten
// wholesale by a plane this app does not control, and a verdict minted from
// it would assert evidence nobody here ever saw.
//
// 🔴 The routine scan NEVER measures an adopted row. Sized against the real
// library, "measure whatever lacks stored hashes" meant hundreds of GB of
// reads — and extraction of every multi-file arcade set — on every first
// scan. So without Force: stored hashes answer for free, a skip marker or a
// directory answers unknown for free, and everything else is counted as
// unmeasured and left verdict-absent. Force is the explicit, per-platform
// way to pay for measurement.
func (r *Runner) ensureVerdict(ctx context.Context, item *db.LibraryItem, e entry, workRoot string, opts Opts) (catalog, detail, errDetail string) {
	existing := db.LibraryCatalogStatus(item.Metadata)
	if existing != "" && !opts.Force {
		return "", "", ""
	}

	if !opts.Force {
		// Stored $.gamarr hashes answer without touching the file.
		if gh, ok := db.ParseGamarrHashes(item.Metadata); ok {
			var unh romfile.Hashes
			if gh.Unh != nil {
				unh = romfile.Hashes{CRC: gh.Unh.CRC, MD5: gh.Unh.MD5, SHA1: gh.Unh.SHA1}
			}
			verdict := r.doubleAsk(item.PlatformSlug, filepath.Base(e.path),
				romfile.Hashes{CRC: gh.CRC, MD5: gh.MD5, SHA1: gh.SHA1}, unh)
			return r.recordVerdict(item.ID, existing, verdict, opts), "", ""
		}
		// A permanent skip marker is a measurement that already happened:
		// the entry was extracted once and can never carry a single-ROM
		// hash. Same class as a directory: unknown, for free.
		if skip := db.ParseHashSkip(item.Metadata); skip != "" {
			return r.recordVerdict(item.ID, existing, db.CatalogUnknown, opts), detailForMarker(skip), ""
		}
		if e.isDir {
			return r.recordVerdict(item.ID, existing, db.CatalogUnknown, opts), DetailDirectory, ""
		}
		return "", DetailUnmeasured, ""
	}

	// Force: the operator asked to pay for a re-measure.
	if e.isDir {
		return r.recordVerdict(item.ID, existing, db.CatalogUnknown, opts), DetailDirectory, ""
	}
	res, err := romfile.Measure(ctx, e.path, workRoot)
	if err != nil {
		d, hard := classifyMeasure(err)
		if hard {
			return "", "", d
		}
		r.markSkip(item.ID, err, opts)
		return r.recordVerdict(item.ID, existing, db.CatalogUnknown, opts), d, ""
	}
	r.addBytes(res.Size)
	if !opts.DryRun {
		if err := r.store.SaveLibraryHashes(item.ID, *measuredHashes(res)); err != nil {
			return "", "", "save hashes: " + err.Error()
		}
	}
	verdict := r.doubleAsk(item.PlatformSlug, filepath.Base(e.path), res.Hashes, payloadOf(res))
	return r.recordVerdict(item.ID, existing, verdict, opts), "", ""
}

// recordVerdict writes a verdict unless doing so would downgrade a banked
// one to unknown (converted formats were verified pre-conversion). Returns
// what the row's verdict now is (or would be, dry-run), "" when unchanged.
func (r *Runner) recordVerdict(id int64, existing, verdict string, opts Opts) string {
	if verdict == existing {
		return ""
	}
	if existing != "" && verdict == db.CatalogUnknown {
		return ""
	}
	if !opts.DryRun {
		r.store.SetLibraryCatalogStatusByID(id, verdict)
	}
	return verdict
}

// doubleAsk is the trust gate's question, replicated: ask the catalog about
// the whole-file hashes, and when a container header was stripped ask again
// with the payload's — a whole-file miss on a headered platform is not a
// miss. Pointer, not import: the gate lives in internal/download
// (catalogVerdict), which this package must not depend on.
func (r *Runner) doubleAsk(slug, fileName string, whole, payload romfile.Hashes) string {
	v := r.store.MatchDatRom(slug, fileName, whole.CRC, whole.MD5, whole.SHA1)
	if v.Status != db.CatalogVerified && !payload.Zero() {
		if pv := r.store.MatchDatRom(slug, fileName, payload.CRC, payload.MD5, payload.SHA1); pv.Status == db.CatalogVerified {
			v = pv
		}
	}
	return v.Status
}

// reconcile reads what the DB believes lives under the scanned tree and
// reports rows the walk never confirmed: gone files as missing (never
// deleted — this scanner does not own removals), present-but-unreached files
// as unvisited (an enumeration gap worth seeing), rows under unscanned
// directories and rows inside adopted multi-file entries as counts.
func (r *Runner) reconcile(scope, root string, seen map[string]bool, scanned map[string]bool) {
	prefix := root
	if scope != "" && scope != "all" {
		prefix = filepath.Join(root, platform.ToRommFSSlug(scope))
	}
	for _, item := range r.store.ListLibraryItemsUnderPath(prefix) {
		if seen[item.FilePath] {
			continue
		}
		rel, err := filepath.Rel(root, item.FilePath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if !scanned[top] {
			r.count(DetailUnscanned)
			continue
		}
		if underSeen(item.FilePath, root, seen) {
			r.count(DetailNested)
			continue
		}
		row := Row{
			LibraryID:    item.ID,
			PlatformSlug: item.PlatformSlug,
			Path:         item.FilePath,
			Name:         filepath.Base(item.FilePath),
		}
		if _, err := os.Stat(item.FilePath); os.IsNotExist(err) {
			row.Status = StatusMissing
			r.mu.Lock()
			r.missing++
			r.rows = append(r.rows, row)
			r.mu.Unlock()
			continue
		}
		row.Status = StatusUnvisited
		row.Detail = "file exists but the walk never enumerated it"
		r.mu.Lock()
		r.unvisited++
		r.rows = append(r.rows, row)
		r.mu.Unlock()
	}
}

func (r *Runner) count(key string) {
	r.mu.Lock()
	r.counts[key]++
	r.mu.Unlock()
}

// addBytes records bytes actually READ by a measurement. Only measurements
// count: an adoption that answered from stored hashes read nothing, and a
// progress line claiming hundreds of GB for a zero-I/O pass is a lie.
func (r *Runner) addBytes(n int64) {
	r.mu.Lock()
	r.bytesHashed += n
	r.mu.Unlock()
}

// underSeen reports whether path sits inside an entry the walk adopted or
// created — a disc inside a tracked game folder is accounted for by its
// folder's row, not missing from the scan.
func underSeen(path, root string, seen map[string]bool) bool {
	for p := filepath.Dir(path); len(p) > len(root); p = filepath.Dir(p) {
		if seen[p] {
			return true
		}
	}
	return false
}

// classifyMeasure maps a Measure failure onto a detail string; hard reports
// whether it is a real error (counted toward the breaker) rather than a
// classification of the entry.
func classifyMeasure(err error) (detail string, hard bool) {
	var multi *romfile.MultiFileError
	switch {
	case errors.Is(err, romfile.ErrIsDirectory):
		return DetailDirectory, false
	case errors.Is(err, romfile.ErrRarArchive):
		return DetailRar, false
	case errors.Is(err, romfile.ErrNoSpace):
		return DetailNoSpace, false
	case errors.As(err, &multi):
		return DetailMultiFile, false
	}
	return err.Error(), true
}

// measuredHashes maps a measurement onto the row shape the store persists.
func measuredHashes(res romfile.Result) *db.LibraryHashes {
	h := &db.LibraryHashes{CRC: res.CRC, MD5: res.MD5, SHA1: res.SHA1}
	if res.Stripped {
		h.Unh = &db.UnheaderedHashes{
			CRC: res.Payload.CRC, MD5: res.Payload.MD5, SHA1: res.Payload.SHA1,
			Header: res.HeaderKind,
		}
	}
	return h
}

// payloadOf returns the payload hashes when a header was stripped, zero
// otherwise.
func payloadOf(res romfile.Result) romfile.Hashes {
	if res.Stripped {
		return res.Payload
	}
	return romfile.Hashes{}
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// copyCounts returns a snapshot of the tally.
//
// 🔴 Status() must not hand out the live map: the caller encodes it outside
// the lock while the run goroutine is still incrementing.
func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
