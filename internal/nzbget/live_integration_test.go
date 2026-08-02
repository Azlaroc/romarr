package nzbget

import (
	"os"
	"testing"
)

// TestLiveNZBGetReadOnly exercises the client against a real NZBGet instance
// without changing its queue or history. It is skipped unless NZBGET_LIVE_URL
// is set; NZBGET_LIVE_USER and NZBGET_LIVE_PASS provide optional basic auth.
func TestLiveNZBGetReadOnly(t *testing.T) {
	url := os.Getenv("NZBGET_LIVE_URL")
	if url == "" {
		t.Skip("NZBGET_LIVE_URL not set")
	}
	client := New(url, os.Getenv("NZBGET_LIVE_USER"), os.Getenv("NZBGET_LIVE_PASS"))

	version, err := client.TestConnection()
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	t.Logf("NZBGet version %s", version)

	queue, err := client.GetQueue()
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	t.Logf("queue items: %d", len(queue))

	history, err := client.GetHistory()
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	t.Logf("history items: %d", len(history))
}
