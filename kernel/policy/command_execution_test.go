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
			Args: map[string]any{"command": "ls -la"},
		},
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

func TestRouteCommandExecution_UnsafeCommandRequiresApproval(t *testing.T) {
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
			Args: map[string]any{"command": "python3 app.py"},
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
			Args: map[string]any{
				"command":             "ls",
				"sandbox_permissions": "invalid",
			},
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
