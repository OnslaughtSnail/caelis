package shell

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
)

type recordingRunner struct {
	result toolexec.CommandResult
	err    error
	calls  []toolexec.CommandRequest
}

func testSandboxType() string {
	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	return "docker"
}

func (r *recordingRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	r.calls = append(r.calls, req)
	return r.result, r.err
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

func TestBash_DefaultUnsafeCommandRequiresApprovalWhenNoApprover(t *testing.T) {
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
	_, err = tool.Run(context.Background(), map[string]any{"command": "python3 app.py"})
	if err == nil {
		t.Fatal("expected approval required")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected approval-required error, got: %v", err)
	}
	if len(host.calls) != 0 {
		t.Fatalf("expected host runner not called, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
}

func TestBash_DefaultUnsafeCommandWithApprovalRunsOnHost(t *testing.T) {
	host := &recordingRunner{result: toolexec.CommandResult{Stdout: "host-ok"}}
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
	out, err := tool.Run(ctx, map[string]any{"command": "python3 app.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 {
		t.Fatalf("expected host runner called once, got %d", len(host.calls))
	}
	if len(sandbox.calls) != 0 {
		t.Fatalf("expected sandbox runner not called, got %d", len(sandbox.calls))
	}
	if out["stdout"] != "host-ok" {
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
	out, err := tool.Run(context.Background(), map[string]any{"command": "cat <<'EOF' > a.txt\nx\nEOF"})
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

func TestBash_DefaultRequireEscalatedForcesHostApproval(t *testing.T) {
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
		"command":             "ls",
		"sandbox_permissions": "require_escalated",
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

func TestBash_DefaultRequireEscalatedDeniedStopsExecution(t *testing.T) {
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
		"command":             "ls",
		"sandbox_permissions": "require_escalated",
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
	fallbackSandbox := &failingProbeRunner{probeErr: errors.New("docker unavailable")}
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

func TestBash_InvalidSandboxPermissions(t *testing.T) {
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
		"command":             "ls",
		"sandbox_permissions": "invalid",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestBash_AppliesDefaultTimeout(t *testing.T) {
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "ok"}}
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
	if _, err := tool.Run(context.Background(), map[string]any{"command": "ls"}); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected one sandbox call, got %d", len(sandbox.calls))
	}
	if sandbox.calls[0].Timeout != defaultBashTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultBashTimeout, sandbox.calls[0].Timeout)
	}
	if sandbox.calls[0].IdleTimeout != defaultBashIdle {
		t.Fatalf("expected default idle timeout %s, got %s", defaultBashIdle, sandbox.calls[0].IdleTimeout)
	}
}

func TestBash_TimeoutOverrideViaArgs(t *testing.T) {
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "ok"}}
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
	if _, err := tool.Run(context.Background(), map[string]any{
		"command":    "ls",
		"timeout_ms": 1500,
	}); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected one sandbox call, got %d", len(sandbox.calls))
	}
	if sandbox.calls[0].Timeout != 1500*time.Millisecond {
		t.Fatalf("expected timeout 1500ms, got %s", sandbox.calls[0].Timeout)
	}
	if sandbox.calls[0].IdleTimeout != defaultBashIdle {
		t.Fatalf("expected idle timeout to remain default %s, got %s", defaultBashIdle, sandbox.calls[0].IdleTimeout)
	}
}

func TestBash_NegativeTimeoutRejected(t *testing.T) {
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
		"command":    "ls",
		"timeout_ms": -1,
	})
	if err == nil {
		t.Fatal("expected timeout validation error")
	}
}

func TestBash_IdleTimeoutOverrideViaArgs(t *testing.T) {
	sandbox := &recordingRunner{result: toolexec.CommandResult{Stdout: "ok"}}
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
	if _, err := tool.Run(context.Background(), map[string]any{
		"command":         "ls",
		"idle_timeout_ms": 2300,
	}); err != nil {
		t.Fatal(err)
	}
	if len(sandbox.calls) != 1 {
		t.Fatalf("expected one sandbox call, got %d", len(sandbox.calls))
	}
	if sandbox.calls[0].IdleTimeout != 2300*time.Millisecond {
		t.Fatalf("expected idle timeout 2300ms, got %s", sandbox.calls[0].IdleTimeout)
	}
}

func TestBash_NegativeIdleTimeoutRejected(t *testing.T) {
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
		"command":         "ls",
		"idle_timeout_ms": -1,
	})
	if err == nil {
		t.Fatal("expected idle timeout validation error")
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
