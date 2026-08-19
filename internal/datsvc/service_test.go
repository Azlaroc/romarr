package datsvc

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/dat"
	"gamarr/internal/db"
)

// catalog builds a clrmamepro DAT with n games, deterministic hashes and a
// caller-chosen header version, so a test can express "the same catalog",
// "one game added" and "a re-dump of game 2" precisely.
func catalog(version string, n int, salt string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "clrmamepro (\n\tname \"Test Catalog\"\n\tversion \"%s\"\n)\n\n", version)
	for i := 1; i <= n; i++ {
		sha := fmt.Sprintf("%040x", i*7919+len(salt))
		fmt.Fprintf(&b, "game (\n\tname \"Game %d (USA)\"\n\tdescription \"Game %d (USA)\"\n"+
			"\trom ( name \"Game %d (USA).rom\" size %d crc %08x md5 %032x sha1 %s )\n)\n\n",
			i, i, i, 2048*i, i, i, sha)
	}
	return []byte(b.String())
}

// zipOf builds a deflate zip of the given members.
func zipOf(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	return zipWith(t, zip.Deflate, members)
}

type harness struct {
	svc   *Service
	store *db.JobStore
	cfg   *config.Config
	url   string
	hits  map[string]int
	// hooked records every platform the snapshot callback fired for, so a
	// test can assert that a no-op refresh stays a no-op downstream.
	hooked []string
}

// newHarness wires a real store, a temp DataDir and a stub source. serve maps
// a request path to a body; a nil body means "fail this fetch".
func newHarness(t *testing.T, serve func(path string) ([]byte, bool)) *harness {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "dat.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	h := &harness{store: store, hits: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits[r.URL.Path]++
		body, ok := serve(r.URL.Path)
		if !ok {
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)

	h.cfg = &config.Config{DataDir: t.TempDir()}
	h.svc = New(h.cfg, store, WithHTTPClient(srv.Client()),
		WithOnSnapshot(func(slug string) { h.hooked = append(h.hooked, slug) }))
	h.url = srv.URL
	return h
}

// pointAt repoints an authority at the stub, which is exactly what the e2e
// journey does over the API — repointing is a data edit, not a deploy.
func (h *harness) pointAt(t *testing.T, authority, path string) {
	t.Helper()
	base := h.url + path
	if err := h.store.UpdateDatAuthority(authority, db.DatAuthorityPatch{FetchBase: &base}); err != nil {
		t.Fatalf("point %s at stub: %v", authority, err)
	}
}

func (h *harness) authority(t *testing.T, name string) db.DatAuthorityRow {
	t.Helper()
	a, err := h.store.GetDatAuthority(name)
	if err != nil {
		t.Fatalf("GetDatAuthority(%q): %v", name, err)
	}
	return a
}

