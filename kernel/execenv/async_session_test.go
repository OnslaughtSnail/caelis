package execenv

import (
	"context"
	"strings"
	"testing"
	"time"
)

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

	// Give it time to process
	time.Sleep(100 * time.Millisecond)

	// Terminate
	session.Terminate()

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
		session.WaitWithTimeout(time.Second)
	}
}

func TestAsyncSession_ReadOutput(t *testing.T) {
	session := NewAsyncSession(AsyncSessionConfig{
		Command: "echo first && sleep 0.1 && echo second",
	})

	if err := session.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for first output
	time.Sleep(50 * time.Millisecond)

	stdout1, stderr1, marker1, marker2 := session.ReadOutput(0, 0)
	t.Logf("First read: stdout=%q stderr=%q markers=%d,%d", string(stdout1), string(stderr1), marker1, marker2)

	// Wait for more output
	time.Sleep(200 * time.Millisecond)

	stdout2, _, _, _ := session.ReadOutput(marker1, marker2)
	t.Logf("Second read: stdout=%q", string(stdout2))

	session.WaitWithTimeout(2 * time.Second)

	finalStdout, _ := session.ReadAllOutput()
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
	sm.WaitSessionWithTimeout(session.ID, 5*time.Second)
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
	sm.WaitSessionWithTimeout(s1.ID, 2*time.Second)
	sm.WaitSessionWithTimeout(s2.ID, 2*time.Second)
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
		Command: "echo async-test",
	})
	if err != nil {
		t.Fatalf("StartAsync failed: %v", err)
	}

	// Wait for it
	result, err := runner.WaitSession(context.Background(), sessionID, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitSession failed: %v", err)
	}

	if !strings.Contains(result.Stdout, "async-test") {
		t.Fatalf("Expected 'async-test' in output, got %q", result.Stdout)
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

	time.Sleep(100 * time.Millisecond)

	// Read output
	stdout, _, _, _, err := runner.ReadOutput(sessionID, 0, 0)
	if err != nil {
		t.Fatalf("ReadOutput failed: %v", err)
	}

	if !strings.Contains(string(stdout), "hello from test") {
		t.Fatalf("Expected 'hello from test' in output, got %q", string(stdout))
	}

	// Terminate
	runner.TerminateSession(sessionID)
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
