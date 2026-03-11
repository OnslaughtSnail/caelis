package shell

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type recordingRunner struct {
	result toolexec.CommandResult
	err    error
	calls  []toolexec.CommandRequest
	onRun  func(toolexec.CommandRequest)
}

type asyncRecordingRunner struct {
	recordingRunner
	status     toolexec.SessionStatus
	readResult struct {
		stdout       []byte
		stderr       []byte
		stdoutMarker int64
		stderrMarker int64
	}
	startSessionID string
}

type stubTaskManager struct {
	startBash task.Snapshot
	lastBash  task.BashStartRequest
}

type stubFileSystem struct {
	cwd  string
	home string
}

func testSandboxType() string {
	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	return "landlock"
}

func (r *recordingRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	r.calls = append(r.calls, req)
	if r.onRun != nil {
		r.onRun(req)
	}
	return r.result, r.err
}

func (r *asyncRecordingRunner) StartAsync(ctx context.Context, req toolexec.CommandRequest) (string, error) {
	_, err := r.Run(ctx, req)
	if err != nil {
		return "", err
	}
	if r.startSessionID == "" {
		r.startSessionID = "bash-session-1"
	}
	return r.startSessionID, nil
}

func (r *asyncRecordingRunner) WriteInput(sessionID string, input []byte) error {
	_ = sessionID
	_ = input
	return nil
}

func (r *asyncRecordingRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	_ = sessionID
	_ = stdoutMarker
	_ = stderrMarker
	return r.readResult.stdout, r.readResult.stderr, r.readResult.stdoutMarker, r.readResult.stderrMarker, nil
}

func (r *asyncRecordingRunner) GetSessionStatus(sessionID string) (toolexec.SessionStatus, error) {
	_ = sessionID
	return r.status, nil
}

