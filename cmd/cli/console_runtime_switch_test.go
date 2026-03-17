package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func (panicSender) Send(msg any) {
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

func (r fakeRuntime) PermissionMode() toolexec.PermissionMode { return r.permissionMode }
func (r fakeRuntime) SandboxType() string                     { return r.sandboxType }
func (r fakeRuntime) SandboxPolicy() toolexec.SandboxPolicy   { return toolexec.SandboxPolicy{} }
func (r fakeRuntime) FallbackToHost() bool                    { return r.fallbackToHost }
func (r fakeRuntime) FallbackReason() string                  { return r.fallbackReason }
func (r fakeRuntime) FileSystem() toolexec.FileSystem         { return nil }
func (r fakeRuntime) HostRunner() toolexec.CommandRunner      { return r.hostRunner }
func (r fakeRuntime) SandboxRunner() toolexec.CommandRunner {
	if r.permissionMode == toolexec.PermissionModeFullControl && r.hostRunner != nil {
		return r.hostRunner
	}
	return r.sandboxRunner
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
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
			fallbackToHost: true,
			fallbackReason: "probe failed",
		}, nil
	}

	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{execRuntime: rt, sandboxType: cliTestSandboxType()}
	_, err = handleSandbox(console, []string{"bwrap"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateExecutionRuntime_ClosesPreviousRuntime(t *testing.T) {
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
	if sandboxRunner.closed != 1 {
		t.Fatalf("expected previous runtime closed once, got %d", sandboxRunner.closed)
	}
}
