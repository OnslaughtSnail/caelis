package execenv

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func waitForCondition(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !check() {
		t.Fatalf("condition not satisfied within %s", timeout)
	}
}

func TestAsyncSession_BasicExecution(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: "echo hello",
	})

	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for completion
	exitCode, err := session.WaitWithTimeout(5 * time.Second)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d", exitCode)
	}

	stdout, stderr := session.ReadAllOutput()
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("Expected stdout to contain 'hello', got %q", stdout)
	}
	if stderr != "" {
		t.Logf("Stderr: %q", stderr)
	}
}

func TestAsyncSession_WriteInput(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: "cat",
	})

	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Write input
	if err := session.WriteInput([]byte("test input\n")); err != nil {
		t.Fatalf("WriteInput failed: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		stdout, _ := session.ReadAllOutput()
		return strings.Contains(stdout, "test input")
	})

	// Terminate
	if err := session.Terminate(); err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}

	stdout, _ := session.ReadAllOutput()
	if !strings.Contains(stdout, "test input") {
		t.Fatalf("Expected stdout to contain 'test input', got %q", stdout)
	}
}

func TestAsyncSession_Terminate(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: "sleep 100",
	})

	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	if session.HasExited() {
		t.Fatal("Session should not have exited yet")
	}

	// Terminate
	if err := session.Terminate(); err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	if !session.HasExited() {
		// Wait a bit more
		if _, err := session.WaitWithTimeout(time.Second); err != nil {
			t.Fatalf("WaitWithTimeout failed: %v", err)
		}
	}
}

func TestAsyncSession_ReadOutput(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: "echo first && sleep 0.1 && echo second",
	})

	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	var marker1, marker2 int64
	waitForCondition(t, time.Second, func() bool {
		stdout, stderr, nextStdout, nextStderr := session.ReadOutput(0, 0)
		t.Logf("ReadOutput poll: stdout=%q stderr=%q markers=%d,%d", string(stdout), string(stderr), nextStdout, nextStderr)
		marker1, marker2 = nextStdout, nextStderr
		return strings.Contains(string(stdout), "first")
	})

	if _, err := session.WaitWithTimeout(2 * time.Second); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	stdout2, _, _, _ := session.ReadOutput(marker1, marker2)
	finalStdout, _ := session.ReadAllOutput()
	t.Logf("Second read: stdout=%q final=%q", string(stdout2), finalStdout)
	if !strings.Contains(finalStdout, "first") || !strings.Contains(finalStdout, "second") {
		t.Fatalf("Expected both 'first' and 'second' in output, got %q", finalStdout)
	}
}

func TestSessionManager_StartAndGet(t *testing.T) {
	sm := NewSessionManager(DefaultSessionManagerConfig())
	defer sm.Close()

	session, err := sm.StartSession(AsyncSessionConfig{
		Command: "echo test",
	})
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	// Get the session
	retrieved, err := sm.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if retrieved.ID != session.ID {
		t.Fatalf("Session ID mismatch: %s != %s", retrieved.ID, session.ID)
	}

	// Wait for completion
	if _, err := sm.WaitSessionWithTimeout(session.ID, 5*time.Second); err != nil {
		t.Fatalf("WaitSessionWithTimeout failed: %v", err)
	}
}

func TestSessionManager_ListSessions(t *testing.T) {
	sm := NewSessionManager(DefaultSessionManagerConfig())
	defer sm.Close()

	// Start two sessions
	s1, _ := sm.StartSession(AsyncSessionConfig{Command: "sleep 0.5"})
	s2, _ := sm.StartSession(AsyncSessionConfig{Command: "echo done"})

	time.Sleep(100 * time.Millisecond)

	sessions := sm.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}

	// Clean up
	if _, err := sm.WaitSessionWithTimeout(s1.ID, 2*time.Second); err != nil {
		t.Fatalf("WaitSessionWithTimeout s1 failed: %v", err)
	}
	if _, err := sm.WaitSessionWithTimeout(s2.ID, 2*time.Second); err != nil {
		t.Fatalf("WaitSessionWithTimeout s2 failed: %v", err)
	}
}

func TestSessionManager_Terminate(t *testing.T) {
	sm := NewSessionManager(DefaultSessionManagerConfig())
	defer sm.Close()

	session, _ := sm.StartSession(AsyncSessionConfig{
		Command: "sleep 100",
	})

	time.Sleep(100 * time.Millisecond)

	if err := sm.TerminateSession(session.ID); err != nil {
		t.Fatalf("TerminateSession failed: %v", err)
	}
}

