package hashfill

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// Silence slog: the runner intentionally logs warnings that look like
// failures in `go test -v` output and are not.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
