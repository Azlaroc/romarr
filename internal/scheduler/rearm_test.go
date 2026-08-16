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

func TestRunNowSurvivesStopLoop(t *testing.T) {
	// Regression: after a runtime disable (StopLoop), manual cycles must
	// still work — an early version left the stop channel closed, so every
	// later RunNow cycle aborted on entry. Runtime StopLoop re-opens the
	// channel; only the shutdown Stop leaves it closed.
	cfg := &config.Config{SchedulerEnabled: true, SchedulerIntervalHours: 1}
	s := newRearmScheduler(t, cfg)

	s.EnsureRunning()
	s.StopLoop()
	select {
	case <-s.stopChan():
		t.Fatal("stop channel closed after StopLoop — RunNow cycles would abort on entry")
	default:
	}

	s.Stop()
	select {
	case <-s.stopChan():
		// Shutdown: closed, and post-shutdown cycles abort. Correct.
	default:
		t.Fatal("stop channel open after shutdown Stop")
	}
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
