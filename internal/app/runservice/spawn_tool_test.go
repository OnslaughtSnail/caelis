package runservice

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type stubTaskManager struct {
	lastStart task.SpawnStartRequest
	snapshot  task.Snapshot
	err       error
}

func (s *stubTaskManager) StartBash(context.Context, task.BashStartRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) StartSpawn(_ context.Context, req task.SpawnStartRequest) (task.Snapshot, error) {
	s.lastStart = req
	if s.err != nil {
		return task.Snapshot{}, s.err
	}
	return s.snapshot, nil
}

func (s *stubTaskManager) Wait(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Status(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Write(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) Cancel(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) List(context.Context) ([]task.Snapshot, error) {
	return nil, nil
}

func TestSelfSpawnToolStartsSelfChildSession(t *testing.T) {
	toolImpl, err := NewSelfSpawnTool("")
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		snapshot: task.Snapshot{
			TaskID:  "task-1",
			Kind:    task.KindSpawn,
			State:   task.StateRunning,
			Running: true,
		},
	}
	ctx := task.WithManager(context.Background(), manager)
	result, err := toolImpl.Run(ctx, map[string]any{
		"agent":         "self",
		"prompt":        "child task",
		"yield_seconds": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.lastStart.Prompt != "child task" {
		t.Fatalf("expected child task prompt, got %q", manager.lastStart.Prompt)
	}
	if manager.lastStart.Kind != task.KindSpawn {
		t.Fatalf("expected spawn kind, got %q", manager.lastStart.Kind)
	}
	if manager.lastStart.Agent != "self" {
		t.Fatalf("expected self agent, got %q", manager.lastStart.Agent)
	}
	if manager.lastStart.Yield != 2*time.Second {
		t.Fatalf("expected 2s yield, got %s", manager.lastStart.Yield)
	}
	if manager.lastStart.Timeout != 0 {
		t.Fatalf("expected no spawn timeout, got %s", manager.lastStart.Timeout)
	}
	if result["task_id"] != "task-1" {
		t.Fatalf("expected task_id=task-1, got %#v", result["task_id"])
	}
}

func TestSelfSpawnToolRejectsLegacyContinuationArgs(t *testing.T) {
	toolImpl, err := NewSelfSpawnTool("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := task.WithManager(context.Background(), &stubTaskManager{})
	_, err = toolImpl.Run(ctx, map[string]any{
		"prompt":     "child task",
		"session_id": "child-1",
	})
	if err == nil {
		t.Fatal("expected legacy continuation args to be rejected")
	}
	if !strings.Contains(err.Error(), "TASK write") {
		t.Fatalf("expected migration hint to TASK write, got %v", err)
	}
}

func TestSelfSpawnToolDeclarationOmitsLegacyContinuationArgs(t *testing.T) {
	toolImpl, err := NewSelfSpawnTool("")
	if err != nil {
		t.Fatal(err)
	}
	decl := toolImpl.Declaration()
	props, _ := decl.Parameters["properties"].(map[string]any)
	for _, legacy := range []string{"session", "session_id", "new_session"} {
		if _, ok := props[legacy]; ok {
			t.Fatalf("did not expect legacy arg %q in SPAWN declaration", legacy)
		}
	}
}
