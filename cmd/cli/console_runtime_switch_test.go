package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	kernelpolicy "github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

type closeableSwitchRunner struct {
	closed int
}

type panicSender struct{}

func (panicSender) Send(_ any) {
	panic("unexpected TUI send")
}

type fakeRuntime struct {
	permissionMode toolexec.PermissionMode
	sandboxType    string
	fallbackToHost bool
	fallbackReason string
	hostRunner     toolexec.CommandRunner
	sandboxRunner  toolexec.CommandRunner
}

type testRuntimeSession struct {
	ref    toolexec.CommandSessionRef
	runner toolexec.AsyncCommandRunner
}

func (s *testRuntimeSession) Ref() toolexec.CommandSessionRef { return s.ref }
func (s *testRuntimeSession) WriteInput(input []byte) error {
	return s.runner.WriteInput(s.ref.SessionID, input)
}
func (s *testRuntimeSession) ReadOutput(stdoutMarker, stderrMarker int64) ([]byte, []byte, int64, int64, error) {
	return s.runner.ReadOutput(s.ref.SessionID, stdoutMarker, stderrMarker)
}
func (s *testRuntimeSession) Status() (toolexec.SessionStatus, error) {
	return s.runner.GetSessionStatus(s.ref.SessionID)
}
func (s *testRuntimeSession) Wait(ctx context.Context, timeout time.Duration) (toolexec.CommandResult, error) {
	return s.runner.WaitSession(ctx, s.ref.SessionID, timeout)
}
func (s *testRuntimeSession) Terminate() error { return s.runner.TerminateSession(s.ref.SessionID) }

func runtimeSessionFromRunner(ctx context.Context, backend string, runner toolexec.CommandRunner, req toolexec.CommandRequest) (toolexec.Session, error) {
	async, ok := runner.(toolexec.AsyncCommandRunner)
	if !ok || async == nil {
		return nil, fmt.Errorf("async execution unavailable")
	}
	sessionID, err := async.StartAsync(ctx, req)
	if err != nil {
		return nil, err
	}
	return &testRuntimeSession{
		ref:    toolexec.CommandSessionRef{Backend: backend, SessionID: sessionID},
		runner: async,
	}, nil
}

func runtimeSessionFromRef(backend string, runner toolexec.CommandRunner, ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	async, ok := runner.(toolexec.AsyncCommandRunner)
	if !ok || async == nil {
		return nil, fmt.Errorf("async execution unavailable")
	}
	return &testRuntimeSession{
		ref:    toolexec.CommandSessionRef{Backend: backend, SessionID: strings.TrimSpace(ref.SessionID)},
		runner: async,
	}, nil
}

func (r fakeRuntime) PermissionMode() toolexec.PermissionMode { return r.permissionMode }
func (r fakeRuntime) SandboxType() string                     { return r.sandboxType }
func (r fakeRuntime) SandboxPolicy() toolexec.SandboxPolicy   { return toolexec.SandboxPolicy{} }
func (r fakeRuntime) FallbackToHost() bool                    { return r.fallbackToHost }
func (r fakeRuntime) FallbackReason() string                  { return r.fallbackReason }
func (r fakeRuntime) Diagnostics() toolexec.SandboxDiagnostics {
	return toolexec.SandboxDiagnostics{
		ResolvedType:   r.sandboxType,
		FallbackToHost: r.fallbackToHost,
		FallbackReason: r.fallbackReason,
	}
}
func (r fakeRuntime) FileSystem() toolexec.FileSystem { return nil }
func (r fakeRuntime) State() toolexec.RuntimeState {
	return toolexec.RuntimeState{
		Mode:             r.permissionMode,
		RequestedSandbox: r.sandboxType,
		ResolvedSandbox:  r.sandboxType,
		FallbackReason:   r.fallbackReason,
	}
}
func (r fakeRuntime) Execute(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	if decision := r.DecideRoute(req.Command, req.SandboxPermission); decision.Route == toolexec.ExecutionRouteHost && r.hostRunner != nil {
		return r.hostRunner.Run(ctx, req)
	}
	runner := r.sandboxRunner
	if r.permissionMode == toolexec.PermissionModeFullControl && r.hostRunner != nil {
		runner = r.hostRunner
	}
	if runner != nil {
		return runner.Run(ctx, req)
	}
	return toolexec.CommandResult{}, fmt.Errorf("runner unavailable")
}
func (r fakeRuntime) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	decision := r.DecideRoute(req.Command, req.SandboxPermission)
	if decision.Route == toolexec.ExecutionRouteHost {
		return runtimeSessionFromRunner(ctx, "host", r.hostRunner, req)
	}
	return runtimeSessionFromRunner(ctx, strings.TrimSpace(r.sandboxType), r.sandboxRunner, req)
}
func (r fakeRuntime) OpenSession(ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	if strings.EqualFold(strings.TrimSpace(ref.Backend), "host") || strings.TrimSpace(ref.Backend) == "" {
		return runtimeSessionFromRef("host", r.hostRunner, ref)
	}
	return runtimeSessionFromRef(strings.TrimSpace(ref.Backend), r.sandboxRunner, ref)
}
func (r *fakeRuntime) SetPermissionMode(mode toolexec.PermissionMode) error {
	r.permissionMode = mode
	return nil
}
func (r fakeRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	if r.permissionMode == toolexec.PermissionModeFullControl {
		return toolexec.CommandDecision{Route: toolexec.ExecutionRouteHost}
	}
	return toolexec.CommandDecision{Route: toolexec.ExecutionRouteSandbox}
}

