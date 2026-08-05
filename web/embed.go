package web

import (
	"embed"
	"io/fs"
	"log/slog"
)

// DistFS holds the built React SPA (web/frontend → vite build → web/dist).
// The "all:" prefix embeds dotfiles too, so a repo carrying only the
// web/dist/.gitkeep placeholder still compiles — the real UI is produced by
// the Docker/CI build. Everything is embedded so the binary stays fully
// self-contained and the UI works with no internet access.
//
//go:embed all:dist
var DistFS embed.FS

// IndexHTML is the SPA shell served at "/" and for every client-side deep
// link (see handleSPA). It is read once from the embedded dist. A build made
// against the placeholder (no real frontend) leaves it empty and logs a
// warning instead of panicking, so `go build`, `go test`, and goreleaser all
// work without a Node toolchain.
var IndexHTML []byte

func init() {
	data, err := fs.ReadFile(DistFS, "dist/index.html")
	if err != nil {
		slog.Warn("web: dist/index.html not embedded — run `npm run build` in web/frontend", "err", err)
		return
	}
	IndexHTML = data
}
