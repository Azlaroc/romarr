package qbit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNew(t *testing.T) {
	c := New("http://localhost:8080", "admin", "pass")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL=%q", c.baseURL)
	}
	if c.user != "admin" {
		t.Errorf("user=%q", c.user)
	}
	if c.authenticated {
		t.Error("should not be authenticated initially")
	}
}

func TestNew_TrailingSlash(t *testing.T) {
	c := New("http://localhost:8080/", "admin", "pass")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected trailing slash stripped, got %q", c.baseURL)
	}
}

func TestLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	if !c.Login() {
		t.Error("expected login to succeed")
	}
	if !c.authenticated {
		t.Error("expected authenticated=true after login")
	}
}

func TestLogin_Success_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	if !c.Login() {
		t.Error("expected login to succeed on 204 with empty body")
	}
}

func TestLogin_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Fails."))
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "wrongpass")
	if c.Login() {
		t.Error("expected login to fail")
	}
}

func TestAddTorrent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/add" {
			w.Write([]byte("Ok."))
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	ok := c.AddTorrent("magnet:?xt=urn:btih:abc", "Test", "/downloads", "games")
	if !ok {
		t.Error("expected AddTorrent to succeed")
	}
}

func TestAddTorrent_ReauthOn403(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/add" {
			callCount++
			if callCount == 1 {
				w.WriteHeader(403)
				return
			}
			w.Write([]byte("Ok."))
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	c.authenticated = true
	ok := c.AddTorrent("magnet:?xt=urn:btih:abc", "Test", "/downloads", "games")
	if !ok {
		t.Error("expected AddTorrent to succeed after reauth")
	}
}

