// Package organize handles post-download organization: extracting, renaming,
// and moving finished game files into the vault and ROM library layout.
package organize

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gamarr/internal/config"
	"gamarr/internal/platform"
)

// Pipeline handles post-download game file organization.
type Pipeline struct {
	cfg *config.Config
}

// NewPipeline creates a new file organization pipeline.
func NewPipeline(cfg *config.Config) *Pipeline {
	return &Pipeline{cfg: cfg}
}

// OrganizeGame moves a downloaded game file to the appropriate library directory.
// For ROMs: {roms_path}/{platform_slug}/{cleaned_filename}
// For PC games: {vault_path}/{game_name}/
func (p *Pipeline) OrganizeGame(sourcePath, platform, platformSlug string, isPC bool) (string, error) {
	if _, err := os.Stat(sourcePath); err != nil {
		return "", fmt.Errorf("source path not found: %s", sourcePath)
	}

	if isPC {
		return p.organizePC(sourcePath)
	}
	if platformSlug != "" {
		return p.organizeROM(sourcePath, platformSlug)
	}
	return sourcePath, fmt.Errorf("unknown platform, cannot organize")
}

func (p *Pipeline) organizePC(sourcePath string) (string, error) {
	destDir := p.cfg.GamesVaultPath
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return sourcePath, err
	}

	fi, err := os.Stat(sourcePath)
	if err != nil {
		return sourcePath, err
	}

	baseName := filepath.Base(sourcePath)
	var cleanName string
	if fi.IsDir() {
		// Directory names have no file extension: "Game.v1.0-FITGIRL" must
		// not be parsed as name "Game.v1" + extension ".0-FITGIRL", which
		// would leave the scene suffix uncleaned.
		cleanName = cleanBaseName(baseName)
	} else {
		cleanName = CleanFilename(baseName)
	}
	// The name derives from an externally supplied path; keep it a single
	// component so it cannot escape the vault dir.
	dest := filepath.Join(destDir, filepath.Base(cleanName))

	// Check duplicate.
	if exists, _ := DuplicateCheck(dest); exists {
		return dest, fmt.Errorf("already exists at destination: %s", dest)
	}

	if err := moveContent(sourcePath, dest); err != nil {
		return sourcePath, err
	}

	slog.Info("PC game organized", "source", sourcePath, "dest", dest)
	return dest, nil
}

func (p *Pipeline) organizeROM(sourcePath, platformSlug string) (string, error) {
	// The fs_slug must be a single local path component (mirrors
	// download.romDestDir — a divergent layout here would give manual imports
	// a path the RomM sync can't adopt) and must not climb out of the library.
	destDir := filepath.Join(p.cfg.GamesRomsPath, sanitizeSlug(platform.ToRommFSSlug(platformSlug)))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return sourcePath, err
	}

	baseName := filepath.Base(sourcePath)
	dest := filepath.Join(destDir, baseName)

	// Check duplicate.
	if exists, _ := DuplicateCheck(dest); exists {
		return dest, fmt.Errorf("already exists at destination: %s", dest)
	}

	if err := moveContent(sourcePath, dest); err != nil {
		return sourcePath, err
	}

	slog.Info("ROM organized", "source", sourcePath, "dest", dest, "platform", platformSlug)
	return dest, nil
}

// sanitizeSlug reduces an externally supplied platform slug to a single safe
// path component: no separators, no traversal (mirrors download.sanitizeFilename
// so the two organize paths cannot diverge on the on-disk layout).
func sanitizeSlug(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || !filepath.IsLocal(name) {
		return "download"
	}
	return name
}

