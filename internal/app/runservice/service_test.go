package runservice

import (
	"context"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestServiceAssembleTools_AddsOptionalPlanAndSpawn(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	baseTool, err := tool.NewFunction[struct{}, struct{}]("FAKE_TOOL", "fake", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(ServiceConfig{
		Runtime:         rt,
		AppName:         "app",
		UserID:          "user",
		Execution:       execRT,
		Tools:           []tool.Tool{baseTool},
		EnablePlan:      true,
		EnableSelfSpawn: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tools, err := svc.AssembleTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected base + PLAN + SPAWN, got %d", len(tools))
	}
	if tools[0].Name() != "FAKE_TOOL" {
		t.Fatalf("expected first tool FAKE_TOOL, got %q", tools[0].Name())
	}
	if tools[1].Name() != tool.PlanToolName {
		t.Fatalf("expected second tool %q, got %q", tool.PlanToolName, tools[1].Name())
	}
	if tools[2].Name() != tool.SpawnToolName {
		t.Fatalf("expected third tool %q, got %q", tool.SpawnToolName, tools[2].Name())
	}

	visible, err := svc.VisibleTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 8 {
		t.Fatalf("expected READ + WRITE + PATCH + BASH + TASK + base + PLAN + SPAWN, got %d", len(visible))
	}
	if visible[0].Name() != tool.ReadToolName {
		t.Fatalf("expected first visible tool %q, got %q", tool.ReadToolName, visible[0].Name())
	}
	if visible[4].Name() != tool.TaskToolName {
		t.Fatalf("expected fifth visible tool %q, got %q", tool.TaskToolName, visible[4].Name())
	}
}
