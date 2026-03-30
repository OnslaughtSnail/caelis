package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type bashWatchTestRuntime struct {
	cwd  string
	host toolexec.AsyncCommandRunner
}

func (r bashWatchTestRuntime) PermissionMode() toolexec.PermissionMode {
	return toolexec.PermissionModeDefault
}
func (r bashWatchTestRuntime) SandboxType() string                   { return "test" }
func (r bashWatchTestRuntime) SandboxPolicy() toolexec.SandboxPolicy { return toolexec.SandboxPolicy{} }
func (r bashWatchTestRuntime) FallbackToHost() bool                  { return false }
func (r bashWatchTestRuntime) FallbackReason() string                { return "" }
func (r bashWatchTestRuntime) Diagnostics() toolexec.SandboxDiagnostics {
	return toolexec.SandboxDiagnostics{}
}
func (r bashWatchTestRuntime) FileSystem() toolexec.FileSystem { return previewTestFS{cwd: r.cwd} }
func (r bashWatchTestRuntime) State() toolexec.RuntimeState {
	return toolexec.RuntimeState{Mode: toolexec.PermissionModeDefault, ResolvedSandbox: "test"}
}
func (r bashWatchTestRuntime) Execute(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}
func (r bashWatchTestRuntime) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	return runtimeSessionFromRunner(ctx, "host", r.host, req)
}
func (r bashWatchTestRuntime) OpenSession(ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	return runtimeSessionFromRef("host", r.host, ref)
}
func (r bashWatchTestRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
}

type bashWatchMockRunner struct {
	mu       sync.Mutex
	reads    []watchReadStep
	statuses []toolexec.SessionState
	readAt   int
	stateAt  int
}

type watchReadStep struct {
	stdout string
	stderr string
}

func (r *bashWatchMockRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (r *bashWatchMockRunner) StartAsync(context.Context, toolexec.CommandRequest) (string, error) {
	return "watch-session-1", nil
}

func (r *bashWatchMockRunner) WriteInput(string, []byte) error { return nil }

func (r *bashWatchMockRunner) ReadOutput(string, int64, int64) ([]byte, []byte, int64, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reads) == 0 {
		return nil, nil, 0, 0, nil
	}
	idx := r.readAt
	if idx >= len(r.reads) {
		idx = len(r.reads) - 1
	}
	step := r.reads[idx]
	if r.readAt < len(r.reads)-1 {
		r.readAt++
	}
	return []byte(step.stdout), []byte(step.stderr), int64(r.readAt), int64(r.readAt), nil
}

func (r *bashWatchMockRunner) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.statuses) == 0 {
		return toolexec.SessionStatus{}, nil
	}
	idx := r.stateAt
	if idx >= len(r.statuses) {
		idx = len(r.statuses) - 1
	}
	state := r.statuses[idx]
	if r.stateAt < len(r.statuses)-1 {
		r.stateAt++
	}
	return toolexec.SessionStatus{State: state}, nil
}

func (r *bashWatchMockRunner) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (r *bashWatchMockRunner) TerminateSession(string) error { return nil }

func (r *bashWatchMockRunner) ListSessions() []toolexec.SessionInfo { return nil }

func TestEnsureBashTaskWatchContext_UsesBaseContextAfterTurnCancel(t *testing.T) {
	runner := &bashWatchMockRunner{
		reads: []watchReadStep{
			{},
			{stdout: "late output\n"},
			{},
		},
		statuses: []toolexec.SessionState{
			toolexec.SessionStateRunning,
			toolexec.SessionStateRunning,
			toolexec.SessionStateCompleted,
		},
	}
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:         context.Background(),
		tuiSender:       sender,
		execRuntime:     bashWatchTestRuntime{cwd: t.TempDir(), host: runner},
		bashTaskWatches: map[string]context.CancelFunc{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.ensureBashTaskWatchContext(ctx, "task-1", "call-1", "session-1", "host", string(toolexec.ExecutionRouteHost))
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, raw := range sender.Snapshot() {
			msg, ok := raw.(tuievents.TaskStreamMsg)
			if !ok {
				continue
			}
			if msg.Stream == "stdout" && msg.Chunk == "late output\n" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected bash watch to survive canceled request context and emit late output, got %#v", sender.Snapshot())
}

func TestSyncBashTaskWatchContext_UsesHiddenUIWatchFields(t *testing.T) {
	runner := &bashWatchMockRunner{
		reads: []watchReadStep{
			{},
			{stdout: "streamed via hidden ui fields\n"},
			{},
		},
		statuses: []toolexec.SessionState{
			toolexec.SessionStateRunning,
			toolexec.SessionStateRunning,
			toolexec.SessionStateCompleted,
		},
	}
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:         context.Background(),
		tuiSender:       sender,
		execRuntime:     bashWatchTestRuntime{cwd: t.TempDir(), host: runner},
		bashTaskWatches: map[string]context.CancelFunc{},
	}

	c.syncBashTaskWatchContext(context.Background(), "call-1", "BASH", map[string]any{
		"task_id":    "task-1",
		"state":      "running",
		"session_id": "session-1",
		"route":      string(toolexec.ExecutionRouteHost),
	})

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, raw := range sender.Snapshot() {
			msg, ok := raw.(tuievents.TaskStreamMsg)
			if !ok {
				continue
			}
			if msg.TaskID == "task-1" && msg.Stream == "stdout" && msg.Chunk == "streamed via hidden ui fields\n" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected bash watch to start from hidden ui fields, got %#v", sender.Snapshot())
}