func (r *closeableSwitchRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return toolexec.CommandResult{}, nil
}

func (r *closeableSwitchRunner) Close() error {
	r.closed++
	return nil
}

type recordingSwitchRunner struct {
	calls []toolexec.CommandRequest
}

func (r *recordingSwitchRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	r.calls = append(r.calls, req)
	return toolexec.CommandResult{Stdout: "ok"}, nil
}

func (r *recordingSwitchRunner) StartAsync(ctx context.Context, req toolexec.CommandRequest) (string, error) {
	if _, err := r.Run(ctx, req); err != nil {
		return "", err
	}
	return "bash-session-1", nil
}

func (r *recordingSwitchRunner) WriteInput(string, []byte) error { return nil }

func (r *recordingSwitchRunner) ReadOutput(string, int64, int64) ([]byte, []byte, int64, int64, error) {
	return []byte("ok"), nil, int64(len("ok")), 0, nil
}

func (r *recordingSwitchRunner) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	return toolexec.SessionStatus{
		ID:       "bash-session-1",
		State:    toolexec.SessionStateCompleted,
		ExitCode: 0,
	}, nil
}

func (r *recordingSwitchRunner) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{Stdout: "ok"}, nil
}

func (r *recordingSwitchRunner) TerminateSession(string) error { return nil }

func (r *recordingSwitchRunner) ListSessions() []toolexec.SessionInfo { return nil }

func TestHandlePermission_SwitchMode(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:       context.Background(),
		execRuntime:   rt,
		sandboxType:   cliTestSandboxType(),
		resolved:      &appassembly.ResolvedSpec{Tools: []tool.Tool{bashTool}},
		showReasoning: true,
	}
	_, err = handlePermission(console, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeDefault {
		t.Fatalf("expected default mode, got %q", console.execRuntime.PermissionMode())
	}
	if console.sessionMode != sessionmode.DefaultMode {
		t.Fatalf("expected session mode %q, got %q", sessionmode.DefaultMode, console.sessionMode)
	}
	if len(console.resolved.Tools) != 1 || console.resolved.Tools[0] == nil || console.resolved.Tools[0].Name() != toolshell.BashToolName {
		t.Fatalf("expected refreshed BASH tool, got %+v", console.resolved.Tools)
	}
}

func TestHandlePermission_FullControlEnablesFullAccessSessionMode(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:     context.Background(),
		execRuntime: rt,
		sandboxType: cliTestSandboxType(),
		sessionMode: sessionmode.DefaultMode,
		resolved:    &appassembly.ResolvedSpec{},
	}
	_, err = handlePermission(console, []string{"full_control"})
	if err != nil {
		t.Fatal(err)
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected full_control permission mode, got %q", console.execRuntime.PermissionMode())
	}
	if console.sessionMode != sessionmode.FullMode {
		t.Fatalf("expected session mode %q, got %q", sessionmode.FullMode, console.sessionMode)
	}
}

