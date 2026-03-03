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
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name() != ReadToolName {
		t.Fatalf("expected first tool %q, got %q", ReadToolName, tools[0].Name())
	}
}
