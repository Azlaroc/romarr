// Package rommconnect notifies a RomM server that library content changed by
// triggering targeted platform scans — the Connect plane, in *arr terms: the
// producer that just imported a file tells the consumer exactly what to look
// at, instead of the consumer watching the filesystem.
//
// RomM has no REST scan endpoint: scanning is a socket.io event ("scan")
// gated on the tasks.run scope, and the socket handshake authenticates via
// session cookie only. The client therefore logs in over REST to mint a
// session, then speaks the socket.io protocol over its HTTP long-polling
// transport, which needs nothing beyond the standard library. One TriggerScan
// call is one short-lived session: login, handshake, join the root namespace,
// emit the scan event, listen briefly for a rejection, disconnect.
//
// The scan itself runs asynchronously inside RomM (it can take hours under
// metadata throttling); the client deliberately does not wait for scan:done.
// The one prompt answer worth waiting for is scan:done_ko, which RomM emits
// immediately when a scan is already in progress — surfaced as
// ErrScanInProgress so callers can requeue and retry.
package rommconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultListenWait = 5 * time.Second
	// socketPath is where RomM mounts its socket.io server.
	socketPath = "/ws/socket.io/"
	// maxResponseSize bounds any single transport read; socket.io control
	// frames are tiny, so this is purely defensive.
	maxResponseSize = 1 << 20
)

// ErrScanInProgress is returned when RomM rejects the trigger because another
// scan is already running or queued. RomM drops such requests rather than
// queuing them, so the caller must retry later.
var ErrScanInProgress = errors.New("romm: a scan is already in progress")

// HTTPError is a non-2xx response from RomM.
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("RomM %s: HTTP %d", e.Path, e.Status)
}

// Client triggers scans on one RomM instance. The account must hold the
// tasks.run scope (an admin-role user in RomM 5.x); lesser roles connect fine
// but their scan events are silently rejected server-side.
type Client struct {
	baseURL    string
	user       string
	pass       string
	timeout    time.Duration
	listenWait time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout overrides the per-request HTTP timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithListenWait overrides how long TriggerScan listens for a rejection after
// emitting (used by tests to keep the suite fast).
func WithListenWait(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.listenWait = d
		}
	}
}

// New builds a Connect client. baseURL is the server root (e.g.
// http://romm:8080); user/pass are a RomM account holding tasks.run.
func New(baseURL, user, pass string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		user:       user,
		pass:       pass,
		timeout:    defaultTimeout,
		listenWait: defaultListenWait,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// heartbeatSourceKeys maps /api/heartbeat METADATA_SOURCES flags to the wire
// values RomM's scan event expects in its "apis" list (the MetadataSource
// enum). GAMELIST is intentionally absent: it has no heartbeat flag.
var heartbeatSourceKeys = []struct{ key, wire string }{
	{"IGDB_API_ENABLED", "igdb"},
	{"MOBY_API_ENABLED", "moby"},
	{"SS_API_ENABLED", "ss"},
	{"RA_API_ENABLED", "ra"},
	{"LAUNCHBOX_API_ENABLED", "launchbox"},
	{"HASHEOUS_API_ENABLED", "hasheous"},
	{"TGDB_API_ENABLED", "tgdb"},
	{"STEAMGRIDDB_API_ENABLED", "sgdb"},
	{"FLASHPOINT_API_ENABLED", "flashpoint"},
	{"HLTB_API_ENABLED", "hltb"},
	{"LIBRETRO_API_ENABLED", "libretro"},
	{"PLAYMATCH_API_ENABLED", "playmatch"},
}

// EnabledSources reads RomM's unauthenticated heartbeat and returns the wire
// names of every enabled metadata source. The scan event applies NO default
// when "apis" is empty — a scan run that way registers files as blank,
// never-rematched tiles — so callers must feed this list into TriggerScan
// verbatim and treat an empty result as "do not scan".
func (c *Client) EnabledSources(ctx context.Context) ([]string, error) {
	hc := &http.Client{Timeout: c.timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/heartbeat", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &HTTPError{Status: resp.StatusCode, Path: "/api/heartbeat"}
	}
	var hb struct {
		MetadataSources map[string]bool `json:"METADATA_SOURCES"`
	}
	body, err := readBounded(resp)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(body), &hb); err != nil {
		return nil, fmt.Errorf("decode heartbeat: %w", err)
	}
	var sources []string
	for _, s := range heartbeatSourceKeys {
		if hb.MetadataSources[s.key] {
			sources = append(sources, s.wire)
		}
	}
	return sources, nil
}

