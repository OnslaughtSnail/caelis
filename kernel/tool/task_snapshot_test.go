package tool

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

func TestSnapshotResultMap_YieldedTaskUsesTaskIDAndMsg(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		TaskID:  "t-123",
		Kind:    task.KindSpawn,
		State:   task.StateRunning,
		Running: true,
		Result: map[string]any{
			"progress_state": "running",
		},
	})
	if got := result["task_id"]; got != "t-123" {
		t.Fatalf("expected task_id, got %#v", result)
	}
	if got := result["state"]; got != string(task.StateRunning) {
		t.Fatalf("expected running state, got %#v", result)
	}
	message := strings.TrimSpace(resultString(result["msg"]))
	if !strings.Contains(message, "use TASK with task_id t-123") {
		t.Fatalf("expected yielded message to guide TASK usage, got %#v", result)
	}
	if got := strings.TrimSpace(resultString(result["result"])); got != "subagent is running" {
		t.Fatalf("expected yielded progress snippet, got %#v", result)
	}
	if _, exists := result["snippet"]; exists {
		t.Fatalf("expected no snippet field in yielded result, got %#v", result)
	}
	for _, key := range []string{"running", "message", "output", "ok"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect legacy field %q in yielded result, got %#v", key, result)
		}
	}
}

func TestSnapshotResultMap_CompletedTaskUsesResultAndMsg(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		Kind:   task.KindBash,
		State:  task.StateCompleted,
		Output: task.Output{Stdout: "done\n"},
	})
	if got := result["state"]; got != string(task.StateCompleted) {
		t.Fatalf("expected completed state, got %#v", result)
	}
	if got := result["stdout"]; got != "done\n" {
		t.Fatalf("expected stdout=done, got %#v", result)
	}
	if got := result["msg"]; got != "task success" {
		t.Fatalf("expected msg=task success, got %#v", result)
	}
	if _, exists := result["result"]; exists {
		t.Fatalf("expected no compact result field on completed bash output, got %#v", result)
	}
	if _, exists := result["snippet"]; exists {
		t.Fatalf("expected no snippet field in completed result, got %#v", result)
	}
}

func TestSnapshotResultMap_CancelledTaskUsesStateAndMsg(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		Kind:   task.KindBash,
		State:  task.StateCancelled,
		Output: task.Output{Stdout: "last line\n"},
	})
	if got := result["state"]; got != string(task.StateCancelled) {
		t.Fatalf("expected cancelled state, got %#v", result)
	}
	if got := result["msg"]; got != "cancelled" {
		t.Fatalf("expected msg=cancelled, got %#v", result)
	}
	if got := result["stdout"]; got != "last line\n" {
		t.Fatalf("expected stdout to preserve last output, got %#v", result)
	}
	if _, exists := result["result"]; exists {
		t.Fatalf("expected no compact result field on cancelled bash output, got %#v", result)
	}
	for _, key := range []string{"ok", "cancelled", "message", "output"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect legacy field %q on cancelled result, got %#v", key, result)
		}
	}
}

func TestSnapshotResultMap_CompletedSpawnUsesFullFinalResult(t *testing.T) {
	full := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6"
	result := SnapshotResultMap(task.Snapshot{
		Kind:  task.KindSpawn,
		State: task.StateCompleted,
		Result: map[string]any{
			"final_result": full,
		},
	})
	if got := result["result"]; got != full {
		t.Fatalf("expected full final_result for completed spawn, got %#v", result)
	}
}

func TestSnapshotResultMap_CompletedTTYBashUsesPreviewAndOutputMeta(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		Kind:  task.KindBash,
		State: task.StateCompleted,
		Result: map[string]any{
			"latest_output": "hello alice",
			"output_meta": map[string]any{
				"tty":             true,
				"streamed":        true,
				"model_truncated": false,
			},
		},
		Output: task.Output{Stdout: "full transcript"},
	})
	if got := result["result"]; got != "hello alice" {
		t.Fatalf("expected tty preview result, got %#v", result)
	}
	if _, exists := result["stdout"]; exists {
		t.Fatalf("expected tty snapshot to suppress stdout field, got %#v", result)
	}
	if _, exists := result["output_meta"]; exists {
		t.Fatalf("expected uninformative tty output_meta to be omitted, got %#v", result)
	}
}

func TestSnapshotResultMap_KeepsMeaningfulOutputMeta(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		Kind:  task.KindBash,
		State: task.StateCompleted,
		Result: map[string]any{
			"output_meta": map[string]any{
				"tty":                  true,
				"streamed":             true,
				"capture_truncated":    true,
				"capture_cap_bytes":    262144,
				"stdout_dropped_bytes": 512,
			},
		},
		Output: task.Output{Stdout: "full transcript"},
	})
	meta, _ := result["output_meta"].(map[string]any)
	if len(meta) == 0 {
		t.Fatalf("expected meaningful output_meta to survive serialization, got %#v", result)
	}
	if got := meta["truncated"]; got != true {
		t.Fatalf("expected compact truncation signal, got %#v", meta)
	}
	if len(meta) != 1 {
		t.Fatalf("expected only truncated marker, got %#v", meta)
	}
}

func TestAppendTaskSnapshotEvents_SkipsTTYTranscript(t *testing.T) {
	out := AppendTaskSnapshotEvents(map[string]any{}, task.Snapshot{
		TaskID: "t-tty-1",
		Kind:   task.KindBash,
		State:  task.StateCompleted,
		Output: task.Output{Stdout: "full transcript"},
		Result: map[string]any{
			"output_meta": map[string]any{
				"tty": true,
			},
		},
	})
	meta, _ := out["metadata"].(map[string]any)
	events, _ := meta["task_stream"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected only final state event for tty snapshot, got %#v", out)
	}
}

func resultString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
