// Package hashfill computes and stores the hashes of library rows that carry
// none.
//
// Ownership is decided in three tiers — a hash is proof, a canonical name is
// strong, a parsed title is a guess — and a row with no hash can only ever be
// matched by the weaker two. That is not a per-platform inconvenience: the
// same index answers collection mode's gap list, the selector's owned-check,
// the duplicate gate and the declutter's verdict ladder, and the declutter
// rightly refuses to archive anything matched by a guess.
//
// Rows arrive hashless when they were synced from a RomM row that carries no
// rom-level hashes. On current RomM (verified 4.9.2 and 5.1.0) that is NOT a
// scan-type property: newly-added rows build files and hashes on any scan
// type, quick included, unless SKIP_HASH_CALCULATION is set or the platform
// is in RomM's NON_HASHABLE_PLATFORMS set (switch, wiiu, PC, PS3+, ...).
// The hashless population in the live library dates to a July-2025 bulk
// import that predates RomM 4.0.0 ("Hashed Edition", 2025-07-20) — hashing
// did not exist yet, and upgrades never backfill. New hashless rows can still
// appear via non-hashable platforms or a future SKIP_HASH_CALCULATION, so
// this stays a re-runnable sweep rather than a one-time migration.
// (Corrected 2026-08-28: an earlier version of this comment claimed "a quick
// scan does not hash" — wrong for newly-added rows, and exactly the class of
// confidently-recorded rule this codebase has been burned by.)
//
// Shape: ONE phase, not the renamer's preview-then-apply. Nothing here moves
// or deletes anything — it writes a JSON field on rows that have none — so
// there is no diff for a human to approve, and a preview phase would pay the
// entire cost of the run (the hashing) only to throw the answer away. DryRun
// gives the same affordance for free: identical pass, no write, and the rows
// are held and paged exactly as the other two runners' previews are.
package hashfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/romfile"
)

// Row statuses.
const (
	StatusHashed = "hashed"
	StatusSkip   = "skip"
	StatusError  = "error"
)

// Skip reasons. Stable strings: they are displayed, counted and asserted on.
//
// The first three are permanent facts about the entry and are recorded on the
// row so it stops being enumerated — otherwise "rows still needing a hash"
// parks a remainder that reads as a stuck job. The rest are today's weather
// and are deliberately NOT recorded: they are worth retrying.
const (
	SkipDirectory = "directory — a multi-file entry has no single ROM identity"
	SkipMultiFile = "archive holds more than one file"
	SkipRar       = "rar unsupported (no extractor in image)"
	SkipMissing   = "file is gone since the row was written"
	SkipNoSpace   = "not enough free space to extract"
)

// maxConsecutiveErrors aborts a run that is failing for a systemic reason —
// a vanished mount, a broken extractor — rather than grinding through
// thousands of rows to report the same failure N times.
//
// 🔴 Only StatusError increments it. A platform of multi-file packs skips
// hundreds of rows in a row perfectly legitimately; treating that as a fault
// would abort exactly the runs that have the most to say.
const maxConsecutiveErrors = 25

// extractHeadroom is how many times the archive's own size must be free
// before extracting it. ROMs compress well; 4x covers the ratios seen in the
// library without being so generous it refuses ordinary work.
const extractHeadroom = 4

// workDirName is this runner's scratch dir, and it must not be the renamer's:
// that one is os.RemoveAll'd at the start of every rename preview, which
// would delete a concurrent backfill's in-flight extraction out from under
// it. Dot-prefixed so library scans skip it, like .archive and the renamer's.
const workDirName = ".gamarr-hash-tmp"

