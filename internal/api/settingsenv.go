package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"gamarr/internal/converto"
)

// handleSettingsEnv reports the read-only, boot-time runtime configuration
// (env-derived paths and the knobs not yet migrated to the DB settings
// store) so the settings UI can display it. Everything here requires a
// restart to change, which is why it is a separate surface from the editable
// /api/settings document. Knobs promoted to /api/settings leave this doc.
func (s *Server) handleSettingsEnv(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg
	writeJSON(w, 200, map[string]interface{}{
		"paths": map[string]interface{}{
			"roms_path":          cfg.GamesRomsPath,
			"vault_path":         cfg.GamesVaultPath,
			"roms_free_bytes":    freeBytes(cfg.GamesRomsPath),
			"vault_free_bytes":   freeBytes(cfg.GamesVaultPath),
			"platform_dir_count": platformDirCount(cfg.GamesRomsPath),
		},
		"converto": s.convertoInfo(r.Context()),
	})
}

// freeBytes returns the filesystem free space for path, or 0 when the path
// does not resolve (missing mount, fresh install) — display data, never an
// error surface.
func freeBytes(path string) uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return st.Bavail * uint64(st.Bsize)
}

// platformDirCount counts the visible platform directories at the top of the
// ROM library root. Dot-prefixed entries are pipeline scratch
// (.gamarr-tmp, .gamarr-normalize-tmp), not platforms.
func platformDirCount(romsPath string) int {
	entries, err := os.ReadDir(romsPath)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

func (s *Server) convertoInfo(ctx context.Context) map[string]interface{} {
	cv := converto.New(s.cfg)
	info := map[string]interface{}{"available": false, "version": ""}
	if !cv.Available() {
		return info
	}
	info["available"] = true
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if v, err := cv.Version(vctx); err == nil {
		info["version"] = v
	}
	return info
}
