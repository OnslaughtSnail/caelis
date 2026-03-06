package policy

import (
	"context"
	"runtime"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type noopCommandRunner struct{}

func (noopCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return toolexec.CommandResult{}, nil
}

func testSandboxTypeForPolicy() string {
	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	return "docker"
}

func TestRouteCommandExecution_SafeCommandSandboxAllow(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxTypeForPolicy(),
		SandboxRunner:  noopCommandRunner{},
		HostRunner:     noopCommandRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := RouteCommandExecution(CommandExecutionConfig{Runtime: rt})
	in, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "BASH",
			Args: `{"command":"ls -la"}`,
		},
		Args: map[string]any{"command": "ls -la"},
	})
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = NormalizeDecision(in.Decision)
	if in.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision, got %q", in.Decision.Effect)
	}
	route, ok := DecisionRouteFromMetadata(in.Decision)
	if !ok || route != DecisionRouteSandbox {
		t.Fatalf("expected sandbox route metadata, got route=%q ok=%v", route, ok)
	}
	if v, ok := in.Decision.Metadata[DecisionMetaFallbackOnCommandNotFound].(bool); !ok || !v {
		t.Fatalf("expected sandbox fallback metadata enabled, got %v", in.Decision.Metadata[DecisionMetaFallbackOnCommandNotFound])
	}
}

func TestRouteCommandExecution_UnsafeCommandSandboxAllow(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxTypeForPolicy(),
		SandboxRunner:  noopCommandRunner{},
		HostRunner:     noopCommandRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := RouteCommandExecution(CommandExecutionConfig{Runtime: rt})
	in, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "BASH",
			Args: `{"command":"python3 app.py"}`,
		},
		Args: map[string]any{"command": "python3 app.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = NormalizeDecision(in.Decision)
	if in.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision, got %q", in.Decision.Effect)
	}
	route, ok := DecisionRouteFromMetadata(in.Decision)
	if !ok || route != DecisionRouteSandbox {
		t.Fatalf("expected sandbox route metadata, got route=%q ok=%v", route, ok)
	}
}

func TestRouteCommandExecution_InvalidSandboxPermissionDenied(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxTypeForPolicy(),
		SandboxRunner:  noopCommandRunner{},
		HostRunner:     noopCommandRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := RouteCommandExecution(CommandExecutionConfig{Runtime: rt})
	in, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "BASH",
			Args: `{"command":"ls","sandbox_permissions":"invalid"}`,
		},
		Args: map[string]any{
			"command":             "ls",
			"sandbox_permissions": "invalid",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = NormalizeDecision(in.Decision)
	if in.Decision.Effect != DecisionEffectDeny {
		t.Fatalf("expected deny decision, got %q", in.Decision.Effect)
	}
}

func TestRouteCommandExecution_RequireEscalatedBoolAccepted(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxTypeForPolicy(),
		SandboxRunner:  noopCommandRunner{},
		HostRunner:     noopCommandRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := RouteCommandExecution(CommandExecutionConfig{Runtime: rt})
	in, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "BASH",
			Args: `{"command":"python3 app.py","require_escalated":true}`,
		},
		Args: map[string]any{
			"command":            "python3 app.py",
			"require_escalated":  true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = NormalizeDecision(in.Decision)
	if in.Decision.Effect != DecisionEffectRequireApproval {
		t.Fatalf("expected require_approval decision, got %q", in.Decision.Effect)
	}
	route, ok := DecisionRouteFromMetadata(in.Decision)
	if !ok || route != DecisionRouteHost {
		t.Fatalf("expected host route metadata, got route=%q ok=%v", route, ok)
	}
}

func TestDetectDestructiveCommand(t *testing.T) {
	cases := []struct {
		cmd             string
		wantBase        string
		wantDestructive bool
	}{
		{"rm file.go", "rm", true},
		{"rm -rf /tmp/dir", "rm", true},
		{"/bin/rm file.txt", "rm", true},
		{"rmdir mydir", "rmdir", true},
		{"shred secret.txt", "shred", true},
		{"dd if=/dev/zero of=/dev/sda", "dd", true},
		{"dd if=/dev/zero bs=1M count=10", "", false}, // no of= → safe
		{"ls -la", "", false},
		{"grep -r foo .", "", false},
		{"git diff", "", false},
		{"cat file.txt", "", false},
	}
	for _, tc := range cases {
		base, reason := detectDestructiveCommand(tc.cmd)
		if tc.wantDestructive {
			if base == "" {
				t.Errorf("command %q: expected destructive detection (base=%q), got no match", tc.cmd, tc.wantBase)
				continue
			}
			if base != tc.wantBase {
				t.Errorf("command %q: expected base=%q, got %q", tc.cmd, tc.wantBase, base)
			}
			if reason == "" {
				t.Errorf("command %q: expected non-empty reason", tc.cmd)
			}
		} else {
			if base != "" {
				t.Errorf("command %q: expected safe (no match), got base=%q reason=%q", tc.cmd, base, reason)
			}
		}
	}
}

func TestRouteCommandExecution_DestructiveCommandRequiresApproval(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxTypeForPolicy(),
		SandboxRunner:  noopCommandRunner{},
		HostRunner:     noopCommandRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := RouteCommandExecution(CommandExecutionConfig{Runtime: rt})
	for _, cmd := range []string{"rm file.go", "rm -rf /tmp/foo", "shred secret.txt"} {
		in, err := hook.BeforeTool(context.Background(), ToolInput{
			Call: model.ToolCall{Name: "BASH", Args: `{"command":"` + cmd + `"}`},
			Args: map[string]any{"command": cmd},
		})
		if err != nil {
			t.Fatalf("command %q: unexpected error: %v", cmd, err)
		}
		in.Decision = NormalizeDecision(in.Decision)
		if in.Decision.Effect != DecisionEffectRequireApproval {
			t.Errorf("command %q: expected require_approval, got %q", cmd, in.Decision.Effect)
		}
		route, ok := DecisionRouteFromMetadata(in.Decision)
		if !ok || route != DecisionRouteSandbox {
			t.Errorf("command %q: expected sandbox route, got route=%q ok=%v", cmd, route, ok)
		}
	}
}
