package renamer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/converto"
	"gamarr/internal/db"
	"gamarr/internal/platform"
)

// Verdicts for a collision row's hash comparison.
const (
	VerdictByteIdentical = "byte-identical"
	VerdictDifferent     = "different-bytes"
	VerdictUnknown       = "unknown"
)

// maxConsecutiveErrors aborts a preview when the identify pipeline fails this
// many items in a row (Playmatch outage, broken binary) instead of grinding
// through the whole batch.
const maxConsecutiveErrors = 25

// workDirName is the scratch workspace at the roms root — dot-prefixed so
// library scans ignore it; reaped at every run start.
const workDirName = ".gamarr-normalize-tmp"

// compilationEntryRe flags DAT names that look like extractions from modern
// compilation/re-release products rather than original releases. Some
// No-Intro DATs carry byte-identical entries for both ("Super Pocket - The
// Atari Collection (World) (Extracted)", "(Atari Anthology)", "(Atari Lynx
// Collection 1)"), making the hash lookup ambiguous — the resolver can
// legitimately return the compilation entry. Only parenthesized tags match,
// and "Collection" only with a trailing number, so title-position words
// ("Konami GB Collection Vol. 1 (Europe)") are never flagged.
var compilationEntryRe = regexp.MustCompile(`\([^)]*\b(?:Anthology|Collection \d|Extracted)\b[^)]*\)`)

// Collision describes the library entry already holding a row's proposed
// canonical name.
type Collision struct {
	WithLibraryID int64  `json:"with_library_id,omitempty"`
	WithName      string `json:"with_name"`
	Verdict       string `json:"verdict"`
}

