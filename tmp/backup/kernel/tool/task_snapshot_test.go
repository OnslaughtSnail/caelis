package tool

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

func TestSnapshotResultMap_YieldedSpawnUsesMsgWithoutTextPayload(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		TaskID:  "t-123",
		Kind:    task.KindSpawn,
		State:   task.StateRunning,
		Running: true,
		Result: map[string]any{
			"progress_state":       "running",
			"progress_seq":         12,
			"progress_age_seconds": 3,
		},
	})
	if got := result["task_id"]; got != "t-123" {
		t.Fatalf("expected task_id, got %#v", result)
	}
	if got := result["state"]; got != string(task.StateRunning) {
		t.Fatalf("expected running state, got %#v", result)
	}
	if got := result["msg"]; got != "subagent is still running" {
		t.Fatalf("expected running spawn msg, got %#v", result)
	}
	for _, key := range []string{"progress_seq", "result", "progress_age_seconds", "snippet", "running", "message", "ok"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect field %q in yielded result, got %#v", key, result)
		}
	}
}

func TestSnapshotResultMap_RunningBashUsesMinimalPayload(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		TaskID:  "bash-1",
		Kind:    task.KindBash,
		State:   task.StateRunning,
		Running: true,
		Result: map[string]any{
			"latest_output": "Password:",
		},
	})
	if got := result["task_id"]; got != "bash-1" {
		t.Fatalf("expected task_id, got %#v", result)
	}
	if got := result["msg"]; got != "bash is still running" {
		t.Fatalf("expected running bash msg, got %#v", result)
	}
	for _, key := range []string{"next_action", "suggested_args", "result", "latest_output"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect field %q in running bash result, got %#v", key, result)
		}
	}
}

func TestCompactTaskListItem_ActiveTaskIncludesPreviewSummary(t *testing.T) {
	item := CompactTaskListItem(task.Snapshot{
		TaskID:  "bash-1",
		Kind:    task.KindBash,
		State:   task.StateWaitingInput,
		Running: true,
		Result: map[string]any{
			"latest_output": "Password:",
		},
	})
	if got := item["summary"]; got != "Password:" {
		t.Fatalf("expected latest output summary, got %#v", item)
	}
	if got := item["kind"]; got != string(task.KindBash) {
		t.Fatalf("expected kind to remain visible, got %#v", item)
	}
}

func TestCompactTaskListItem_WaitingApprovalFallsBackToActiveMessage(t *testing.T) {
	item := CompactTaskListItem(task.Snapshot{
		TaskID:  "spawn-1",
		Kind:    task.KindSpawn,
		State:   task.StateWaitingApproval,
		Running: true,
	})
	if got := item["summary"]; got != "subagent is waiting for user approval" {
		t.Fatalf("expected waiting approval summary, got %#v", item)
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
	for _, key := range []string{"msg", "result", "snippet"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect field %q in completed bash result, got %#v", key, result)
		}
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
	if got := result["stdout"]; got != "last line\n" {
		t.Fatalf("expected stdout to preserve last output, got %#v", result)
	}
	for _, key := range []string{"msg", "result", "ok", "cancelled", "message", "output"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect legacy field %q on cancelled result, got %#v", key, result)
		}
	}
}

func TestSnapshotResultMap_CompletedSpawnUsesFullOutput(t *testing.T) {
	full := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6"
	result := SnapshotResultMap(task.Snapshot{
		TaskID: "spawn-1",
		Kind:   task.KindSpawn,
		State:  task.StateCompleted,
		Result: map[string]any{
			"final_result": full,
		},
	})
	if got := result["output"]; got != full {
		t.Fatalf("expected output for completed spawn, got %#v", result)
	}
}

func TestSnapshotResultMap_CompletedTTYBashReturnsTerminalOutput(t *testing.T) {
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
	if got := result["stdout"]; got != "full transcript" {
		t.Fatalf("expected tty snapshot to return stdout, got %#v", result)
	}
	if _, exists := result["output_meta"]; exists {
		t.Fatalf("expected output_meta to stay hidden, got %#v", result)
	}
}

func TestSnapshotResultMap_RunningSpawnUsesMsgOnly(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		TaskID:  "spawn-1",
		Kind:    task.KindSpawn,
		State:   task.StateRunning,
		Running: true,
		Result: map[string]any{
			"latest_output":        "old\nline\nlatest\nanswer",
			"progress_seq":         17,
			"progress_age_seconds": 4,
		},
	})
	if got := result["task_id"]; got != "spawn-1" {
		t.Fatalf("expected task_id, got %#v", result)
	}
	if got := result["msg"]; got != "subagent is still running" {
		t.Fatalf("expected running spawn msg, got %#v", result)
	}
	for _, key := range []string{"result", "progress_age_seconds", "latest_output", "progress_seq"} {
		if _, exists := result[key]; exists {
			t.Fatalf("did not expect field %q in running spawn result, got %#v", key, result)
		}
	}
}

func TestSnapshotResultMap_WaitingApprovalSpawnMentionsUserApproval(t *testing.T) {
	result := SnapshotResultMap(task.Snapshot{
		TaskID:  "spawn-approval-1",
		Kind:    task.KindSpawn,
		State:   task.StateWaitingApproval,
		Running: true,
	})
	if got := result["msg"]; got != "subagent is waiting for user approval" {
		t.Fatalf("expected explicit user approval msg, got %#v", result)
	}
}

func TestSnapshotResultMap_HidesOutputMeta(t *testing.T) {
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
	if _, exists := result["output_meta"]; exists {
		t.Fatalf("expected output_meta to stay hidden, got %#v", result)
	}
	if got := result["msg"]; got != "output truncated" {
		t.Fatalf("expected truncation msg, got %#v", result)
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

func TestAppendTaskSnapshotEvents_StreamsRunningSpawnOutput(t *testing.T) {
	out := AppendTaskSnapshotEvents(map[string]any{}, task.Snapshot{
		TaskID:  "spawn-1",
		Kind:    task.KindSpawn,
		State:   task.StateRunning,
		Running: true,
		Output:  task.Output{Log: "latest child chunk\n"},
	})
	events := taskstream.EventsFromResult(out)
	if len(events) != 1 {
		t.Fatalf("expected one running spawn stream event, got %#v", out)
	}
	if events[0].Stream != "assistant" || !strings.Contains(events[0].Chunk, "latest child chunk") {
		t.Fatalf("unexpected spawn stream event %#v", events[0])
	}
	if events[0].Final {
		t.Fatalf("expected running spawn stream event to be non-final, got %#v", events[0])
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
