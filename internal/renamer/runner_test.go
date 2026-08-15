package renamer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
)

func newTestStore(t *testing.T) *db.JobStore {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "gamarr.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// newTestRunner builds a Runner over a real temp library dir and the fake
// converto binary. Returns the runner, the store, and the roms root.
func newTestRunner(t *testing.T) (*Runner, *db.JobStore, string) {
	t.Helper()
	romsRoot := t.TempDir()
	cfg := &config.Config{
		ConvertoBin:        fakeConverto(t),
		ConvertoTimeoutSec: 30,
		DataDir:            t.TempDir(),
		GamesRomsPath:      romsRoot,
	}
	store := newTestStore(t)
	return New(cfg, store, nil), store, romsRoot
}

// seedROM writes a rom file under <romsRoot>/<slug>/ and registers a library
// row for it. Content drives the fake converto (MATCH:/FAIL/miss).
func seedROM(t *testing.T, store *db.JobStore, romsRoot, slug, name, content string) int64 {
	t.Helper()
	dir := filepath.Join(romsRoot, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := writeROM(t, dir, name, content)
	id, err := store.AddLibraryItem(&db.LibraryItem{
		Title: name, PlatformSlug: slug, FilePath: p, FileSize: int64(len(content)),
		Source: "ddl", SourceType: "ddl", SourceID: "ddl:" + p, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitDone(t *testing.T, r *Runner) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !r.running.Load() {
			return r.Status()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runner did not finish")
	return nil
}

func rowsByStatus(r *Runner) map[string][]PreviewRow {
	rows, _ := r.PreviewPage(1, 1000)
	out := map[string][]PreviewRow{}
	for _, row := range rows {
		out[row.Status] = append(out[row.Status], row)
	}
	return out
}

func TestPreviewClassification(t *testing.T) {
	r, store, root := newTestRunner(t)
	seedROM(t, store, root, "gb", "Old Name (U).gb", "MATCH:Canonical Name (USA).gb:a")
	seedROM(t, store, root, "gb", "Canonical Already (USA).gb", "MATCH:Canonical Already (USA).gb:b")
	seedROM(t, store, root, "gb", "Mystery.gb", "no dat entry")

	if !r.TriggerPreview("gb") {
		t.Fatal("TriggerPreview refused")
	}
	st := waitDone(t, r)
	if st["total"] != 3 || st["done"] != 3 {
		t.Errorf("status = %v", st)
	}
	by := rowsByStatus(r)
	if len(by["rename"]) != 1 || by["rename"][0].NewName != "Canonical Name (USA).gb" {
		t.Errorf("rename rows = %+v", by["rename"])
	}
	if len(by["noop"]) != 1 || len(by["skip"]) != 1 || by["skip"][0].Reason != SkipNoDatMatch {
		t.Errorf("noop/skip rows = %+v / %+v", by["noop"], by["skip"])
	}
}

func TestPreviewCollisionOnDisk(t *testing.T) {
	r, store, root := newTestRunner(t)
	// The canonical target already exists and its row carries a $.romm md5
	// equal to the old copy's inner hash → byte-identical verdict.
	oldContent := "MATCH:Hagane (USA).sfc:same-bytes"
	sum := md5hex(oldContent)
	seedROM(t, store, root, "snes", "Hagane (U).sfc", oldContent)
	dir := filepath.Join(root, "snes")
	writeROM(t, dir, "Hagane (USA).sfc", "whatever new bytes")
	store.AddLibraryItem(&db.LibraryItem{
		Title: "Hagane (USA)", PlatformSlug: "snes",
		FilePath: filepath.Join(dir, "Hagane (USA).sfc"),
		Source:   "romm", SourceType: "romm", SourceID: "romm:9",
		Metadata: `{"romm":{"md5":"` + sum + `"}}`,
	})

	r.TriggerPreview("snes")
	waitDone(t, r)
	by := rowsByStatus(r)
	var coll *PreviewRow
	for i := range by["skip"] {
		if by["skip"][i].Collision != nil {
			coll = &by["skip"][i]
		}
	}
	if coll == nil || coll.Collision.Verdict != VerdictByteIdentical {
		t.Fatalf("collision row = %+v", coll)
	}

	// Different stored hash → different-bytes.
	r2, store2, root2 := newTestRunner(t)
	seedROM(t, store2, root2, "n64", "Pokemon Stadium (U) (v1.0).z64", "MATCH:Pokemon Stadium (USA) (Rev 1).z64:v10bytes")
	dir2 := filepath.Join(root2, "n64")
	writeROM(t, dir2, "Pokemon Stadium (USA) (Rev 1).z64", "rev1")
	store2.AddLibraryItem(&db.LibraryItem{
		Title: "Pokemon Stadium (USA) (Rev 1)", PlatformSlug: "n64",
		FilePath: filepath.Join(dir2, "Pokemon Stadium (USA) (Rev 1).z64"),
		Source:   "romm", SourceType: "romm", SourceID: "romm:10",
		Metadata: `{"romm":{"md5":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`,
	})
	r2.TriggerPreview("n64")
	waitDone(t, r2)
	by2 := rowsByStatus(r2)
	var rev *PreviewRow
	for i := range by2["skip"] {
		if by2["skip"][i].Collision != nil {
			rev = &by2["skip"][i]
		}
	}
	if rev == nil || rev.Collision.Verdict != VerdictDifferent {
		t.Errorf("rev collision = %+v", by2["skip"])
	}
}

func TestPreviewIntraRunCollision(t *testing.T) {
	r, store, root := newTestRunner(t)
	// Two non-canonical copies proposing the same target, identical bytes.
	c := "MATCH:Game (USA).gb:identical"
	seedROM(t, store, root, "gb", "Game (U) [!].gb", c)
	seedROM(t, store, root, "gb", "Game (U) [b1].gb", c)

	r.TriggerPreview("gb")
	waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["rename"]) != 1 || len(by["skip"]) != 1 {
		t.Fatalf("rows = %+v", by)
	}
	if by["skip"][0].Collision == nil || by["skip"][0].Collision.Verdict != VerdictByteIdentical {
		t.Errorf("intra-run collision = %+v", by["skip"][0])
	}
}

func TestApplyRenamesDiskDBAndSidecar(t *testing.T) {
	r, store, root := newTestRunner(t)
	id := seedROM(t, store, root, "gb", "Old (U).gb", "MATCH:New (USA).gb:x")
	oldPath := filepath.Join(root, "gb", "Old (U).gb")
	if err := os.WriteFile(oldPath+".gamarr.json", []byte(`{"t":"Old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	notified := map[string]int{}
	r.importNotify = func(fsSlug string) { notified[fsSlug]++ }

	r.TriggerPreview("gb")
	waitDone(t, r)
	if !r.TriggerApply(nil) {
		t.Fatal("TriggerApply refused")
	}
	st := waitDone(t, r)
	if st["renamed"] != 1 || st["errors"] != 0 {
		t.Errorf("status = %v", st)
	}

	newPath := filepath.Join(root, "gb", "New (USA).gb")
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(newPath + ".gamarr.json"); err != nil {
		t.Errorf("sidecar not moved: %v", err)
	}
	item, _ := store.GetLibraryItem(id)
	if item.FilePath != newPath || item.SourceID != "ddl:"+newPath {
		t.Errorf("db row = %+v", item)
	}
	if notified["gb"] != 1 {
		t.Errorf("importNotify = %v, want one gb notification", notified)
	}

	// Second preview: everything is canonical now → all noop, resume story.
	r.TriggerPreview("gb")
	waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["noop"]) != 1 || len(by["rename"]) != 0 {
		t.Errorf("re-preview rows = %+v", by)
	}
}

func TestApplyTOCTOUAndExclude(t *testing.T) {
	r, store, root := newTestRunner(t)
	idA := seedROM(t, store, root, "gb", "A (U).gb", "MATCH:A (USA).gb:a")
	seedROM(t, store, root, "gb", "B (U).gb", "MATCH:B (USA).gb:b")

	r.TriggerPreview("gb")
	waitDone(t, r)

	// The A target appears between preview and apply.
	writeROM(t, filepath.Join(root, "gb"), "A (USA).gb", "sniped")

	if !r.TriggerApply([]int64{}) {
		t.Fatal("apply refused")
	}
	st := waitDone(t, r)
	if st["renamed"] != 1 || st["collisions"].(int) < 1 {
		t.Errorf("status = %v", st)
	}
	if _, err := os.Stat(filepath.Join(root, "gb", "A (U).gb")); err != nil {
		t.Errorf("A must not have been renamed over the sniped target: %v", err)
	}

	// Exclusion: re-seed a fresh case and exclude it.
	r2, store2, root2 := newTestRunner(t)
	idC := seedROM(t, store2, root2, "gb", "C (U).gb", "MATCH:C (USA).gb:c")
	r2.TriggerPreview("gb")
	waitDone(t, r2)
	if r2.TriggerApply([]int64{idC}) {
		t.Error("apply with everything excluded should refuse (nothing planned)")
	}
	if _, err := os.Stat(filepath.Join(root2, "gb", "C (U).gb")); err != nil {
		t.Errorf("excluded file must be untouched: %v", err)
	}
	_ = idA
}

func TestStopCancelsAndRunnerReusable(t *testing.T) {
	r, store, root := newTestRunner(t)
	for i := 0; i < 50; i++ {
		seedROM(t, store, root, "gb", "Game "+string(rune('A'+i%26))+string(rune('0'+i/26))+" (U).gb",
			"MATCH:Game Out "+string(rune('A'+i%26))+string(rune('0'+i/26))+" (USA).gb:x")
	}
	if !r.TriggerPreview("gb") {
		t.Fatal("trigger refused")
	}
	if r.TriggerPreview("gb") {
		t.Error("second trigger while running must refuse")
	}
	r.Stop()
	waitDone(t, r)

	// A fresh run after Stop must work (the key delta vs the sync/scheduler
	// terminal Stop).
	if !r.TriggerPreview("gb") {
		t.Fatal("runner not reusable after Stop")
	}
	st := waitDone(t, r)
	if st["done"] != 50 {
		t.Errorf("post-stop run incomplete: %v", st)
	}
}

func TestCircuitBreakerAbortsPreview(t *testing.T) {
	r, store, root := newTestRunner(t)
	for i := 0; i < maxConsecutiveErrors+10; i++ {
		seedROM(t, store, root, "gb",
			"Broken "+string(rune('A'+i%26))+string(rune('0'+i/26))+" (U).gb", "FAIL always")
	}
	r.TriggerPreview("gb")
	st := waitDone(t, r)
	if st["done"].(int) > maxConsecutiveErrors {
		t.Errorf("breaker did not abort: %v", st)
	}
	if st["last_error"] == "" {
		t.Error("last_error empty after abort")
	}
}

func TestStatusJSONShape(t *testing.T) {
	r, _, _ := newTestRunner(t)
	b, err := json.Marshal(r.Status())
	if err != nil || len(b) == 0 {
		t.Fatalf("status not serializable: %v", err)
	}
	var nilRunner *Runner
	if st := nilRunner.Status(); st["enabled"] != false {
		t.Errorf("nil runner status = %v", st)
	}
	nilRunner.Stop() // must not panic
	if nilRunner.TriggerPreview("gb") || nilRunner.TriggerApply(nil) {
		t.Error("nil runner triggers must refuse")
	}
}
