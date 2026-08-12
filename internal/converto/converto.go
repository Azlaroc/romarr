// Package converto is a thin Go client for the rom-converto CLI, the external
// ROM/disc conversion engine baked into the image. It wraps the frozen verb
// contract (chd / dat / playlist) the RomArr normalize+convert pipeline builds
// on, adding what the inline 7z/clamdscan exec sites in this repo lack:
// per-call context timeouts, captured stderr for diagnostics, and a startup
// availability/version probe.
//
// The client shells out with an argument slice (never a shell), so there is no
// command-injection surface; sanitizeLog only defends log lines against forged
// newlines. It deliberately does not import internal/download — download calls
// into converto, exactly as it calls safety.ScanWithClamAV.
package converto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gamarr/internal/config"
)

// Client invokes the rom-converto binary. Construct with New.
type Client struct {
	bin      string        // binary name on PATH or an absolute path
	apiBase  string        // Playmatch API base URL; empty = the tool's public default (dat verbs only)
	timeout  time.Duration // per-call ceiling; 0 disables
	cacheDir string        // XDG_CACHE_HOME for the tool's persistent hash/verify cache
}

// Options carries the subset of rom-converto's common flags the pipeline uses.
// Each verb method emits only the flags that verb actually accepts.
type Options struct {
	Output       string // -o/--output explicit output path (chd)
	OutputDir    string // --output-dir (chd, playlist)
	OnConflict   string // error | overwrite | skip | rename | overwrite-invalid
	PlaylistMode string // multiple | always (playlist)
	DryRun       bool   // --dry-run (global)
	Quiet        bool   // -q/--quiet (global)
	Recursive    bool   // -R/--recursive
	MaxDepth     int    // --max-depth; 0 = unset
}

// New builds a Client from config. Falls back to the "rom-converto" name on
// PATH when CONVERTO_BIN is unset; points the tool's cache at DataDir so it
// survives restarts and never bloats an image layer.
func New(cfg *config.Config) *Client {
	bin := cfg.ConvertoBin
	if bin == "" {
		bin = "rom-converto"
	}
	var cacheDir string
	if cfg.DataDir != "" {
		cacheDir = filepath.Join(cfg.DataDir, "converto-cache")
	}
	return &Client{
		bin:      bin,
		apiBase:  cfg.ConvertoAPIBase,
		timeout:  time.Duration(cfg.ConvertoTimeoutSec) * time.Second,
		cacheDir: cacheDir,
	}
}

// Available reports whether the binary resolves on PATH (or as an absolute
// path). Used for the boot-time probe and to gate conversion features.
func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin)
	return err == nil
}

// Version returns the raw `rom-converto --version` line (e.g.
// "rom-converto dev-6b5bc5e"; the release build self-labels with a commit
// hash rather than the tag, so callers must not assume a "vX.Y.Z" form).
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "--version", "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CompressCHD compresses a disc image (.cue or .iso; CD/DVD mode auto-detected)
// to a CHD and returns the output path. Offline — no Playmatch lookup.
func (c *Client) CompressCHD(ctx context.Context, input string, o Options) (string, error) {
	args := c.baseArgs(o)
	args = append(args, "chd", "compress")
	args = append(args, outputFlags(o)...)
	args = append(args, input)
	if _, err := c.run(ctx, "chd compress", args...); err != nil {
		return "", err
	}
	return c.chdOutputPath(input, o), nil
}

// VerifyCHD structurally verifies a CHD: rom-converto/chdman recompute the
// decompressed data's SHA1 and check it against the hash stored in the CHD
// header at creation, so a non-error return means the CHD faithfully reproduces
// the source it was made from. Used as the sole-copy safety gate before a source
// disc image is deleted. Returns nil on pass, an error on any failure.
func (c *Client) VerifyCHD(ctx context.Context, input string) error {
	args := c.baseArgs(Options{Quiet: true})
	args = append(args, "chd", "verify", input)
	_, err := c.run(ctx, "chd verify", args...)
	return err
}

