package metadata

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"gamarr/internal/platform"
)

// Silence slog during test runs — production code paths intentionally emit
// INFO/WARN logs that look like noise in `go test -v` output but are not
// test failures.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// This package resolves IGDB's platform identities through the registry,
	// so it needs one attached for the whole run. TestMain, not per-test: a
	// per-test cleanup would detach it for everything after.
	platform.SetRegistry(platform.StaticRegistry{
		{Slug: "snes", DisplayName: "Super Nintendo", IGDBSlug: "snes", IGDBID: 19},
		{Slug: "psx", DisplayName: "PlayStation", IGDBSlug: "ps", IGDBID: 7},
	})
	code := m.Run()
	platform.SetRegistry(nil)
	os.Exit(code)
}
