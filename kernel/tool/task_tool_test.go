package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

func TestTaskSnapshotResult_RunningSpawnOmitsPublicEvents(t *testing.T) {
	result := taskSnapshotResult(task.Snapshot{
		TaskID:  "spawn-1",
		Kind:    task.KindSpawn,
		State:   task.StateRunning,
		Running: true,
		Result: map[string]any{
			"progress_seq": 17,
		},
		Output: task.Output{Log: "intermediate child text\n"},
	}, true)

	if _, exists := result["events"]; exists {
		t.Fatalf("did not expect public events for running spawn result, got %#v", result)
	}
	if got := result["msg"]; got != "subagent is still running" {
		t.Fatalf("expected running spawn msg to remain visible, got %#v", result)
	}
	hiddenEvents := taskstream.EventsFromResult(result)
	if len(hiddenEvents) != 1 || hiddenEvents[0].Stream != "assistant" {
		t.Fatalf("expected hidden assistant task_stream event for UI, got %#v", hiddenEvents)
	}
}

func TestTaskSnapshotResult_WaitingInputBashExposesTrimmedActionEvent(t *testing.T) {
	stdout := strings.Repeat("x", 700)
	result := taskSnapshotResult(task.Snapshot{
		TaskID:  "bash-1",
		Kind:    task.KindBash,
		State:   task.StateWaitingInput,
		Running: true,
		Output:  task.Output{Stdout: stdout},
	}, true)

	events, ok := result["events"].([]map[string]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected one public action event for waiting_input bash, got %#v", result)
	}
	text := strings.TrimSpace(resultString(events[0]["text"]))
	if text == "" {
		t.Fatalf("expected public action event text, got %#v", events)
	}
	if len(text) > maxActionTaskEventBytes {
		t.Fatalf("expected action event text to be trimmed to %d bytes, got %d", maxActionTaskEventBytes, len(text))
	}
}

func TestTaskSnapshotResult_RunningBashOmitsPublicEvents(t *testing.T) {
	result := taskSnapshotResult(task.Snapshot{
		TaskID:  "bash-1",
		Kind:    task.KindBash,
		State:   task.StateRunning,
		Running: true,
		Output:  task.Output{Stdout: "still running\n"},
	}, true)

	if _, exists := result["events"]; exists {
		t.Fatalf("did not expect public events for running bash timeout payload, got %#v", result)
	}
	if got := result["msg"]; got != "bash is still running" {
		t.Fatalf("expected running bash msg, got %#v", result)
	}
	hiddenEvents := taskstream.EventsFromResult(result)
	if len(hiddenEvents) != 1 || hiddenEvents[0].Stream != "stdout" {
		t.Fatalf("expected hidden stdout task_stream event for UI, got %#v", hiddenEvents)
	}
}

type stubListTaskManager struct {
	items []task.Snapshot
}

func (s stubListTaskManager) StartBash(context.Context, task.BashStartRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubListTaskManager) StartSpawn(context.Context, task.SpawnStartRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubListTaskManager) Wait(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubListTaskManager) Write(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubListTaskManager) Cancel(context.Context, task.ControlRequest) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s stubListTaskManager) List(context.Context) ([]task.Snapshot, error) {
	return append([]task.Snapshot(nil), s.items...), nil
}

func TestTaskTool_ListIncludesKindAndPaginationMetadata(t *testing.T) {
	tool, err := NewTaskTool()
	if err != nil {
		t.Fatal(err)
	}
	ctx := task.WithManager(context.Background(), stubListTaskManager{
		items: []task.Snapshot{
			{TaskID: "t-spawn", Kind: task.KindSpawn, State: task.StateRunning},
			{TaskID: "t-bash", Kind: task.KindBash, State: task.StateCompleted},
		},
	})
	out, err := tool.Run(ctx, map[string]any{
		"action": "list",
		"limit":  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["total"]; got != 2 {
		t.Fatalf("expected total=2, got %#v", out)
	}
	tasks, ok := out["tasks"].([]map[string]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("expected one paginated task, got %#v", out)
	}
	if got := tasks[0]["kind"]; got != string(task.KindSpawn) {
		t.Fatalf("expected kind on task list item, got %#v", out)
	}
}

func TestTaskTool_DescriptionExplainsWriteSemantics(t *testing.T) {
	tool, err := NewTaskTool()
	if err != nil {
		t.Fatal(err)
	}
	if got := tool.Description(); !strings.Contains(got, "send bash stdin") || !strings.Contains(got, "completed spawn session") {
		t.Fatalf("expected TASK description to explain write semantics, got %q", got)
	}
}
