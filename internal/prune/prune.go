// Package prune is the declutter half of collection mode: the same 1G1R set
// read in the other direction.
//
// Collection mode asks "what does the set want that I do not have" and fills
// the deficit. This asks "what do I have that the set does not want" and offers
// to prune the surplus. One policy object, two directions.
//
// Three rules it will not bend:
//
//   - **Archive, never delete.** Every prune is a MOVE into an archive tree
//     plus a manifest line, so any of it can be walked back. Nothing here
//     removes bytes.
//   - **Never prune the only copy.** Surplus is only surplus when the dump the
//     set actually keeps is on disk. If the keeper is a gap, the copy you have
//     is the copy you have, whatever its region.
//   - **Never act on the catalog's silence.** A file no catalog has heard of is
//     reported and counted, never archived: "not in the DAT" is not evidence of
//     redundancy, and on a bulk-imported platform it is most of the library.
package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/collection"
	"gamarr/internal/collectionsvc"
	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/platform"
	"gamarr/internal/selection"
)

// setTitle is what the set knows about one game, indexed by every parsed form
// of its title. Owned matters as much as the title does: an off-catalog file
// claiming a game whose catalogued dump is ON DISK is a spare copy, while one
// claiming a game the set is still MISSING may be the only copy there is.
type setTitle struct {
	keeper string
	title  string
	owned  bool
}

type setTitles map[string]setTitle

func indexSetTitles(entries []collection.Entry) setTitles {
	out := setTitles{}
	for _, e := range entries {
		keeper, ok := e.Keeper()
		if !ok {
			continue
		}
		hit := setTitle{keeper: keeper.Name, title: e.Title, owned: e.Status == collection.StatusOwned}
		for _, key := range selection.OwnershipKeys(e.Title) {
			// An owned answer beats a missing one for the same key: two groups
			// can normalise to the same title, and the safe reading is the one
			// that says a copy exists.
			if prev, dup := out[key]; dup && (prev.owned || !hit.owned) {
				continue
			}
			out[key] = hit
		}
	}
	return out
}

// lookup matches a library file name against the set's titles, using the same
// parsed-title vocabulary ownership already speaks.
func (c setTitles) lookup(name string) (setTitle, bool) {
	for _, key := range selection.OwnershipKeys(name) {
		if hit, found := c[key]; found {
			return hit, true
		}
	}
	return setTitle{}, false
}

// Verdicts. Stable strings: displayed, logged, and asserted on.
const (
	// VerdictArchive: a catalogued dump the set does not keep, identified by
	// hash or by canonical name, in a group whose keeper is on disk.
	VerdictArchive = "archive"
	// VerdictReview: surplus that is not safe to act on unattended — matched
	// only by a parsed title, or in a group whose keeper is still missing.
	VerdictReview = "review"
	// VerdictExcludedGroup: an owned dump in a group policy leaves out of the
	// set entirely (prototypes, unlicensed, hacks). Archiving it removes the
	// game rather than a duplicate of it, so it needs its own opt-in.
	VerdictExcludedGroup = "excluded-group"
	// VerdictUncataloguedHack: the catalog has never heard of this file AND
	// its name declares what it is — a hack, an alternate or bad dump, a
	// trained or pirate release, a fan translation. No-Intro is the authority
	// for cart platforms, so a file carrying a GoodTools-era junk marker and
	// absent from the catalog is non-canonical by the library's own standard.
	// This is what makes a shop list six tiles all called "Asteroids".
	VerdictUncataloguedHack = "uncatalogued-hack"
	// VerdictUncatalogued: the catalog has never heard of this file. Counted
	// and listed, never actioned.
	VerdictUncatalogued = "uncatalogued"
	// VerdictUncataloguedDupe: the catalog has never heard of this file
	// either, but its title is a game the set covers AND we hold the
	// catalogued dump of. These are the hacks, alternate dumps and
	// re-releases that pile up under a game you already own properly — the
	// clutter that makes a shop list eighteen Asteroids. Opt-in, because the
	// evidence is a title match rather than a hash.
	VerdictUncataloguedDupe = "uncatalogued-duplicate"
)

// Row statuses.
const (
	StatusPlanned  = "planned"
	StatusArchived = "archived"
	StatusSkipped  = "skip"
	StatusReported = "reported" // review / uncatalogued: nothing to apply
)

