package main

import (
	"context"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

type closeableSwitchRunner struct {
	closed int
}

type fakeRuntime struct {
	permissionMode toolexec.PermissionMode
	sandboxType    string
	fallbackToHost bool
	fallbackReason string
}

func (r fakeRuntime) PermissionMode() toolexec.PermissionMode { return r.permissionMode }
func (r fakeRuntime) SandboxType() string                     { return r.sandboxType }
func (r fakeRuntime) SandboxPolicy() toolexec.SandboxPolicy   { return toolexec.SandboxPolicy{} }
func (r fakeRuntime) FallbackToHost() bool                    { return r.fallbackToHost }
func (r fakeRuntime) FallbackReason() string                  { return r.fallbackReason }
func (r fakeRuntime) FileSystem() toolexec.FileSystem         { return nil }
func (r fakeRuntime) HostRunner() toolexec.CommandRunner      { return nil }
func (r fakeRuntime) SandboxRunner() toolexec.CommandRunner   { return nil }
func (r fakeRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
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
		resolved:      &bootstrap.ResolvedSpec{Tools: []tool.Tool{bashTool}},
		showReasoning: true,
	}
	_, err = handlePermission(console, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if console.execRuntime.PermissionMode() != toolexec.PermissionModeDefault {
		t.Fatalf("expected default mode, got %q", console.execRuntime.PermissionMode())
	}
	if len(console.resolved.Tools) != 1 || console.resolved.Tools[0] == nil || console.resolved.Tools[0].Name() != toolshell.BashToolName {
		t.Fatalf("expected refreshed BASH tool, got %+v", console.resolved.Tools)
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
		resolved:    &bootstrap.ResolvedSpec{},
	}
	if err := console.updateExecutionRuntime(toolexec.PermissionModeFullControl, cliTestSandboxType()); err != nil {
		t.Fatal(err)
	}
	if sandboxRunner.closed != 1 {
		t.Fatalf("expected previous runtime closed once, got %d", sandboxRunner.closed)
	}
}