func TestHandlePermission_DefaultPreservesPlanMode(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:     context.Background(),
		execRuntime: rt,
		sandboxType: cliTestSandboxType(),
		sessionMode: sessionmode.PlanMode,
		resolved:    &appassembly.ResolvedSpec{},
	}
	_, err = handlePermission(console, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if console.sessionMode != sessionmode.PlanMode {
		t.Fatalf("expected plan mode to remain active, got %q", console.sessionMode)
	}
}

func TestSyncSessionModeFromStore_ClampsPersistedFullAccessToCurrentPermission(t *testing.T) {
	store := inmemory.New()
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "s-restore",
		sessionStore: store,
		sessionMode:  sessionmode.DefaultMode,
	}
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(rt) }()
	console.execRuntime = rt
	sess := &session.Session{AppName: console.appName, UserID: console.userID, ID: console.sessionID}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), sess, sessionmode.StoreSnapshot(map[string]any{}, sessionmode.FullMode)); err != nil {
		t.Fatal(err)
	}

	console.syncSessionModeFromStore()

	if console.execRuntime.PermissionMode() != toolexec.PermissionModeDefault {
		t.Fatalf("expected runtime permission to stay default, got %q", console.execRuntime.PermissionMode())
	}
	if console.sessionMode != sessionmode.DefaultMode {
		t.Fatalf("expected restored session mode to clamp to default, got %q", console.sessionMode)
	}
}

func TestSyncSessionModeFromStore_DoesNotPersistRuntimeDefaults(t *testing.T) {
	store := inmemory.New()
	cfg := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	if err := cfg.save(); err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "s-config",
		sessionStore: store,
		configStore:  cfg,
	}
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(rt) }()
	console.execRuntime = rt
	sess := &session.Session{AppName: console.appName, UserID: console.userID, ID: console.sessionID}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), sess, sessionmode.StoreSnapshot(map[string]any{}, sessionmode.FullMode)); err != nil {
		t.Fatal(err)
	}

	console.syncSessionModeFromStore()

	if cfg.PermissionMode() != "default" {
		t.Fatalf("expected global permission default to stay unchanged, got %q", cfg.PermissionMode())
	}
}

func TestSetSessionMode_FullAccessDoesNotPersistGlobalRuntimeDefaults(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	cfg := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	if err := cfg.save(); err != nil {
		t.Fatal(err)
	}
	store := inmemory.New()
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "s-session-mode",
		sessionStore: store,
		configStore:  cfg,
		execRuntime:  rt,
		sandboxType:  cliTestSandboxType(),
		resolved:     &appassembly.ResolvedSpec{},
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{
		AppName: console.appName,
		UserID:  console.userID,
		ID:      console.sessionID,
	}); err != nil {
		t.Fatal(err)
	}

	if err := console.setSessionMode(sessionmode.FullMode); err != nil {
		t.Fatal(err)
	}

	if console.execRuntime.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected current runtime to switch to full_control, got %q", console.execRuntime.PermissionMode())
	}
	if cfg.PermissionMode() != "default" {
		t.Fatalf("expected global runtime default to stay default, got %q", cfg.PermissionMode())
	}
}

func TestSetSessionMode_DoesNotSendTUIStatusInline(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	store := inmemory.New()
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "s-no-send",
		sessionStore: store,
		execRuntime:  rt,
		sandboxType:  cliTestSandboxType(),
		resolved:     &appassembly.ResolvedSpec{},
		tuiSender:    panicSender{},
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{
		AppName: console.appName,
		UserID:  console.userID,
		ID:      console.sessionID,
	}); err != nil {
		t.Fatal(err)
	}

	if err := console.setSessionMode(sessionmode.PlanMode); err != nil {
		t.Fatal(err)
	}
}

