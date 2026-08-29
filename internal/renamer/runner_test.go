package renamer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
// converto binary (the online fallback, disabled unless a test arms the
// normalize_online_fallback setting). Returns the runner, the store, and the
// roms root.
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
	cfg.AttachSettings(store)
	return New(cfg, store, nil), store, romsRoot
}

// seedROM writes a rom file under <romsRoot>/<slug>/ and registers a library
// row for it (no stored hashes — the hashless arm computes and persists them).
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

// seedGhostROM registers a library row whose file does not exist — an
// identify error, for circuit-breaker tests.
func seedGhostROM(t *testing.T, store *db.JobStore, romsRoot, slug, name string) {
	t.Helper()
	p := filepath.Join(romsRoot, slug, name)
	if _, err := store.AddLibraryItem(&db.LibraryItem{
		Title: name, PlatformSlug: slug, FilePath: p,
		Source: "ddl", SourceType: "ddl", SourceID: "ddl:" + p, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
}

// seedDat installs the platform's active DAT snapshot: canonical rom
// filename → the CONTENT whose md5 catalogues it. One call per platform per
// test (re-seeding retires the earlier snapshot).
func seedDat(t *testing.T, store *db.JobStore, slug string, entries map[string]string) {
	t.Helper()
	var games []db.DatGameRow
	for romName, content := range entries {
		stem := strings.TrimSuffix(romName, filepath.Ext(romName))
		games = append(games, db.DatGameRow{
			Name: stem, BareTitle: stem, TotalSize: int64(len(content)),
			Roms: []db.DatRomRow{{Name: romName, Size: int64(len(content)), MD5: md5hex(content)}},
		})
	}
	meta := db.DatSnapshotMeta{Authority: "no-intro", PlatformSlug: slug, Version: "test"}
	if _, err := store.InsertDatSnapshot(meta, games); err != nil {
		t.Fatalf("InsertDatSnapshot: %v", err)
	}
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
	seedDat(t, store, "gb", map[string]string{
		"Canonical Name (USA).gb":    "content-a",
		"Canonical Already (USA).gb": "content-b",
	})
	seedROM(t, store, root, "gb", "Old Name (U).gb", "content-a")
	seedROM(t, store, root, "gb", "Canonical Already (USA).gb", "content-b")
	idMystery := seedROM(t, store, root, "gb", "Mystery.gb", "no dat entry")

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
	if by["rename"][0].NameSource != NameSourceDat {
		t.Errorf("name_source = %q, want dat", by["rename"][0].NameSource)
	}
	if len(by["noop"]) != 1 || len(by["skip"]) != 1 || by["skip"][0].Reason != SkipNoDatMatch {
		t.Errorf("noop/skip rows = %+v / %+v", by["noop"], by["skip"])
	}
	// The loud miss is counted on its own, and dat-sourced proposals too.
	if st["dat_misses"] != 1 || st["source_dat"] != 2 || st["source_playmatch"] != 0 {
		t.Errorf("counters = misses:%v dat:%v playmatch:%v", st["dat_misses"], st["source_dat"], st["source_playmatch"])
	}
	// The self-heal persisted the miss row's hashes: next preview answers
	// from the stored-hash arm.
	item, _ := store.GetLibraryItem(idMystery)
	if gh, ok := db.ParseGamarrHashes(item.Metadata); !ok || gh.MD5 != md5hex("no dat entry") {
		t.Errorf("self-heal did not persist hashes: %q", item.Metadata)
	}
}

func TestPreviewCollisionOnDisk(t *testing.T) {
	r, store, root := newTestRunner(t)
	// The canonical target already exists and its row carries a $.romm md5
	// equal to the old copy's inner hash → byte-identical verdict.
	oldContent := "same-bytes"
	sum := md5hex(oldContent)
	seedDat(t, store, "snes", map[string]string{"Hagane (USA).sfc": oldContent})
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
	seedDat(t, store2, "n64", map[string]string{"Pokemon Stadium (USA) (Rev 1).z64": "v10bytes"})
	seedROM(t, store2, root2, "n64", "Pokemon Stadium (U) (v1.0).z64", "v10bytes")
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
	c := "identical"
	seedDat(t, store, "gb", map[string]string{"Game (USA).gb": c})
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
	seedDat(t, store, "gb", map[string]string{"New (USA).gb": "x"})
	id := seedROM(t, store, root, "gb", "Old (U).gb", "x")
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

	// Persistent run history: the apply left one library_renamed activity
	// entry with the run summary (survives restarts, unlike runner state).
	activity, _ := store.GetActivity(1, 20)
	var summary string
	renamedEntries := 0
	for _, a := range activity {
		if a.EventType == "library_renamed" {
			renamedEntries++
			summary = a.Detail
		}
	}
	if renamedEntries != 1 {
		t.Errorf("library_renamed entries = %d, want 1", renamedEntries)
	}
	if !strings.Contains(summary, "1 renamed") || !strings.Contains(summary, "0 errors") {
		t.Errorf("summary = %q", summary)
	}

	// Second preview: everything is canonical now → all noop — and answered
	// from the stored-hash arm, since the first preview persisted hashes.
	r.TriggerPreview("gb")
	waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["noop"]) != 1 || len(by["rename"]) != 0 {
		t.Errorf("re-preview rows = %+v", by)
	}
}

func TestApplyTOCTOUAndExclude(t *testing.T) {
	r, store, root := newTestRunner(t)
	seedDat(t, store, "gb", map[string]string{"A (USA).gb": "a", "B (USA).gb": "b"})
	idA := seedROM(t, store, root, "gb", "A (U).gb", "a")
	seedROM(t, store, root, "gb", "B (U).gb", "b")

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
	seedDat(t, store2, "gb", map[string]string{"C (USA).gb": "c"})
	idC := seedROM(t, store2, root2, "gb", "C (U).gb", "c")
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
			"unique content "+string(rune('A'+i%26))+string(rune('0'+i/26)))
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
	// Library rows whose files are gone: every identify is an error.
	for i := 0; i < maxConsecutiveErrors+10; i++ {
		seedGhostROM(t, store, root, "gb",
			"Ghost "+string(rune('A'+i%26))+string(rune('0'+i/26))+" (U).gb")
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
	st := r.Status()
	for _, key := range []string{"dat_misses", "source_dat", "source_playmatch"} {
		if _, ok := st[key]; !ok {
			t.Errorf("status missing %q", key)
		}
	}
	b, err := json.Marshal(st)
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

func TestPreviewCompilationTieAndReview(t *testing.T) {
	r, store, root := newTestRunner(t)
	seedDat(t, store, "a26", map[string]string{
		// x: a hash tie between the original release and a compilation
		// extraction — the resolver prefers the original, no review needed.
		"Dark Cavern (USA).a26": "x",
		"Super Pocket - The Atari Collection (World) (Extracted).a26": "x",
		// y: the compilation entry is the ONLY candidate → review guard.
		"Some Game (Atari Anthology).a26": "y",
		// z: both sides tagged → legitimately a compilation build, renames.
		"Skyworks Game (Atari Anthology).a26": "z",
	})
	seedROM(t, store, root, "a26", "Dark Cavern (1982) (Mattel) [!].a26", "x")
	seedROM(t, store, root, "a26", "Mystery Game (U).a26", "y")
	seedROM(t, store, root, "a26", "Skyworks Game (Atari Anthology) (U).a26", "z")

	r.TriggerPreview("a26")
	waitDone(t, r)
	by := rowsByStatus(r)

	renames := map[string]string{}
	for _, row := range by["rename"] {
		renames[row.OldName] = row.NewName
	}
	if renames["Dark Cavern (1982) (Mattel) [!].a26"] != "Dark Cavern (USA).a26" {
		t.Errorf("tie not resolved to the original release: %v", renames)
	}
	if renames["Skyworks Game (Atari Anthology) (U).a26"] != "Skyworks Game (Atari Anthology).a26" {
		t.Errorf("both-tagged compilation should rename: %v", renames)
	}
	if len(by["review"]) != 1 || by["review"][0].OldName != "Mystery Game (U).a26" {
		t.Fatalf("review rows = %+v", by["review"])
	}
	if by["review"][0].NewName == "" || by["review"][0].Reason == "" {
		t.Errorf("review row missing proposal/reason: %+v", by["review"][0])
	}

	// Review rows are never applied.
	if !r.TriggerApply(nil) {
		t.Fatal("TriggerApply refused")
	}
	waitDone(t, r)
	if _, err := os.Stat(filepath.Join(root, "a26", "Mystery Game (U).a26")); err != nil {
		t.Error("review-flagged file was renamed")
	}
	if _, err := os.Stat(filepath.Join(root, "a26", "Dark Cavern (USA).a26")); err != nil {
		t.Error("tie-resolved rename did not apply")
	}
	if _, err := os.Stat(filepath.Join(root, "a26", "Skyworks Game (Atari Anthology).a26")); err != nil {
		t.Error("legit compilation rename did not apply")
	}
}

func TestPreviewAmbiguousIsReview(t *testing.T) {
	r, store, root := newTestRunner(t)
	seedDat(t, store, "nes", map[string]string{
		"Contra (USA).nes":         "w",
		"Probotector (Europe).nes": "w",
	})
	seedROM(t, store, root, "nes", "contra dump.nes", "w")

	r.TriggerPreview("nes")
	waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["review"]) != 1 || !strings.Contains(by["review"][0].Reason, "ambiguous DAT candidates") {
		t.Fatalf("review rows = %+v", by["review"])
	}
	if len(by["rename"]) != 0 {
		t.Errorf("ambiguous must not plan a rename: %+v", by["rename"])
	}
}

func TestPreviewFallbackDisabledByDefault(t *testing.T) {
	r, store, root := newTestRunner(t)
	// No catalog. Content the fake converto WOULD match — proving the online
	// engine is not consulted when the setting is off.
	seedROM(t, store, root, "gb", "Offline (U).gb", "MATCH:Should Not Happen (USA).gb:q")

	r.TriggerPreview("gb")
	st := waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["skip"]) != 1 || by["skip"][0].Reason != SkipNoDatMatch {
		t.Fatalf("rows = %+v", by)
	}
	if st["dat_misses"] != 1 {
		t.Errorf("dat_misses = %v, want 1", st["dat_misses"])
	}
}