// Row is one visited library entry.
type Row struct {
	LibraryID    int64  `json:"library_id"`
	PlatformSlug string `json:"platform_slug"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	MD5          string `json:"md5,omitempty"`
	UnhMD5       string `json:"unh_md5,omitempty"`
	Header       string `json:"header,omitempty"`
}

// Opts are one run's switches.
type Opts struct {
	// DryRun hashes everything and writes nothing.
	DryRun bool
	// Force re-visits rows that already carry a hash or a permanent skip
	// marker — for re-hashing after a derivation change, such as a new
	// container-header rule.
	Force bool
}

// Runner sweeps one platform, or all of them, once at a time.
type Runner struct {
	cfg   *config.Config
	store *db.JobStore

	running atomic.Bool

	mu          sync.Mutex
	cancel      context.CancelFunc
	phase       string // idle | hashing
	scope       string
	opts        Opts
	total       int
	done        int
	hashed      int
	stripped    int
	skipped     int
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

// Trigger starts an async sweep over scope ("all" or a platform slug).
// Returns false when a run is already in flight.
func (r *Runner) Trigger(scope string, opts Opts) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.phase = "hashing"
	r.scope = scope
	r.opts = opts
	r.total, r.done, r.hashed, r.stripped, r.skipped, r.errCount = 0, 0, 0, 0, 0, 0
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
		hashed, stripped, skipped, errs, dry := r.hashed, r.stripped, r.skipped, r.errCount, r.opts.DryRun
		label := r.scope
		r.mu.Unlock()
		if dry {
			return // a dry run changed nothing; the activity log records changes
		}
		if label == "" || label == "all" {
			label = "all platforms"
		}
		// One summary row per run. Per-file entries would be noise at
		// campaign scale, and the runner's own state is in-memory — the
		// activity log is the durable record that the run happened.
		r.store.LogActivity("library_hashed", "Library hash backfill ("+label+")",
			fmt.Sprintf("%d hashed (%d header-stripped), %d skipped, %d errors",
				hashed, stripped, skipped, errs), "", nil)
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

// Status reports the current or last run, plus how much work is outstanding.
// Nil-receiver safe.
func (r *Runner) Status() map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"phase": "idle", "running": false, "configured": false}
	}
	pending := r.store.CountLibraryItemsNeedingHash()
	outstanding := 0
	for _, n := range pending {
		outstanding += n
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
		"hashed":     r.hashed,
		"stripped":   r.stripped,
		"skipped":    r.skipped,
		"errors":     r.errCount,
		// Bytes, not just rows: a platform whose work is concentrated in a
		// handful of multi-gigabyte files leaves `done` sitting on the same
		// integer for minutes, and this is the only sign the run is alive.
		"bytes_hashed": r.bytesHashed,
		"last_error":   r.lastErr,
		"counts":       copyCounts(r.counts),
		"pending":      pending,
		"pending_all":  outstanding,
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

func (r *Runner) run(ctx context.Context, scope string, opts Opts) {
	slug := scope
	if scope == "all" {
		slug = ""
	}
	items := r.store.ListLibraryItemsNeedingHash(slug, opts.Force)
	r.mu.Lock()
	r.total = len(items)
	r.mu.Unlock()

	workRoot := filepath.Join(r.cfg.GamesRomsPath, workDirName)
	os.RemoveAll(workRoot) // reap leftovers from a crashed run
	defer os.RemoveAll(workRoot)

	consecutive := 0
	for i := range items {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.lastErr = "stopped"
			r.mu.Unlock()
			return
		default:
		}

		row := r.visit(ctx, items[i], workRoot, opts)
		r.mu.Lock()
		r.done++
		r.rows = append(r.rows, row)
		switch row.Status {
		case StatusHashed:
			r.hashed++
			if row.Header != "" {
				r.stripped++
			}
			r.bytesHashed += row.Size
		case StatusSkip:
			r.skipped++
			r.counts[row.Reason]++
		case StatusError:
			r.errCount++
			r.lastErr = row.Reason
		}
		r.mu.Unlock()

		if row.Status == StatusError {
			consecutive++
			if consecutive >= maxConsecutiveErrors {
				r.mu.Lock()
				r.lastErr = fmt.Sprintf("aborted after %d consecutive errors: %s", consecutive, row.Reason)
				r.mu.Unlock()
				slog.Error("hash backfill aborted", "consecutive_errors", consecutive, "last", row.Reason)
				return
			}
			continue
		}
		consecutive = 0
	}
}

// visit measures one library entry. It never modifies the entry: an archive's
// inner ROM is extracted into a scratch dir that is removed before returning,
// and a raw file is hashed in place — no staging, because nothing here
// renames anything and a hardlink would only be work.
func (r *Runner) visit(ctx context.Context, item db.LibraryItem, workRoot string, opts Opts) Row {
	row := Row{
		LibraryID:    item.ID,
		PlatformSlug: item.PlatformSlug,
		Path:         item.FilePath,
		Name:         filepath.Base(item.FilePath),
	}

	fi, err := os.Stat(item.FilePath)
	switch {
	case os.IsNotExist(err):
		return r.skip(row, SkipMissing, false, opts)
	case err != nil:
		row.Status, row.Reason = StatusError, "unreadable: "+err.Error()
		return row
	case fi.IsDir():
		return r.skip(row, SkipDirectory, true, opts)
	}
	if strings.EqualFold(filepath.Ext(item.FilePath), ".rar") {
		return r.skip(row, SkipRar, true, opts)
	}

	target := item.FilePath
	if romfile.IsArchive(item.FilePath) {
		if err := os.MkdirAll(workRoot, 0o755); err != nil {
			row.Status, row.Reason = StatusError, "workspace: "+err.Error()
			return row
		}
		itemDir, err := os.MkdirTemp(workRoot, "item-")
		if err != nil {
			row.Status, row.Reason = StatusError, "stage dir: "+err.Error()
			return row
		}
		defer os.RemoveAll(itemDir)

		// An extraction needs room for the inner ROM, which can be several
		// times the archive. Refusing loudly beats filling the volume the
		// library lives on and failing every remaining row.
		if free := freeBytes(workRoot); free > 0 && uint64(fi.Size())*extractHeadroom > free {
			return r.skip(row, SkipNoSpace, false, opts)
		}

		extracted, err := romfile.ExtractSingle(ctx, item.FilePath, itemDir)
		if err != nil {
			var multi *romfile.MultiFileError
			if errors.As(err, &multi) {
				return r.skip(row, SkipMultiFile, true, opts)
			}
			row.Status, row.Reason = StatusError, "extract failed: "+err.Error()
			return row
		}
		target = extracted
	}

	res, err := romfile.HashPayload(target)
	if err != nil {
		row.Status, row.Reason = StatusError, "hash failed: "+err.Error()
		return row
	}

	row.Status = StatusHashed
	row.Size = res.Size
	row.MD5 = res.MD5
	if res.Stripped {
		row.UnhMD5 = res.Payload.MD5
		row.Header = res.HeaderKind
	}
	if opts.DryRun {
		return row
	}

	h := db.LibraryHashes{CRC: res.CRC, MD5: res.MD5, SHA1: res.SHA1}
	if res.Stripped {
		h.Unh = &db.UnheaderedHashes{
			CRC: res.Payload.CRC, MD5: res.Payload.MD5, SHA1: res.Payload.SHA1,
			Header: res.HeaderKind,
		}
	}
	if err := r.store.SaveLibraryHashes(item.ID, h); err != nil {
		row.Status, row.Reason = StatusError, "save failed: "+err.Error()
		row.MD5, row.UnhMD5, row.Header = "", "", ""
		return row
	}
	return row
}

// skip records a skip, marking the row when the reason is permanent so it
// stops being enumerated. A dry run marks nothing: it is supposed to leave no
// trace, and a marker is a write.
func (r *Runner) skip(row Row, reason string, permanent bool, opts Opts) Row {
	row.Status, row.Reason = StatusSkip, reason
	if !permanent || opts.DryRun {
		return row
	}
	if err := r.store.MarkLibraryHashSkipped(row.LibraryID, markerFor(reason)); err != nil {
		slog.Warn("mark hash skip", "library_id", row.LibraryID, "error", err)
	}
	return row
}

func markerFor(reason string) string {
	switch reason {
	case SkipDirectory:
		return db.HashSkipDirectory
	case SkipMultiFile:
		return db.HashSkipMultiFile
	case SkipRar:
		return db.HashSkipRar
	}
	return ""
}

// freeBytes returns the filesystem free space for the nearest existing parent
// of path, or 0 when nothing resolves — in which case the caller proceeds, on
// the grounds that an unmeasurable volume is not evidence of a full one.
func freeBytes(path string) uint64 {
	for p := path; p != "" && p != "/"; p = filepath.Dir(p) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err == nil {
			return st.Bavail * uint64(st.Bsize)
		}
	}
	return 0
}

// copyCounts returns a snapshot of the verdict tally.
//
// 🔴 Status() must not hand out the live map. The caller holds it after the
// mutex is released — writeJSON encodes it outside the lock — while the run
// goroutine is still incrementing, which is a data race the -race build
// catches and an ordinary run corrupts silently.
func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