func TestHostRunner_Async(t *testing.T) {
	runner := newHostRunnerWithConfig(DefaultHostRunnerConfig())
	defer runner.Close()

	// Start async command
	sessionID, err := runner.StartAsync(context.Background(), CommandRequest{
		Command: "printf async-test; sleep 0.05",
	})
	if err != nil {
		t.Fatalf("StartAsync failed: %v", err)
	}

	var stdout []byte
	waitForCondition(t, 10*time.Second, func() bool {
		var readErr error
		stdout, _, _, _, readErr = runner.ReadOutput(sessionID, 0, 0)
		if readErr != nil {
			t.Fatalf("ReadOutput failed: %v", readErr)
		}
		if strings.Contains(string(stdout), "async-test") {
			return true
		}
		status, statusErr := runner.GetSessionStatus(sessionID)
		if statusErr != nil {
			t.Fatalf("GetSessionStatus failed: %v", statusErr)
		}
		return status.State == SessionStateCompleted || status.State == SessionStateError || status.State == SessionStateTerminated
	})

	result, err := runner.WaitSession(context.Background(), sessionID, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitSession failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(string(stdout), "async-test") && !strings.Contains(result.Stdout, "async-test") {
		t.Fatalf("expected 'async-test' in async output, read=%q wait=%q", string(stdout), result.Stdout)
	}
}

func TestHostRunner_AsyncWriteRead(t *testing.T) {
	runner := newHostRunnerWithConfig(DefaultHostRunnerConfig())
	defer runner.Close()

	// Start cat command
	sessionID, err := runner.StartAsync(context.Background(), CommandRequest{
		Command: "cat",
	})
	if err != nil {
		t.Fatalf("StartAsync failed: %v", err)
	}

	// Write input
	if err := runner.WriteInput(sessionID, []byte("hello from test\n")); err != nil {
		t.Fatalf("WriteInput failed: %v", err)
	}

	var stdout []byte
	waitForCondition(t, 5*time.Second, func() bool {
		var err error
		stdout, _, _, _, err = runner.ReadOutput(sessionID, 0, 0)
		if err != nil {
			t.Fatalf("ReadOutput failed: %v", err)
		}
		return strings.Contains(string(stdout), "hello from test")
	})

	// Terminate
	if err := runner.TerminateSession(sessionID); err != nil {
		t.Fatalf("TerminateSession failed: %v", err)
	}
}

func TestHostRunner_AsyncTTYWriteRead(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script utility unavailable")
	}
	runner := newHostRunnerWithConfig(DefaultHostRunnerConfig())
	defer runner.Close()

	sessionID, err := runner.StartAsync(context.Background(), CommandRequest{
		Command: `bash -c 'printf "name? "; read name; printf "hello %s\n" "$name"'`,
		TTY:     true,
	})
	if err != nil {
		t.Fatalf("StartAsync failed: %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		stdout, _, _, _, err := runner.ReadOutput(sessionID, 0, 0)
		if err != nil {
			t.Fatalf("ReadOutput failed: %v", err)
		}
		return strings.Contains(string(stdout), "name?")
	})

	if err := runner.WriteInput(sessionID, []byte("alice\n")); err != nil {
		t.Fatalf("WriteInput failed: %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		stdout, _, _, _, err := runner.ReadOutput(sessionID, 0, 0)
		if err != nil {
			t.Fatalf("ReadOutput failed: %v", err)
		}
		return strings.Contains(string(stdout), "hello alice")
	})
}

func TestHostRunner_ListSessions(t *testing.T) {
	runner := newHostRunnerWithConfig(DefaultHostRunnerConfig())
	defer runner.Close()

	// Start a session
	_, err := runner.StartAsync(context.Background(), CommandRequest{
		Command: "sleep 1",
	})
	if err != nil {
		t.Fatalf("StartAsync failed: %v", err)
	}

	sessions := runner.ListSessions()
	if len(sessions) == 0 {
		t.Fatal("Expected at least one session")
	}
}

func TestHostRunner_StartAsync_FailsAfterRunnerClose(t *testing.T) {
	runner := newHostRunnerWithConfig(DefaultHostRunnerConfig())
	if err := runner.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err := runner.StartAsync(context.Background(), CommandRequest{
		Command: "echo async-closed",
	})
	if err == nil {
		t.Fatal("expected StartAsync to fail after session manager close")
	}
	if !strings.Contains(err.Error(), "host runner is closed") {
		t.Fatalf("expected host runner closed error, got %v", err)
	}
}

func TestAsyncSession_StatusConcurrentWithExit(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: `printf "boom" >&2; exit 7`,
	})
	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for !session.HasExited() {
			_ = session.Status()
			time.Sleep(time.Millisecond)
		}
		_ = session.Status()
	}()

	exitCode, err := session.WaitWithTimeout(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitWithTimeout failed: %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", exitCode)
	}
	<-done

	status := session.Status()
	if status.ExitCode != 7 {
		t.Fatalf("expected status exit code 7, got %d", status.ExitCode)
	}
}