func TestGetTorrents(t *testing.T) {
	torrents := []Torrent{
		{Name: "Game1", Hash: "abc", Progress: 0.5, State: "downloading"},
		{Name: "Game2", Hash: "def", Progress: 1.0, State: "stoppedUP"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			json.NewEncoder(w).Encode(torrents)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	result := c.GetTorrents("games")
	if len(result) != 2 {
		t.Fatalf("expected 2 torrents, got %d", len(result))
	}
	if result[0].Name != "Game1" {
		t.Errorf("name=%q", result[0].Name)
	}
}

func TestGetTorrents_ReauthOn403(t *testing.T) {
	callCount := 0
	torrents := []Torrent{{Name: "Game1", Hash: "abc"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			callCount++
			if callCount == 1 {
				w.WriteHeader(403)
				return
			}
			json.NewEncoder(w).Encode(torrents)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	c.authenticated = true
	result := c.GetTorrents("")
	if len(result) != 1 {
		t.Fatalf("expected 1 torrent after reauth, got %d", len(result))
	}
}

func TestGetTorrentFiles(t *testing.T) {
	files := []TorrentFile{
		{Name: "game/setup.exe"},
		{Name: "game/data.bin"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/files" {
			json.NewEncoder(w).Encode(files)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	result := c.GetTorrentFiles("abc123")
	if len(result) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result))
	}
	if result[0].Name != "game/setup.exe" {
		t.Errorf("name=%q", result[0].Name)
	}
}

func TestDeleteTorrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/delete" {
			w.WriteHeader(200)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	ok := c.DeleteTorrent("abc123", true)
	if !ok {
		t.Error("expected DeleteTorrent to succeed")
	}
}

func TestDeleteTorrent_ReauthOn403(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/delete" {
			callCount++
			if callCount == 1 {
				w.WriteHeader(403)
				return
			}
			w.WriteHeader(200)
			return
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pass")
	c.authenticated = true
	ok := c.DeleteTorrent("abc123", false)
	if !ok {
		t.Error("expected DeleteTorrent to succeed after reauth")
	}
}

// ── qBittorrent >= 5.2 WebAPI responses (issue #11) ────────────────────────────
// qBittorrent 5.2.0 changed the WebAPI to send 204 for responses with no
// body: login success became 204/empty (was 200 "Ok."), bad credentials
// became 401 (was 200 "Fails."), torrents/add success became 200 with a JSON
// body (was 200 "Ok."), and rejected adds became 409. Response shapes below
// were captured verbatim from qBittorrent 5.2.3.

func qbit52Server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.FormValue("password") == "goodpass" {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Unauthorized"))
			}
		case "/api/v2/torrents/add":
			if r.FormValue("urls") == "" {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte("Conflict"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"added_torrent_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],"failure_count":0,"pending_count":0,"success_count":1}`))
		case "/api/v2/torrents/delete":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
}

func TestLogin_QBit52_Success(t *testing.T) {
	srv := qbit52Server(t)
	defer srv.Close()

	c := New(srv.URL, "admin", "goodpass")
	if !c.Login() {
		t.Error("expected login to succeed on 5.2-style 204 response")
	}
}

func TestLogin_QBit52_BadCredentials(t *testing.T) {
	srv := qbit52Server(t)
	defer srv.Close()

	c := New(srv.URL, "admin", "wrongpass")
	if c.Login() {
		t.Error("expected login to fail on 5.2-style 401 response")
	}
}

func TestAddTorrent_QBit52_JSONResponse(t *testing.T) {
	srv := qbit52Server(t)
	defer srv.Close()

	c := New(srv.URL, "admin", "goodpass")
	if !c.AddTorrent("magnet:?xt=urn:btih:abc", "Test", "/downloads", "games") {
		t.Error("expected AddTorrent to succeed on 5.2-style JSON response")
	}
}

func TestAddTorrent_QBit52_Rejected409(t *testing.T) {
	srv := qbit52Server(t)
	defer srv.Close()

	c := New(srv.URL, "admin", "goodpass")
	if c.AddTorrent("", "Test", "/downloads", "games") {
		t.Error("expected AddTorrent to fail on 5.2-style 409 response")
	}
}

func TestDeleteTorrent_QBit52_NoContent(t *testing.T) {
	srv := qbit52Server(t)
	defer srv.Close()

	c := New(srv.URL, "admin", "goodpass")
	if !c.DeleteTorrent("abc123", true) {
		t.Error("expected DeleteTorrent to succeed on a 204 response")
	}
}

func TestAddAccepted(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"legacy Ok", 200, "Ok.", true},
		{"legacy Fails", 200, "Fails.", false},
		{"52 json success", 200, `{"added_torrent_ids":["a"],"failure_count":0,"pending_count":0,"success_count":1}`, true},
		{"52 json pending", 200, `{"added_torrent_ids":[],"failure_count":0,"pending_count":1,"success_count":0}`, true},
		{"52 json all failed", 200, `{"added_torrent_ids":[],"failure_count":1,"pending_count":0,"success_count":0}`, false},
		{"52 conflict", 409, "Conflict", false},
		{"bare 204", 204, "", true},
		{"server error", 500, "", false},
	}
	for _, tc := range cases {
		if got := addAccepted(tc.status, []byte(tc.body)); got != tc.want {
			t.Errorf("%s: addAccepted(%d, %q)=%v, want %v", tc.name, tc.status, tc.body, got, tc.want)
		}
	}
}

func TestAddTorrentOpts_FormEncoding(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		r.ParseForm()
		got = r.PostForm
		w.Write([]byte("Ok."))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	ok := c.AddTorrentOpts("magnet:?xt=urn:btih:aaaa", AddOptions{
		SavePath: "/downloads", Category: "games", Tags: "gamarr-abc", Stopped: true,
	})
	if !ok {
		t.Fatal("AddTorrentOpts returned false")
	}
	want := map[string]string{
		"urls": "magnet:?xt=urn:btih:aaaa", "savepath": "/downloads",
		"category": "games", "tags": "gamarr-abc",
		// Both spellings so 5.x (stopped) and 4.x (paused) honor the flag.
		"stopped": "true", "paused": "true",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("form[%s] = %q, want %q", k, got.Get(k), v)
		}
	}
}

func TestAddTorrentOpts_OmitsOptionalKeys(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		r.ParseForm()
		got = r.PostForm
		w.Write([]byte("Ok."))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	c.AddTorrentOpts("magnet:x", AddOptions{SavePath: "/d", Category: "games"})
	if got.Has("tags") || got.Has("stopped") || got.Has("paused") {
		t.Errorf("optional keys sent when unset: %v", got)
	}
}