func TestSwappableRuntime_TracksPermissionSwitchForPoliciesAndBash(t *testing.T) {
	host := &recordingSwitchRunner{}
	runtimeView := newSwappableRuntime(fakeRuntime{
		permissionMode: toolexec.PermissionModeDefault,
		sandboxType:    cliTestSandboxType(),
		hostRunner:     host,
	})
	hook := kernelpolicy.RouteCommandExecution(kernelpolicy.CommandExecutionConfig{
		Runtime:  runtimeView,
		ToolName: toolshell.BashToolName,
	})
	bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: runtimeView})
	if err != nil {
		t.Fatal(err)
	}

	runtimeView.Set(fakeRuntime{
		permissionMode: toolexec.PermissionModeFullControl,
		sandboxType:    cliTestSandboxType(),
		hostRunner:     host,
	})

	toolIn, err := hook.BeforeTool(context.Background(), kernelpolicy.ToolInput{
		Call: model.ToolCall{Name: toolshell.BashToolName},
		Args: map[string]any{"command": "pwd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	route, ok := kernelpolicy.DecisionRouteFromMetadata(toolIn.Decision)
	if !ok || route != kernelpolicy.DecisionRouteHost {
		t.Fatalf("expected policy hook to see updated host route, got decision %+v", toolIn.Decision)
	}

	ctx := kernelpolicy.WithToolDecision(context.Background(), toolIn.Decision)
	if _, err := bashTool.Run(ctx, map[string]any{"command": "pwd"}); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner to execute BASH after runtime swap, got %d calls", len(host.calls))
	}
}

func TestExecutionRuntimeForSession_PrefersSwappableRuntime(t *testing.T) {
	host := &recordingSwitchRunner{}
	runtimeView := newSwappableRuntime(fakeRuntime{
		permissionMode: toolexec.PermissionModeDefault,
		sandboxType:    cliTestSandboxType(),
		hostRunner:     host,
	})
	console := &cliConsole{
		execRuntime:     fakeRuntime{permissionMode: toolexec.PermissionModeFullControl},
		execRuntimeView: runtimeView,
	}
	if got := console.executionRuntimeForSession(); got != runtimeView {
		t.Fatalf("expected session runtime to prefer swappable view, got %#v", got)
	}
}

func TestHandlePermission_PersistsGlobalRuntimeDefaults(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	cfg := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	if err := cfg.save(); err != nil {
		t.Fatal(err)
	}
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeDefault, SandboxType: cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:     context.Background(),
		configStore: cfg,
		execRuntime: rt,
		sandboxType: cliTestSandboxType(),
		sessionMode: sessionmode.DefaultMode,
		resolved:    &appassembly.ResolvedSpec{},
	}

	if _, err := handlePermission(console, []string{"full_control"}); err != nil {
		t.Fatal(err)
	}

	if cfg.PermissionMode() != "full_control" {
		t.Fatalf("expected explicit /permission to persist global default, got %q", cfg.PermissionMode())
	}
}

