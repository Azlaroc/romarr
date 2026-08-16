package scheduler

import (
	"path/filepath"
	"testing"
	"time"

	"gamarr/internal/config"
	"gamarr/internal/db"
)

func newRearmScheduler(t *testing.T, cfg *config.Config) *Scheduler {
	t.Helper()
	store, err := db.New(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return New(cfg, store, nil, nil, nil)
}

func TestEnsureRunningStopLoopCycle(t *testing.T) {
	cfg := &config.Config{SchedulerEnabled: true, SchedulerIntervalHours: 1}
	s := newRearmScheduler(t, cfg)

	if s.LoopRunning() {
		t.Fatal("loop live before start")
	}
	if !s.EnsureRunning() {
		t.Fatal("EnsureRunning = false with scheduler enabled")
	}
	if !s.LoopRunning() {
		t.Fatal("loop not live after EnsureRunning")
	}
	// Idempotent.
	if !s.EnsureRunning() {
		t.Fatal("second EnsureRunning = false")
	}

	s.StopLoop()
	if s.LoopRunning() {
		t.Fatal("loop still live after StopLoop")
	}
	// Idempotent stop.
	s.StopLoop()

	// A full second cycle must work (the old Stop() closed the channel
	// permanently; re-arm requires a fresh one).
	if !s.EnsureRunning() {
		t.Fatal("re-arm after stop failed")
	}
	if !s.LoopRunning() {
		t.Fatal("loop not live after re-arm")
	}
	s.StopLoop()

	// Give stopped goroutines a moment to exit cleanly (race detector food).
	time.Sleep(10 * time.Millisecond)
}

func TestEnsureRunningRespectsDisabled(t *testing.T) {
	cfg := &config.Config{SchedulerEnabled: false}
	s := newRearmScheduler(t, cfg)
	if s.EnsureRunning() {
		t.Fatal("EnsureRunning should refuse while disabled")
	}
	if s.LoopRunning() {
		t.Fatal("no loop should be live")
	}
}

func TestStatusReportsLoopLiveness(t *testing.T) {
	cfg := &config.Config{SchedulerEnabled: true, SchedulerIntervalHours: 1}
	s := newRearmScheduler(t, cfg)

	if st := s.Status(); st["enabled"] != false {
		t.Fatalf("enabled before start = %v, want false", st["enabled"])
	}
	s.EnsureRunning()
	if st := s.Status(); st["enabled"] != true {
		t.Fatalf("enabled after start = %v, want true", st["enabled"])
	}
	s.StopLoop()
	if st := s.Status(); st["enabled"] != false {
		t.Fatalf("enabled after stop = %v, want false — the boot-capture lie is back", st["enabled"])
	}
}