// PreviewRow is one library file's classification.
type PreviewRow struct {
	LibraryID    int64  `json:"library_id"`
	PlatformSlug string `json:"platform_slug"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	// Title is the set group this file belongs to; Keeper is the dump the set
	// keeps instead. Both empty for an uncatalogued file — that is the point.
	Title  string `json:"title,omitempty"`
	Keeper string `json:"keeper,omitempty"`
	// MatchedBy is the evidence tier that tied this file to its catalogue
	// entry: hash, name, or title. It is why some rows are review-only.
	MatchedBy string `json:"matched_by,omitempty"`
	Verdict   string `json:"verdict"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	// ArchivedTo is filled in by the apply pass.
	ArchivedTo string `json:"archived_to,omitempty"`
}

// Runner holds one preview and applies it. Same shape as the bulk renamer: an
// async preview that classifies, then a separate apply over the held rows minus
// an exclusion list, so a human always sees the diff before anything moves.
type Runner struct {
	cfg          *config.Config
	store        *db.JobStore
	coll         *collectionsvc.Service
	importNotify func(fsSlug string)

	running atomic.Bool

	mu           sync.Mutex
	cancel       context.CancelFunc
	phase        string // idle | preview | apply
	scope        string
	includeOut   bool
	includeDupes bool
	includeHacks bool
	total        int
	done         int
	archived     int
	skipped      int
	errCount     int
	lastErr      string
	startedAt    time.Time
	finishedAt   time.Time
	rows         []PreviewRow
	counts       map[string]int
	uncatalogued int
}

// New builds a Runner. importNotify may be nil.
func New(cfg *config.Config, store *db.JobStore, coll *collectionsvc.Service, importNotify func(string)) *Runner {
	return &Runner{cfg: cfg, store: store, coll: coll, importNotify: importNotify, phase: "idle"}
}

// ArchiveRoot is where pruned files go.
//
// Inside the ROM root and dot-prefixed on purpose: the roms tree is what the
// cloud-sync exclusion covers, so an archive beside it would start uploading
// the very files an operator just decided they did not need online, and a
// hidden directory is skipped by the library scans that already ignore the
// renamer's scratch dir.
func (r *Runner) ArchiveRoot() string {
	if v, ok := r.store.GetSetting("prune_archive_path"); ok && v != "" {
		return v
	}
	return filepath.Join(r.cfg.GamesRomsPath, ".archive")
}

// Status reports the current or last run. Nil-receiver safe.
func (r *Runner) Status() map[string]interface{} {
	if r == nil {
		return map[string]interface{}{"phase": "idle", "running": false, "configured": false}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]interface{}{
		"configured":    true,
		"running":       r.running.Load(),
		"phase":         r.phase,
		"scope":         r.scope,
		"include_out":   r.includeOut,
		"include_dupes": r.includeDupes,
		"include_hacks": r.includeHacks,
		"total":         r.total,
		"done":          r.done,
		"archived":      r.archived,
		"skipped":       r.skipped,
		"errors":        r.errCount,
		"last_error":    r.lastErr,
		"counts":        copyCounts(r.counts),
		"uncatalogued":  r.uncatalogued,
		"archive_root":  r.ArchiveRoot(),
		"started_at":    timeOrEmpty(r.startedAt),
		"finished_at":   timeOrEmpty(r.finishedAt),
	}
}

