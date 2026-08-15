package rommconnect

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubRomm speaks just enough of RomM's REST + socket.io-polling surface for
// the client: /api/login sets a session cookie, /api/heartbeat reports
// sources, and /ws/socket.io/ walks the engine.io handshake. Every socket
// request asserts the session cookie minted at login, mirroring RomM's
// cookie-only handshake auth.
type stubRomm struct {
	t *testing.T

	mu         sync.Mutex
	loggedIn   bool
	nsJoined   bool
	emitted    []string // raw "42..." payloads received
	closed     bool
	pollQueue  []string // bodies served to successive polls after ns join
	loginCode  int
	refuseNS   string // if set, namespace connect is answered with 44+this
	heartbeat  string
	sawCookies []string
}

func newStub(t *testing.T) *stubRomm {
	return &stubRomm{
		t:         t,
		loginCode: http.StatusOK,
		heartbeat: `{"METADATA_SOURCES":{"ANY_SOURCE_ENABLED":true,"IGDB_API_ENABLED":true,"SS_API_ENABLED":true,"MOBY_API_ENABLED":false,"STEAMGRIDDB_API_ENABLED":true,"RA_API_ENABLED":true,"LIBRETRO_API_ENABLED":true,"HLTB_API_ENABLED":false}}`,
	}
}

func (s *stubRomm) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if _, _, ok := r.BasicAuth(); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if s.loginCode != http.StatusOK {
			w.WriteHeader(s.loginCode)
			return
		}
		s.mu.Lock()
		s.loggedIn = true
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "romm_session", Value: "stub-session", Path: "/"})
		fmt.Fprint(w, "null")
	})
	mux.HandleFunc("/api/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, s.heartbeat)
	})
	mux.HandleFunc("/ws/socket.io/", s.socket)
	return mux
}

func (s *stubRomm) socket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, err := r.Cookie("romm_session"); err != nil || c.Value != "stub-session" {
		s.sawCookies = append(s.sawCookies, "MISSING")
	} else {
		s.sawCookies = append(s.sawCookies, c.Value)
	}

	sid := r.URL.Query().Get("sid")
	switch r.Method {
	case http.MethodGet:
		if sid == "" {
			fmt.Fprint(w, `0{"sid":"EIO-SID","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`)
			return
		}
		if !s.nsJoined {
			if s.refuseNS != "" {
				fmt.Fprintf(w, "44%s", s.refuseNS)
				return
			}
			s.nsJoined = true
			fmt.Fprint(w, `40{"sid":"NS-SID"}`)
			return
		}
		if len(s.pollQueue) > 0 {
			body := s.pollQueue[0]
			s.pollQueue = s.pollQueue[1:]
			fmt.Fprint(w, body)
			return
		}
		// Nothing queued: an engine.io ping is what a quiet server sends.
		fmt.Fprint(w, "2")
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		pkt := string(body)
		switch {
		case pkt == "40":
			fmt.Fprint(w, "ok")
		case pkt == "41":
			s.closed = true
			fmt.Fprint(w, "ok")
		case strings.HasPrefix(pkt, "42"):
			s.emitted = append(s.emitted, pkt)
			fmt.Fprint(w, "ok")
		default:
			fmt.Fprint(w, "ok")
		}
	}
}

func newTestClient(srv *httptest.Server) *Client {
	return New(srv.URL, "romarr", "secret", WithListenWait(150*time.Millisecond), WithTimeout(2*time.Second))
}

