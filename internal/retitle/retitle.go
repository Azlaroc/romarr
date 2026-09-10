// Package retitle is the one-shot admin task that moves library TITLES to
// their catalog-canonical form: the full DAT game name when the stored-hash
// ladder resolves one, the on-disk file name's stem when it does not. It is
// a preview/apply task, never a boot migration — nothing changes until an
// operator has seen the diff and said go, and every applied row carries a
// `$.gamarr.retitle.from` breadcrumb so the whole sweep can be reverted.
//
// Titles are DISPLAY identity. Files never move here, hashes never change,
// and the decision never keys on character class — a katakana title is
// retitled because the catalog names the dump, not because it is katakana
// (Düx and Ishidó are legit titles that merely happen to be non-ASCII).
package retitle

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gamarr/internal/datname"
	"gamarr/internal/db"
)

// Row statuses.
const (
	StatusRetitle = "retitle" // included in apply
	StatusNoop    = "noop"    // already canonical
	StatusSkip    = "skip"    // excluded; Reason says why
)

// Sources a new title can come from.
const (
	SourceDat  = "dat"  // hash ladder resolved a catalog game name
	SourceStem = "stem" // file name stem, libscan.create's own rule
)

// PreviewRow is one library row's decided move.
type PreviewRow struct {
	LibraryID int64  `json:"library_id"`
	Platform  string `json:"platform_slug"`
	Old       string `json:"old"`
	New       string `json:"new,omitempty"`
	Source    string `json:"source,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// Runner holds one preview at a time and applies exactly the preview it
// holds. Same discipline as prune: the apply pass never re-decides.
type Runner struct {
	store *db.JobStore

	mu         sync.Mutex
	state      string // idle | previewing | applying | done | error
	rows       []PreviewRow
	counts     map[string]int
	followSkip map[string]bool // lowered old titles whose wishlist follow is suppressed
	startedAt  time.Time
	finishedAt time.Time
	lastErr    string
	cancel     context.CancelFunc
}

func New(store *db.JobStore) *Runner {
	return &Runner{store: store, state: "idle"}
}

// Status is the polling surface.
func (r *Runner) Status() map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"state": "unavailable"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]interface{}{
		"state":  r.state,
		"rows":   len(r.rows),
		"counts": r.counts,
	}
	if !r.startedAt.IsZero() {
		out["started_at"] = r.startedAt.UTC().Format(time.RFC3339)
	}
	if !r.finishedAt.IsZero() {
		out["finished_at"] = r.finishedAt.UTC().Format(time.RFC3339)
	}
	if r.lastErr != "" {
		out["error"] = r.lastErr
	}
	return out
}

// PreviewPage returns one page of the held preview and the total row count.
func (r *Runner) PreviewPage(page, pageSize int) ([]PreviewRow, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 200
	}
	start := (page - 1) * pageSize
	if start >= len(r.rows) {
		return nil, len(r.rows)
	}
	end := start + pageSize
	if end > len(r.rows) {
		end = len(r.rows)
	}
	out := make([]PreviewRow, end-start)
	copy(out, r.rows[start:end])
	return out, len(r.rows)
}

// TriggerPreview starts an async preview pass over the whole non-PC library.
// False when a pass is already running.
func (r *Runner) TriggerPreview() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == "previewing" || r.state == "applying" {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.state, r.cancel = "previewing", cancel
	r.rows, r.counts, r.followSkip = nil, nil, nil
	r.startedAt, r.finishedAt, r.lastErr = time.Now(), time.Time{}, ""
	go r.runPreview(ctx)
	return true
}

// TriggerApply applies the held preview's retitle rows, minus excludeIDs.
// False when no finished preview is held or a pass is running.
func (r *Runner) TriggerApply(excludeIDs []int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != "done" || len(r.rows) == 0 {
		return false
	}
	excl := make(map[int64]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excl[id] = struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.state, r.cancel = "applying", cancel
	go r.runApply(ctx, excl)
	return true
}

// Stop cancels an in-flight pass.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

// Revert runs the inverse sweep over every breadcrumbed row. It needs no
// held preview — the breadcrumbs ARE the worklist.
func (r *Runner) Revert() (int, error) {
	r.mu.Lock()
	if r.state == "previewing" || r.state == "applying" {
		r.mu.Unlock()
		return 0, fmt.Errorf("a retitle pass is running")
	}
	r.mu.Unlock()
	n, err := r.store.RevertRetitles()
	if err == nil && n > 0 {
		r.store.LogActivity("retitle_reverted", "Library retitle",
			fmt.Sprintf("Reverted %d titles to their recorded originals", n), "", nil)
	}
	return n, err
}

// decide is the per-row policy: catalog name when the ladder resolves one,
// file-name stem otherwise. Pure over its inputs.
func decide(item db.LibraryItem, matches []db.DatRomMatch, hashed bool) PreviewRow {
	row := PreviewRow{LibraryID: item.ID, Platform: item.PlatformSlug, Old: item.Title}

	newTitle, source, reason := "", "", ""
	if hashed && len(matches) > 0 {
		cands := make([]datname.Candidate, 0, len(matches))
		for _, m := range matches {
			cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
		}
		res := datname.Resolve(cands)
		if res.Outcome == datname.Resolved && res.GameName != "" {
			newTitle, source = res.GameName, SourceDat
		} else {
			reason = "catalog ambiguous"
		}
	} else if !hashed {
		reason = "unhashed"
	} else {
		reason = "no catalog match"
	}

	if newTitle == "" {
		// libscan.create's rule, with NormalizeTitleKey's extension heuristic
		// (≤4 chars, no spaces or brackets) so a directory-shaped path or a
		// dotted title ("Game (v1.0)") keeps its base name whole.
		base := filepath.Base(item.FilePath)
		if dot := strings.LastIndexByte(base, '.'); dot > 0 && dot < len(base)-1 {
			if ext := base[dot+1:]; len(ext) <= 4 && !strings.ContainsAny(ext, " ()[]") {
				base = base[:dot]
			}
		}
		if base != "" && base != "." && base != "/" {
			newTitle, source = base, SourceStem
		} else {
			row.Status, row.Reason = StatusSkip, "no usable file name ("+reason+")"
			return row
		}
	}

	if newTitle == item.Title {
		row.Status, row.Source = StatusNoop, source
		return row
	}
	row.New, row.Source, row.Status, row.Reason = newTitle, source, StatusRetitle, reason
	return row
}

func (r *Runner) runPreview(ctx context.Context) {
	items, err := r.store.LibraryItemsForRetitle()
	if err != nil {
		r.finish("error", err.Error())
		return
	}
	rows := make([]PreviewRow, 0, len(items))
	counts := map[string]int{}
	for i := range items {
		if ctx.Err() != nil {
			r.finish("error", "cancelled")
			return
		}
		matches, hashed := r.store.LookupDatMatchesForItem(&items[i])
		row := decide(items[i], matches, hashed)
		counts[row.Status]++
		if row.Status == StatusRetitle {
			counts["source:"+row.Source]++
		}
		rows = append(rows, row)
	}

	// A shared old title whose rows diverge on the new one cannot follow the
	// wishlist safely: the batch's last writer would win silently. The rows
	// still retitle; only the follow is suppressed, and each is flagged.
	followSkip := map[string]bool{}
	newByOld := map[string]map[string]bool{}
	for _, row := range rows {
		if row.Status != StatusRetitle {
			continue
		}
		key := strings.ToLower(row.Old) + "|" + row.Platform
		if newByOld[key] == nil {
			newByOld[key] = map[string]bool{}
		}
		newByOld[key][row.New] = true
	}
	for i, row := range rows {
		if row.Status != StatusRetitle {
			continue
		}
		if len(newByOld[strings.ToLower(row.Old)+"|"+row.Platform]) > 1 {
			followSkip[strings.ToLower(row.Old)] = true
			rows[i].Reason = strings.TrimSpace(rows[i].Reason + " (wishlist follow suppressed: old title shared with a diverging row)")
			counts["follow_suppressed"]++
		}
	}

	r.mu.Lock()
	r.rows, r.counts, r.followSkip = rows, counts, followSkip
	r.mu.Unlock()
	r.finish("done", "")
	slog.Info("retitle: preview complete", "rows", len(rows), "counts", counts)
}

func (r *Runner) runApply(ctx context.Context, excl map[int64]struct{}) {
	r.mu.Lock()
	rows := r.rows
	followSkip := r.followSkip
	r.mu.Unlock()

	var changes []db.RetitleChange
	for _, row := range rows {
		if row.Status != StatusRetitle {
			continue
		}
		if _, skip := excl[row.LibraryID]; skip {
			continue
		}
		changes = append(changes, db.RetitleChange{
			LibraryID: row.LibraryID, PlatformSlug: row.Platform,
			Old: row.Old, New: row.New,
		})
	}
	if ctx.Err() != nil {
		r.finish("error", "cancelled")
		return
	}
	// Batched by count, each batch one transaction: an apply over ~17k rows
	// must not hold one giant write transaction against a live scheduler.
	const batch = 500
	applied := 0
	for start := 0; start < len(changes); start += batch {
		if ctx.Err() != nil {
			break
		}
		end := start + batch
		if end > len(changes) {
			end = len(changes)
		}
		if err := r.store.ApplyRetitleBatch(changes[start:end], followSkip); err != nil {
			r.finish("error", fmt.Sprintf("applied %d of %d, then: %v", applied, len(changes), err))
			return
		}
		applied += end - start
	}
	// ONE summary activity row for the whole sweep — per-row entries would
	// drown the feed.
	r.store.LogActivity("retitle_applied", "Library retitle",
		fmt.Sprintf("Retitled %d of %d planned rows to catalog-canonical names (excluded: %d)",
			applied, len(changes)+len(excl), len(excl)), "", nil)
	r.finish("done", "")
	slog.Info("retitle: apply complete", "applied", applied, "excluded", len(excl))
}

func (r *Runner) finish(state, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state, r.lastErr, r.finishedAt = state, errMsg, time.Now()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}