func timeOrEmpty(t time.Time) interface{} {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// PreviewPage returns a page of the held preview plus the total.
func (r *Runner) PreviewPage(page, pageSize int) ([]PreviewRow, int) {
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

// Opts are a preview's opt-ins. Both default off: each adds a class of file
// whose evidence is weaker than "a catalogued dump the set does not keep", and
// neither should arrive because someone left a toggle on.
type Opts struct {
	// IncludeExcluded adds owned dumps from policy-excluded groups (hacks,
	// prototypes, unlicensed). Archiving one removes a game rather than a
	// duplicate of it.
	IncludeExcluded bool
	// IncludeUncataloguedDupes adds uncatalogued files whose title is a game
	// the set covers and whose catalogued dump we already hold.
	IncludeUncataloguedDupes bool
	// IncludeUncataloguedHacks adds uncatalogued files whose NAME declares
	// them a hack, alternate dump, bad dump, overdump, pirate release or fan
	// translation.
	IncludeUncataloguedHacks bool
}

// hackMarkerRe matches the GoodTools-era markers a file uses to declare what it
// is: [a1] alternate, [h2] hack, [t1] trained, [b] bad dump, [o1] overdump,
// [p1] pirate, [f1] fixed, [T-Eng]/[T+Ger] translation — plus the explicit
// "(Something Hack)" tag that modern homebrew sets use.
//
// Deliberately tight. [!] alone means VERIFIED GOOD and must never match, and a
// bracket carrying anything else ("[USA]", "[Rev 1]") is not a marker either.
var hackMarkerRe = regexp.MustCompile(`(?i)\[(?:[ahtbopf]\d*|T[-+][^\]]*)\]|\([^)]*\bhack\b[^)]*\)`)

// hackMarker returns the marker a name declares, or "".
func hackMarker(name string) string {
	return hackMarkerRe.FindString(name)
}

// TriggerPreview starts a classification pass over one platform (or "all").
func (r *Runner) TriggerPreview(scope string, opts Opts) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	includeOut := opts.IncludeExcluded
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel, r.phase, r.scope, r.includeOut = cancel, "preview", scope, includeOut
	r.includeDupes = opts.IncludeUncataloguedDupes
	r.includeHacks = opts.IncludeUncataloguedHacks
	r.total, r.done, r.archived, r.skipped, r.errCount = 0, 0, 0, 0, 0
	r.lastErr, r.rows, r.counts, r.uncatalogued = "", nil, map[string]int{}, 0
	r.startedAt, r.finishedAt = time.Now(), time.Time{}
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		defer cancel()
		r.runPreview(ctx, scope, opts)
		r.mu.Lock()
		r.finishedAt = time.Now()
		r.mu.Unlock()
	}()
	return true
}

