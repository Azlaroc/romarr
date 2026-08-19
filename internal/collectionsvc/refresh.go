package collectionsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gamarr/internal/collection"
	"gamarr/internal/db"
)

// Fetching clone lists. Small artifacts — the largest (Game Boy) is 278 groups
// — so this is a plain sequential walk rather than datsvc's phase machine; the
// stored rows ARE the status, and this only has to say whether a run is in
// flight and what it last did.

const (
	fetchTimeout = 60 * time.Second
	// maxListBytes caps one list. The biggest today is well under 1MB; the cap
	// exists so a redirect into something else fails fast rather than filling
	// memory.
	maxListBytes = 8 << 20
	userAgent    = "RomArr/clonelist"
)

// politenessDelay spaces sequential fetches. A var so tests need not sleep.
var politenessDelay = 250 * time.Millisecond

// ErrBusy is returned when a refresh is already running.
var ErrBusy = errors.New("a clone-list refresh is already running")

// Per-platform outcomes, mirroring datsvc's vocabulary so two catalog-shaped
// planes do not report the same facts in two different words.
const (
	StatusImported  = "imported"
	StatusUnchanged = "unchanged"
	StatusSkipped   = "skipped"
	StatusError     = "error"
)

// ListResult is what one platform's refresh did.
type ListResult struct {
	Platform string `json:"platform"`
	List     string `json:"list,omitempty"`
	Status   string `json:"status"`
	Groups   int    `json:"groups,omitempty"`
	Titles   int    `json:"titles,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RefreshState is the service's run status.
type RefreshState struct {
	Running    bool         `json:"running"`
	Total      int          `json:"total"`
	Done       int          `json:"done"`
	Results    []ListResult `json:"results,omitempty"`
	LastError  string       `json:"last_error,omitempty"`
	StartedAt  string       `json:"started_at,omitempty"`
	FinishedAt string       `json:"finished_at,omitempty"`
}

// runner holds the fetch-side state. Kept beside the read-only Service rather
// than inside it so building a set never depends on a fetch client.
type runner struct {
	http    *http.Client
	running atomic.Bool

	mu    sync.Mutex
	state RefreshState
}

func (s *Service) run() *runner {
	s.once.Do(func() {
		s.runner = &runner{http: &http.Client{Timeout: fetchTimeout}}
	})
	return s.runner
}

// SetHTTPClient points the fetcher at a stub. Tests only.
func (s *Service) SetHTTPClient(c *http.Client) {
	if c != nil {
		s.run().http = c
	}
}

// RefreshStatus reports the current or last run.
func (s *Service) RefreshStatus() RefreshState {
	r := s.run()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.state
	st.Running = r.running.Load()
	return st
}

// RefreshCloneLists starts an asynchronous refresh of every platform carrying
// a clone-list locator. false means one was already running.
func (s *Service) RefreshCloneLists() bool {
	r := s.run()
	if !r.running.CompareAndSwap(false, true) {
		return false
	}
	plats := s.store.ListCloneListPlatforms()
	r.mu.Lock()
	r.state = RefreshState{Running: true, Total: len(plats), StartedAt: nowStamp()}
	r.mu.Unlock()

	go func() {
		defer r.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.runRefresh(ctx, plats)
	}()
	return true
}

// RefreshCloneListsSync runs the walk inline. The API never calls it; the
// tests and any future cadence loop do.
func (s *Service) RefreshCloneListsSync(ctx context.Context) []ListResult {
	r := s.run()
	if !r.running.CompareAndSwap(false, true) {
		return nil
	}
	defer r.running.Store(false)
	plats := s.store.ListCloneListPlatforms()
	r.mu.Lock()
	r.state = RefreshState{Running: true, Total: len(plats), StartedAt: nowStamp()}
	r.mu.Unlock()
	s.runRefresh(ctx, plats)
	return s.RefreshStatus().Results
}

func (s *Service) runRefresh(ctx context.Context, plats []db.CloneListPlatform) {
	r := s.run()
	base := s.cfg.CloneListFetchBase()
	delay := politeDelay(base)

	var imported, failed int
	for i, p := range plats {
		if ctx.Err() != nil {
			break
		}
		if i > 0 && delay > 0 && !wait(ctx, delay) {
			break
		}
		res := s.refreshOne(ctx, base, p)
		switch res.Status {
		case StatusError:
			failed++
		case StatusImported:
			imported++
		}
		r.mu.Lock()
		r.state.Done++
		r.state.Results = append(r.state.Results, res)
		if res.Status == StatusError {
			r.state.LastError = res.Platform + ": " + res.Error
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.state.FinishedAt = nowStamp()
	r.mu.Unlock()
	if s.store != nil {
		s.store.LogActivity("clonelists_refreshed", "Clone lists refreshed",
			fmt.Sprintf("%d imported, %d unchanged, %d failed", imported, len(plats)-imported-failed, failed), "", nil)
	}
	slog.Info("clone list refresh finished", "platforms", len(plats), "imported", imported, "failed", failed)
}

func (s *Service) refreshOne(ctx context.Context, base string, p db.CloneListPlatform) ListResult {
	res := ListResult{Platform: p.PlatformSlug, List: p.Name}
	body, err := s.fetch(ctx, base, p.Name)
	if err != nil {
		res.Status, res.Error = StatusError, err.Error()
		return res
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	// Same bytes as the stored list: nothing to do. Unlike the DAT plane there
	// is no raw copy to re-materialize, so the digest alone decides.
	if have, ok := s.store.GetCloneList(p.PlatformSlug); ok && have.SourceSHA256 == digest {
		res.Status, res.Groups, res.Titles = StatusUnchanged, have.GroupCount, have.TitleCount
		return res
	}

	list, err := collection.ParseCloneList(body)
	if err != nil {
		res.Status, res.Error = StatusError, err.Error()
		return res
	}
	meta := db.CloneListRow{
		PlatformSlug: p.PlatformSlug,
		ListName:     p.Name,
		SourceSHA256: digest,
		LastUpdated:  list.LastUpdated,
	}
	var rows []db.CloneGroupRow
	for _, g := range list.Groups {
		cats := strings.Join(g.Categories, ",")
		for _, t := range g.Titles {
			rows = append(rows, db.CloneGroupRow{
				PlatformSlug: p.PlatformSlug, GroupName: g.Name,
				Categories: cats, SearchTerm: t.SearchTerm, Priority: t.Priority,
			})
		}
	}
	if err := s.store.ReplaceCloneList(meta, rows); err != nil {
		res.Status, res.Error = StatusError, err.Error()
		return res
	}
	stored, _ := s.store.GetCloneList(p.PlatformSlug)
	res.Status, res.Groups, res.Titles = StatusImported, stored.GroupCount, stored.TitleCount
	return res
}

func (s *Service) fetch(ctx context.Context, base, name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("platform has no clone list assigned")
	}
	target := strings.TrimRight(base, "/") + "/" + url.PathEscape(name+".json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.run().http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 404 is the common one and it means the locator is wrong — the five
		// names that disagree with dat_code are exactly this failure waiting
		// to happen, which is why they are seeded data with a test.
		return nil, fmt.Errorf("fetch %s: HTTP %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxListBytes {
		return nil, fmt.Errorf("clone list %s exceeds %d bytes", name, maxListBytes)
	}
	return body, nil
}

func politeDelay(base string) time.Duration {
	if u, err := url.Parse(base); err == nil {
		host := u.Hostname()
		if host == "localhost" || net.ParseIP(host).IsLoopback() {
			return 0
		}
	}
	return politenessDelay
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func nowStamp() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }
