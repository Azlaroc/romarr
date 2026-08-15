package download

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gamarr/internal/db"
)

// seedSetMemberJob registers a job carrying disc-set membership, the way
// DownloadDDL/DownloadTorrent/DownloadNZB stamp it at create time.
func seedSetMemberJob(t *testing.T, jobs *db.JobStore, jobID string, set DiscSet, status string) {
	t.Helper()
	data := map[string]interface{}{
		"status":        status,
		"title":         fmt.Sprintf("Test Game (USA) (Disc %d)", set.Index),
		"platform":      "PS1",
		"platform_slug": "psx",
		"is_pc":         false,
	}
	applyDiscSetJobData(data, set)
	jobs.Set(jobID, data)
}

// importSetMember drives one member payload through the real pipeline.
func importSetMember(t *testing.T, m *Manager, jobID string, set DiscSet) {
	t.Helper()
	staging := filepath.Join(t.TempDir(), fmt.Sprintf("Test Game (USA) (Disc %d).bin", set.Index))
	if err := os.WriteFile(staging, []byte("disc-data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.fulfillLocalROM(staging, fulfillMeta{
		JobID: jobID, Title: fmt.Sprintf("Test Game (USA) (Disc %d)", set.Index),
		Platform: "PS1", PlatformSlug: "psx",
		Source: "ddl", SourceClient: "ddl",
		DiscSet: set,
	}); err != nil {
		t.Fatalf("fulfillLocalROM disc %d: %v", set.Index, err)
	}
}

func memberSet(id string, index int) DiscSet {
	return DiscSet{ID: id, Index: index, Total: 3, Dir: "Test Game (USA)"}
}

func jobString(t *testing.T, jobs *db.JobStore, jobID, key string) string {
	t.Helper()
	job, ok := jobs.Get(jobID)
	if !ok {
		t.Fatalf("job %s missing", jobID)
	}
	s, _ := job[key].(string)
	return s
}

func jobBool(t *testing.T, jobs *db.JobStore, jobID, key string) bool {
	t.Helper()
	job, ok := jobs.Get(jobID)
	if !ok {
		t.Fatalf("job %s missing", jobID)
	}
	b, _ := job[key].(bool)
	return b
}

func TestDiscSetConvergesAndFinalizesOnLastDisc(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-t1"

	// Discs land out of order: 2, 1, then 3.
	for i, idx := range []int{2, 1} {
		jobID := fmt.Sprintf("job-%d", i)
		seedSetMemberJob(t, jobs, jobID, memberSet(setID, idx), "downloading")
		importSetMember(t, m, jobID, memberSet(setID, idx))
	}

	setDir := filepath.Join(cfg.GamesRomsPath, "psx", "Test Game (USA)")
	for _, n := range []string{"Test Game (USA) (Disc 1).bin", "Test Game (USA) (Disc 2).bin"} {
		if !pathExists(filepath.Join(setDir, n)) {
			t.Fatalf("member %s not in shared set dir", n)
		}
	}
	if jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("library row minted before the set completed")
	}
	if !jobBool(t, jobs, "job-0", "set_member_done") {
		t.Fatal("member not marked set_member_done")
	}
	if jobBool(t, jobs, "job-0", "set_finalized") {
		t.Fatal("member finalized before the set completed")
	}

	seedSetMemberJob(t, jobs, "job-2", memberSet(setID, 3), "downloading")
	importSetMember(t, m, "job-2", memberSet(setID, 3))

	if !jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("no library row after last disc landed")
	}
	for _, jobID := range []string{"job-0", "job-1", "job-2"} {
		if !jobBool(t, jobs, jobID, "set_finalized") {
			t.Errorf("%s not set_finalized after completion", jobID)
		}
	}
	if got := jobString(t, jobs, "job-2", "detail"); got != "Disc set complete (3 discs)" {
		t.Errorf("detail = %q", got)
	}
	if !pathExists(filepath.Join(setDir, ".gamarr.json")) {
		t.Error("set sidecar missing")
	}
	items := jobs.RecentLibraryItems(10)
	rows := 0
	for _, it := range items {
		if it.SourceID == "set:"+setID {
			rows++
			if it.Title != "Test Game (USA)" {
				t.Errorf("library title = %q, want set dir name", it.Title)
			}
			if it.FilePath != setDir {
				t.Errorf("library path = %q, want %q", it.FilePath, setDir)
			}
		}
	}
	if rows != 1 {
		t.Errorf("library rows for set = %d, want 1", rows)
	}
}