// TriggerApply archives the held preview's planned rows, minus excludeIDs.
func (r *Runner) TriggerApply(excludeIDs []int64) bool {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return false
	}
	r.mu.Lock()
	planned := 0
	for _, row := range r.rows {
		if row.Status == StatusPlanned {
			planned++
		}
	}
	r.mu.Unlock()
	if planned == 0 {
		r.running.Store(false)
		return false
	}

	excl := make(map[int64]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excl[id] = struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel, r.phase, r.done = cancel, "apply", 0
	r.startedAt, r.finishedAt = time.Now(), time.Time{}
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		defer cancel()
		r.runApply(ctx, excl)
		r.mu.Lock()
		r.finishedAt = time.Now()
		r.mu.Unlock()
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

func (r *Runner) runPreview(ctx context.Context, scope string, opts Opts) {
	includeOut := opts.IncludeExcluded
	platforms := []string{scope}
	if scope == "all" {
		platforms = nil
		for _, p := range r.store.PlatformRows() {
			if !p.IsSystem {
				platforms = append(platforms, p.Slug)
			}
		}
	}

	cycle := r.coll.NewCycle()
	var rows []PreviewRow
	counts := map[string]int{}
	uncatalogued := 0

	for _, slug := range platforms {
		if ctx.Err() != nil {
			r.noteErr("stopped")
			return
		}
		res := cycle.Set(slug)
		if len(res.Entries) == 0 {
			continue
		}
		for _, e := range res.Entries {
			for _, c := range e.Members {
				if c.Owned == nil || c.Keeper {
					continue
				}
				row := classify(e, c, slug, includeOut)
				counts[row.Verdict]++
				rows = append(rows, row)
			}
		}
		// The catalog's silence. Never acted on by default — but a file whose
		// TITLE is a game the set covers, and whose catalogued dump we already
		// hold, is a different thing from a homebrew game that exists nowhere
		// else. Both are uncatalogued; only one is clutter.
		known := indexSetTitles(res.Entries)
		claimed := collection.ClaimedLibraryIDs(res.Entries)
		for _, item := range r.store.ListLibraryItemsForRename(slug) {
			if claimed[item.ID] {
				continue
			}
			name := filepath.Base(item.FilePath)
			row := PreviewRow{
				LibraryID: item.ID, PlatformSlug: slug, Path: item.FilePath,
				Name: name, Size: item.FileSize,
				Verdict: VerdictUncatalogued, Status: StatusReported,
				Reason: "no catalogued dump matches this file — the catalog's silence is not evidence of redundancy",
			}
			hit, inSet := known.lookup(name)
			switch {
			case inSet && hit.owned:
				row.Verdict = VerdictUncataloguedDupe
				row.Title, row.Keeper = hit.title, hit.keeper
				row.Reason = "not in the catalog, and the catalogued " + hit.keeper + " is already on disk"
				if opts.IncludeUncataloguedDupes {
					row.Status = StatusPlanned
				}
			case inSet:
				// 🔴 The set wants this game and does not have it. Whatever
				// markers the file carries, archiving it takes away the only
				// copy of a game — the same rule that protects a lone European
				// dump, one tier out.
				row.Verdict = VerdictReview
				row.Title, row.Keeper = hit.title, hit.keeper
				row.Reason = "not in the catalog, and the catalogued " + hit.keeper +
					" is NOT on disk — this may be the only copy of this game"
			case hackMarker(name) != "":
				// The file says what it is. A hack named for itself
				// ("Asteroids SS (Asteroids Hack)") never collides with the
				// title it hacks, so the duplicate rule cannot see it — this is
				// the rule that can.
				row.Verdict = VerdictUncataloguedHack
				row.Reason = "not in the catalog, and its name declares it non-canonical: " + hackMarker(name)
				if opts.IncludeUncataloguedHacks {
					row.Status = StatusPlanned
				}
			default:
				uncatalogued++
			}
			counts[row.Verdict]++
			rows = append(rows, row)
		}
	}

	r.mu.Lock()
	r.rows, r.counts, r.uncatalogued = rows, counts, uncatalogued
	r.total = counts[VerdictArchive]
	if includeOut {
		r.total += counts[VerdictExcludedGroup]
	}
	if opts.IncludeUncataloguedDupes {
		r.total += counts[VerdictUncataloguedDupe]
	}
	if opts.IncludeUncataloguedHacks {
		r.total += counts[VerdictUncataloguedHack]
	}
	r.phase = "preview"
	r.mu.Unlock()
	slog.Info("prune: preview complete", "scope", scope, "rows", len(rows),
		"planned", r.total, "uncatalogued", uncatalogued)
}

// classify turns one owned non-keeper dump into a verdict.
func classify(e collection.Entry, c collection.Candidate, slug string, includeOut bool) PreviewRow {
	row := PreviewRow{
		LibraryID: c.Owned.LibraryID, PlatformSlug: slug,
		Path: c.Owned.FilePath, Name: filepath.Base(c.Owned.FilePath),
		Size: c.TotalSize, Title: e.Title, MatchedBy: c.Owned.MatchedBy,
		Status: StatusReported,
	}
	if keeper, ok := e.Keeper(); ok {
		row.Keeper = keeper.Name

		switch {
		case keeper.Owned == nil:
			// 🔴 Never prune the only copy. The set prefers a dump we do not
			// have; the one we do have is not surplus, it is the collection.
			row.Verdict = VerdictReview
			row.Reason = "the dump the set keeps (" + keeper.Name + ") is not on disk — this is the only copy"
		case c.Owned.MatchedBy == collection.MatchTitle:
			// A parsed title cannot tell a USA dump from a European one. Good
			// enough to report, not good enough to move a file on.
			row.Verdict = VerdictReview
			row.Reason = "matched by title only — too weak to archive unattended"
		default:
			row.Verdict = VerdictArchive
			row.Status = StatusPlanned
			row.Reason = "surplus: the set keeps " + keeper.Name
			// A dump policy excludes — a hack, a prototype, an unlicensed
			// release — that sits in a group whose keeper IS owned is still
			// surplus, but saying only "the set keeps X" hides WHY this one was
			// never a candidate. Both facts, in the order that matters.
			if c.Excluded && c.Reason != "" {
				row.Reason = c.Reason + "; the set keeps " + keeper.Name
			}
		}
		return row
	}

	// No keeper at all: policy leaves the whole group out of the set.
	row.Verdict = VerdictExcludedGroup
	row.Reason = "policy excludes this group entirely (" + firstReason(c, e) + ")"
	if includeOut {
		row.Status = StatusPlanned
	}
	return row
}

func firstReason(c collection.Candidate, e collection.Entry) string {
	if c.Reason != "" {
		return c.Reason
	}
	if e.Reason != "" {
		return e.Reason
	}
	return "excluded"
}

func (r *Runner) runApply(ctx context.Context, excl map[int64]struct{}) {
	root := r.ArchiveRoot()
	touched := map[string]struct{}{}

	r.mu.Lock()
	n := len(r.rows)
	r.mu.Unlock()

	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			r.noteErr("stopped")
			return
		}
		r.mu.Lock()
		row := r.rows[i]
		r.mu.Unlock()
		if row.Status != StatusPlanned {
			continue
		}
		if _, skip := excl[row.LibraryID]; skip {
			continue
		}

		dest, err := r.archive(root, row)
		r.mu.Lock()
		r.done++
		if err != nil {
			r.rows[i].Status = StatusSkipped
			r.rows[i].Reason = err.Error()
			r.skipped++
			r.errCount++
			r.lastErr = err.Error()
		} else {
			r.rows[i].Status = StatusArchived
			r.rows[i].ArchivedTo = dest
			r.archived++
			touched[row.PlatformSlug] = struct{}{}
		}
		r.mu.Unlock()
	}

	r.mu.Lock()
	archived := r.archived
	r.phase = "preview" // the rows stay readable after an apply
	r.mu.Unlock()

	if archived > 0 {
		r.store.LogActivity("library_pruned", "Declutter",
			fmt.Sprintf("Archived %d surplus %s", archived, plural(archived, "file", "files")), "", nil)
	}
	// RomM last, once per platform: it re-reads a tree that has changed.
	if r.importNotify != nil {
		for slug := range touched {
			// 🔴 RomM speaks its own folder vocabulary: our "genesis" is its
			// "genesis-slash-megadrive". Notifying with our slug names a folder
			// it does not have, and the rescan quietly covers nothing — the
			// bulk renamer converts here for the same reason.
			r.importNotify(platform.ToRommFSSlug(slug))
		}
	}
	slog.Info("prune: apply complete", "archived", archived, "skipped", r.skipped)
}

