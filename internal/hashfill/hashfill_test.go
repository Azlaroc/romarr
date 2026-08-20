package hashfill

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
	"gamarr/internal/romfile"
)

type env struct {
	runner *Runner
	store  *db.JobStore
	roms   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	roms := filepath.Join(dir, "roms")
	if err := os.MkdirAll(roms, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := db.New(filepath.Join(dir, "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{DataDir: dir, GamesRomsPath: roms}
	cfg.AttachSettings(store)
	return &env{runner: New(cfg, store), store: store, roms: roms}
}

// rom writes a file into the roms tree and returns its path.
func (e *env) rom(t *testing.T, slug, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(e.roms, slug, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// row registers a library row pointing at path.
func (e *env) row(t *testing.T, slug, title, path, meta string) int64 {
	t.Helper()
	if meta == "" {
		meta = "{}"
	}
	id, err := e.store.AddLibraryItem(&db.LibraryItem{
		Title: title, Platform: slug, PlatformSlug: slug, FilePath: path,
		FileSize: 1, Source: "romm", SourceID: "romm:" + title, Metadata: meta,
	})
	if err != nil {
		t.Fatalf("AddLibraryItem: %v", err)
	}
	return id
}

func (e *env) runSync(t *testing.T, scope string, opts Opts) map[string]interface{} {
	t.Helper()
	if !e.runner.Trigger(scope, opts) {
		t.Fatal("Trigger returned false with no run in flight")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := e.runner.Status()
		if running, _ := st["running"].(bool); !running {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) meta(t *testing.T, id int64) map[string]interface{} {
	t.Helper()
	item, err := e.store.GetLibraryItem(id)
	if err != nil {
		t.Fatalf("GetLibraryItem: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(item.Metadata), &out); err != nil {
		t.Fatalf("metadata not JSON: %v (%s)", err, item.Metadata)
	}
	return out
}

func gamarr(t *testing.T, tree map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = tree["gamarr"]
	for _, k := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func inesROM(payload string) []byte {
	head := make([]byte, 16)
	copy(head, []byte{'N', 'E', 'S', 0x1a})
	head[4] = 2
	return append(head, []byte(payload)...)
}

func TestHashesRawRows(t *testing.T) {
	e := newEnv(t)
	body := []byte("cartridge bytes")
	id := e.row(t, "gb", "Game", e.rom(t, "gb", "Game (USA).gb", body), "{}")

	st := e.runSync(t, "gb", Opts{})
	if st["hashed"] != 1 || st["total"] != 1 {
		t.Fatalf("status = %+v, want one row hashed", st)
	}
	if got := gamarr(t, e.meta(t, id), "md5"); got != md5hex(body) {
		t.Errorf("$.gamarr.md5 = %v, want the file's md5", got)
	}
	if got := gamarr(t, e.meta(t, id), "unh"); got != nil {
		t.Errorf("$.gamarr.unh = %v on an unheadered file", got)
	}
	if n := st["bytes_hashed"]; n != int64(len(body)) {
		t.Errorf("bytes_hashed = %v, want %d", n, len(body))
	}
}

func TestHashesHeaderedNESBothWays(t *testing.T) {
	e := newEnv(t)
	payload := "the cartridge's own bytes"
	body := inesROM(payload)
	id := e.row(t, "nes", "NES Game", e.rom(t, "nes", "NES Game (USA).nes", body), "{}")

	st := e.runSync(t, "nes", Opts{})
	if st["hashed"] != 1 || st["stripped"] != 1 {
		t.Fatalf("status = %+v, want one hashed and one stripped", st)
	}
	tree := e.meta(t, id)
	if got := gamarr(t, tree, "md5"); got != md5hex(body) {
		t.Errorf("$.gamarr.md5 = %v, want the whole file's md5 (its identity)", got)
	}
	if got := gamarr(t, tree, "unh", "md5"); got != md5hex([]byte(payload)) {
		t.Errorf("$.gamarr.unh.md5 = %v, want the payload's md5 (what the catalog knows)", got)
	}
	if got := gamarr(t, tree, "unh", "header"); got != romfile.HeaderKindINES {
		t.Errorf("$.gamarr.unh.header = %v", got)
	}
}

func TestSkipsAndMarksPermanentCases(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.roms, "switch", "Some Game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dirID := e.row(t, "switch", "Dir Game", dir, "{}")
	rarID := e.row(t, "switch", "Rar Game", e.rom(t, "switch", "Old.rar", []byte("x")), "{}")
	goneID := e.row(t, "switch", "Gone Game", filepath.Join(e.roms, "switch", "vanished.nsp"), "{}")

	st := e.runSync(t, "switch", Opts{})
	if st["skipped"] != 3 || st["errors"] != 0 {
		t.Fatalf("status = %+v, want 3 skips and no errors", st)
	}
	if got := gamarr(t, e.meta(t, dirID), "hash_skipped"); got != db.HashSkipDirectory {
		t.Errorf("directory marker = %v", got)
	}
	if got := gamarr(t, e.meta(t, rarID), "hash_skipped"); got != db.HashSkipRar {
		t.Errorf("rar marker = %v", got)
	}
	// A missing file is today's weather, not a permanent fact: it must NOT
	// be marked, or a restored file never gets hashed.
	if got := gamarr(t, e.meta(t, goneID), "hash_skipped"); got != nil {
		t.Errorf("missing file was marked permanently skipped: %v", got)
	}

	// The permanent ones drop out of the work list; the transient one stays.
	if n := e.store.CountLibraryItemsNeedingHash()["switch"]; n != 1 {
		t.Errorf("pending switch = %d, want only the missing file still queued", n)
	}
}

func TestAlreadyHashedRowsAreNotVisited(t *testing.T) {
	e := newEnv(t)
	e.row(t, "gb", "RomM Hashed", e.rom(t, "gb", "a.gb", []byte("a")), `{"romm":{"md5":"aa"}}`)
	fresh := e.row(t, "gb", "Fresh", e.rom(t, "gb", "b.gb", []byte("b")), "{}")

	st := e.runSync(t, "gb", Opts{})
	if st["total"] != 1 || st["hashed"] != 1 {
		t.Fatalf("status = %+v, want only the hashless row visited", st)
	}
	if got := gamarr(t, e.meta(t, fresh), "md5"); got != md5hex([]byte("b")) {
		t.Errorf("fresh row md5 = %v", got)
	}

	// Re-running is a no-op: idempotency comes from enumeration, so the
	// second pass has nothing to do rather than rewriting what it finds.
	st = e.runSync(t, "gb", Opts{})
	if st["total"] != 0 || st["hashed"] != 0 {
		t.Errorf("re-run status = %+v, want nothing to do", st)
	}
}

func TestForceRevisitsEverything(t *testing.T) {
	e := newEnv(t)
	e.row(t, "gb", "Hashed", e.rom(t, "gb", "a.gb", []byte("a")), `{"gamarr":{"md5":"stale"}}`)
	st := e.runSync(t, "gb", Opts{Force: true})
	if st["total"] != 1 || st["hashed"] != 1 {
		t.Fatalf("status = %+v, want the hashed row re-visited", st)
	}
	if st["force"] != true {
		t.Errorf("status does not report force: %+v", st)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	body := inesROM("payload")
	id := e.row(t, "nes", "Game", e.rom(t, "nes", "g.nes", body), "{}")
	dirID := e.row(t, "nes", "Dir", filepath.Join(e.roms, "nes", "dir"), "{}")
	if err := os.MkdirAll(filepath.Join(e.roms, "nes", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	st := e.runSync(t, "nes", Opts{DryRun: true})
	if st["hashed"] != 1 || st["stripped"] != 1 {
		t.Fatalf("status = %+v, want the work reported", st)
	}
	if got := gamarr(t, e.meta(t, id), "md5"); got != nil {
		t.Errorf("dry run wrote $.gamarr.md5 = %v", got)
	}
	if got := gamarr(t, e.meta(t, dirID), "hash_skipped"); got != nil {
		t.Errorf("dry run wrote a skip marker = %v", got)
	}

	// The rows are still reported, which is the whole point: this is the
	// preview, and it must show the hash it WOULD have written.
	rows, total := e.runner.ResultsPage(1, 100)
	if total != 2 {
		t.Fatalf("results total = %d, want 2", total)
	}
	var sawHash bool
	for _, r := range rows {
		if r.Status == StatusHashed && r.UnhMD5 == md5hex([]byte("payload")) {
			sawHash = true
		}
	}
	if !sawHash {
		t.Errorf("dry-run rows do not carry the payload hash: %+v", rows)
	}
}

func TestPreservesSiblingMetadata(t *testing.T) {
	e := newEnv(t)
	id := e.row(t, "psx", "Set Game", e.rom(t, "psx", "s.bin", []byte("s")),
		`{"romm":{"rom_id":9},"gamarr":{"set":{"id":"s1","total":2},"catalog":"verified"}}`)
	e.runSync(t, "psx", Opts{})
	tree := e.meta(t, id)
	if got := gamarr(t, tree, "set", "id"); got != "s1" {
		t.Errorf("disc-set marker lost: %v", got)
	}
	if got := gamarr(t, tree, "catalog"); got != "verified" {
		t.Errorf("catalog verdict lost: %v", got)
	}
	if _, ok := tree["romm"].(map[string]interface{}); !ok {
		t.Errorf("RomM identity lost: %+v", tree)
	}
}

func TestSkipsDoNotTripTheErrorBudget(t *testing.T) {
	// A pack-heavy platform legitimately skips hundreds of rows in a row.
	// Treating that as a fault would abort the runs with the most to say.
	e := newEnv(t)
	for i := 0; i < maxConsecutiveErrors+5; i++ {
		name := filepath.Join(e.roms, "arcade", "pack", string(rune('a'+i%26)))
		if err := os.MkdirAll(name, 0o755); err != nil {
			t.Fatal(err)
		}
		e.row(t, "arcade", "Pack "+name, name, "{}")
	}
	st := e.runSync(t, "arcade", Opts{})
	if st["done"] != maxConsecutiveErrors+5 {
		t.Errorf("done = %v, want every row visited (%d)", st["done"], maxConsecutiveErrors+5)
	}
	if st["errors"] != 0 {
		t.Errorf("errors = %v, want 0", st["errors"])
	}
}

func TestAbortsAfterConsecutiveErrors(t *testing.T) {
	e := newEnv(t)
	// Unreadable-directory-as-parent: stat fails with EACCES rather than
	// ENOENT, which is the error class a vanished mount produces.
	blocked := filepath.Join(e.roms, "gb", "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxConsecutiveErrors+10; i++ {
		e.row(t, "gb", "Game "+string(rune('a'+i%26))+string(rune('a'+i/26)),
			filepath.Join(blocked, "g", "rom.gb"), "{}")
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Skip("cannot drop directory permissions here")
	}
	t.Cleanup(func() { os.Chmod(blocked, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny")
	}

	st := e.runSync(t, "gb", Opts{})
	if st["done"] != maxConsecutiveErrors {
		t.Errorf("done = %v, want the run aborted at %d", st["done"], maxConsecutiveErrors)
	}
	if msg, _ := st["last_error"].(string); !strings.Contains(msg, "consecutive errors") {
		t.Errorf("last_error = %q, want it to name the abort", msg)
	}
}

func TestSingleFlightRefusesAConcurrentRun(t *testing.T) {
	// Forced rather than raced: a run over a small fixture finishes in
	// microseconds, so triggering twice and hoping to land inside the window
	// tests the scheduler's timing, not the guard.
	e := newEnv(t)
	e.runner.running.Store(true)
	if e.runner.Trigger("all", Opts{}) {
		t.Error("Trigger started a run while one was marked in flight")
	}
	e.runner.running.Store(false)
	if !e.runner.Trigger("all", Opts{}) {
		t.Error("Trigger refused once the runner was free again")
	}
	e.runner.Stop()
	waitStopped(t, e.runner)
}

func TestStopEndsARunInFlight(t *testing.T) {
	e := newEnv(t)
	// Enough rows that a stop lands mid-sweep on most runs; the assertion
	// holds either way, because a stopped run and a completed one both end.
	for i := 0; i < 200; i++ {
		name := "g" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".gb"
		e.row(t, "gb", name, e.rom(t, "gb", name, []byte("bytes")), "{}")
	}
	if !e.runner.Trigger("gb", Opts{}) {
		t.Fatal("Trigger refused")
	}
	e.runner.Stop()
	waitStopped(t, e.runner)
	st := e.runner.Status()
	if done, total := st["done"].(int), st["total"].(int); done > total {
		t.Errorf("done %d > total %d", done, total)
	}
}

func waitStopped(t *testing.T, r *Runner) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if running, _ := r.Status()["running"].(bool); !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNilRunnerIsSafe(t *testing.T) {
	var r *Runner
	if st := r.Status(); st["configured"] != false {
		t.Errorf("nil Status = %+v, want configured:false", st)
	}
	if rows, total := r.ResultsPage(1, 10); rows != nil || total != 0 {
		t.Errorf("nil ResultsPage = %+v %d", rows, total)
	}
	if r.Trigger("all", Opts{}) {
		t.Error("nil Trigger returned true")
	}
	r.Stop() // must not panic
}

func TestLogsOneActivityRowPerRun(t *testing.T) {
	e := newEnv(t)
	e.row(t, "gb", "Game", e.rom(t, "gb", "g.gb", []byte("g")), "{}")
	e.runSync(t, "gb", Opts{})
	e.runSync(t, "gb", Opts{DryRun: true})

	entries, _ := e.store.GetActivity(1, 50)
	var hashed int
	for _, a := range entries {
		if a.EventType == "library_hashed" {
			hashed++
		}
	}
	if hashed != 1 {
		t.Errorf("library_hashed rows = %d, want exactly one (a dry run changes nothing)", hashed)
	}
}

func TestHashesInnerROMOfAnArchive(t *testing.T) {
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not on PATH")
	}
	e := newEnv(t)
	inner := inesROM("archived payload")
	dir := t.TempDir()
	innerPath := filepath.Join(dir, "NES Game (USA).nes")
	if err := os.WriteFile(innerPath, inner, 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(e.roms, "nes", "NES Game (U)_nes.7z")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("7z", "a", archive, innerPath).CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v: %s", err, out)
	}
	id := e.row(t, "nes", "Archived", archive, "{}")

	st := e.runSync(t, "nes", Opts{})
	if st["hashed"] != 1 {
		t.Fatalf("status = %+v", st)
	}
	tree := e.meta(t, id)
	if got := gamarr(t, tree, "md5"); got != md5hex(inner) {
		t.Errorf("$.gamarr.md5 = %v, want the INNER rom's md5, not the archive's", got)
	}
	if got := gamarr(t, tree, "unh", "md5"); got != md5hex([]byte("archived payload")) {
		t.Errorf("$.gamarr.unh.md5 = %v", got)
	}
	// The scratch dir must be gone, and the archive untouched.
	if _, err := os.Stat(filepath.Join(e.roms, workDirName)); !os.IsNotExist(err) {
		t.Errorf("scratch dir survived the run: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("library copy disturbed: %v", err)
	}
}

func TestMultiFileArchiveIsAPermanentSkip(t *testing.T) {
	e := newEnv(t)
	// Stub the extractor so this runs everywhere, not only where 7z is.
	prev := romfile.Exec7z
	t.Cleanup(func() { romfile.Exec7z = prev })
	romfile.Exec7z = func(ctx context.Context, archive, destDir string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c",
			"printf a > \"$1/a.gb\" && printf b > \"$1/b.gb\"", "sh", destDir)
	}
	id := e.row(t, "gb", "Pack", e.rom(t, "gb", "Pack.zip", []byte("archive bytes")), "{}")

	st := e.runSync(t, "gb", Opts{})
	if st["skipped"] != 1 || st["errors"] != 0 {
		t.Fatalf("status = %+v, want one skip", st)
	}
	if got := gamarr(t, e.meta(t, id), "hash_skipped"); got != db.HashSkipMultiFile {
		t.Errorf("marker = %v, want the multi-file marker", got)
	}
}

func TestExtractorFailureIsAnErrorNotASkip(t *testing.T) {
	e := newEnv(t)
	prev := romfile.Exec7z
	t.Cleanup(func() { romfile.Exec7z = prev })
	romfile.Exec7z = func(ctx context.Context, archive, destDir string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo broken >&2; exit 2")
	}
	id := e.row(t, "gb", "Broken", e.rom(t, "gb", "Broken.zip", []byte("x")), "{}")

	st := e.runSync(t, "gb", Opts{})
	if st["errors"] != 1 || st["skipped"] != 0 {
		t.Fatalf("status = %+v, want one error and no skip", st)
	}
	// An archive we could not open is not evidence of anything permanent.
	if got := gamarr(t, e.meta(t, id), "hash_skipped"); got != nil {
		t.Errorf("a failed extraction was marked permanently skipped: %v", got)
	}
}