func TestDiscSetFinalizeIdempotentAndSingleFlight(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-t2"

	for i := 1; i <= 3; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		seedSetMemberJob(t, jobs, jobID, memberSet(setID, i), "downloading")
		importSetMember(t, m, jobID, memberSet(setID, i))
	}
	if !jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("set did not finalize")
	}

	// Redundant barrier calls — sequential and concurrent — must not mint a
	// second row or error.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.maybeFinalizeDiscSet(setID)
		}()
	}
	wg.Wait()

	rows := 0
	for _, it := range jobs.RecentLibraryItems(20) {
		if it.SourceID == "set:"+setID {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("library rows after redundant finalizes = %d, want 1", rows)
	}
}

func TestDiscSetSweepDegradesWithTerminalMember(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-t3"

	set1 := DiscSet{ID: setID, Index: 1, Total: 2, Dir: "Test Game (USA)"}
	seedSetMemberJob(t, jobs, "ok-job", set1, "downloading")
	importSetMember(t, m, "ok-job", set1)
	seedSetMemberJob(t, jobs, "dead-job", DiscSet{ID: setID, Index: 2, Total: 2, Dir: "Test Game (USA)"}, "error")

	if jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("incomplete set finalized early")
	}

	m.SweepDiscSets()

	if !jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("degraded set not tracked in library")
	}
	if got := jobString(t, jobs, "ok-job", "detail"); got != "Incomplete disc set (1 of 2 imported)" {
		t.Errorf("detail = %q", got)
	}
	if !jobBool(t, jobs, "dead-job", "set_finalized") {
		t.Error("terminal member not finalized by sweep")
	}
}

func TestDiscSetSweepAllFailedNoLibraryRow(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-t4"

	seedSetMemberJob(t, jobs, "f1", DiscSet{ID: setID, Index: 1, Total: 2, Dir: "Test Game (USA)"}, "error")
	seedSetMemberJob(t, jobs, "f2", DiscSet{ID: setID, Index: 2, Total: 2, Dir: "Test Game (USA)"}, "interrupted")

	m.SweepDiscSets()

	if jobs.LibraryHasSourceID("set:" + setID) {
		t.Error("failed set must not get a library row")
	}
	for _, jobID := range []string{"f1", "f2"} {
		if !jobBool(t, jobs, jobID, "set_finalized") {
			t.Errorf("%s not finalized by sweep", jobID)
		}
	}
}

func TestDiscSetSweepCompletesCrashedFinalize(t *testing.T) {
	// All members imported (set_member_done persisted) but finalize never ran
	// — the restart-between-import-and-barrier case. The boot sweep must
	// finish the job as a normal (non-degraded) finalize.
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-t5"

	setDir := filepath.Join(cfg.GamesRomsPath, "psx", "Test Game (USA)")
	if err := os.MkdirAll(setDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("Test Game (USA) (Disc %d).bin", i)
		if err := os.WriteFile(filepath.Join(setDir, name), []byte("disc-data"), 0644); err != nil {
			t.Fatal(err)
		}
		jobID := fmt.Sprintf("crashed-%d", i)
		seedSetMemberJob(t, jobs, jobID, DiscSet{ID: setID, Index: i, Total: 2, Dir: "Test Game (USA)"}, "completed")
		jobs.UpdateMulti(jobID, map[string]interface{}{
			"set_member_done": true,
			"imported_path":   filepath.Join(setDir, name),
		})
	}

	m.SweepDiscSets()

	if !jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("crashed finalize not completed by sweep")
	}
	if got := jobString(t, jobs, "crashed-1", "detail"); got != "Disc set complete (2 discs)" {
		t.Errorf("detail = %q (sweep must finalize non-degraded)", got)
	}
}

func TestDiscSetSweepTimeoutHonorsConfig(t *testing.T) {
	// SELECTOR_SET_TIMEOUT_HOURS below the old 24h const: a set idle past the
	// configured timeout degrades even though its missing member is still
	// nominally downloading.
	cfg := newTestConfig(t)
	cfg.SelectorSetTimeoutHours = 1
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-to1"

	set1 := DiscSet{ID: setID, Index: 1, Total: 2, Dir: "Test Game (USA)"}
	seedSetMemberJob(t, jobs, "done-job", set1, "downloading")
	importSetMember(t, m, "done-job", set1)
	seedSetMemberJob(t, jobs, "slow-job", DiscSet{ID: setID, Index: 2, Total: 2, Dir: "Test Game (USA)"}, "downloading")
	backdated := time.Now().Add(-2 * time.Hour).Unix()
	for _, jobID := range []string{"done-job", "slow-job"} {
		jobs.UpdateMulti(jobID, map[string]interface{}{"set_started_at": backdated})
	}

	m.SweepDiscSets()

	if !jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("set not degraded after configured timeout elapsed")
	}
	if got := jobString(t, jobs, "done-job", "detail"); got != "Incomplete disc set (1 of 2 imported)" {
		t.Errorf("detail = %q", got)
	}
}