// archive moves one file into the archive tree and records the move.
//
// A rename, never a copy: these are multi-gigabyte files on the same volume,
// and a cross-device move is a mistake worth refusing loudly rather than an
// hour of silent copying.
func (r *Runner) archive(root string, row PreviewRow) (string, error) {
	if row.Path == "" {
		return "", fmt.Errorf("library row has no file path")
	}
	if _, err := os.Stat(row.Path); err != nil {
		return "", fmt.Errorf("file is gone since the preview: %w", err)
	}
	dir := filepath.Join(root, row.PlatformSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("archive dir: %w", err)
	}
	dest := filepath.Join(dir, row.Name)
	if _, err := os.Stat(dest); err == nil {
		// Something of that name is already archived. Keep both rather than
		// clobbering the earlier decision.
		dest = filepath.Join(dir, uniqueName(row.Name))
	}
	if err := os.Rename(row.Path, dest); err != nil {
		return "", fmt.Errorf("archive move failed (cross-device archive paths are not supported): %w", err)
	}
	// The sidecar rides along, exactly as it does for a rename.
	if _, err := os.Stat(row.Path + ".gamarr.json"); err == nil {
		if err := os.Rename(row.Path+".gamarr.json", dest+".gamarr.json"); err != nil {
			slog.Warn("prune: sidecar move failed", "error", err)
		}
	}
	if err := r.appendManifest(root, row, dest); err != nil {
		slog.Warn("prune: manifest write failed", "error", err)
	}
	// The library row goes last and only after the bytes moved: a row pointing
	// at a path that no longer exists is worse than no row.
	if err := r.store.DeleteLibraryItem(row.LibraryID); err != nil {
		slog.Warn("prune: library row delete failed", "id", row.LibraryID, "error", err)
	}
	return dest, nil
}

// manifestEntry is one archived file's record. The manifest is what makes this
// reversible by hand: it names where a file came from, what replaced it, and
// why — an archive directory alone would leave a future operator guessing.
type manifestEntry struct {
	ArchivedAt string `json:"archived_at"`
	Platform   string `json:"platform"`
	From       string `json:"from"`
	To         string `json:"to"`
	Title      string `json:"title,omitempty"`
	Keeper     string `json:"keeper,omitempty"`
	MatchedBy  string `json:"matched_by,omitempty"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason,omitempty"`
	LibraryID  int64  `json:"library_id"`
}

func (r *Runner) appendManifest(root string, row PreviewRow, dest string) error {
	entry := manifestEntry{
		ArchivedAt: time.Now().UTC().Format(time.RFC3339),
		Platform:   row.PlatformSlug, From: row.Path, To: dest,
		Title: row.Title, Keeper: row.Keeper, MatchedBy: row.MatchedBy,
		Verdict: row.Verdict, Reason: row.Reason, LibraryID: row.LibraryID,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, "manifest.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func uniqueName(name string) string {
	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	return fmt.Sprintf("%s.%d%s", stem, time.Now().UTC().UnixNano(), ext)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (r *Runner) noteErr(msg string) {
	r.mu.Lock()
	r.errCount++
	r.lastErr = msg
	r.mu.Unlock()
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
