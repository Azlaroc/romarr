package download

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyDownloadHash(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "rom.bin")
	data := []byte("some rom bytes")
	writeFileT(t, fp, data)

	m := md5.Sum(data)
	s := sha1.Sum(data)
	wantMD5 := hex.EncodeToString(m[:])
	wantSHA1 := hex.EncodeToString(s[:])

	cases := []struct {
		name            string
		md5, sha1, want string
	}{
		{"both match", wantMD5, wantSHA1, "ok"},
		{"md5 only", wantMD5, "", "ok"},
		{"case-insensitive", strings.ToUpper(wantMD5), "", "ok"},
		{"bad md5", "deadbeef", "", "mismatch"},
		{"bad sha1", "", "deadbeef", "mismatch"},
		{"good md5 bad sha1", wantMD5, "deadbeef", "mismatch"},
		{"no expected", "", "", "skipped"},
	}
	for _, c := range cases {
		if got := verifyDownloadHash(fp, c.md5, c.sha1); got != c.want {
			t.Errorf("%s: verifyDownloadHash = %q, want %q", c.name, got, c.want)
		}
	}
	if got := verifyDownloadHash(filepath.Join(dir, "ghost"), wantMD5, ""); got != "skipped" {
		t.Errorf("missing file = %q, want skipped", got)
	}
}

// MaybeConvert is a pure pass-through when the setting is off (the shipped
// default).
func TestMaybeConvertDisabledPassthrough(t *testing.T) {
	cfg := newTestConfig(t)
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil) // ConvertROMs defaults false

	const p = "/roms/psx/Some Game (USA)"
	if got := m.MaybeConvert("", p, "psx", "ok"); got != p {
		t.Fatalf("MaybeConvert(disabled) = %q, want passthrough %q", got, p)
	}
}

// With ConvertROMs enabled but the binary unavailable, a disc-system import must
// still complete and track the ROM unchanged — convert is non-blocking. Pointing
// ConvertoBin at an absent binary keeps this deterministic even in CI.
func TestOrganizeDDLFileConvertNonBlockingWhenBinaryAbsent(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.ConvertoBin = "rom-converto-absent-" + t.Name()
	jobs := newTestJobs(t)
	m := New(cfg, jobs, nil)
	m.SaveSettings(&Settings{ConvertROMs: true})

	src := filepath.Join(cfg.QBSavePath, "Some Game (USA).iso")
	writeFileT(t, src, []byte("disc-bytes"))

	jobID := newJobID()
	jobs.Set(jobID, map[string]interface{}{"status": "organizing", "error": nil})
	m.organizeDDLFile(jobID, src, "Some Game (USA)", "PS1", "psx", false, "", "")

	dest := filepath.Join(cfg.GamesRomsPath, "psx", "Some Game (USA).iso")
	if !pathExists(dest) {
		t.Fatalf("ROM not organized to %s", dest)
	}
	if !jobs.LibraryHasSourceID("ddl:" + dest) {
		t.Errorf("library not tracking the moved ROM at %q", dest)
	}
	job, _ := jobs.Get(jobID)
	if st, _ := job["status"].(string); st != "completed" {
		t.Errorf("status = %q, want completed", st)
	}
}
