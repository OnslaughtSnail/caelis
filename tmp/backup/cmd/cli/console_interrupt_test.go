package main

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleInterruptSignals_ReadlineIdleIgnoresSignal(t *testing.T) {
	console := &cliConsole{editor: &readlineEditor{}}
	sigCh := make(chan os.Signal, 1)
	exitCh := make(chan struct{}, 1)
	stop := make(chan struct{})
	go console.handleInterruptSignals(sigCh, exitCh, stop)
	defer close(stop)

	sigCh <- os.Interrupt
	select {
	case <-exitCh:
		t.Fatal("expected no exit on first readline Ctrl+C")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestHandleInterruptSignals_NonReadlineDoubleInterruptExits(t *testing.T) {
	console := &cliConsole{editor: &stubLineEditor{}}
	sigCh := make(chan os.Signal, 2)
	exitCh := make(chan struct{}, 1)
	stop := make(chan struct{})
	go console.handleInterruptSignals(sigCh, exitCh, stop)
	defer close(stop)

	sigCh <- os.Interrupt
	select {
	case <-exitCh:
		t.Fatal("expected first interrupt not to exit")
	case <-time.After(80 * time.Millisecond):
	}

	sigCh <- os.Interrupt
	select {
	case <-exitCh:
	case <-time.After(120 * time.Millisecond):
		t.Fatal("expected second interrupt to request exit")
	}
}

func TestHandleInterruptSignals_ActiveRunCancels(t *testing.T) {
	console := &cliConsole{editor: &readlineEditor{}}
	var canceled int32
	console.setActiveRunCancel(func() {
		atomic.AddInt32(&canceled, 1)
	})
	sigCh := make(chan os.Signal, 1)
	exitCh := make(chan struct{}, 1)
	stop := make(chan struct{})
	go console.handleInterruptSignals(sigCh, exitCh, stop)
	defer close(stop)

	sigCh <- os.Interrupt
	time.Sleep(80 * time.Millisecond)
	if atomic.LoadInt32(&canceled) != 1 {
		t.Fatalf("expected active run to be canceled once, got %d", atomic.LoadInt32(&canceled))
	}
	select {
	case <-exitCh:
		t.Fatal("expected no exit while canceling active run")
	default:
	}
}