func TestHandlePermission_InvalidMode(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{execRuntime: rt, sandboxType: cliTestSandboxType()}
	_, err = handlePermission(console, []string{"invalid"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandleSandbox_UnknownType(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{execRuntime: rt, sandboxType: cliTestSandboxType()}
	_, err = handleSandbox(console, []string{"unknown-type"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown sandbox type") &&
		!strings.Contains(err.Error(), "unsupported on darwin") &&
		!strings.Contains(err.Error(), "unsupported on linux") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSandbox_InFullControlOnlyUpdatesConfig(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{execRuntime: rt, sandboxType: cliTestSandboxType()}
	_, err = handleSandbox(console, []string{cliTestSandboxType()})
	if err != nil {
		t.Fatal(err)
	}
	if console.sandboxType != cliTestSandboxType() {
		t.Fatalf("expected sandbox type %s, got %q", cliTestSandboxType(), console.sandboxType)
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected mode to remain full_control, got %q", console.execRuntime.PermissionMode())
	}
}

func TestHandleSandbox_RejectsUnavailableSelection(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	prevSelector := cliSandboxSelector
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
		cliSandboxSelector = prevSelector
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
			fallbackToHost: true,
			fallbackReason: "probe failed",
		}, nil
	}
	cliSandboxSelector = func(cfg toolexec.Config) (toolexec.CommandRunner, toolexec.SandboxDiagnostics, error) {
		return nil, toolexec.SandboxDiagnostics{
			RequestedType:  cfg.SandboxType,
			ResolvedType:   cfg.SandboxType,
			FallbackToHost: true,
			FallbackReason: "probe failed",
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{execRuntime: rt, sandboxType: cliTestSandboxType()}
	_, err = handleSandbox(console, []string{cliTestSandboxType()})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateExecutionRuntime_ReusesRuntimeForPermissionOnlySwitch(t *testing.T) {
	sandboxRunner := &closeableSwitchRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    cliTestSandboxType(),
		SandboxRunner:  sandboxRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:     context.Background(),
		execRuntime: rt,
		sandboxType: cliTestSandboxType(),
		resolved:    &appassembly.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeFullControl, cliTestSandboxType()); err != nil {
		t.Fatal(err)
	}
	if console.execRuntime != rt {
		t.Fatal("expected permission-only switch to keep the same runtime instance")
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected runtime mode to switch in place, got %q", console.execRuntime.PermissionMode())
	}
	if sandboxRunner.closed != 0 {
		t.Fatalf("expected sandbox runner to stay open on permission-only switch, got %d closes", sandboxRunner.closed)
	}
}

func TestUpdateExecutionRuntime_ReusesRuntimeForPermissionOnlySwitchWithAutoSelection(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		t.Fatalf("unexpected runtime rebuild: %+v", cfg)
		return nil, errors.New("unexpected runtime rebuild")
	}

	rt := &fakeRuntime{
		permissionMode: toolexec.PermissionModeDefault,
		sandboxType:    "bwrap",
	}
	console := &cliConsole{
		baseCtx:               context.Background(),
		execRuntime:           rt,
		sandboxType:           "",
		appliedSandboxType:    "",
		appliedSandboxTypeSet: true,
		resolved:              &appassembly.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeFullControl, ""); err != nil {
		t.Fatal(err)
	}
	if console.execRuntime != rt {
		t.Fatal("expected auto-selection permission switch to keep the same runtime instance")
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected runtime mode to switch in place, got %q", console.execRuntime.PermissionMode())
	}
}

func TestUpdateExecutionRuntime_RebuildsOnceAfterDeferredSandboxChange(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})

	builds := 0
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		builds++
		return &fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
		}, nil
	}

	current := &fakeRuntime{
		permissionMode: toolexec.PermissionModeFullControl,
		sandboxType:    "bwrap",
	}
	console := &cliConsole{
		baseCtx:               context.Background(),
		execRuntime:           current,
		sandboxType:           "landlock",
		appliedSandboxType:    "",
		appliedSandboxTypeSet: true,
		resolved:              &appassembly.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeDefault, console.sandboxType); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatalf("expected exactly one rebuild, got %d", builds)
	}
	if console.execRuntime == current {
		t.Fatal("expected deferred sandbox change to rebuild runtime")
	}
	if console.execRuntime.SandboxType() != "landlock" {
		t.Fatalf("expected rebuilt runtime to use deferred sandbox selection, got %q", console.execRuntime.SandboxType())
	}
	if console.appliedSandboxType != "landlock" {
		t.Fatalf("expected applied sandbox selection updated after rebuild, got %q", console.appliedSandboxType)
	}
}

func TestUpdateExecutionRuntime_RebuildsWhenRequestedSandboxChanged(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{permissionMode: cfg.PermissionMode, sandboxType: cfg.SandboxType}, nil
	}

	sandboxRunner := &closeableSwitchRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    cliTestSandboxType(),
		SandboxRunner:  sandboxRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:               context.Background(),
		execRuntime:           rt,
		sandboxType:           "other-sandbox",
		appliedSandboxType:    cliTestSandboxType(),
		appliedSandboxTypeSet: true,
		resolved:              &appassembly.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeDefault, console.sandboxType); err != nil {
		t.Fatal(err)
	}
	if console.execRuntime == rt {
		t.Fatal("expected sandbox change to rebuild runtime even after console state was updated")
	}
	if console.execRuntime.SandboxType() != "other-sandbox" {
		t.Fatalf("expected rebuilt runtime to use updated sandbox, got %q", console.execRuntime.SandboxType())
	}
	if sandboxRunner.closed != 1 {
		t.Fatalf("expected previous runtime closed once on sandbox change, got %d closes", sandboxRunner.closed)
	}
}

func TestUpdateExecutionRuntime_ClosesPreviousRuntimeOnRebuild(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{permissionMode: cfg.PermissionMode, sandboxType: cfg.SandboxType}, nil
	}

	sandboxRunner := &closeableSwitchRunner{}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    cliTestSandboxType(),
		SandboxRunner:  sandboxRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:     context.Background(),
		execRuntime: rt,
		sandboxType: cliTestSandboxType(),
		resolved:    &appassembly.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeDefault, "other-sandbox"); err != nil {
		t.Fatal(err)
	}
	if sandboxRunner.closed != 1 {
		t.Fatalf("expected previous runtime closed once, got %d", sandboxRunner.closed)
	}
}
