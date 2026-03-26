package acpext

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleWatchdog_UsesInitWindowBeforeFirstUpdate(t *testing.T) {
	var fired atomic.Int32
	watchdog := newIdleWatchdog(40*time.Millisecond, 140*time.Millisecond, func(time.Duration) {
		fired.Add(1)
	})
	watchdog.Start()
	defer watchdog.Stop()

	time.Sleep(70 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("expected init window to prevent early idle timeout before first update")
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected watchdog to fire after the initialization window elapsed")
}

func TestIdleWatchdog_PauseResumeSkipsApprovalWaits(t *testing.T) {
	var fired atomic.Int32
	watchdog := newIdleWatchdog(50*time.Millisecond, 50*time.Millisecond, func(time.Duration) {
		fired.Add(1)
	})
	watchdog.Start()
	defer watchdog.Stop()

	watchdog.Beat()
	watchdog.Pause()
	time.Sleep(120 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("expected paused watchdog to ignore approval wait")
	}

	watchdog.Resume()
	time.Sleep(25 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("expected watchdog resume to refresh the idle window")
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected watchdog to fire after resume idle window elapsed")
}
