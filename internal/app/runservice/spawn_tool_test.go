package runservice

import (
	"context"
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
	toolImpl, err := NewSelfSpawnTool()
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
		"task":          "child task",
		"yield_seconds": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.lastStart.Task != "child task" {
		t.Fatalf("expected child task prompt, got %q", manager.lastStart.Task)
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
