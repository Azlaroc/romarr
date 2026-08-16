package rommconnect

import (
	"testing"
	"time"
)

// A never-started notifier must not block Stop forever, and Stop must be
// idempotent; a double Start must not panic (single-use guard).
func TestNotifierLifecycleGuards(t *testing.T) {
	n := NewNotifier(nil, NotifierOptions{})

	done := make(chan struct{})
	go func() {
		n.Stop() // never started: must return, not block on done
		n.Stop() // idempotent
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop on a never-started notifier blocked")
	}

	n2 := NewNotifier(nil, NotifierOptions{})
	n2.Start()
	n2.Start() // guarded: must not double-close done
	n2.Stop()
}
