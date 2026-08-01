// Package nzbget is a minimal NZBGet JSON-RPC client used for Usenet (NZB)
// downloads.
package nzbget

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseSize = 8 << 20

// Client is an NZBGet JSON-RPC client.
type Client struct {
	baseURL string
	user    string
	pass    string
	client  *http.Client
}

// QueueItem represents an item returned by listgroups.
type QueueItem struct {
	NZBID           int64  `json:"NZBID"`
	NZBName         string `json:"NZBName"`
	Status          string `json:"Status"`
	DestDir         string `json:"DestDir"`
	FinalDir        string `json:"FinalDir"`
	FileSizeMB      int64  `json:"FileSizeMB"`
	RemainingSizeMB int64  `json:"RemainingSizeMB"`
}

// HistoryItem represents an item returned by history.
type HistoryItem struct {
	NZBID    int64  `json:"NZBID"`
	Name     string `json:"Name"`
	Status   string `json:"Status"`
	DestDir  string `json:"DestDir"`
	FinalDir string `json:"FinalDir"`
}

// StoragePath returns the final directory selected by NZBGet, falling back to
// the regular destination directory when no post-processing script changed it.
func (h HistoryItem) StoragePath() string {
	if h.FinalDir != "" {
		return h.FinalDir
	}
	return h.DestDir
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("RPC %d: %s", e.Code, e.Message)
}

// New creates an NZBGet client. Username and password may be empty when the
// server has authentication disabled.
func New(baseURL, user, pass string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		pass:    pass,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// AddNZBByURL adds an NZB URL to the download queue and returns its NZBID.
func (c *Client) AddNZBByURL(nzbURL, title, category string) (int64, error) {
	filename := strings.TrimSpace(title)
	filename = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\\<>:"|?*`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, filename)
	if filename != "" && !strings.HasSuffix(strings.ToLower(filename), ".nzb") {
		filename += ".nzb"
	}

	// Current NZBGet versions accept AutoCategory before PPParameters. If an
	// older server rejects that signature, retry once with the legacy list.
	params := []interface{}{
		filename, nzbURL, category, 0, false, false, "", 0, "SCORE", false,
		[]map[string]string{},
	}
	var id int64
	err := c.call("append", params, &id)
	if rpcErr, ok := err.(*rpcError); ok && isInvalidParams(rpcErr) {
		legacyParams := append([]interface{}{}, params[:9]...)
		legacyParams = append(legacyParams, params[10])
		err = c.call("append", legacyParams, &id)
	}
	if err != nil {
		return 0, fmt.Errorf("NZBGet append: %w", err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("NZBGet append returned invalid NZBID %d", id)
	}
	return id, nil
}

// GetQueue returns the current NZBGet download queue.
func (c *Client) GetQueue() ([]QueueItem, error) {
	var items []QueueItem
	if err := c.call("listgroups", []interface{}{0}, &items); err != nil {
		return nil, fmt.Errorf("NZBGet listgroups: %w", err)
	}
	return items, nil
}

// GetHistory returns visible NZBGet history items.
func (c *Client) GetHistory() ([]HistoryItem, error) {
	var items []HistoryItem
	if err := c.call("history", []interface{}{false}, &items); err != nil {
		return nil, fmt.Errorf("NZBGet history: %w", err)
	}
	return items, nil
}

// TestConnection verifies NZBGet is reachable and returns its version string.
func (c *Client) TestConnection() (string, error) {
	var version string
	if err := c.call("version", []interface{}{}, &version); err != nil {
		return "", fmt.Errorf("NZBGet version: %w", err)
	}
	if version == "" {
		return "", fmt.Errorf("NZBGet returned an empty version")
	}
	return version, nil
}

func (c *Client) call(method string, params []interface{}, result interface{}) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/jsonrpc", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > maxResponseSize {
		return fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(responseBody, &rpcResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if len(rpcResp.Result) == 0 || string(rpcResp.Result) == "null" {
		return fmt.Errorf("response missing result")
	}
	if err := json.Unmarshal(rpcResp.Result, result); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}

func isInvalidParams(err *rpcError) bool {
	message := strings.ToLower(err.Message)
	return err.Code == -32602 || strings.Contains(message, "parameter") || strings.Contains(message, "argument")
}