// scanOptions is the payload of RomM's "scan" socket event. Slices must
// marshal as [] rather than null, so they are always initialized.
type scanOptions struct {
	Platforms       []int    `json:"platforms"`
	PlatformFSSlugs []string `json:"platform_fs_slugs"`
	RomsIDs         []int    `json:"roms_ids"`
	Type            string   `json:"type"`
	APIs            []string `json:"apis"`
}

// TriggerScan asks RomM for a quick scan of the given platform folders
// (fs_slugs — RomM resolves them itself, so unknown slugs are a safe no-op
// and no path translation between containers is ever needed). apis is the
// enabled metadata-source list, normally from EnabledSources; an empty list
// is refused rather than sent (see EnabledSources). Returns ErrScanInProgress
// when RomM rejects the trigger because a scan is already running.
func (c *Client) TriggerScan(ctx context.Context, fsSlugs, apis []string) error {
	if len(fsSlugs) == 0 {
		return nil
	}
	if len(apis) == 0 {
		return errors.New("romm: refusing to trigger a scan with no metadata sources (blank-tile ingest)")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("cookie jar: %w", err)
	}
	s := &session{
		base:    c.baseURL,
		http:    &http.Client{Timeout: c.timeout, Jar: jar},
		timeout: c.timeout,
	}

	if err := s.login(ctx, c.user, c.pass); err != nil {
		return err
	}
	if err := s.handshake(ctx); err != nil {
		return err
	}

	opts := scanOptions{
		Platforms:       []int{},
		PlatformFSSlugs: fsSlugs,
		RomsIDs:         []int{},
		Type:            "quick",
		APIs:            apis,
	}
	if err := s.emit(ctx, "scan", opts); err != nil {
		return err
	}
	err = s.listenForRejection(ctx, c.listenWait)
	s.close(ctx)
	return err
}

// session is one logged-in engine.io long-polling connection.
type session struct {
	base    string
	http    *http.Client
	timeout time.Duration
	sid     string
}

// login mints a RomM session; the cookie lands in the jar and rides along on
// every subsequent socket request (the handshake's only auth channel).
func (s *session) login(ctx context.Context, user, pass string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api/login", nil)
	if err != nil {
		return fmt.Errorf("build login: %w", err)
	}
	req.SetBasicAuth(user, pass)
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPError{Status: resp.StatusCode, Path: "/api/login"}
	}
	return nil
}

// socketURL builds the polling-transport URL, with the engine.io session id
// once one exists.
func (s *session) socketURL() string {
	q := url.Values{}
	q.Set("EIO", "4")
	q.Set("transport", "polling")
	if s.sid != "" {
		q.Set("sid", s.sid)
	}
	return s.base + socketPath + "?" + q.Encode()
}