func (r *asyncRecordingRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (toolexec.CommandResult, error) {
	_ = ctx
	_ = sessionID
	_ = timeout
	return r.result, r.err
}

func (r *asyncRecordingRunner) TerminateSession(sessionID string) error {
	_ = sessionID
	return nil
}

func (r *asyncRecordingRunner) ListSessions() []toolexec.SessionInfo {
	return nil
}

func (s *stubTaskManager) StartBash(_ context.Context, req task.BashStartRequest) (task.Snapshot, error) {
	s.lastBash = req
	return s.startBash, nil
}

func (s *stubTaskManager) StartDelegate(context.Context, task.DelegateStartRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Wait(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Status(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Write(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubFileSystem) Getwd() (string, error)                      { return s.cwd, nil }
func (s stubFileSystem) UserHomeDir() (string, error)                { return s.home, nil }
func (s stubFileSystem) Open(string) (*os.File, error)               { return nil, fs.ErrNotExist }
func (s stubFileSystem) ReadDir(string) ([]os.DirEntry, error)       { return nil, fs.ErrNotExist }
func (s stubFileSystem) Stat(string) (os.FileInfo, error)            { return nil, fs.ErrNotExist }
func (s stubFileSystem) ReadFile(string) ([]byte, error)             { return nil, fs.ErrNotExist }
func (s stubFileSystem) WriteFile(string, []byte, os.FileMode) error { return fs.ErrPermission }
func (s stubFileSystem) Glob(string) ([]string, error)               { return nil, nil }
func (s stubFileSystem) WalkDir(string, fs.WalkDirFunc) error        { return nil }

func (s *stubTaskManager) Cancel(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) List(context.Context) ([]task.Snapshot, error) {
	return nil, nil
}

type failingProbeRunner struct {
	recordingRunner
	probeErr error
}

func (r *failingProbeRunner) Probe(ctx context.Context) error {
	_ = ctx
	return r.probeErr
}

type fixedApprover struct {
	allow bool
}

func (a fixedApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	_ = req
	return a.allow, nil
}

func withSandboxFallbackDecision(ctx context.Context) context.Context {
	decision := policy.DecisionWithRoute(policy.Decision{
		Effect: policy.DecisionEffectAllow,
	}, policy.DecisionRouteSandbox)
	if decision.Metadata == nil {
		decision.Metadata = map[string]any{}
	}
	decision.Metadata[policy.DecisionMetaFallbackOnCommandNotFound] = true
	return policy.WithToolDecision(ctx, decision)
}

func TestBash_DefaultSafeCommandRunsInSandbox(t *testing.T) {
	host := &recordingRunner{}
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "sandbox-ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{"command": "ls -la"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected sandbox runner called once, got %d", len(sandbox.calls))
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host runner not called, got %d", len(host.calls))
	}
	if out["stdout"] != "sandbox-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_StreamsCommandOutputThroughContext(t *testing.T) {
	var got []toolexec.OutputChunk
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{Stdout: "done"},
		onRun: func(req toolexec.CommandRequest) {
			if req.OnOutput == nil {
				t.Fatal("expected output callback on command request")
			}
			req.OnOutput(toolexec.CommandOutputChunk{Stream: "stdout", Text: "line-1\n"})
			req.OnOutput(toolexec.CommandOutputChunk{Stream: "stderr", Text: "warn-1\n"})
		},
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     &recordingRunner{},
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithToolCallInfo(context.Background(), BashToolName, "call-1")
	ctx = toolexec.WithOutputStreamer(ctx, toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
		got = append(got, chunk)
	}))
	if _, err := tool.Run(ctx, map[string]any{"command": "echo hi"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 streamed output chunks, got %d", len(got))
	}
	if got[0].ToolName != BashToolName || got[0].ToolCallID != "call-1" || got[0].Stream != "stdout" {
		t.Fatalf("unexpected first output chunk: %+v", got[0])
	}
	if got[1].Stream != "stderr" || strings.TrimSpace(got[1].Text) != "warn-1" {
		t.Fatalf("unexpected second output chunk: %+v", got[1])
	}
}

func TestBash_YieldReturnsSharedTaskHandle(t *testing.T) {
	host := &asyncRecordingRunner{
		status: toolexec.SessionStatus{
			ID:      "bash-session-1",
			Command: "sleep 1",
			State:   toolexec.SessionStateRunning,
		},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := task.WithManager(context.Background(), &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	})
	out, err := tool.Run(ctx, map[string]any{
		"command":       "sleep 1",
		"yield_time_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected yielded task handle, got %#v", out)
	}
}

func TestBash_DefaultUnsafeCommandRunsInSandboxWithoutApprovalWhenNoApprover(t *testing.T) {
	host := &recordingRunner{}
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "sandbox-ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{"command": "python3 app.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host runner not called, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected sandbox runner called once, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "sandbox-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_DefaultUnsafeCommandWithApprovalStillRunsInSandbox(t *testing.T) {
	host := &recordingRunner{}
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "sandbox-ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{"command": "python3 app.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host runner not called, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected sandbox runner called once, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "sandbox-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_FullControlRunsOnHostWithoutApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"command":       "cat <<'EOF' > a.txt\nx\nEOF",
		"yield_time_ms": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if out["stdout"] != "host-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_RequireEscalatedBoolForcesHostApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-approved"}}
	sandbox := &recordingRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{
		"command":           "python3 app.py",
		"require_escalated": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "host-approved" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_RequireEscalatedWhitelistedCommandSkipsApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-whitelisted"}}
	sandbox := &recordingRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"command":           "cd /tmp && ls -la *.png",
		"require_escalated": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "host-whitelisted" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_RequireEscalatedDeniedStopsExecution(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-approved"}}
	sandbox := &recordingRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: false})
	_, err = tool.Run(ctx, map[string]any{
		"command":           "python3 app.py",
		"require_escalated": true,
	})
	if err == nil {
		t.Fatal("expected approval denied error")
	}
	if !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
	if !toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalAborted) {
		t.Fatalf("expected approval-aborted code %q, got %q", toolexec.ErrorCodeApprovalAborted, toolexec.ErrorCodeOf(err))
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host runner not called when approval denied, got %d", len(host.calls))
	}
}

func TestBash_ConsumesPolicyDecisionRequireApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-approved"}}
	sandbox := &recordingRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	ctx = policy.WithToolDecision(ctx, policy.DecisionWithRoute(policy.Decision{
		Effect: policy.DecisionEffectRequireApproval,
		Reason: "policy requires host route",
	}, policy.DecisionRouteHost))

	out, err := tool.Run(ctx, map[string]any{"command": "ls -la"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "host-approved" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_ConsumesPolicyDecisionDeny(t *testing.T) {
	host := &recordingRunner{}
	sandbox := &recordingRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := policy.WithToolDecision(context.Background(), policy.Decision{
		Effect: policy.DecisionEffectDeny,
		Reason: "blocked by policy",
	})
	_, err = tool.Run(ctx, map[string]any{"command": "ls -la"})
	if err == nil {
		t.Fatal("expected policy denial error")
	}
	if len(host.calls) != 0 || len(sandbox.calls) != 0 {
		t.Fatalf("expected no runner call, got host=%d sandbox=%d", len(host.calls), len(sandbox.calls))
	}
}

func TestBash_DefaultFallbackAllCommandsNeedApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-fallback"}}
	fallbackSandbox := &failingProbeRunner{probeErr: errors.New("bwrap unavailable")}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  fallbackSandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rt.FallbackToHost() {
		t.Fatal("expected fallback to host")
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{"command": "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if out["stdout"] != "host-fallback" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_InvalidRequireEscalatedType(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     &recordingRunner{},
		SandboxRunner:  &recordingRunner{},
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
		"command":           "ls",
		"require_escalated": "invalid",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestBash_TTYWithoutYieldAutomaticallyBecomesTask(t *testing.T) {
	host := &asyncRecordingRunner{
		status: toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		readResult: struct {
			stdout       []byte
			stderr       []byte
			stdoutMarker int64
			stderrMarker int64
		}{
			stdout:       []byte("What is your name?\n"),
			stdoutMarker: int64(len("What is your name?\n")),
		},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := task.WithManager(context.Background(), &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:         "t-bash-1",
			Kind:           task.KindBash,
			State:          task.StateRunning,
			Running:        true,
			Yielded:        true,
			SupportsInput:  true,
			SupportsCancel: true,
			Output:         task.Output{Stdout: "What is your name?\n"},
		},
	})
	out, err := tool.Run(ctx, map[string]any{
		"tty":     true,
		"command": `bash -c 'echo "What is your name?"; read name; echo "Hello $name"'`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected interactive command to yield task handle, got %#v", out)
	}
	if got := out["supports_input"]; got != true {
		t.Fatalf("expected interactive command to expose supports_input, got %#v", out)
	}
}

func TestBash_DefaultYieldInFullControlStartsTask(t *testing.T) {
	host := &asyncRecordingRunner{
		status:         toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	}
	ctx := task.WithManager(context.Background(), manager)
	out, err := tool.Run(ctx, map[string]any{"command": "sleep 1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected default yield task handle, got %#v", out)
	}
	if manager.lastBash.Yield != defaultBashWait {
		t.Fatalf("expected default wait %s, got %s", defaultBashWait, manager.lastBash.Yield)
	}
}

func TestBash_DefaultYieldInSandboxStartsSandboxTask(t *testing.T) {
	sandbox := &asyncRecordingRunner{
		status:         toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		startSessionID: "sandbox-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     &recordingRunner{},
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	}
	out, err := tool.Run(task.WithManager(context.Background(), manager), map[string]any{"command": "sleep 1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected sandbox task handle, got %#v", out)
	}
	if manager.lastBash.Route != string(toolexec.ExecutionRouteSandbox) {
		t.Fatalf("expected sandbox route, got %+v", manager.lastBash)
	}
	if manager.lastBash.Yield != defaultBashWait {
		t.Fatalf("expected default wait %s, got %s", defaultBashWait, manager.lastBash.Yield)
	}
}

func TestBash_NilYieldUsesDefaultWait(t *testing.T) {
	host := &asyncRecordingRunner{
		status:         toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	}
	ctx := task.WithManager(context.Background(), manager)
	out, err := tool.Run(ctx, map[string]any{
		"command":       "sleep 1",
		"yield_time_ms": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected default yield task handle, got %#v", out)
	}
	if manager.lastBash.Yield != defaultBashWait {
		t.Fatalf("expected nil yield to fall back to default wait %s, got %s", defaultBashWait, manager.lastBash.Yield)
	}
}

func TestBash_ExplicitNegativeYieldFallsBackToDefaultWait(t *testing.T) {
	host := &asyncRecordingRunner{
		status:         toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	}
	out, err := tool.Run(task.WithManager(context.Background(), manager), map[string]any{
		"command":       "ls",
		"yield_time_ms": -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected default-yield task handle, got %#v", out)
	}
	if manager.lastBash.Yield != defaultBashWait {
		t.Fatalf("expected negative yield to fall back to default wait %s, got %s", defaultBashWait, manager.lastBash.Yield)
	}
}

func TestBash_ExplicitZeroYieldUsesDefaultWait(t *testing.T) {
	host := &asyncRecordingRunner{
		status:         toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		startSessionID: "bash-session-1",
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     host,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		startBash: task.Snapshot{
			TaskID:  "t-bash-1",
			Kind:    task.KindBash,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
	}
	out, err := tool.Run(task.WithManager(context.Background(), manager), map[string]any{
		"command":       "sleep 1",
		"yield_time_ms": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["task_id"]; got != "t-bash-1" {
		t.Fatalf("expected default-yield task handle, got %#v", out)
	}
	if manager.lastBash.Yield != defaultBashWait {
		t.Fatalf("expected explicit zero to fall back to default wait %s, got %s", defaultBashWait, manager.lastBash.Yield)
	}
}

func TestBash_SandboxCommandMissingEscalatesToHostAfterApproval(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{
			Stderr:   "sh: grep: command not found",
			ExitCode: 127,
		},
		err: errors.New("sandbox command failed"),
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	ctx = withSandboxFallbackDecision(ctx)
	out, err := tool.Run(ctx, map[string]any{"command": "grep foo bar.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected sandbox called once, got %d", len(sandbox.calls))
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host called once, got %d", len(host.calls))
	}
	if out["stdout"] != "host-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_SandboxCommandMissingRequiresApprovalWhenNoApprover(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{
			Stderr:   "grep: not found",
			ExitCode: 127,
		},
		err: errors.New("sandbox command failed"),
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withSandboxFallbackDecision(context.Background())
	_, err = tool.Run(ctx, map[string]any{"command": "grep foo bar.txt"})
	if err == nil {
		t.Fatal("expected approval required")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected approval-required error, got: %v", err)
	}
	if !toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalRequired) {
		t.Fatalf("expected approval-required code %q, got %q", toolexec.ErrorCodeApprovalRequired, toolexec.ErrorCodeOf(err))
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host not called without approval, got %d", len(host.calls))
	}
}

func TestBash_SandboxCommandMissingNoPolicyFallbackDoesNotEscalate(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{
			Stderr:   "grep: not found",
			ExitCode: 127,
		},
		err: errors.New("sandbox command failed"),
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{"command": "grep foo bar.txt"})
	if err == nil {
		t.Fatal("expected sandbox error")
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host not called without policy fallback, got %d", len(host.calls))
	}
}

func TestBash_SandboxErrorNonMissingDoesNotEscalate(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{
			Stderr:   "grep: runtime error",
			ExitCode: 1,
		},
		err: errors.New("sandbox build failed"),
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{"command": "grep foo bar.txt"})
	if err == nil {
		t.Fatal("expected sandbox error")
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host not called on non-missing sandbox error, got %d", len(host.calls))
	}
}

func TestBash_ErrorIncludesRouteForDebug(t *testing.T) {
	sandbox := &recordingRunner{
		result: toolexec.CommandResult{
			Stdout:   "node: cannot find module",
			ExitCode: 1,
		},
		err: errors.New("sandbox command failed"),
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     &recordingRunner{},
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{"command": "node script.js"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route=sandbox") {
		t.Fatalf("expected route in error message, got: %v", err)
	}
}

func TestBash_DefaultsWorkdirFromRuntimeFileSystem(t *testing.T) {
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		FileSystem:     stubFileSystem{cwd: "/workspace/subdir", home: "/home/tester"},
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"command": "pwd"}); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected one sandbox call, got %d", len(sandbox.calls))
	}
	if sandbox.calls[0].Dir != "/workspace/subdir" {
		t.Fatalf("expected runtime cwd as default workdir, got %q", sandbox.calls[0].Dir)
	}
}

func TestBash_ACPXCommandRequiresHostEscalation(t *testing.T) {
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		FileSystem:     stubFileSystem{cwd: "/repo", home: "/home/tester"},
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{"command": "acpx codex exec 'inspect repo'"})
	if err == nil {
		t.Fatal("expected approval-required error")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected ApprovalRequiredError, got %T: %v", err, err)
	}
	if !strings.Contains(approvalErr.Reason, "operation not permitted") || !strings.Contains(approvalErr.Reason, "require_escalated=true") {
		t.Fatalf("unexpected approval reason: %q", approvalErr.Reason)
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d calls", len(sandbox.calls))
	}
}

func TestBash_ACPXCommandRunsOnHostWhenEscalated(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "sandbox-ok"}}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		FileSystem:     stubFileSystem{cwd: "/repo", home: "/home/tester"},
		HostRunner:     host,
		SandboxRunner:  sandbox,
		SandboxType:    testSandboxType(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{
		"command":           "acpx codex exec 'inspect repo'",
		"require_escalated": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != "host-ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
	if host.calls[0].Command != "acpx codex exec 'inspect repo'" {
		t.Fatalf("expected host command unchanged, got %q", host.calls[0].Command)
	}
}