func TestTriggerScanHappyPath(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.TriggerScan(t.Context(), []string{"psx", "gb"}, []string{"igdb", "ss"}); err != nil {
		t.Fatalf("TriggerScan: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if !stub.loggedIn {
		t.Fatal("client never logged in")
	}
	if len(stub.emitted) != 1 {
		t.Fatalf("emitted %d events, want 1", len(stub.emitted))
	}
	var event []json.RawMessage
	if err := json.Unmarshal([]byte(stub.emitted[0][2:]), &event); err != nil {
		t.Fatalf("emitted packet not a socket.io event: %v", err)
	}
	var name string
	if err := json.Unmarshal(event[0], &name); err != nil || name != "scan" {
		t.Fatalf("event name = %q, want scan", name)
	}
	var opts struct {
		Platforms       []int    `json:"platforms"`
		PlatformFSSlugs []string `json:"platform_fs_slugs"`
		RomsIDs         []int    `json:"roms_ids"`
		Type            string   `json:"type"`
		APIs            []string `json:"apis"`
	}
	if err := json.Unmarshal(event[1], &opts); err != nil {
		t.Fatalf("decode scan options: %v", err)
	}
	if opts.Type != "quick" {
		t.Errorf("type = %q, want quick", opts.Type)
	}
	if len(opts.PlatformFSSlugs) != 2 || opts.PlatformFSSlugs[0] != "psx" {
		t.Errorf("platform_fs_slugs = %v", opts.PlatformFSSlugs)
	}
	if len(opts.APIs) != 2 {
		t.Errorf("apis = %v", opts.APIs)
	}
	if opts.Platforms == nil || opts.RomsIDs == nil {
		t.Error("platforms/roms_ids must marshal as [], not null")
	}
	for _, c := range stub.sawCookies {
		if c == "MISSING" {
			t.Error("a socket request arrived without the session cookie")
		}
	}
	if !stub.closed {
		t.Error("client never sent the namespace disconnect")
	}
}

func TestTriggerScanRejectedInProgress(t *testing.T) {
	stub := newStub(t)
	stub.pollQueue = []string{`42["scan:done_ko","A scan is already in progress"]`}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	err := c.TriggerScan(t.Context(), []string{"psx"}, []string{"igdb"})
	if !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("err = %v, want ErrScanInProgress", err)
	}
}

func TestTriggerScanRejectedOther(t *testing.T) {
	stub := newStub(t)
	stub.pollQueue = []string{`42["scan:done_ko","nope"]`}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	err := c.TriggerScan(t.Context(), []string{"psx"}, []string{"igdb"})
	if err == nil || errors.Is(err, ErrScanInProgress) {
		t.Fatalf("err = %v, want a plain rejection error", err)
	}
}

func TestTriggerScanSkipsUnrelatedBroadcasts(t *testing.T) {
	stub := newStub(t)
	// A log-stream broadcast (seen from admin sessions in the wild) followed
	// by silence must read as accepted.
	stub.pollQueue = []string{`42["logs:entry",{"level":"INFO","message":"Scanning"}]`}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.TriggerScan(t.Context(), []string{"psx"}, []string{"igdb"}); err != nil {
		t.Fatalf("TriggerScan: %v", err)
	}
}

func TestTriggerScanRefusedNamespace(t *testing.T) {
	stub := newStub(t)
	stub.refuseNS = `{"message":"connection refused"}`
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	err := c.TriggerScan(t.Context(), []string{"psx"}, []string{"igdb"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v, want namespace refusal", err)
	}
}

func TestTriggerScanBadLogin(t *testing.T) {
	stub := newStub(t)
	stub.loginCode = http.StatusUnauthorized
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	err := c.TriggerScan(t.Context(), []string{"psx"}, []string{"igdb"})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want HTTPError 401", err)
	}
}

func TestTriggerScanGuards(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.TriggerScan(t.Context(), nil, []string{"igdb"}); err != nil {
		t.Fatalf("no slugs must be a silent no-op, got %v", err)
	}
	err := c.TriggerScan(t.Context(), []string{"psx"}, nil)
	if err == nil || !strings.Contains(err.Error(), "metadata sources") {
		t.Fatalf("empty apis must be refused, got %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.loggedIn || len(stub.emitted) > 0 {
		t.Error("guarded calls must not touch the server")
	}
}

func TestEnabledSources(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := newTestClient(srv)
	sources, err := c.EnabledSources(t.Context())
	if err != nil {
		t.Fatalf("EnabledSources: %v", err)
	}
	want := []string{"igdb", "ss", "ra", "sgdb", "libretro"}
	if len(sources) != len(want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	got := map[string]bool{}
	for _, s := range sources {
		got[s] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing source %q in %v", w, sources)
		}
	}
	if got["moby"] || got["hltb"] {
		t.Errorf("disabled sources leaked into %v", sources)
	}
}
