package execenv

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHostRunner_RunTimeout(t *testing.T) {
	runner := newHostRunner()
	start := time.Now()
	_, err := runner.Run(context.Background(), CommandRequest{
		Command: "sleep 1",
		Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("expected timeout message, got %v", err)
	}
	if !IsErrorCode(err, ErrorCodeHostCommandTimeout) {
		t.Fatalf("expected timeout error code %q, got %q", ErrorCodeHostCommandTimeout, ErrorCodeOf(err))
	}
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("expected command to be cancelled quickly, elapsed=%s", elapsed)
	}
}

func TestHostRunner_RunIdleTimeout(t *testing.T) {
	runner := newHostRunner()
	_, err := runner.Run(context.Background(), CommandRequest{
		Command:     "echo hello && sleep 1",
		Timeout:     3 * time.Second,
		IdleTimeout: 120 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected idle-timeout error")
	}
	if !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("expected idle-timeout message, got %v", err)
	}
	if !IsErrorCode(err, ErrorCodeHostIdleTimeout) {
		t.Fatalf("expected idle-timeout error code %q, got %q", ErrorCodeHostIdleTimeout, ErrorCodeOf(err))
	}
}
