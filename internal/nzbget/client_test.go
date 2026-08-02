package nzbget

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rpcTestRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

func rpcServer(t *testing.T, handler func(http.ResponseWriter, *http.Request, rpcTestRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		handler(w, r, req)
	}))
}

func writeRPCResult(t *testing.T, w http.ResponseWriter, result interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  result,
	}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestAddNZBByURL(t *testing.T) {
	var gotAuth, gotMethod string
	var gotParams []interface{}
	srv := rpcServer(t, func(w http.ResponseWriter, r *http.Request, req rpcTestRequest) {
		user, pass, ok := r.BasicAuth()
		if ok {
			gotAuth = user + ":" + pass
		}
		gotMethod = req.Method
		gotParams = req.Params
		writeRPCResult(t, w, 42)
	})
	defer srv.Close()

	id, err := New(srv.URL, "nzbget", "secret").AddNZBByURL(
		"https://indexer.example/download/1", `A/B: Game`, "games",
	)
	if err != nil {
		t.Fatalf("AddNZBByURL: %v", err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
	if gotAuth != "nzbget:secret" {
		t.Errorf("basic auth = %q", gotAuth)
	}
	if gotMethod != "append" || len(gotParams) != 11 {
		t.Fatalf("method=%q params=%v", gotMethod, gotParams)
	}
	if gotParams[0] != "A_B_ Game.nzb" || gotParams[1] != "https://indexer.example/download/1" || gotParams[2] != "games" {
		t.Errorf("append params = %v", gotParams)
	}
}

func TestAddNZBByURLLegacyFallback(t *testing.T) {
	calls := 0
	srv := rpcServer(t, func(w http.ResponseWriter, _ *http.Request, req rpcTestRequest) {
		calls++
		if len(req.Params) == 11 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"error":   map[string]interface{}{"code": -32602, "message": "Invalid parameter count"},
			})
			return
		}
		if len(req.Params) != 10 {
			t.Errorf("legacy params len=%d, want 10", len(req.Params))
		}
		writeRPCResult(t, w, 7)
	})
	defer srv.Close()

	id, err := New(srv.URL, "", "").AddNZBByURL("https://example/x.nzb", "Game", "games")
	if err != nil || id != 7 || calls != 2 {
		t.Fatalf("id=%d calls=%d err=%v", id, calls, err)
	}
}

func TestAddNZBByURLNoRetryOnUnrelatedError(t *testing.T) {
	calls := 0
	srv := rpcServer(t, func(w http.ResponseWriter, _ *http.Request, _ rpcTestRequest) {
		calls++
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]interface{}{"code": -1, "message": "Download parameter storage is full"},
		})
	})
	defer srv.Close()

	_, err := New(srv.URL, "", "").AddNZBByURL("https://example/x.nzb", "Game", "games")
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("append attempts = %d, want 1 (no legacy retry on unrelated errors)", calls)
	}
}

func TestQueueHistoryAndVersion(t *testing.T) {
	srv := rpcServer(t, func(w http.ResponseWriter, _ *http.Request, req rpcTestRequest) {
		switch req.Method {
		case "listgroups":
			writeRPCResult(t, w, []map[string]interface{}{{
				"NZBID": 9, "NZBName": "Game", "Status": "DOWNLOADING",
				"FileSizeMB": 100, "RemainingSizeMB": 25,
			}})
		case "history":
			writeRPCResult(t, w, []map[string]interface{}{{
				"NZBID": 9, "Name": "Game", "Status": "SUCCESS/UNPACK",
				"DestDir": "/downloads/Game", "FinalDir": "/final/Game",
			}})
		case "version":
			writeRPCResult(t, w, "26.2")
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	})
	defer srv.Close()

	c := New(srv.URL, "", "")
	queue, err := c.GetQueue()
	if err != nil || len(queue) != 1 || queue[0].NZBID != 9 || queue[0].RemainingSizeMB != 25 {
		t.Fatalf("queue=%v err=%v", queue, err)
	}
	history, err := c.GetHistory()
	if err != nil || len(history) != 1 || history[0].StoragePath() != "/final/Game" {
		t.Fatalf("history=%v err=%v", history, err)
	}
	version, err := c.TestConnection()
	if err != nil || version != "26.2" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestRPCFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{"unauthorized", http.StatusUnauthorized, ``, "HTTP 401"},
		{"rpc error", http.StatusOK, `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"boom"}}`, "RPC -1: boom"},
		{"invalid json", http.StatusOK, `{`, "decode response"},
		{"missing result", http.StatusOK, `{"jsonrpc":"2.0","id":1}`, "missing result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := New(srv.URL, "", "").TestConnection()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v, want %q", err, tt.want)
			}
		})
	}
}
