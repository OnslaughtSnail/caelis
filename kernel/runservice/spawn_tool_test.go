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
		"yield_time_ms": 2000,
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
	if manager.lastStart.IdleTimeout != 0 {
		t.Fatalf("expected idle timeout to stay internal, got %s", manager.lastStart.IdleTimeout)
	}
	if result["task_id"] != "task-1" {
		t.Fatalf("expected task_id=task-1, got %#v", result["task_id"])
	}
}

func TestSelfSpawnToolReturnsFinalResponseWhenChildAlreadyCompleted(t *testing.T) {
	toolImpl, err := NewSelfSpawnTool("")
	if err != nil {
		t.Fatal(err)
	}
	manager := &stubTaskManager{
		snapshot: task.Snapshot{
			TaskID: "task-2",
			Kind:   task.KindSpawn,
			State:  task.StateCompleted,
			Result: map[string]any{
				"final_result": "55",
			},
		},
	}
	ctx := task.WithManager(context.Background(), manager)
	result, err := toolImpl.Run(ctx, map[string]any{
		"agent":  "self",
		"prompt": "child task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["state"] != string(task.StateCompleted) {
		t.Fatalf("expected completed spawn state, got %#v", result)
	}
	if result["output"] != "55" {
		t.Fatalf("expected completed spawn output, got %#v", result)
	}
	if _, exists := result["task_id"]; exists {
		t.Fatalf("did not expect completed spawn task_id, got %#v", result)
	}
}

func TestSelfSpawnToolRejectsLegacyContinuationArgs(t *testing.T) {
	toolImpl, err := NewSelfSpawnTool("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := task.WithManager(context.Background(), &stubTaskManager{})
	for _, key := range []string{"session_id", "timeout_ms", "timeout_seconds"} {
		_, err = toolImpl.Run(ctx, map[string]any{
			"prompt": "child task",
			key:      "child-1",
		})
		if err == nil {
			t.Fatalf("expected unsupported arg %q to be rejected", key)
		}
		if !strings.Contains(err.Error(), `arg "`+key+`" is no longer supported`) {
			t.Fatalf("expected arg rejection for %q, got %v", key, err)
		}
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
	if _, ok := props["idle_timeout_seconds"]; ok {
		t.Fatal("did not expect idle timeout arg in SPAWN declaration")
	}
	if got := toolImpl.Description(); !strings.Contains(got, "self or any configured ACP agent id such as codex, copilot, or gemini") || !strings.Contains(got, "result includes task_id; continue with TASK wait") || !strings.Contains(got, "use TASK write to start another turn in the same child session") {
		t.Fatalf("expected SPAWN description to explain agent values and TASK flow, got %q", got)
	}
	agentProp, _ := props["agent"].(map[string]any)
	yieldProp, _ := props["yield_time_ms"].(map[string]any)
	if !strings.Contains(asString(agentProp["description"]), "codex, copilot, or gemini") {
		t.Fatalf("expected agent declaration to include concrete examples, got %#v", agentProp)
	}
	if !strings.Contains(asString(yieldProp["description"]), "per-call wait") || !strings.Contains(asString(yieldProp["description"]), "result includes task_id") {
		t.Fatalf("expected yield_time_ms declaration to explain per-call wait, got %#v", yieldProp)
	}
	if _, ok := props["timeout_ms"]; ok {
		t.Fatalf("did not expect timeout_ms in SPAWN declaration, got %#v", props["timeout_ms"])
	}
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
