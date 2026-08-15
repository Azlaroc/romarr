package rommconnect

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeScanAPI records trigger calls and serves scripted responses.
type fakeScanAPI struct {
	mu       sync.Mutex
	sources  []string
	srcErr   error
	scanErrs []error // consumed per TriggerScan call; empty = nil
	calls    [][]string
	apis     [][]string
}

func (f *fakeScanAPI) EnabledSources(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sources, f.srcErr
}

func (f *fakeScanAPI) TriggerScan(_ context.Context, fsSlugs, apis []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), fsSlugs...))
	f.apis = append(f.apis, append([]string(nil), apis...))
	if len(f.scanErrs) == 0 {
		return nil
	}
	err := f.scanErrs[0]
	f.scanErrs = f.scanErrs[1:]
	return err
}

func (f *fakeScanAPI) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestNotifier(f *fakeScanAPI, onEvent func(string, string)) *Notifier {
	return NewNotifier(f, NotifierOptions{
		FlushIdle: 2 * time.Minute,
		FlushMax:  10 * time.Minute,
		RetryBusy: 3 * time.Minute,
		RetryErr:  5 * time.Minute,
		OnEvent:   onEvent,
	})
}

func TestNotifierCoalescesBurst(t *testing.T) {
	f := &fakeScanAPI{sources: []string{"igdb", "ss"}}
	n := newTestNotifier(f, nil)
	t0 := time.Now()

	// A disc set: three imports on psx over 3 minutes, one on gb.
	n.enqueueAt("psx", t0)
	n.enqueueAt("gb", t0)
	n.enqueueAt("psx", t0.Add(90*time.Second))
	n.enqueueAt("psx", t0.Add(3*time.Minute))

	// gb went idle at t0+2m; psx is still hot.
	n.flushDue(t0.Add(2 * time.Minute))
	if got := f.callCount(); got != 1 {
		t.Fatalf("calls after first flush = %d, want 1 (gb only)", got)
	}
	if len(f.calls[0]) != 1 || f.calls[0][0] != "gb" {
		t.Fatalf("first flush = %v, want [gb]", f.calls[0])
	}

	// psx settles: idle 2m after its last import.
	n.flushDue(t0.Add(5 * time.Minute))
	if got := f.callCount(); got != 2 {
		t.Fatalf("calls after second flush = %d, want 2", got)
	}
	if len(f.calls[1]) != 1 || f.calls[1][0] != "psx" {
		t.Fatalf("second flush = %v, want [psx]", f.calls[1])
	}
	if len(f.apis[1]) != 2 {
		t.Fatalf("apis = %v, want the enabled sources", f.apis[1])
	}

	// Nothing left.
	n.flushDue(t0.Add(10 * time.Minute))
	if got := f.callCount(); got != 2 {
		t.Fatalf("calls after empty flush = %d, want 2", got)
	}
}

func TestNotifierFlushMaxBoundsBusyPlatform(t *testing.T) {
	f := &fakeScanAPI{sources: []string{"igdb"}}
	n := newTestNotifier(f, nil)
	t0 := time.Now()

	// Imports every minute keep the platform hot; FlushMax must fire anyway.
	for i := 0; i <= 10; i++ {
		n.enqueueAt("psx", t0.Add(time.Duration(i)*time.Minute))
		n.flushDue(t0.Add(time.Duration(i) * time.Minute))
	}
	if got := f.callCount(); got != 1 {
		t.Fatalf("calls = %d, want exactly 1 (the FlushMax flush)", got)
	}
}

func TestNotifierBusyRequeues(t *testing.T) {
	f := &fakeScanAPI{sources: []string{"igdb"}, scanErrs: []error{ErrScanInProgress}}
	events := recordEvents(t)
	n := newTestNotifier(f, events.fn)
	t0 := time.Now()

	n.enqueueAt("psx", t0)
	n.flushDue(t0.Add(2 * time.Minute)) // rejected: scan in progress
	if f.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", f.callCount())
	}

	// Gated: nothing before the retry window elapses.
	n.flushDue(t0.Add(3 * time.Minute))
	if f.callCount() != 1 {
		t.Fatalf("calls during retry gate = %d, want 1", f.callCount())
	}

	// Past the gate the batch goes out again.
	n.flushDue(t0.Add(6 * time.Minute))
	if f.callCount() != 2 {
		t.Fatalf("calls after gate = %d, want 2", f.callCount())
	}
	if len(f.calls[1]) != 1 || f.calls[1][0] != "psx" {
		t.Fatalf("retried batch = %v, want [psx]", f.calls[1])
	}
	// Busy is not a failure: no error events.
	if len(events.all()) != 0 {
		t.Fatalf("events = %v, want none for busy", events.all())
	}
}

func TestNotifierFailureRequeuesAndReportsTransitions(t *testing.T) {
	f := &fakeScanAPI{sources: []string{"igdb"}, scanErrs: []error{errors.New("boom"), errors.New("boom2")}}
	events := recordEvents(t)
	n := newTestNotifier(f, events.fn)
	t0 := time.Now()

	n.enqueueAt("psx", t0)
	n.flushDue(t0.Add(2 * time.Minute))  // boom -> failing
	n.flushDue(t0.Add(8 * time.Minute))  // boom2 -> still failing, no repeat event
	n.flushDue(t0.Add(15 * time.Minute)) // recovers

	if f.callCount() != 3 {
		t.Fatalf("calls = %d, want 3", f.callCount())
	}
	got := events.all()
	want := []string{"romm_connect_error", "romm_connect_recovered"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestNotifierRefusesEmptySources(t *testing.T) {
	f := &fakeScanAPI{sources: nil}
	n := newTestNotifier(f, nil)
	t0 := time.Now()

	n.enqueueAt("psx", t0)
	n.flushDue(t0.Add(2 * time.Minute))
	if f.callCount() != 0 {
		t.Fatalf("TriggerScan called with no sources available")
	}

	// Sources come back (config fixed server-side); the batch is still alive.
	f.mu.Lock()
	f.sources = []string{"igdb"}
	f.mu.Unlock()
	n.flushDue(t0.Add(10 * time.Minute))
	if f.callCount() != 1 {
		t.Fatalf("batch lost after empty-sources hold: calls = %d, want 1", f.callCount())
	}
}

func TestNotifierEnqueueEmptySlugIgnored(t *testing.T) {
	f := &fakeScanAPI{sources: []string{"igdb"}}
	n := newTestNotifier(f, nil)
	n.Enqueue("")
	n.flushDue(time.Now().Add(time.Hour))
	if f.callCount() != 0 {
		t.Fatal("empty slug must not produce a scan")
	}
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func recordEvents(_ *testing.T) *eventRecorder { return &eventRecorder{} }

func (r *eventRecorder) fn(event, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}