// refreshAndWait runs a manual refresh to completion.
func (h *harness) refreshAndWait(t *testing.T, authority string) {
	t.Helper()
	started, err := h.svc.RefreshAuthority(authority)
	if err != nil {
		t.Fatalf("RefreshAuthority(%q): %v", authority, err)
	}
	if !started {
		t.Fatalf("RefreshAuthority(%q) reported a run already in flight", authority)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !h.svc.Status()["running"].(bool) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("refresh did not finish within 30s")
}

func known(t *testing.T, store *db.JobStore, slug string) int {
	t.Helper()
	for _, row := range store.DatCoverage() {
		if row.PlatformSlug == slug {
			return row.Known
		}
	}
	return 0
}

func TestRefreshImportsEveryAssignedPlatform(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	auth := h.authority(t, "no-intro")
	if auth.LastStatus != RunOK {
		t.Fatalf("last_status = %q (%s), want ok", auth.LastStatus, auth.LastError)
	}
	if auth.LastRefresh == "" {
		t.Fatal("last_refresh not stamped")
	}
	// The seeded pack assigns twenty cart platforms to this authority; every
	// one of them must have been fetched and imported.
	plats := assignedPlatforms(h.store.ListDatPlatforms(), "no-intro")
	if len(plats) < 20 {
		t.Fatalf("seed assigns %d platforms to no-intro, want the shipped twenty", len(plats))
	}
	for _, p := range plats {
		if got := known(t, h.store, p.PlatformSlug); got != 3 {
			t.Fatalf("coverage known for %s = %d, want 3", p.PlatformSlug, got)
		}
		raw := filepath.Join(h.cfg.DataDir, "dat", "no-intro", p.PlatformSlug+".dat")
		if _, err := os.Stat(raw); err != nil {
			t.Fatalf("raw copy for %s missing: %v", p.PlatformSlug, err)
		}
	}
	// The stored code omits the extension; the driver appends it.
	if h.hits["/mirror/Atari - 2600.dat"] != 1 {
		t.Fatalf("hits = %v, want one fetch of the seeded Atari - 2600 DAT name", h.hits)
	}
}

func TestRefreshUnzipsADiscAuthority(t *testing.T) {
	inner := catalog("2026-08-16 18-16-58", 4, "disc")
	archive := zipOf(t, map[string][]byte{
		"Sony - PlayStation - Datfile (10970) (2026-08-16 18-16-58).dat": inner,
	})
	h := newHarness(t, func(string) ([]byte, bool) { return archive, true })
	h.pointAt(t, "redump", "/datfile/")
	h.refreshAndWait(t, "redump")

	if got := known(t, h.store, "psx"); got != 4 {
		t.Fatalf("psx known = %d, want the zipped catalog's four games", got)
	}
	if h.hits["/datfile/psx/"] != 1 {
		t.Fatalf("hits = %v, want the trailing-slash datfile path", h.hits)
	}
}

func TestRefreshPartialFailureKeepsPreviousSnapshot(t *testing.T) {
	body := catalog("2026.08.01", 3, "")
	fail := false
	h := newHarness(t, func(path string) ([]byte, bool) {
		if fail && strings.Contains(path, "Game Boy.dat") {
			return nil, false
		}
		if fail {
			return catalog("2026.09.01", 5, "v2"), true
		}
		return body, true
	})
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	before, _ := h.store.ActiveDatSnapshot("gb")
	fail = true
	h.refreshAndWait(t, "no-intro")

	auth := h.authority(t, "no-intro")
	if auth.LastStatus != RunPartial {
		t.Fatalf("last_status = %q, want partial", auth.LastStatus)
	}
	if !strings.Contains(auth.LastError, "gb") {
		t.Fatalf("last_error = %q, want the failed platform named", auth.LastError)
	}
	// Stale beats absent: the platform that failed keeps the catalog it had.
	after, ok := h.store.ActiveDatSnapshot("gb")
	if !ok || after.ID != before.ID {
		t.Fatalf("gb snapshot changed on a failed fetch (%d -> %d)", before.ID, after.ID)
	}
	if got := known(t, h.store, "nes"); got != 5 {
		t.Fatalf("nes known = %d, want the platforms that succeeded to advance", got)
	}
}

func TestUnchangedSourceWritesNoSnapshot(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")
	first, _ := h.store.ActiveDatSnapshot("gb")

	h.refreshAndWait(t, "no-intro")
	second, _ := h.store.ActiveDatSnapshot("gb")
	if first.ID != second.ID {
		t.Fatalf("identical bytes wrote a new snapshot (%d -> %d)", first.ID, second.ID)
	}
	if second.DiffAdded != first.DiffAdded {
		t.Fatal("an unchanged re-fetch must not recompute a diff")
	}
}

func TestUnchangedSourceStillRestoresALostRawCopy(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	raw := filepath.Join(h.cfg.DataDir, "dat", "no-intro", "gb.dat")
	if err := os.Remove(raw); err != nil {
		t.Fatalf("remove raw copy: %v", err)
	}
	h.refreshAndWait(t, "no-intro")
	if _, err := os.Stat(raw); err != nil {
		t.Fatalf("a lost raw copy must be re-materialized, not short-circuited: %v", err)
	}
}

func TestPinnedVersionFreezesAutoAdvanceOnly(t *testing.T) {
	version := "2026.08.01"
	h := newHarness(t, func(string) ([]byte, bool) { return catalog(version, 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	pin := "2026.08.01"
	if err := h.store.UpdateDatAuthority("no-intro", db.DatAuthorityPatch{PinnedVersion: &pin}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	version = "2026.09.01"
	before, _ := h.store.ActiveDatSnapshot("gb")

	// The cadence path must refuse to advance past the pin.
	h.svc.runRefresh(context.Background(), h.authority(t, "no-intro"), true)
	after, _ := h.store.ActiveDatSnapshot("gb")
	if after.ID != before.ID {
		t.Fatal("the automatic path advanced past a pinned version")
	}

	// The button stays live: pinning freezes auto-advance, not the operator.
	h.refreshAndWait(t, "no-intro")
	manual, _ := h.store.ActiveDatSnapshot("gb")
	if manual.ID == before.ID || manual.Version != "2026.09.01" {
		t.Fatalf("a manual refresh must still advance a pinned authority (version %q)", manual.Version)
	}
}

func TestRefreshRefusesHandFedAuthority(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	// MAME ships dormant on the upload driver.
	if _, err := h.svc.RefreshAuthority("mame"); !errors.Is(err, ErrUploadOnly) {
		t.Fatalf("err = %v, want ErrUploadOnly", err)
	}
	if auth := h.authority(t, "mame"); auth.LastStatus != "" {
		t.Fatalf("a caller error must not be recorded as an authority failure (last_status = %q)", auth.LastStatus)
	}
}

func TestRefreshRejectsUnknownAuthority(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	if _, err := h.svc.RefreshAuthority("tosec"); err == nil {
		t.Fatal("an unassigned authority must not refresh")
	}
}

// The escape hatch has to actually replace what a fetch installed, or it is
// not an escape hatch.
func TestUploadReplacesFetchedSnapshot(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	fetched, _ := h.store.ActiveDatSnapshot("gb")
	if fetched.GameCount != 3 {
		t.Fatalf("fetched snapshot has %d games, want 3", fetched.GameCount)
	}

	hand := catalog("2026.08.17-hand", 7, "hand")
	res, err := h.svc.ImportUpload("no-intro", "gb", hand)
	if err != nil {
		t.Fatalf("ImportUpload: %v", err)
	}
	if len(res.Imported) != 1 || res.Imported[0].Games != 7 {
		t.Fatalf("imported = %+v, want the seven-game upload", res.Imported)
	}

	active, ok := h.store.ActiveDatSnapshot("gb")
	if !ok {
		t.Fatal("no active snapshot after upload")
	}
	if active.ID == fetched.ID {
		t.Fatal("the upload did not replace the fetched snapshot")
	}
	if active.GameCount != 7 || active.Version != "2026.08.17-hand" {
		t.Fatalf("active snapshot = %d games / version %q, want the uploaded catalog", active.GameCount, active.Version)
	}
	if active.DiffAdded != 4 {
		t.Fatalf("diff_added = %d, want 4 against the catalog it replaced", active.DiffAdded)
	}
	if got := known(t, h.store, "gb"); got != 7 {
		t.Fatalf("coverage known = %d, want the uploaded catalog's count", got)
	}
	// The on-disk copy has to follow, or a later "unchanged" check compares
	// against bytes nobody has.
	raw, err := os.ReadFile(filepath.Join(h.cfg.DataDir, "dat", "no-intro", "gb.dat"))
	if err != nil {
		t.Fatalf("raw copy: %v", err)
	}
	if string(raw) != string(hand) {
		t.Fatal("raw copy still holds the fetched catalog")
	}
}

func TestUploadPackFansOutAndReportsSkipped(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	pack := zipOf(t, map[string][]byte{
		// A daily pack stamps its members; the match has to see through that.
		"Nintendo - Game Boy (20260801-063015).dat":       catalog("2026.08.01", 3, "gb"),
		"Nintendo - Game Boy Color (20260801-063015).dat": catalog("2026.08.01", 4, "gbc"),
		"Commodore - Amiga (20260801-063015).dat":         catalog("2026.08.01", 9, "amiga"),
		"readme.txt": []byte("not a catalog"),
	})
	res, err := h.svc.ImportUpload("no-intro", "", pack)
	if err != nil {
		t.Fatalf("ImportUpload: %v", err)
	}
	if len(res.Imported) != 2 {
		t.Fatalf("imported %d catalogs, want gb and gbc", len(res.Imported))
	}
	if got := known(t, h.store, "gb"); got != 3 {
		t.Fatalf("gb known = %d, want 3", got)
	}
	if got := known(t, h.store, "gbc"); got != 4 {
		t.Fatalf("gbc known = %d, want 4", got)
	}
	// Skipping the ~79 unassigned members of a daily pack is the normal case,
	// not a failure — but it has to be reported, not silent.
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Member, "Amiga") {
		t.Fatalf("skipped = %+v, want the unassigned Amiga catalog", res.Skipped)
	}
}

func TestUploadSingleDatRequiresAPlatform(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	if _, err := h.svc.ImportUpload("no-intro", "", catalog("2026.08.01", 3, "")); err == nil {
		t.Fatal("a bare DAT with no platform is ambiguous and must be refused")
	}
}

func TestUploadRejectsPlatformFromAnotherAuthority(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	// psx belongs to redump; uploading it under no-intro is an operator slip.
	if _, err := h.svc.ImportUpload("no-intro", "psx", catalog("2026.08.01", 3, "")); err == nil {
		t.Fatal("a platform assigned to another authority must be refused")
	}
}

func TestUploadRejectsEmptyBody(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	if _, err := h.svc.ImportUpload("no-intro", "gb", nil); err == nil {
		t.Fatal("an empty upload must be refused")
	}
}

func TestMatchMemberPrefersTheLongestCode(t *testing.T) {
	plats := []db.DatPlatformRow{
		{PlatformSlug: "gb", Authority: "no-intro", DatCode: "Nintendo - Game Boy", Enabled: true},
		{PlatformSlug: "gbc", Authority: "no-intro", DatCode: "Nintendo - Game Boy Color", Enabled: true},
	}
	cases := map[string]string{
		"Nintendo - Game Boy.dat":                         "gb",
		"Nintendo - Game Boy (20260801-063015).dat":       "gb",
		"Nintendo - Game Boy Color.dat":                   "gbc",
		"Nintendo - Game Boy Color (20260801-063015).dat": "gbc",
	}
	for member, want := range cases {
		got, ok := matchMember(member, plats)
		if !ok || got.PlatformSlug != want {
			t.Fatalf("matchMember(%q) = %q/%v, want %q", member, got.PlatformSlug, ok, want)
		}
	}
	if _, ok := matchMember("Atari - Jaguar.dat", plats); ok {
		t.Fatal("an unassigned catalog must not match")
	}
}

// The scheduler's closed-channel bug: a runtime disable that leaves the stop
// channel closed silently kills every later arm.
func TestStopLoopReopensTheStopChannel(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	h.cfg.DatAutoRefreshEnabled = true

	h.svc.EnsureRunning()
	if !h.svc.LoopRunning() {
		t.Fatal("cadence did not arm with the toggle on")
	}
	h.svc.StopLoop()
	if h.svc.LoopRunning() {
		t.Fatal("cadence still armed after StopLoop")
	}
	h.svc.EnsureRunning()
	if !h.svc.LoopRunning() {
		t.Fatal("cadence did not re-arm: StopLoop left the stop channel closed")
	}
	h.svc.Stop()
	if h.svc.LoopRunning() {
		t.Fatal("cadence survived shutdown")
	}
}

func TestCadenceStaysDormantWhileTheToggleIsOff(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return nil, false })
	h.svc.EnsureRunning()
	if h.svc.LoopRunning() {
		t.Fatal("the cadence must ship off: no goroutine, no ticker, no network")
	}
}

// 🔴 Identical bytes imported by an OLDER parser are stale rows, not current
// ones. Without the parser version in the unchanged check, an improvement to
// what the parser derives never reaches a catalog whose source has not moved —
// and most catalogs do not move for months.
func TestParserBumpReimportsUnchangedBytes(t *testing.T) {
	h := newHarness(t, func(string) ([]byte, bool) { return catalog("2026.08.01", 3, ""), true })
	h.pointAt(t, "no-intro", "/mirror/")
	h.refreshAndWait(t, "no-intro")

	first, ok := h.store.ActiveDatSnapshot("gb")
	if !ok {
		t.Fatal("no snapshot after the first refresh")
	}
	if first.ParserVersion != dat.ParserVersion {
		t.Fatalf("stored parser version = %d, want the current %d", first.ParserVersion, dat.ParserVersion)
	}

	// Pretend those rows came from an older derivation.
	if err := h.store.SetSnapshotParserVersion(first.ID, dat.ParserVersion-1); err != nil {
		t.Fatalf("age the snapshot: %v", err)
	}
	h.refreshAndWait(t, "no-intro")

	second, _ := h.store.ActiveDatSnapshot("gb")
	if second.ID == first.ID {
		t.Error("a stale-parser catalog was short-circuited on identical bytes")
	}
	if second.ParserVersion != dat.ParserVersion {
		t.Errorf("re-imported at parser version %d, want %d", second.ParserVersion, dat.ParserVersion)
	}
}
