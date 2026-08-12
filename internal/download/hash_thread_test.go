package download

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// A DDL grab of an archive.org result must carry the expected md5/sha1 all the
// way to organize-time — persisted on the job record — so the convert stage
// (#261) has an expected hash to verify against before a destructive convert.
func TestDownloadDDLThreadsExpectedHashToOrganizeTime(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="American Pool (USA).sfc"`)
		w.Write([]byte("rom-bytes"))
	}))
	defer srv.Close()

	const wantMD5 = "d41d8cd98f00b204e9800998ecf8427e"
	const wantSHA1 = "da39a3ee5e6b4b0d3255bfef95601890afd80709"

	m := New(cfg, jobs, nil)
	jobID := m.DownloadDDL(srv.URL+"/dl", "", "American Pool", "SNES", "snes", false, wantMD5, wantSHA1)

	// Wait until organize has tracked the ROM (status flips to completed just
	// before TrackInLibrary), then confirm the expected hashes reached that point
	// on the persisted job record.
	dest := filepath.Join(cfg.GamesRomsPath, "snes", "American Pool (USA).sfc")
	waitFor(t, 10*time.Second, "library tracking", func() bool {
		return jobs.LibraryHasSourceID("ddl:" + dest)
	})
	job := waitJobStatus(t, jobs, jobID, "completed", 5*time.Second)
	if got, _ := job["md5"].(string); got != wantMD5 {
		t.Errorf("md5 at organize-time = %q, want %q", got, wantMD5)
	}
	if got, _ := job["sha1"].(string); got != wantSHA1 {
		t.Errorf("sha1 at organize-time = %q, want %q", got, wantSHA1)
	}
}

// A source without hashes (torrent/Vimm, or archive.org items lacking them)
// leaves md5/sha1 empty so the convert stage skips verify rather than blocking
// the import.
func TestDownloadDDLEmptyHashStaysEmpty(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="No Hash (USA).sfc"`)
		w.Write([]byte("rom-bytes"))
	}))
	defer srv.Close()

	m := New(cfg, jobs, nil)
	jobID := m.DownloadDDL(srv.URL+"/dl", "", "No Hash", "SNES", "snes", false, "", "")

	job := waitJobStatus(t, jobs, jobID, "completed", 10*time.Second)
	if got, _ := job["md5"].(string); got != "" {
		t.Errorf("md5 = %q, want empty", got)
	}
	if got, _ := job["sha1"].(string); got != "" {
		t.Errorf("sha1 = %q, want empty", got)
	}
}