// DetectPlatform tries to detect the platform from a file extension.
func DetectPlatform(filename string) (platform, platformSlug string, isPC bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".nsp", ".xci", ".nsz":
		return "Switch", "switch", false
	case ".3ds", ".cia":
		return "3DS", "3ds", false
	case ".nds":
		return "DS", "nds", false
	case ".gba":
		return "Game Boy Advance", "gba", false
	case ".gb":
		return "Game Boy", "gb", false
	case ".gbc":
		return "Game Boy Color", "gbc", false
	case ".n64", ".z64", ".v64":
		return "N64", "n64", false
	case ".nes":
		return "NES", "nes", false
	case ".sfc", ".smc":
		return "SNES", "snes", false
	case ".gcm", ".gcz":
		return "GameCube", "ngc", false
	case ".wbfs", ".wad":
		return "Wii", "wii", false
	case ".rpx":
		return "Wii U", "wiiu", false
	case ".pbp", ".cso":
		return "PSP", "psp", false
	case ".pkg":
		return "PS3", "ps3", false
	case ".gdi", ".cdi":
		return "Dreamcast", "dc", false
	case ".iso":
		// ISO is ambiguous — could be PS2, PSP, PS1, Xbox, etc.
		// Try to guess from filename.
		lower := strings.ToLower(filename)
		if strings.Contains(lower, "ps2") || strings.Contains(lower, "playstation 2") {
			return "PS2", "ps2", false
		}
		if strings.Contains(lower, "psp") {
			return "PSP", "psp", false
		}
		if strings.Contains(lower, "ps1") || strings.Contains(lower, "psx") {
			return "PS1", "psx", false
		}
		if strings.Contains(lower, "xbox") {
			return "Xbox", "xbox", false
		}
		if strings.Contains(lower, "wii") {
			return "Wii", "wii", false
		}
		if strings.Contains(lower, "gamecube") || strings.Contains(lower, "ngc") {
			return "GameCube", "ngc", false
		}
		return "", "", false
	case ".exe", ".msi":
		return "PC", "", true
	default:
		return "", "", false
	}
}

// CleanFilename removes scene tags and group names from filenames.
var (
	sceneGroupRe  = regexp.MustCompile(`(?i)\b(SKIDROW|CODEX|PLAZA|CPY|EMPRESS|DODI|FitGirl|RUNE|RAZORDOX|TINYISO|ELAMIGOS|REPACK|GOG)\b`)
	sceneTagsRe   = regexp.MustCompile(`(?i)\b(PROPER|REPACK|INTERNAL|RIP|MULTI\d+)\b`)
	bracketRe     = regexp.MustCompile(`\[[^\]]*\]`)
	dashGroupRe   = regexp.MustCompile(`\s*-\s*[A-Z]{3,}$`)
	multiDotRe    = regexp.MustCompile(`\.{2,}`)
	multiSpaceRe  = regexp.MustCompile(`\s{2,}`)
	unsafeCharsRe = regexp.MustCompile(`[<>:"/\\|?*]`)
)

func CleanFilename(name string) string {
	// Preserve extension.
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return cleanBaseName(base) + ext
}

// cleanBaseName cleans an extensionless name — a directory name, or a file
// name with its extension already split off.
func cleanBaseName(base string) string {
	// Replace dots with spaces (scene naming: Game.Name.v1.2-GROUP).
	if strings.Count(base, ".") > 2 {
		base = strings.ReplaceAll(base, ".", " ")
	}

	// Remove scene group names.
	base = sceneGroupRe.ReplaceAllString(base, "")
	base = sceneTagsRe.ReplaceAllString(base, "")
	base = bracketRe.ReplaceAllString(base, "")
	base = dashGroupRe.ReplaceAllString(base, "")

	// Clean up whitespace and special chars.
	base = unsafeCharsRe.ReplaceAllString(base, "")
	base = multiSpaceRe.ReplaceAllString(base, " ")
	base = multiDotRe.ReplaceAllString(base, ".")
	base = strings.TrimSpace(base)
	base = strings.Trim(base, ".-_ ")

	if base == "" {
		base = "Unknown"
	}

	return base
}

// DuplicateCheck checks if a file or directory already exists at the destination.
func DuplicateCheck(destPath string) (bool, error) {
	_, err := os.Stat(destPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ── File operations ──────────────────────────────────────────────────────────

func moveContent(src, dest string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := os.Rename(src, dest); err == nil {
			return nil
		}
		if err := copyDir(src, dest); err != nil {
			return err
		}
		return os.RemoveAll(src)
	}
	return moveFile(src, dest)
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fall back to copy + delete (cross-device).
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