// DatRename renames ROMs to their canonical Playmatch names (hash-verified,
// in place). Online: consults the Playmatch API (public instance unless
// CONVERTO_API_BASE points elsewhere).
func (c *Client) DatRename(ctx context.Context, input string, o Options) error {
	args := c.baseArgs(o)
	args = append(args, "dat", "rename")
	if o.OnConflict != "" {
		args = append(args, "--on-conflict", o.OnConflict)
	}
	if o.Recursive {
		args = append(args, "--recursive")
	}
	if o.MaxDepth > 0 {
		args = append(args, "--max-depth", strconv.Itoa(o.MaxDepth))
	}
	if c.apiBase != "" {
		args = append(args, "--api-base", c.apiBase)
	}
	args = append(args, input)
	_, err := c.run(ctx, "dat rename", args...)
	return err
}

// Playlist scans dir for multi-disc sets and writes one .m3u per game.
// Filename-based grouping only — no DAT lookup, offline.
func (c *Client) Playlist(ctx context.Context, dir string, o Options) error {
	args := c.baseArgs(o)
	args = append(args, "playlist")
	if o.OutputDir != "" {
		args = append(args, "--output-dir", o.OutputDir)
	}
	if o.PlaylistMode != "" {
		args = append(args, "--playlist-mode", o.PlaylistMode)
	}
	args = append(args, dir)
	_, err := c.run(ctx, "playlist", args...)
	return err
}

// baseArgs emits the global flags that clap accepts before the subcommand.
// --no-update-check keeps the tool from phoning home for a newer release; the
// pinned image controls the version.
func (c *Client) baseArgs(o Options) []string {
	args := []string{"--no-update-check"}
	if o.Quiet {
		args = append(args, "--quiet")
	}
	if o.DryRun {
		args = append(args, "--dry-run")
	}
	return args
}

// outputFlags emits the output/conflict/recursion flags shared by verbs that
// write files (currently chd compress).
func outputFlags(o Options) []string {
	var args []string
	if o.Output != "" {
		args = append(args, "--output", o.Output)
	}
	if o.OutputDir != "" {
		args = append(args, "--output-dir", o.OutputDir)
	}
	if o.OnConflict != "" {
		args = append(args, "--on-conflict", o.OnConflict)
	}
	if o.Recursive {
		args = append(args, "--recursive")
	}
	if o.MaxDepth > 0 {
		args = append(args, "--max-depth", strconv.Itoa(o.MaxDepth))
	}
	return args
}

// chdOutputPath mirrors rom-converto's own output derivation so callers know
// where the CHD landed: explicit --output wins; otherwise the input basename
// with a .chd extension, under --output-dir when set, else beside the input.
func (c *Client) chdOutputPath(input string, o Options) string {
	if o.Output != "" {
		return o.Output
	}
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)) + ".chd"
	dir := o.OutputDir
	if dir == "" {
		dir = filepath.Dir(input)
	}
	return filepath.Join(dir, base)
}

// run executes the binary with a per-call timeout, capturing stdout (returned)
// and stderr (folded into errors). It fails open on a missing binary the same
// way safety.ScanWithClamAV does, so a degraded image surfaces a clear error
// rather than a panic.
func (c *Client) run(ctx context.Context, label string, args ...string) (string, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = c.env()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\n")
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, fmt.Errorf("rom-converto %s timed out after %s", label, c.timeout)
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return out, fmt.Errorf("rom-converto not available (%s): %w", c.bin, err)
		}
		return out, fmt.Errorf("rom-converto %s failed: %w: %s", label, err, sanitizeLog(strings.TrimSpace(stderr.String())))
	}
	return out, nil
}

// env inherits the process environment and redirects the tool's persistent
// cache to cacheDir (created best-effort).
func (c *Client) env() []string {
	env := os.Environ()
	if c.cacheDir != "" {
		_ = os.MkdirAll(c.cacheDir, 0o755)
		env = append(env, "XDG_CACHE_HOME="+c.cacheDir)
	}
	return env
}

// sanitizeLog strips newlines from tool output before it reaches an error/log
// line, preventing forged entries. Package-local copy — matches the existing
// per-package duplication in download/util.go and api/security.go.
func sanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}