// handshake opens the engine.io session and joins the root socket.io
// namespace. Packet framing: an engine.io poll body is packets joined by
// 0x1e; "0{json}" opens, "40" joins a namespace, "44{json}" refuses it.
func (s *session) handshake(ctx context.Context) error {
	body, err := s.poll(ctx, s.timeout)
	if err != nil {
		return fmt.Errorf("engine.io open: %w", err)
	}
	var open struct {
		SID string `json:"sid"`
	}
	found := false
	for _, p := range splitPackets(body) {
		if strings.HasPrefix(p, "0") {
			if err := json.Unmarshal([]byte(p[1:]), &open); err != nil {
				return fmt.Errorf("decode open packet: %w", err)
			}
			found = true
			break
		}
	}
	if !found || open.SID == "" {
		return fmt.Errorf("engine.io open: no session in %q", truncate(body))
	}
	s.sid = open.SID

	if err := s.post(ctx, "40"); err != nil {
		return fmt.Errorf("namespace connect: %w", err)
	}
	body, err = s.poll(ctx, s.timeout)
	if err != nil {
		return fmt.Errorf("namespace ack: %w", err)
	}
	for _, p := range splitPackets(body) {
		switch {
		case strings.HasPrefix(p, "40"):
			return nil
		case strings.HasPrefix(p, "44"):
			return fmt.Errorf("romm refused socket connection: %s", truncate(p[2:]))
		}
	}
	return fmt.Errorf("namespace ack: unexpected %q", truncate(body))
}

// emit sends one socket.io event ("42" + JSON array of name and argument).
func (s *session) emit(ctx context.Context, event string, arg any) error {
	payload, err := json.Marshal([]any{event, arg})
	if err != nil {
		return fmt.Errorf("marshal %s: %w", event, err)
	}
	if err := s.post(ctx, "42"+string(payload)); err != nil {
		return fmt.Errorf("emit %s: %w", event, err)
	}
	return nil
}

// listenForRejection polls for up to wait, looking for scan:done_ko. RomM
// emits it immediately when it drops the request (scan already in progress,
// or an authorization rejection), so silence within the window means the scan
// was accepted. Unrelated broadcast events (log streams, progress from other
// scans) are skipped.
func (s *session) listenForRejection(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		body, err := s.poll(ctx, remaining)
		if err != nil {
			// A poll cut off by the deadline is the success case: nothing
			// arrived in time. Report anything else.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		for _, p := range splitPackets(body) {
			if !strings.HasPrefix(p, "42") {
				continue
			}
			var event []json.RawMessage
			if err := json.Unmarshal([]byte(p[2:]), &event); err != nil || len(event) == 0 {
				continue
			}
			var name string
			if err := json.Unmarshal(event[0], &name); err != nil {
				continue
			}
			if name != "scan:done_ko" {
				continue
			}
			msg := ""
			if len(event) > 1 {
				_ = json.Unmarshal(event[1], &msg)
			}
			if strings.Contains(strings.ToLower(msg), "in progress") {
				return ErrScanInProgress
			}
			return fmt.Errorf("romm rejected scan: %s", msg)
		}
	}
}

// close leaves the namespace; the engine.io session and the RomM login both
// expire server-side. Best effort — the scan is already queued.
func (s *session) close(ctx context.Context) {
	_ = s.post(ctx, "41")
}

// poll performs one long-poll GET bounded by wait.
func (s *session) poll(ctx context.Context, wait time.Duration) (string, error) {
	pctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, s.socketURL(), nil)
	if err != nil {
		return "", fmt.Errorf("build poll: %w", err)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &HTTPError{Status: resp.StatusCode, Path: socketPath}
	}
	b, err := readBounded(resp)
	if err != nil {
		return "", err
	}
	return b, nil
}

// post sends one engine.io packet.
func (s *session) post(ctx context.Context, packet string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.socketURL(), strings.NewReader(packet))
	if err != nil {
		return fmt.Errorf("build post: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPError{Status: resp.StatusCode, Path: socketPath}
	}
	_, err = readBounded(resp)
	return err
}

// readBounded drains a response body under the defensive size cap.
func readBounded(resp *http.Response) (string, error) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if len(b) > maxResponseSize {
		return "", fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	return string(b), nil
}

// splitPackets splits an engine.io polling body on the 0x1e record separator.
func splitPackets(body string) []string {
	return strings.Split(body, "\x1e")
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