// PreviewRow is one library entry's classification in the held preview.
// Status: "rename" (planned), "renamed" (applied), "noop" (already
// canonical), "skip" (Reason says why; Collision set when the canonical name
// is taken), "review" (proposed name looks like a hash-ambiguous
// compilation-entry resolution — never applied automatically).
type PreviewRow struct {
	LibraryID    int64      `json:"library_id"`
	PlatformSlug string     `json:"platform_slug"`
	OldPath      string     `json:"old_path"`
	OldName      string     `json:"old_name"`
	NewName      string     `json:"new_name,omitempty"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	Collision    *Collision `json:"collision,omitempty"`

	md5 string // inner-rom hash for intra-run verdicts; not serialized
}

// Runner orchestrates the on-demand bulk rename: an async preview pass that
// classifies every in-scope library entry, then a separate apply pass over
// the held preview. One run at a time; Stop cancels the in-flight run and
// the Runner stays usable (unlike the sync/scheduler Stop, which is
// process-shutdown-only). State is in-memory by design — resume is an
// idempotent re-run: applied/canonical entries classify as noops.
type Runner struct {
	cfg          *config.Config
	store        *db.JobStore
	cv           *converto.Client
	importNotify func(fsSlug string)

	running atomic.Bool

	mu         sync.Mutex
	cancel     context.CancelFunc
	phase      string // "idle" | "preview" | "apply"
	scope      string
	total      int
	done       int
	renamed    int
	skipped    int
	collisions int
	reviews    int
	errCount   int
	lastErr    string
	startedAt  time.Time
	finishedAt time.Time
	rows       []PreviewRow
}

// New builds a Runner. importNotify may be nil (RomM Connect disabled).
func New(cfg *config.Config, store *db.JobStore, importNotify func(string)) *Runner {
	return &Runner{
		cfg:          cfg,
		store:        store,
		cv:           converto.New(cfg),
		importNotify: importNotify,
		phase:        "idle",
	}
}

// TriggerPreview starts an async preview over scope ("all" or a platform
// slug). Returns false when a run is already in flight.
func (r *Runner) TriggerPreview(scope string) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.phase = "preview"
	r.scope = scope
	r.total, r.done, r.renamed, r.skipped, r.collisions, r.reviews, r.errCount = 0, 0, 0, 0, 0, 0, 0
	r.lastErr = ""
	r.startedAt = time.Now()
	r.finishedAt = time.Time{}
	r.rows = nil
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		defer cancel()
		r.runPreview(ctx, scope)
		r.mu.Lock()
		r.finishedAt = time.Now()
		r.mu.Unlock()
	}()
	return true
}

// TriggerApply starts an async apply over the held preview's planned
// renames, minus excludeIDs. Returns false when a run is in flight or no
// preview is held.
func (r *Runner) TriggerApply(excludeIDs []int64) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	excl := make(map[int64]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excl[id] = struct{}{}
	}
	r.mu.Lock()
	planned := 0
	for i := range r.rows {
		if r.rows[i].Status == "rename" {
			if _, skip := excl[r.rows[i].LibraryID]; !skip {
				planned++
			}
		}
	}
	if planned == 0 {
		r.mu.Unlock()
		r.running.Store(false)
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.phase = "apply"
	r.total = planned
	r.done, r.renamed, r.errCount = 0, 0, 0
	r.lastErr = ""
	r.startedAt = time.Now()
	r.finishedAt = time.Time{}
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		defer cancel()
		r.runApply(ctx, excl)
		r.mu.Lock()
		r.finishedAt = time.Now()
		scope := r.scope
		renamed, skipped, collisions, errs := r.renamed, r.skipped, r.collisions, r.errCount
		r.mu.Unlock()
		// Persistent run history: runner state is in-memory and the UI shows
		// only the last run — the activity log is the durable record of what
		// an apply did. One summary entry per run; per-file entries would be
		// noise at campaign scale (hundreds of renames per platform).
		if scope == "" {
			scope = "all platforms"
		}
		r.store.LogActivity("library_renamed", "Library rename ("+scope+")",
			fmt.Sprintf("%d renamed, %d skipped, %d collisions, %d errors",
				renamed, skipped, collisions, errs), "", nil)
	}()
	return true
}

// Stop cancels the in-flight run, if any. The Runner remains usable.
func (r *Runner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
}

// Status reports the runner's current state for the UI poller.
func (r *Runner) Status() map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"enabled": false}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	planned := 0
	for i := range r.rows {
		if r.rows[i].Status == "rename" {
			planned++
		}
	}
	return map[string]interface{}{
		"enabled":     true,
		"planned":     planned,
		"running":     r.running.Load(),
		"phase":       r.phase,
		"scope":       r.scope,
		"total":       r.total,
		"done":        r.done,
		"renamed":     r.renamed,
		"skipped":     r.skipped,
		"collisions":  r.collisions,
		"reviews":     r.reviews,
		"errors":      r.errCount,
		"last_error":  r.lastErr,
		"started_at":  timeOrEmpty(r.startedAt),
		"finished_at": timeOrEmpty(r.finishedAt),
		"resume_note": "state is in-memory; to resume, re-run preview — applied and canonical entries become no-ops",
	}
}

// PreviewPage returns one page of held preview rows plus the total count.
func (r *Runner) PreviewPage(page, pageSize int) ([]PreviewRow, int) {
	if r == nil {
		return nil, 0
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total := len(r.rows)
	start := (page - 1) * pageSize
	if start >= total {
		return []PreviewRow{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]PreviewRow, end-start)
	copy(out, r.rows[start:end])
	return out, total
}

func timeOrEmpty(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

func (r *Runner) runPreview(ctx context.Context, scope string) {
	slug := scope
	if scope == "all" {
		slug = ""
	}
	items := r.store.ListLibraryItemsForRename(slug)
	r.mu.Lock()
	r.total = len(items)
	r.mu.Unlock()

	workRoot := filepath.Join(r.cfg.GamesRomsPath, workDirName)
	os.RemoveAll(workRoot) // reap leftovers from a crashed run
	defer os.RemoveAll(workRoot)
	ident := NewIdentifier(r.cv, workRoot)

	// Proposed targets from planned renames this run — a second entry
	// proposing an already-claimed target is the classic dupe pair where
	// neither copy is canonical yet. Values are copies: rows live in an
	// appending slice, so pointers into it would go stale.
	type targetClaim struct {
		id      int64
		oldName string
		md5     string
	}
	targets := map[string]targetClaim{}
	consecutive := 0

	for i := range items {
		select {
		case <-ctx.Done():
			r.noteErr("stopped")
			return
		default:
		}
		it := &items[i]
		row := PreviewRow{
			LibraryID:    it.ID,
			PlatformSlug: it.PlatformSlug,
			OldPath:      it.FilePath,
			OldName:      filepath.Base(it.FilePath),
		}

		identity, err := ident.Identify(ctx, it.FilePath)
		switch {
		case ctx.Err() != nil:
			r.noteErr("stopped")
			return
		case err != nil:
			row.Status = "skip"
			row.Reason = "identify error: " + err.Error()
			row.md5 = identity.MD5
			consecutive++
			r.appendRow(row, func() { r.skipped++; r.errCount++; r.lastErr = err.Error() })
			if consecutive >= maxConsecutiveErrors {
				r.noteErr(fmt.Sprintf("aborted after %d consecutive identify errors: %v", consecutive, err))
				return
			}
			continue
		}
		consecutive = 0
		row.md5 = identity.MD5

		switch {
		case identity.SkipReason != "":
			row.Status = "skip"
			row.Reason = identity.SkipReason
			r.appendRow(row, func() { r.skipped++ })
		case identity.ProposedName == row.OldName:
			row.Status = "noop"
			r.appendRow(row, func() {})
		default:
			row.NewName = identity.ProposedName
			target := filepath.Join(filepath.Dir(it.FilePath), identity.ProposedName)
			if prev, taken := targets[target]; taken {
				row.Status = "skip"
				row.Reason = "canonical name already proposed for another entry"
				row.Collision = &Collision{
					WithLibraryID: prev.id,
					WithName:      prev.oldName,
					Verdict:       compareMD5(row.md5, prev.md5),
				}
				r.appendRow(row, func() { r.skipped++; r.collisions++ })
			} else if _, statErr := os.Stat(target); statErr == nil {
				row.Status = "skip"
				row.Reason = "canonical name already exists in library"
				row.Collision = r.collisionWith(target, row.md5)
				r.appendRow(row, func() { r.skipped++; r.collisions++ })
			} else if compilationEntryRe.MatchString(identity.ProposedName) && !compilationEntryRe.MatchString(row.OldName) {
				// The intra-run collision guard catches the 2nd..Nth file that
				// hash-matches the same compilation entry; this flags the
				// first claimant, the residual single-file risk.
				row.Status = "review"
				row.Reason = "proposed name looks like a compilation/re-release DAT entry (hash-ambiguous) — not applied automatically"
				r.appendRow(row, func() { r.reviews++ })
			} else {
				row.Status = "rename"
				targets[target] = targetClaim{id: row.LibraryID, oldName: row.OldName, md5: row.md5}
				r.appendRow(row, func() {})
			}
		}
	}
}

func (r *Runner) runApply(ctx context.Context, excl map[int64]struct{}) {
	touched := map[string]struct{}{}

	r.mu.Lock()
	n := len(r.rows)
	r.mu.Unlock()

	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			r.noteErr("stopped")
			return
		default:
		}

		r.mu.Lock()
		row := r.rows[i]
		r.mu.Unlock()
		if row.Status != "rename" {
			continue
		}
		if _, skip := excl[row.LibraryID]; skip {
			continue
		}

		target := filepath.Join(filepath.Dir(row.OldPath), row.NewName)
		update := func(mut func(*PreviewRow), count func()) {
			r.mu.Lock()
			mut(&r.rows[i])
			r.done++
			count()
			r.mu.Unlock()
		}

		// TOCTOU: the target may have appeared since preview (including via
		// an earlier rename this batch).
		if _, err := os.Stat(target); err == nil {
			coll := r.collisionWith(target, row.md5)
			update(func(p *PreviewRow) {
				p.Status = "skip"
				p.Reason = "canonical name appeared since preview"
				p.Collision = coll
			}, func() { r.skipped++; r.collisions++ })
			continue
		}

		if err := os.Rename(row.OldPath, target); err != nil {
			msg := err.Error()
			update(func(p *PreviewRow) {
				p.Status = "skip"
				p.Reason = "rename failed: " + msg
			}, func() { r.errCount++; r.lastErr = msg })
			continue
		}
		// Metadata sidecar rides along when present.
		if _, err := os.Stat(row.OldPath + ".gamarr.json"); err == nil {
			if err := os.Rename(row.OldPath+".gamarr.json", target+".gamarr.json"); err != nil {
				slog.Warn("renamer: sidecar rename failed", "error", err)
			}
		}
		// DB before any RomM rescan, so the sync's adopt-by-path check merges
		// instead of minting a duplicate row.
		if err := r.store.UpdateLibraryItemPath(row.LibraryID, target); err != nil {
			msg := err.Error()
			slog.Warn("renamer: library path update failed", "id", row.LibraryID, "error", err)
			update(func(p *PreviewRow) {
				p.Status = "renamed"
				p.Reason = "file renamed but library update failed: " + msg
			}, func() { r.renamed++; r.errCount++; r.lastErr = msg })
		} else {
			update(func(p *PreviewRow) { p.Status = "renamed" }, func() { r.renamed++ })
		}
		touched[row.PlatformSlug] = struct{}{}
	}

	if r.importNotify != nil {
		for slug := range touched {
			r.importNotify(platform.ToRommFSSlug(slug))
		}
	}
}

// appendRow records a classified row and bumps done plus row-kind counters.
func (r *Runner) appendRow(row PreviewRow, count func()) {
	r.mu.Lock()
	r.rows = append(r.rows, row)
	r.done++
	count()
	r.mu.Unlock()
}

func (r *Runner) noteErr(msg string) {
	r.mu.Lock()
	if r.lastErr == "" || msg == "stopped" {
		r.lastErr = msg
	}
	r.mu.Unlock()
}

// collisionWith builds the collision annotation for the entry holding
// target, with a hash verdict against the stored $.romm/$.gamarr md5s.
func (r *Runner) collisionWith(target, itemMD5 string) *Collision {
	partner := r.store.GetLibraryItemByFilePath(target)
	if partner == nil {
		return &Collision{WithName: filepath.Base(target), Verdict: VerdictUnknown}
	}
	return &Collision{
		WithLibraryID: partner.ID,
		WithName:      partner.Title,
		Verdict:       compareMD5(itemMD5, storedMD5s(partner.Metadata)...),
	}
}

// storedMD5s extracts $.romm.md5 and $.gamarr.md5 from a metadata blob.
func storedMD5s(metadata string) []string {
	var meta map[string]struct {
		MD5 string `json:"md5"`
	}
	if json.Unmarshal([]byte(metadata), &meta) != nil {
		return nil
	}
	var out []string
	for _, fam := range []string{"romm", "gamarr"} {
		if v := meta[fam].MD5; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// compareMD5 renders a verdict: byte-identical when the item hash equals any
// candidate, different-bytes when candidates exist but none match, unknown
// when either side has no hash.
func compareMD5(itemMD5 string, candidates ...string) string {
	itemMD5 = strings.ToLower(strings.TrimSpace(itemMD5))
	if itemMD5 == "" {
		return VerdictUnknown
	}
	seen := false
	for _, c := range candidates {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		seen = true
		if c == itemMD5 {
			return VerdictByteIdentical
		}
	}
	if !seen {
		return VerdictUnknown
	}
	return VerdictDifferent
}