func TestDiscSetSweepTimeoutExtendedByConfig(t *testing.T) {
	// SELECTOR_SET_TIMEOUT_HOURS above the old 24h const: a set idle longer
	// than 24h but within the configured timeout must NOT degrade — proves the
	// hardcoded const is really gone.
	cfg := newTestConfig(t)
	cfg.SelectorSetTimeoutHours = 100
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	const setID = "set-to2"

	set1 := DiscSet{ID: setID, Index: 1, Total: 2, Dir: "Test Game (USA)"}
	seedSetMemberJob(t, jobs, "done-job", set1, "downloading")
	importSetMember(t, m, "done-job", set1)
	seedSetMemberJob(t, jobs, "slow-job", DiscSet{ID: setID, Index: 2, Total: 2, Dir: "Test Game (USA)"}, "downloading")
	backdated := time.Now().Add(-25 * time.Hour).Unix()
	for _, jobID := range []string{"done-job", "slow-job"} {
		jobs.UpdateMulti(jobID, map[string]interface{}{"set_started_at": backdated})
	}

	m.SweepDiscSets()

	if jobs.LibraryHasSourceID("set:" + setID) {
		t.Fatal("set degraded before the configured timeout elapsed")
	}
	if jobBool(t, jobs, "done-job", "set_finalized") {
		t.Error("member finalized before the configured timeout elapsed")
	}
}

func TestDiscSetArchiveMemberFlattensIntoSetDir(t *testing.T) {
	// A zipped disc extracts into a per-disc subdir; for set members that
	// subdir must flatten into the shared set dir so sibling discs' cue
	// sheets sit side by side for the m3u pass.
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not installed")
	}
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	m.SaveSettings(&Settings{ExtractArchives: true})
	const setID = "set-flat"

	set := DiscSet{ID: setID, Index: 1, Total: 2, Dir: "Test Game (USA)"}
	seedSetMemberJob(t, jobs, "zip-job", set, "downloading")
	staging := filepath.Join(t.TempDir(), "Test Game (USA) (Disc 1).zip")
	writeZipT(t, staging, "Test Game (USA) (Disc 1).cue", []byte("FILE data"))
	if _, err := m.fulfillLocalROM(staging, fulfillMeta{
		JobID: "zip-job", Title: "Test Game (USA) (Disc 1)",
		Platform: "PS1", PlatformSlug: "psx",
		Source: "ddl", SourceClient: "ddl", DiscSet: set,
	}); err != nil {
		t.Fatalf("fulfillLocalROM: %v", err)
	}

	setDir := filepath.Join(cfg.GamesRomsPath, "psx", "Test Game (USA)")
	if !pathExists(filepath.Join(setDir, "Test Game (USA) (Disc 1).cue")) {
		t.Error("extracted disc content not flattened to set-dir top level")
	}
	if pathExists(filepath.Join(setDir, "Test Game (USA) (Disc 1)")) {
		t.Error("per-disc extraction subdir left behind")
	}
	if got := jobString(t, jobs, "zip-job", "imported_path"); got != setDir {
		t.Errorf("imported_path = %q, want set dir %q", got, setDir)
	}
}

func TestDiscSetJobPersistenceRoundTrip(t *testing.T) {
	// Job blobs round-trip through SQLite JSON: ints come back float64 after
	// a restart's loadAll. The reader must still reconstruct the membership —
	// this is what keeps the barrier working across a process restart.
	path := filepath.Join(t.TempDir(), "jobs.db")
	jobs, err := db.New(path)
	if err != nil {
		t.Fatal(err)
	}
	set := DiscSet{ID: "set-rt", Index: 2, Total: 3, Dir: "Game (USA)"}
	data := map[string]interface{}{"status": "downloading"}
	applyDiscSetJobData(data, set)
	jobs.Set("rt", data)
	jobs.Close()

	reopened, err := db.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	job, ok := reopened.Get("rt")
	if !ok {
		t.Fatal("job missing after reopen")
	}
	got := discSetFromJob(job)
	if got != set {
		t.Errorf("round-trip = %+v, want %+v", got, set)
	}
}
