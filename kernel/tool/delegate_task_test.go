package tool

import (
	"context"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type stubTaskManager struct {
	delegate task.Snapshot
	wait     task.Snapshot
	status   task.Snapshot
	lastWait task.ControlRequest
}

func (s *stubTaskManager) StartBash(context.Context, task.BashStartRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskManager) StartDelegate(context.Context, task.DelegateStartRequest) (task.Snapshot, error) {
	return s.delegate, nil
}

func (s *stubTaskManager) Wait(_ context.Context, req task.ControlRequest) (task.Snapshot, error) {
	s.lastWait = req
	return s.wait, nil
}

func (s *stubTaskManager) Status(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return s.status, nil
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

func TestDelegateTaskTool_UsesSharedTaskManager(t *testing.T) {
	manager := &stubTaskManager{
		delegate: task.Snapshot{
			TaskID:  "t-async",
			Kind:    task.KindDelegate,
			State:   task.StateRunning,
			Running: true,
			Yielded: true,
		},
		wait: task.Snapshot{
			TaskID:  "t-async",
			Kind:    task.KindDelegate,
			State:   task.StateCompleted,
			Running: false,
			Result: map[string]any{
				"summary": "final",
			},
		},
		status: task.Snapshot{
			TaskID:  "t-async",
			Kind:    task.KindDelegate,
			State:   task.StateRunning,
			Running: true,
		},
	}
	delegateTool, err := NewDelegateTask()
	if err != nil {
		t.Fatal(err)
	}
	taskTool, err := NewTaskTool()
	if err != nil {
		t.Fatal(err)
	}

	ctx := task.WithManager(context.Background(), manager)
	asyncOut, err := delegateTool.Run(ctx, map[string]any{
		"task":          "do work",
		"yield_time_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if asyncOut["task_id"] != "t-async" || asyncOut["running"] != true {
		t.Fatalf("unexpected delegate result: %#v", asyncOut)
	}

	statusOut, err := taskTool.Run(ctx, map[string]any{
		"action":  "status",
		"task_id": "t-async",
	})
	if err != nil {
		t.Fatal(err)
	}
	if statusOut["state"] != string(task.StateRunning) {
		t.Fatalf("unexpected task status: %#v", statusOut)
	}

	waitOut, err := taskTool.Run(ctx, map[string]any{
		"action":        "wait",
		"task_id":       "t-async",
		"yield_time_ms": 2500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if waitOut["summary"] != "final" || waitOut["running"] != false {
		t.Fatalf("unexpected wait result: %#v", waitOut)
	}
	if manager.lastWait.Yield != 2500*time.Millisecond {
		t.Fatalf("expected wait yield to propagate, got %s", manager.lastWait.Yield)
	}
}

func TestSnapshotResultMap_NormalizesActiveRunningState(t *testing.T) {
	out := SnapshotResultMap(task.Snapshot{
		TaskID:  "t-1",
		Kind:    task.KindDelegate,
		State:   task.StateRunning,
		Running: false,
	})
	if got := out["running"]; got != true {
		t.Fatalf("expected active running=true for running state, got %#v", out)
	}
}
