package archiveorg

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"gamarr/internal/sources/driver"
)

// TestLive_BuriedPS1Title hits the real Internet Archive. It is skipped unless
// ROMARR_LIVE_IA=1 so CI never depends on external network. Run manually:
//
//	ROMARR_LIVE_IA=1 go test ./internal/sources/archiveorg -run Live -v
//
// It proves the F2 thesis against live data: one PS1 title buried in a
// 1,500-file collection item resolves to a single file with correct md5/size,
// and its per-file URL is Range-fetchable (resumable).
func TestLive_BuriedPS1Title(t *testing.T) {
	if os.Getenv("ROMARR_LIVE_IA") == "" {
		t.Skip("set ROMARR_LIVE_IA=1 to run the live archive.org test")
	}
	d := New(map[string]string{"psx": psxItem},
		WithHTTPClient(&http.Client{Timeout: 60 * time.Second}))

	rel, err := d.Search(context.Background(), driver.Query{
		Text: "castlevania symphony of the night", PlatformSlug: "psx",
	})
	if err != nil {
		t.Fatalf("live Search: %v", err)
	}
	if len(rel) != 1 {
		t.Fatalf("want 1 buried result, got %d", len(rel))
	}
	got := rel[0]
	t.Logf("resolved: %s\n  size=%d md5=%s\n  url=%s", got.Title, got.Size, got.MD5, got.DownloadURL)
	if got.Size != 392826195 || got.MD5 != "6452e9da539aed682573794c8a794c83" {
		t.Fatalf("unexpected identity: size=%d md5=%s", got.Size, got.MD5)
	}

	// Range-fetch the first 16 bytes to prove per-file resumable download.
	req, _ := http.NewRequest(http.MethodGet, got.DownloadURL, nil)
	req.Header.Set("Range", "bytes=0-15")
	resp, err := d.http.Do(req)
	if err != nil {
		t.Fatalf("live Range GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("want HTTP 206, got %d", resp.StatusCode)
	}
	t.Logf("Range GET ok: %s content-range=%q", resp.Status, resp.Header.Get("Content-Range"))
}
