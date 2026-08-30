// Package supervise owns the restartable background loops (scheduler,
// torrent watcher, RomM sync, RomM Connect notifier, DAT refresh cadence)
// and re-arms them when runtime settings change. Most of the underlying
// loops do not support restart — their stop channels close once — so every
// re-arm constructs a fresh instance via the Builders. The scheduler and the
// DAT service are the exceptions: both re-arm in place, because the API
// holds a stable pointer to them (manual runs, uploads) and their run state
// has to survive a settings flip.
package supervise

import (
	"sync"

	"gamarr/internal/config"
	"gamarr/internal/datsvc"
	"gamarr/internal/download"
	"gamarr/internal/rommconnect"
	"gamarr/internal/scheduler"
)

// Builders construct fresh loop instances, capturing the wiring main() owns
// (clients, stores, callbacks).
type Builders struct {
	Watcher func() *download.Watcher
	Connect func() (*rommconnect.Notifier, func(fsSlug string))
}

// Supervisor serializes all loop lifecycle transitions under one mutex, so
// concurrent settings writes can never interleave a stop and a start.
type Supervisor struct {
	mu              sync.Mutex
	cfg             *config.Config
	sched           *scheduler.Scheduler
	dat             *datsvc.Service
	b               Builders
	setImportNotify func(func(fsSlug string))

	watcher  *download.Watcher
	notifier *rommconnect.Notifier
}

// New wires a Supervisor. setImportNotify attaches/detaches the Connect
// notifier's enqueue on the download manager in lockstep with its lifecycle.
func New(cfg *config.Config, sched *scheduler.Scheduler, dat *datsvc.Service, b Builders, setImportNotify func(func(fsSlug string))) *Supervisor {
	return &Supervisor{cfg: cfg, sched: sched, dat: dat, b: b, setImportNotify: setImportNotify}
}

// StartAll boots every loop the config enables.
func (s *Supervisor) StartAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sched != nil {
		s.sched.Start()
	}
	// A no-op while the DAT cadence is off, which is how it ships: no
	// goroutine, no ticker, no fetch.
	s.dat.EnsureRunning()
	s.startWatcherLocked()
	s.startConnectLocked()
}

// StopAll is the shutdown path (reverse order; notifier detaches first).
// Scheduler.Stop (not StopLoop) so post-shutdown cycles abort on entry.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopConnectLocked()
	s.stopWatcherLocked()
	s.dat.Stop()
	if s.sched != nil {
		s.sched.Stop()
	}
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
		case "romm_connect_enabled":
			s.stopConnectLocked()
			s.startConnectLocked()
		case "dat_auto_refresh_enabled":
			if s.cfg.DatAutoRefreshOn() {
				s.dat.EnsureRunning()
			} else {
				s.dat.StopLoop()
			}
		case "dat_refresh_interval_days":
			// Re-arm in place so the ticker re-reads the interval; an
			// in-flight refresh is left alone.
			if s.dat.LoopRunning() {
				s.dat.StopLoop()
				s.dat.EnsureRunning()
			}
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
