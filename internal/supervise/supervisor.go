// Package supervise owns the restartable background loops (scheduler,
// torrent watcher, RomM sync, RomM Connect notifier) and re-arms them when
// runtime settings change. None of the underlying loops support restart —
// their stop channels close once — so every re-arm constructs a fresh
// instance via the Builders; the scheduler is the exception, re-arming in
// place so its cycle counters survive.
package supervise

import (
	"sync"

	"gamarr/internal/config"
	"gamarr/internal/download"
	"gamarr/internal/romm"
	"gamarr/internal/rommconnect"
	"gamarr/internal/scheduler"
)

// Builders construct fresh loop instances, capturing the wiring main() owns
// (clients, stores, callbacks). RomMSync may return nil (unconfigured).
type Builders struct {
	Watcher  func() *download.Watcher
	RomMSync func() *romm.Syncer
	Connect  func() (*rommconnect.Notifier, func(fsSlug string))
}

// Supervisor serializes all loop lifecycle transitions under one mutex, so
// concurrent settings writes can never interleave a stop and a start.
type Supervisor struct {
	mu              sync.Mutex
	cfg             *config.Config
	sched           *scheduler.Scheduler
	b               Builders
	setImportNotify func(func(fsSlug string))

	watcher  *download.Watcher
	rommSync *romm.Syncer
	notifier *rommconnect.Notifier
}

// New wires a Supervisor. setImportNotify attaches/detaches the Connect
// notifier's enqueue on the download manager in lockstep with its lifecycle.
func New(cfg *config.Config, sched *scheduler.Scheduler, b Builders, setImportNotify func(func(fsSlug string))) *Supervisor {
	return &Supervisor{cfg: cfg, sched: sched, b: b, setImportNotify: setImportNotify}
}

// StartAll boots every loop the config enables.
func (s *Supervisor) StartAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sched != nil {
		s.sched.Start()
	}
	s.startWatcherLocked()
	s.startRomMSyncLocked()
	s.startConnectLocked()
}

// StopAll is the shutdown path (reverse order; notifier detaches first).
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopConnectLocked()
	s.stopRomMSyncLocked()
	s.stopWatcherLocked()
	if s.sched != nil {
		s.sched.StopLoop()
	}
}

// RomMSync returns the live syncer, or nil while sync is disabled — the API
// reads it per request instead of holding a boot-time (possibly stale nil)
// pointer.
func (s *Supervisor) RomMSync() *romm.Syncer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rommSync
}

// Apply re-arms the loops affected by the changed settings keys. Called
// synchronously from the settings PUT after rows persist: when the request
// returns, the new state is live (e.g. flipping RomM sync on must have a
// syncer running before the fs scanner cedes ROM ownership to it).
func (s *Supervisor) Apply(changed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range changed {
		switch key {
		case "watcher_enabled":
			s.stopWatcherLocked()
			s.startWatcherLocked()
		case "scheduler_enabled":
			if s.sched == nil {
				continue
			}
			if s.cfg.SchedulerOn() {
				s.sched.EnsureRunning()
			} else {
				s.sched.StopLoop()
			}
		case "scheduler_interval_hours":
			// Restart just the loop so the ticker re-reads; counters survive
			// (same Scheduler instance).
			if s.sched != nil && s.sched.LoopRunning() {
				s.sched.StopLoop()
				s.sched.EnsureRunning()
			}
		case "romm_sync_enabled", "romm_sync_interval_seconds", "romm_exclude_platforms":
			s.stopRomMSyncLocked()
			s.startRomMSyncLocked()
		case "romm_connect_enabled":
			s.stopConnectLocked()
			s.startConnectLocked()
		}
		// watcher_interval_seconds needs no case: the watcher loop re-reads
		// it every tick.
	}
}

func (s *Supervisor) startWatcherLocked() {
	if s.watcher != nil || s.b.Watcher == nil {
		return
	}
	w := s.b.Watcher()
	if w != nil && w.Start() {
		s.watcher = w
	}
}

func (s *Supervisor) stopWatcherLocked() {
	if s.watcher != nil {
		s.watcher.Stop()
		s.watcher = nil
	}
}

func (s *Supervisor) startRomMSyncLocked() {
	if s.rommSync != nil {
		return
	}
	if !s.cfg.HasRomMAPI() || !s.cfg.RomMSyncOn() || s.b.RomMSync == nil {
		return
	}
	if sy := s.b.RomMSync(); sy != nil {
		sy.Start()
		s.rommSync = sy
	}
}

func (s *Supervisor) stopRomMSyncLocked() {
	if s.rommSync != nil {
		s.rommSync.Stop()
		s.rommSync = nil
	}
}

func (s *Supervisor) startConnectLocked() {
	if s.notifier != nil {
		return
	}
	if !s.cfg.HasRomMAPI() || !s.cfg.RomMConnectOn() || s.b.Connect == nil {
		return
	}
	n, enqueue := s.b.Connect()
	if n == nil {
		return
	}
	n.Start()
	if s.setImportNotify != nil {
		s.setImportNotify(enqueue)
	}
	s.notifier = n
}

func (s *Supervisor) stopConnectLocked() {
	if s.notifier != nil {
		if s.setImportNotify != nil {
			s.setImportNotify(nil)
		}
		s.notifier.Stop()
		s.notifier = nil
	}
}