func TestPreviewFallbackEnabledReviewOnlyAndOutageSafe(t *testing.T) {
	r, store, root := newTestRunner(t)
	store.SetSetting("normalize_online_fallback", "true")

	// A Playmatch hit lands as review, never a planned rename.
	seedROM(t, store, root, "gb", "Online (U).gb", "MATCH:Online Name (USA).gb:r")
	// Engine failures degrade loudly per row and must not feed the breaker:
	// more consecutive failures than the abort threshold, run completes.
	for i := 0; i < maxConsecutiveErrors+5; i++ {
		seedROM(t, store, root, "gb",
			"Broken "+string(rune('A'+i%26))+string(rune('0'+i/26))+" (U).gb", "FAIL always")
	}

	r.TriggerPreview("gb")
	st := waitDone(t, r)
	if st["done"] != maxConsecutiveErrors+6 || st["errors"] != 0 {
		t.Fatalf("run must complete without errors: %v", st)
	}
	by := rowsByStatus(r)
	if len(by["review"]) != 1 || by["review"][0].NameSource != NameSourcePlaymatch {
		t.Fatalf("review rows = %+v", by["review"])
	}
	if !strings.Contains(by["review"][0].Reason, "online fallback") {
		t.Errorf("review reason = %q", by["review"][0].Reason)
	}
	if len(by["rename"]) != 0 {
		t.Error("a playmatch-sourced name must never be planned automatically")
	}
	if st["source_playmatch"] != 1 {
		t.Errorf("source_playmatch = %v, want 1", st["source_playmatch"])
	}
	unavailable := 0
	for _, row := range by["skip"] {
		if row.Reason == SkipFallbackUnavailable {
			unavailable++
		}
	}
	if unavailable != maxConsecutiveErrors+5 {
		t.Errorf("fallback-unavailable skips = %d, want %d", unavailable, maxConsecutiveErrors+5)
	}
}

func TestPreviewRefusesFrozenPlatforms(t *testing.T) {
	r, store, root := newTestRunner(t)
	// No registry attached in this package → the shipped frozen set decides
	// (freezing degrades to shipped values, never to "unfrozen").
	seedROM(t, store, root, "switch", "Some Game [0100AAAA00000000].nsp", "nsp bytes")

	r.TriggerPreview("switch")
	waitDone(t, r)
	by := rowsByStatus(r)
	if len(by["skip"]) != 1 || by["skip"][0].Reason != FrozenReason {
		t.Fatalf("frozen rows = %+v", by)
	}
	if len(by["rename"])+len(by["review"])+len(by["noop"]) != 0 {
		t.Errorf("frozen platform must yield zero proposals: %+v", by)
	}
	// And nothing was staged or hashed for it.
	item, _ := store.GetLibraryItem(by["skip"][0].LibraryID)
	if _, ok := db.ParseGamarrHashes(item.Metadata); ok {
		t.Error("frozen row must not be hashed")
	}
}
