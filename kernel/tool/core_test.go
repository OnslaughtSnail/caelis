package tool

import (
	"context"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

func TestEnsureCoreTools_AddRead(t *testing.T) {
	echoTool, err := NewFunction("echo", "echo", func(ctx context.Context, args struct{}) (struct{}, error) {
		_ = ctx
		_ = args
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	builtins, err := BuildCoreTools(CoreToolsConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := EnsureCoreTools([]Tool{echoTool}, builtins)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}
	if tools[0].Name() != ReadToolName {
		t.Fatalf("expected first tool %q, got %q", ReadToolName, tools[0].Name())
	}
	if tools[1].Name() != filesystem.WriteToolName {
		t.Fatalf("expected second tool %q, got %q", filesystem.WriteToolName, tools[1].Name())
	}
	if tools[2].Name() != filesystem.PatchToolName {
		t.Fatalf("expected third tool %q, got %q", filesystem.PatchToolName, tools[2].Name())
	}
	if tools[3].Name() != toolshell.BashToolName {
		t.Fatalf("expected fourth tool %q, got %q", toolshell.BashToolName, tools[3].Name())
	}
	if tools[4].Name() != TaskToolName {
		t.Fatalf("expected fifth tool %q, got %q", TaskToolName, tools[4].Name())
	}
	if tools[5].Name() != "echo" {
		t.Fatalf("expected sixth tool %q, got %q", "echo", tools[5].Name())
	}
}

func TestEnsureCoreTools_AddKernelCoreTools(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	builtins, err := BuildCoreTools(CoreToolsConfig{
		Runtime: rt,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := EnsureCoreTools(nil, builtins)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected READ + WRITE + PATCH + BASH + TASK, got %d tools", len(tools))
	}
	if tools[1].Name() != filesystem.WriteToolName {
		t.Fatalf("expected second tool %q, got %q", filesystem.WriteToolName, tools[1].Name())
	}
	if tools[2].Name() != filesystem.PatchToolName {
		t.Fatalf("expected third tool %q, got %q", filesystem.PatchToolName, tools[2].Name())
	}
	if tools[3].Name() != toolshell.BashToolName {
		t.Fatalf("expected fourth tool %q, got %q", toolshell.BashToolName, tools[3].Name())
	}
	if tools[4].Name() != TaskToolName {
		t.Fatalf("expected fifth tool %q, got %q", TaskToolName, tools[4].Name())
	}
}

func TestEnsureCoreTools_RejectsReservedNames(t *testing.T) {
	readTool, err := NewFunction(ReadToolName, "shadow read", func(ctx context.Context, args struct{}) (struct{}, error) {
		_ = ctx
		_ = args
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	builtins, err := BuildCoreTools(CoreToolsConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = EnsureCoreTools([]Tool{readTool}, builtins)
	if err == nil {
		t.Fatal("expected reserved core tool name to fail")
	}
	if !strings.Contains(err.Error(), ReadToolName) || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureCoreTools_DedupesBuiltinBashTool(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})

	bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}

	builtins, err := BuildCoreTools(CoreToolsConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := EnsureCoreTools([]Tool{bashTool}, builtins)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected only core tools after deduping builtin BASH, got %d", len(tools))
	}
	bashCount := 0
	for _, one := range tools {
		if one != nil && one.Name() == toolshell.BashToolName {
			bashCount++
		}
	}
	if bashCount != 1 {
		t.Fatalf("expected a single BASH tool after deduping, got %d", bashCount)
	}
}