func TestGetTorrentsFiltered_QueryBuilding(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		gotQuery = r.URL.Query()
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	c.GetTorrentsFiltered("games", "gamarr-x", "aaa|bbb")
	if gotQuery.Get("category") != "games" || gotQuery.Get("tag") != "gamarr-x" || gotQuery.Get("hashes") != "aaa|bbb" {
		t.Errorf("query = %v", gotQuery)
	}
}

func TestTorrentDecode_SeedingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Write([]byte("Ok."))
			return
		}
		w.Write([]byte(`[{"name":"T","hash":"h1","progress":1.0,"state":"stoppedUP",` +
			`"amount_left":512,"category":"games","tags":"a, b","ratio":1.5}]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	ts := c.GetTorrents("")
	if len(ts) != 1 {
		t.Fatalf("torrents = %d, want 1", len(ts))
	}
	got := ts[0]
	if got.AmountLeft != 512 || got.Category != "games" || got.Tags != "a, b" || got.Ratio != 1.5 {
		t.Errorf("decoded = %+v", got)
	}
}

func TestSetFilePriority_FormEncoding(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/filePrio":
			r.ParseForm()
			got = r.PostForm
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	if !c.SetFilePriority("abc123", []int{0, 2, 5}, 0) {
		t.Fatal("SetFilePriority failed")
	}
	if got.Get("hash") != "abc123" {
		t.Errorf("hash = %q", got.Get("hash"))
	}
	if got.Get("id") != "0|2|5" {
		t.Errorf("id = %q, want 0|2|5", got.Get("id"))
	}
	if got.Get("priority") != "0" {
		t.Errorf("priority = %q, want 0", got.Get("priority"))
	}
}

func TestSetFilePriority_EmptyIndexesIsNoop(t *testing.T) {
	c := New("http://127.0.0.1:1", "u", "p") // unreachable: must not be called
	if !c.SetFilePriority("abc", nil, 0) {
		t.Error("empty index list should succeed without a request")
	}
}

func TestStopStartTorrents_404FallbackTo4x(t *testing.T) {
	// A 4.x server has no /stop//start — the client must fall back to
	// /pause//resume.
	var paused, resumed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/pause":
			paused = true
			w.WriteHeader(200)
		case "/api/v2/torrents/resume":
			resumed = true
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	if !c.StopTorrents("h1") || !paused {
		t.Error("StopTorrents did not fall back to /pause")
	}
	if !c.StartTorrents("h1") || !resumed {
		t.Error("StartTorrents did not fall back to /resume")
	}
}

func TestStopStartTorrents_5x(t *testing.T) {
	var stopped, started bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/torrents/stop":
			stopped = true
			w.WriteHeader(200)
		case "/api/v2/torrents/start":
			started = true
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	if !c.StopTorrents("h1") || !stopped {
		t.Error("StopTorrents did not hit /stop")
	}
	if !c.StartTorrents("h1") || !started {
		t.Error("StartTorrents did not hit /start")
	}
}

func TestGetTorrentFiles_FieldsAndIndexFallback(t *testing.T) {
	t.Run("modern server sends index", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				w.Write([]byte("Ok."))
			case "/api/v2/torrents/files":
				w.Write([]byte(`[{"index":3,"name":"a.bin","size":10,"priority":1,"progress":0.5},` +
					`{"index":7,"name":"b.bin","size":20,"priority":0,"progress":1.0}]`))
			}
		}))
		defer srv.Close()
		files := New(srv.URL, "u", "p").GetTorrentFiles("h")
		if len(files) != 2 || files[0].Index != 3 || files[1].Index != 7 {
			t.Fatalf("indexes not honored: %+v", files)
		}
		if files[1].Priority != 0 || files[1].Progress != 1.0 || files[0].Size != 10 {
			t.Errorf("fields not decoded: %+v", files)
		}
	})

	t.Run("pre-2.8.2 server omits index: slice fallback", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				w.Write([]byte("Ok."))
			case "/api/v2/torrents/files":
				w.Write([]byte(`[{"name":"a.bin"},{"name":"b.bin"},{"name":"c.bin"}]`))
			}
		}))
		defer srv.Close()
		files := New(srv.URL, "u", "p").GetTorrentFiles("h")
		if len(files) != 3 || files[0].Index != 0 || files[1].Index != 1 || files[2].Index != 2 {
			t.Fatalf("slice-position fallback not applied: %+v", files)
		}
	})
}
