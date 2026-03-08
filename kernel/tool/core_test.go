package tool

import (
	"context"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestEnsureCoreTools_AddRead(t *testing.T) {
	echoTool, err := NewFunction[struct{}, struct{}]("echo", "echo", func(ctx context.Context, args struct{}) (struct{}, error) {
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
	tools, err := EnsureCoreTools([]Tool{echoTool}, CoreToolsConfig{Runtime: rt})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	if tools[0].Name() != ReadToolName {
		t.Fatalf("expected first tool %q, got %q", ReadToolName, tools[0].Name())
	}
	if tools[1].Name() != DelegateTaskToolName {
		t.Fatalf("expected second tool %q, got %q", DelegateTaskToolName, tools[1].Name())
	}
	if tools[2].Name() != TaskToolName {
		t.Fatalf("expected third tool %q, got %q", TaskToolName, tools[2].Name())
	}
}

func TestEnsureCoreTools_AddDelegateAndTaskTools(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	tools, err := EnsureCoreTools(nil, CoreToolsConfig{
		Runtime: rt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected READ + DELEGATE + TASK, got %d tools", len(tools))
	}
	if tools[1].Name() != DelegateTaskToolName {
		t.Fatalf("expected second tool %q, got %q", DelegateTaskToolName, tools[1].Name())
	}
	if tools[2].Name() != TaskToolName {
		t.Fatalf("expected third tool %q, got %q", TaskToolName, tools[2].Name())
	}
}

func TestEnsureCoreTools_DisableDelegate(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	tools, err := EnsureCoreTools(nil, CoreToolsConfig{
		Runtime:         rt,
		DisableDelegate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected READ + TASK, got %d tools", len(tools))
	}
	if tools[0].Name() != ReadToolName {
		t.Fatalf("expected first tool %q, got %q", ReadToolName, tools[0].Name())
	}
	if tools[1].Name() != TaskToolName {
		t.Fatalf("expected second tool %q, got %q", TaskToolName, tools[1].Name())
	}
}
