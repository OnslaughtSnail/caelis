package acpext

import (
	"sync"
	"time"
)

type idleWatchdog struct {
	idleTimeout time.Duration
	initTimeout time.Duration
	onIdle      func(time.Duration)

	stopCh chan struct{}

	mu        sync.Mutex
	startedAt time.Time
	lastBeat  time.Time
	seenBeat  bool
	paused    bool
	once      sync.Once
}

func newIdleWatchdog(idleTimeout, initTimeout time.Duration, onIdle func(time.Duration)) *idleWatchdog {
	now := time.Now()
	if idleTimeout <= 0 && initTimeout <= 0 {
		return &idleWatchdog{
			stopCh:    make(chan struct{}),
			startedAt: now,
		}
	}
	if idleTimeout <= 0 {
		idleTimeout = initTimeout
	}
	if initTimeout <= 0 {
		initTimeout = idleTimeout
	}
	return &idleWatchdog{
		idleTimeout: idleTimeout,
		initTimeout: initTimeout,
		onIdle:      onIdle,
		stopCh:      make(chan struct{}),
		startedAt:   now,
	}
}

func (w *idleWatchdog) Start() {
	if w == nil {
		return
	}
	interval := w.tickInterval()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if idleFor, ok := w.idleFor(); ok {
					if w.onIdle != nil {
						w.onIdle(idleFor)
					}
					return
				}
			case <-w.stopCh:
				return
			}
		}
	}()
}

func (w *idleWatchdog) Beat() {
	if w == nil {
		return
	}
	now := time.Now()
	w.mu.Lock()
	w.lastBeat = now
	w.seenBeat = true
	w.mu.Unlock()
}

func (w *idleWatchdog) Pause() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.paused = true
	w.mu.Unlock()
}

func (w *idleWatchdog) Resume() {
	if w == nil {
		return
	}
	now := time.Now()
	w.mu.Lock()
	w.paused = false
	if w.seenBeat {
		w.lastBeat = now
	} else {
		w.startedAt = now
	}
	w.mu.Unlock()
}

func (w *idleWatchdog) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		close(w.stopCh)
	})
}

func (w *idleWatchdog) tickInterval() time.Duration {
	if w == nil {
		return 0
	}
	timeout := w.idleTimeout
	if timeout <= 0 || (w.initTimeout > 0 && w.initTimeout < timeout) {
		timeout = w.initTimeout
	}
	if timeout <= 0 {
		return 0
	}
	interval := timeout / 4
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}
	return interval
}

func (w *idleWatchdog) idleFor() (time.Duration, bool) {
	if w == nil {
		return 0, false
	}
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.paused {
		return 0, false
	}
	if w.seenBeat {
		if w.idleTimeout <= 0 {
			return 0, false
		}
		idleFor := now.Sub(w.lastBeat)
		return idleFor, idleFor >= w.idleTimeout
	}
	if w.initTimeout <= 0 {
		return 0, false
	}
	idleFor := now.Sub(w.startedAt)
	return idleFor, idleFor >= w.initTimeout
}
