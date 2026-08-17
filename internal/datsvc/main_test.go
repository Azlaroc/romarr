package datsvc

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// Silence slog during test runs — the refresh path intentionally emits
// INFO/WARN lines that are noise in `go test -v`, not failures.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
