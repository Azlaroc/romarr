package download

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsExtractableArchive(t *testing.T) {
	for _, p := range []string{"a.zip", "a.7z", "a.rar", "dir/B.ZIP", "Game (USA).7Z"} {
		if !isExtractableArchive(p) {
			t.Errorf("isExtractableArchive(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"a.bin", "a.cue", "a.iso", "a.chd", "noext"} {
		if isExtractableArchive(p) {
			t.Errorf("isExtractableArchive(%q) = true, want false", p)
		}
	}
}

func TestGameDirForArchive(t *testing.T) {
	cases := []struct{ archive, want string }{
		{"/roms/psx/American Pool (USA).zip", "/roms/psx/American Pool (USA)"},
		{"/roms/psx/Game.7z", "/roms/psx/Game"},
		{"/roms/psx/Game.rar", "/roms/psx/Game"},
	}
	for _, c := range cases {
		if got := gameDirForArchive(c.archive); got != c.want {
			t.Errorf("gameDirForArchive(%q) = %q, want %q", c.archive, got, c.want)
		}
	}
}

func TestContentSize(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "rom.bin")
	writeFileT(t, file, []byte("12345"))
	if got := contentSize(file); got != 5 {
		t.Errorf("contentSize(file) = %d, want 5", got)
	}

	sub := filepath.Join(dir, "game")
	writeFileT(t, filepath.Join(sub, "disc.bin"), []byte("123"))
	writeFileT(t, filepath.Join(sub, "disc.cue"), []byte("45"))
	if got := contentSize(sub); got != 5 {
		t.Errorf("contentSize(dir) = %d, want 5 (sum of tree)", got)
	}

	if got := contentSize(filepath.Join(dir, "ghost")); got != 0 {
		t.Errorf("contentSize(missing) = %d, want 0", got)
	}
}

// A DDL whose filename can only come from the (percent-encoded) URL path must
// land on disk with a decoded name — not "Game%20%28USA%29.zip".
func TestDownloadDDLDecodesPercentEncodedURLName(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("rom-bytes")) // no Content-Disposition
	}))
	defer srv.Close()

	m := New(cfg, jobs, nil)
	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "downloading"})

	got, err := m.downloadDDL(srv.URL+"/download/coll/Sony%20-%20Playstation/American%20Pool%20%28USA%29.zip", cfg.QBSavePath, jobID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base := filepath.Base(got); base != "American Pool (USA).zip" {
		t.Errorf("filename = %q, want decoded %q", base, "American Pool (USA).zip")
	}
}

// A DDL ROM archive should extract into a clean game-named directory (no
// ".zip.extracted" wrapper), the source archive should be consumed, and the
// library should point at the extracted content with a real size.
func TestOrganizeDDLFileExtractsToCleanGameDir(t *testing.T) {
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not installed")
	}
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	m.SaveSettings(&Settings{ExtractArchives: true})

	// Stage a completed DDL: a zip holding a two-file PS1 disc.
	src := filepath.Join(cfg.QBSavePath, "American Pool (USA).zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"American Pool (USA).bin": "bin-data",
		"American Pool (USA).cue": "cue-data",
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()

	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "organizing", "error": nil})
	m.organizeDDLFile(jobID, src, "American Pool (USA)", "PS1", "psx", false)

	psxDir := filepath.Join(cfg.GamesRomsPath, "psx")
	gameDir := filepath.Join(psxDir, "American Pool (USA)")

	if !pathExists(filepath.Join(gameDir, "American Pool (USA).bin")) ||
		!pathExists(filepath.Join(gameDir, "American Pool (USA).cue")) {
		t.Errorf("disc not extracted into clean game dir %s", gameDir)
	}
	if pathExists(filepath.Join(psxDir, "American Pool (USA).zip.extracted")) {
		t.Error("legacy .zip.extracted wrapper should not exist")
	}
	if pathExists(filepath.Join(psxDir, "American Pool (USA).zip")) {
		t.Error("source archive should be removed after extraction")
	}
	sourceID := "ddl:" + gameDir
	if !jobs.LibraryHasSourceID(sourceID) {
		t.Errorf("library not tracking extracted game dir (sourceID %q)", sourceID)
	}
	job, _ := jobs.Get(jobID)
	if detail, _ := job["detail"].(string); !strings.Contains(detail, "extracted") {
		t.Errorf("detail = %q, want an 'extracted' note", detail)
	}
}
