package rommconnect

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ScanAPI is the client surface the notifier drives (satisfied by *Client).
type ScanAPI interface {
	EnabledSources(ctx context.Context) ([]string, error)
	TriggerScan(ctx context.Context, fsSlugs, apis []string) error
}

// NotifierOptions tune the coalescing notifier. Zero values take defaults.
type NotifierOptions struct {
	// FlushIdle is how long a platform must be quiet before its scan fires;
	// imports arriving in bursts (a disc set, a batch grab) collapse into one
	// scan. Default 2m.
	FlushIdle time.Duration
	// FlushMax caps how long a continuously-busy platform can defer its scan.
	// Default 10m.
	FlushMax time.Duration
	// Tick is the poll interval of the flush loop. Default 30s.
	Tick time.Duration
	// RetryBusy is the wait after RomM reports a scan already in progress
	// (RomM drops concurrent triggers rather than queuing them). Default 3m.
	RetryBusy time.Duration
	// RetryErr is the wait after a transport/login failure. Default 5m.
	RetryErr time.Duration
	// OnEvent, when set, receives state transitions for the activity log:
	// ("romm_connect_error", detail) on the first failure after a success and
	// ("romm_connect_recovered", detail) when triggers work again. Repeated
	// failures while already failing are only slog'd, so an outage cannot
	// flood the activity feed.
	OnEvent func(event, detail string)
}

type slugTimes struct {
	first time.Time // first un-notified import on this platform
	last  time.Time // most recent import on this platform
}

// Notifier batches "platform changed" signals from the import pipeline into
// targeted RomM scan triggers: the Connect plane's processor. Enqueue is
// called from import completion (cheap, lock-only); a background loop flushes
// platforms that have gone quiet, requeueing on rejection so a notification
// is never dropped. Pending state is in-memory by design — a restart loses at
// most one pending scan, which RomM's scheduled nightly rescan (the janitor)
// or the next import covers.
type Notifier struct {
	client ScanAPI
	opts   NotifierOptions

	mu        sync.Mutex
	pending   map[string]*slugTimes
	notBefore time.Time // retry gate after any rejection or failure
	failing   bool
	started   bool

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewNotifier builds a notifier around a Connect client.
func NewNotifier(client ScanAPI, opts NotifierOptions) *Notifier {
	if opts.FlushIdle <= 0 {
		opts.FlushIdle = 2 * time.Minute
	}
	if opts.FlushMax <= 0 {
		opts.FlushMax = 10 * time.Minute
	}
	if opts.Tick <= 0 {
		opts.Tick = 30 * time.Second
	}
	if opts.RetryBusy <= 0 {
		opts.RetryBusy = 3 * time.Minute
	}
	if opts.RetryErr <= 0 {
		opts.RetryErr = 5 * time.Minute
	}
	return &Notifier{
		client:  client,
		opts:    opts,
		pending: make(map[string]*slugTimes),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Enqueue records that a platform folder (RomM fs_slug) just received
// content. Safe from any goroutine; never blocks on the network.
func (n *Notifier) Enqueue(fsSlug string) {
	if fsSlug == "" {
		return
	}
	n.enqueueAt(fsSlug, time.Now())
}

func (n *Notifier) enqueueAt(fsSlug string, now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if st, ok := n.pending[fsSlug]; ok {
		st.last = now
		return
	}
	n.pending[fsSlug] = &slugTimes{first: now, last: now}
}

// Start launches the flush loop. Notifier instances are single-use: a
// runtime re-enable constructs a fresh Notifier rather than restarting this
// one (started guards the accidental double-Start, which would otherwise
// double-close done).
func (n *Notifier) Start() {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return
	}
	n.started = true
	n.mu.Unlock()
	go func() {
		defer close(n.done)
		t := time.NewTicker(n.opts.Tick)
		defer t.Stop()
		for {
			select {
			case <-n.stop:
				return
			case <-t.C:
				n.flushDue(time.Now())
			}
		}
	}()
}

// Stop halts the flush loop. Pending notifications are dropped (see the
// in-memory note on Notifier). Idempotent, and safe on a never-started
// notifier (it must not block forever waiting on a loop that doesn't exist).
func (n *Notifier) Stop() {
	n.stopOnce.Do(func() {
		close(n.stop)
		n.mu.Lock()
		started := n.started
		n.mu.Unlock()
		if started {
			<-n.done
		}
	})
}

// flushDue fires one scan for every platform whose burst has settled. It runs
// the network calls outside the lock and puts the batch back on any failure,
// preserving first-seen times so FlushMax still bounds total latency.
func (n *Notifier) flushDue(now time.Time) {
	n.mu.Lock()
	if now.Before(n.notBefore) {
		n.mu.Unlock()
		return
	}
	var due []string
	for slug, st := range n.pending {
		if now.Sub(st.last) >= n.opts.FlushIdle || now.Sub(st.first) >= n.opts.FlushMax {
			due = append(due, slug)
		}
	}
	batch := make(map[string]*slugTimes, len(due))
	for _, slug := range due {
		batch[slug] = n.pending[slug]
		delete(n.pending, slug)
	}
	n.mu.Unlock()

	if len(due) == 0 {
		return
	}
	sort.Strings(due)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	sources, err := n.client.EnabledSources(ctx)
	if err == nil && len(sources) == 0 {
		// Scanning without metadata sources would ingest blank tiles; treat a
		// fully-disabled RomM as an error state and keep the batch.
		err = errNoSources
	}
	if err == nil {
		err = n.client.TriggerScan(ctx, due, sources)
	}

	switch {
	case err == nil:
		slog.Info("romm connect: scan triggered", "platforms", due, "sources", len(sources))
		n.setFailing(false, "")
	case errors.Is(err, ErrScanInProgress):
		// Not a failure — RomM is busy scanning (possibly for us). Try again
		// once the gate lifts.
		slog.Info("romm connect: scan busy, will retry", "platforms", due, "in", n.opts.RetryBusy)
		n.requeue(batch, now.Add(n.opts.RetryBusy))
	default:
		slog.Warn("romm connect: trigger failed, will retry", "platforms", due, "in", n.opts.RetryErr, "error", err)
		n.requeue(batch, now.Add(n.opts.RetryErr))
		n.setFailing(true, err.Error())
	}
}

var errNoSources = &noSourcesError{}

type noSourcesError struct{}

func (*noSourcesError) Error() string {
	return "romm reports no enabled metadata sources; refusing blank-tile scan"
}

// requeue puts a flushed batch back, merging with anything imported since.
func (n *Notifier) requeue(batch map[string]*slugTimes, notBefore time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for slug, st := range batch {
		if cur, ok := n.pending[slug]; ok {
			if st.first.Before(cur.first) {
				cur.first = st.first
			}
			continue
		}
		n.pending[slug] = st
	}
	if notBefore.After(n.notBefore) {
		n.notBefore = notBefore
	}
}

// setFailing records the up/down state and reports transitions to OnEvent.
func (n *Notifier) setFailing(failing bool, detail string) {
	n.mu.Lock()
	changed := n.failing != failing
	n.failing = failing
	n.mu.Unlock()
	if !changed || n.opts.OnEvent == nil {
		return
	}
	if failing {
		n.opts.OnEvent("romm_connect_error", detail)
	} else {
		n.opts.OnEvent("romm_connect_recovered", "RomM scan triggers working again")
	}
}
