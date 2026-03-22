package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestSummarizeToolResponse_ReadReturnsEmpty(t *testing.T) {
	got := summarizeToolResponse("READ", map[string]any{
		"path":       "/tmp/work/project/main.go",
		"start_line": 3,
		"end_line":   12,
		"has_more":   false,
	})
	if got != "" {
		t.Fatalf("expected empty read summary, got %q", got)
	}
}

func TestSummarizeToolResponse_ReadTruncatedReturnsEmpty(t *testing.T) {
	got := summarizeToolResponse("READ", map[string]any{
		"path":        "/tmp/a.txt",
		"start_line":  1,
		"end_line":    200,
		"has_more":    true,
		"next_offset": 4096,
	})
	if got != "" {
		t.Fatalf("expected empty read summary, got %q", got)
	}
}

func TestSummarizeToolResponse_BashErrorPrefersErrorOverMissingExitCode(t *testing.T) {
	got := summarizeToolResponse("BASH", map[string]any{
		"error": "tool: BASH failed (route=sandbox): tool: sandbox runner is unavailable",
	})
	if !strings.Contains(got, "sandbox route failed") || !strings.Contains(got, "sandbox runner is unavailable") {
		t.Fatalf("unexpected bash error summary: %q", got)
	}
	if strings.Contains(got, "exit_code=0") {
		t.Fatalf("did not expect fallback success summary for bash error: %q", got)
	}
}

func TestSummarizeToolResponse_PatchIsConcise(t *testing.T) {
	got := summarizeToolResponse("PATCH", map[string]any{
		"path":      "a.txt",
		"replaced":  1,
		"old_count": 1,
		"created":   false,
		"metadata": map[string]any{
			"patch": map[string]any{
				"hunk":    "@@ -5,1 +5,1 @@",
				"preview": "--- old\n+++ new\n-old\n+new",
			},
		},
	})
	if !strings.Contains(got, "edited a.txt") {
		t.Fatalf("unexpected patch summary header: %q", got)
	}
	if strings.Contains(got, "--- old") || strings.Contains(got, "@@ -5,1 +5,1 @@") {
		t.Fatalf("did not expect inline textual diff preview, got %q", got)
	}
}

func TestSummarizeToolResponse_PatchWithoutPreviewDoesNotRenderDiff(t *testing.T) {
	got := summarizeToolResponse("PATCH", map[string]any{
		"path":      "a.txt",
		"replaced":  1,
		"old_count": 1,
		"created":   false,
	})
	if strings.Contains(got, "--- old") || strings.Contains(got, "+++ new") {
		t.Fatalf("expected no diff block without preview, got %q", got)
	}
}

func TestSummarizeToolResponse_PatchIgnoresCallPreviewArgs(t *testing.T) {
	got := summarizeToolResponseWithCall("PATCH", map[string]any{
		"path":      "a.txt",
		"replaced":  1,
		"old_count": 1,
		"created":   false,
	}, map[string]any{
		"path": "a.txt",
		"old":  "line1\nold",
		"new":  "line1\nnew",
	})
	if !strings.Contains(got, "edited a.txt") {
		t.Fatalf("unexpected patch summary header: %q", got)
	}
	if strings.Contains(got, "--- old") || strings.Contains(got, "+line1") {
		t.Fatalf("did not expect diff preview generated from call args, got %q", got)
	}
}

func TestSummarizeToolResponse_WriteUnchangedUsesCompactSummary(t *testing.T) {
	got := summarizeToolResponseWithCall("WRITE", map[string]any{
		"path":          "a.txt",
		"created":       false,
		"line_count":    2,
		"added_lines":   0,
		"removed_lines": 0,
	}, map[string]any{
		"path":    "a.txt",
		"content": "same\n",
	})
	if got != "unchanged a.txt" {
		t.Fatalf("unexpected unchanged write summary: %q", got)
	}
}

func TestSummarizeToolResponse_SpawnRendersAssistantWithoutChildID(t *testing.T) {
	got := summarizeToolResponse("SPAWN", map[string]any{
		"child_session_id": "s-1234567890ab",
		"summary":          "## Done\n\n- item",
	})
	if strings.Contains(got, "child=") {
		t.Fatalf("did not expect child session id in spawn summary: %q", got)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("expected spawn summary to render on the next line, got %q", got)
	}
	if !strings.Contains(got, "Done") {
		t.Fatalf("unexpected spawn summary: %q", got)
	}
}

func TestSummarizeToolResponse_SpawnRunningShowsYieldOnly(t *testing.T) {
	got := summarizeToolResponseWithCall("SPAWN", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "running",
		"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
		"result":  "subagent is running",
	}, map[string]any{
		"task":          "inspect repo",
		"yield_time_ms": 30000,
	})
	if !strings.Contains(got, "task yielded before completion") {
		t.Fatalf("unexpected spawn running summary: %q", got)
	}
}

func TestSummarizeToolResponse_BashYieldIsFriendly(t *testing.T) {
	got := summarizeToolResponseWithCall("BASH", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "running",
		"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
		"result":  "progress: 1/10",
	}, map[string]any{
		"command":       "sleep 10",
		"yield_time_ms": 2000,
	})
	if !strings.Contains(got, "task yielded before completion") || !strings.Contains(got, "progress: 1/10") {
		t.Fatalf("unexpected bash running summary: %q", got)
	}
}

func TestSummarizeToolArgs_BashOmitsCommandWrapper(t *testing.T) {
	got := summarizeToolArgs("BASH", map[string]any{
		"command": "go test ./internal/cli/tuiapp",
	})
	if got != "go test ./internal/cli/tuiapp" {
		t.Fatalf("unexpected bash arg summary: %q", got)
	}
	if strings.Contains(got, "{command=") {
		t.Fatalf("expected raw bash command without wrapper, got %q", got)
	}
}

func TestSummarizeToolArgs_SpawnOmitsTaskWrapper(t *testing.T) {
	got := summarizeToolArgs("SPAWN", map[string]any{
		"task": "sleep 8; echo \"Task 1 completed at $(date)\" > task1_result.txt; echo \"This task simulated a long-running delegate with a deliberately verbose payload for summary truncation\"; python3 -c \"print('tail marker')\"",
	})
	if strings.Contains(got, "{task=") {
		t.Fatalf("expected raw spawn task text without wrapper, got %q", got)
	}
	if !strings.Contains(got, "sleep 8;") || !strings.Contains(got, "tail marker") || !strings.Contains(got, "deliberately verbose payload") {
		t.Fatalf("expected spawn summary to keep full normalized text for display-layer truncation, got %q", got)
	}
}

func TestSummarizeToolResponse_TaskWaitRendersFriendlySummary(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "running",
		"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
		"result":  "line-1\nline-2",
	}, map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 5000,
	})
	if got != "task yielded before completion" {
		t.Fatalf("unexpected task wait summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskWaitCompletedDoesNotEchoRequestedDuration(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"state": "completed",
		"msg":   "task success",
	}, map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 10000,
	})
	if got != "Completed" {
		t.Fatalf("unexpected completed task wait summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskWaitFastRunningPrefersStateOverRequestedDuration(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "running",
		"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
		"result":  "still going",
	}, map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 30000,
	})
	if got != "task yielded before completion" {
		t.Fatalf("unexpected fast-running task wait summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskStatusRendersFriendlyState(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "waiting_input",
		"msg":     "waiting for input; use TASK write with task_id t-1234567890ab",
	}, map[string]any{
		"action":  "status",
		"task_id": "t-1234567890ab",
	})
	if !strings.Contains(got, "waiting for input") {
		t.Fatalf("unexpected task status summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskListRendersFriendlySummary(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"tasks": []any{
			map[string]any{"task_id": "t-1", "state": "running", "summary": "task yielded before completion"},
			map[string]any{"task_id": "t-2", "state": "cancelled", "summary": "cancelled"},
		},
	}, map[string]any{
		"action": "list",
	})
	if got != "Listed 2 tasks (1 active)" {
		t.Fatalf("unexpected task list summary: %q", got)
	}
}

func TestSummarizeToolArgs_TaskWaitRendersDuration(t *testing.T) {
	got := summarizeToolArgs("TASK", map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 5000,
	})
	if got != "5 s" {
		t.Fatalf("unexpected task wait args summary: %q", got)
	}
}

func TestSummarizeToolArgs_TaskWaitDefaultsToFiveSeconds(t *testing.T) {
	got := summarizeToolArgs("TASK", map[string]any{
		"action":  "wait",
		"task_id": "t-1234567890ab",
	})
	if got != "5 s" {
		t.Fatalf("unexpected default task wait args summary: %q", got)
	}
	if strings.Contains(got, "t-1234567890ab") {
		t.Fatalf("did not expect task id in wait args summary: %q", got)
	}
}

func TestSummarizeToolArgs_TaskWaitZeroFallsBackToDefault(t *testing.T) {
	got := summarizeToolArgs("TASK", map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 0,
	})
	if got != "5 s" {
		t.Fatalf("unexpected explicit zero task wait args summary: %q", got)
	}
}

func TestSummarizeToolArgs_TaskWaitNegativeFallsBackToDefault(t *testing.T) {
	got := summarizeToolArgs("TASK", map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": -1,
	})
	if got != "5 s" {
		t.Fatalf("unexpected negative task wait args summary: %q", got)
	}
}

func TestSummarizeToolArgs_TaskStatusAndCancelHideRawTaskIDs(t *testing.T) {
	if got := summarizeToolArgs("TASK", map[string]any{
		"action":  "status",
		"task_id": "t-1234567890ab",
	}); got != "" {
		t.Fatalf("expected empty status args summary, got %q", got)
	}
	if got := summarizeToolArgs("TASK", map[string]any{
		"action":  "cancel",
		"task_id": "t-1234567890ab",
	}); got != "" {
		t.Fatalf("expected empty cancel args summary, got %q", got)
	}
}

func TestSummarizeToolArgs_TaskWriteShowsInputPreview(t *testing.T) {
	got := summarizeToolArgs("TASK", map[string]any{
		"action":  "write",
		"task_id": "t-1234567890ab",
		"input":   "hello\n",
	})
	if got != "hello\\n" {
		t.Fatalf("expected write input preview, got %q", got)
	}
	if strings.Contains(got, "action=") || strings.Contains(got, "t-1234567890ab") {
		t.Fatalf("did not expect raw action/task metadata in write preview, got %q", got)
	}
}

func TestPrintEvent_TaskResponseRendersFriendlyLine(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_task_1",
					Name: "TASK",
					Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
				},
			},
		},
	}, state)
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_1",
				Name: "TASK",
				Result: map[string]any{
					"task_id": "t-1234567890ab",
					"state":   "running",
					"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
					"result":  "line-1\nline-2",
				},
			},
		},
	}, state)
	rendered := out.String()
	if !strings.Contains(rendered, "▸ WAIT 5 s") {
		t.Fatalf("expected WAIT call render, got %q", rendered)
	}
	if !strings.Contains(rendered, "✓ WAITED task yielded before completion") {
		t.Fatalf("expected friendly TASK render, got %q", rendered)
	}
	if strings.Contains(rendered, "t-1234567890ab") || strings.Contains(rendered, "line-2") {
		t.Fatalf("did not expect task id or output preview in TASK render, got %q", rendered)
	}
}

func TestPrintEvent_TaskResponseWithoutYieldUsesDefaultFriendlyWait(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_task_2",
					Name: "TASK",
					Args: `{"action":"wait","task_id":"t-1234567890ab"}`,
				},
			},
		},
	}, state)
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_2",
				Name: "TASK",
				Result: map[string]any{
					"task_id": "t-1234567890ab",
					"state":   "running",
					"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
				},
			},
		},
	}, state)
	rendered := out.String()
	if !strings.Contains(rendered, "▸ WAIT 5 s") || !strings.Contains(rendered, "✓ WAITED task yielded before completion") {
		t.Fatalf("expected default friendly wait rendering, got %q", rendered)
	}
}

func TestPrintEvent_TaskStatusAndCancelHideRawArgs(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_task_status", Name: "TASK", Args: `{"action":"status","task_id":"t-1234567890ab"}`},
				{ID: "call_task_cancel", Name: "TASK", Args: `{"action":"cancel","task_id":"t-1234567890ab"}`},
			},
		},
	}, state)
	rendered := out.String()
	if !strings.Contains(rendered, "▸ CHECK") || !strings.Contains(rendered, "▸ CANCEL") {
		t.Fatalf("expected friendly CHECK/CANCEL call rendering, got %q", rendered)
	}
	if strings.Contains(rendered, "action=status") || strings.Contains(rendered, "action=cancel") || strings.Contains(rendered, "t-1234567890ab") {
		t.Fatalf("did not expect raw task args in CHECK/CANCEL rendering, got %q", rendered)
	}
}

func TestSummarizeToolResponse_TaskCancelAvoidsDuplicateCancelledLabel(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "cancelled",
		"running": false,
	}, map[string]any{
		"action":  "cancel",
		"task_id": "t-1234567890ab",
	})
	if got != "" {
		t.Fatalf("expected empty cancel summary to avoid duplicate label, got %q", got)
	}
}

func TestRenderSpawnSummaryPreview_StripsCodeFences(t *testing.T) {
	got := renderSpawnSummaryPreview("## Header\n\n```sh\necho hi\nls\n```\n\nDone")
	if strings.Contains(got, "```") {
		t.Fatalf("expected code fences stripped, got %q", got)
	}
	if strings.Contains(got, "echo hi") || strings.Contains(got, "ls") {
		t.Fatalf("expected fenced block contents hidden, got %q", got)
	}
	if !strings.Contains(got, "Header") || !strings.Contains(got, "Done") {
		t.Fatalf("unexpected spawn preview %q", got)
	}
}

func TestCompactTaskPreview_StripsDelegateCodeFenceContent(t *testing.T) {
	got := compactTaskPreview("step 1\n```text\n12\n-rw-r--r-- demo.html\n```\nstep 2")
	if strings.Contains(got, "demo.html") || strings.Contains(got, "\n12\n") {
		t.Fatalf("expected fenced output stripped from compact preview, got %q", got)
	}
	if !strings.Contains(got, "step 1") || !strings.Contains(got, "step 2") {
		t.Fatalf("expected prose lines preserved, got %q", got)
	}
}

func TestFormatToolResultLine_RendersMultilineBodyBelowHeader(t *testing.T) {
	got := formatToolResultLine("✓ ", "SPAWN", "\nline-1\nline-2")
	if got != "✓ SPAWN\nline-1\nline-2\n" {
		t.Fatalf("unexpected multiline tool result rendering: %q", got)
	}
}

func TestPrintEvent_PatchResponseUsesRecordedToolCallArgs(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_1",
					Name: "PATCH",
					Args: `{"path":"a.txt","old":"alpha","new":"beta"}`,
				},
			},
		},
	}, state)
	printEvent(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_1",
				Name: "PATCH",
				Result: map[string]any{
					"path":      "a.txt",
					"replaced":  1,
					"old_count": 1,
					"created":   false,
				},
			},
		},
	}, state)
	rendered := out.String()
	if !strings.Contains(rendered, "edited a.txt") {
		t.Fatalf("expected patch summary in rendered output, got %q", rendered)
	}
	if strings.Contains(rendered, "--- old") || strings.Contains(rendered, "+beta") {
		t.Fatalf("did not expect textual diff preview rendered from event args, got %q", rendered)
	}
}

func TestTailLines_FiltersBlankLinesBeforeTail(t *testing.T) {
	got := tailLines("a\n\nb\n \n\nc\n", 2)
	if got != "...\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestUsageFloorFromMeta_PrefersTotalThenPromptPlusCompletion(t *testing.T) {
	if got := usageFloorFromMeta(map[string]any{
		"usage": map[string]any{
			"prompt_tokens":     11,
			"completion_tokens": 7,
			"total_tokens":      21,
		},
	}); got != 21 {
		t.Fatalf("expected total_tokens floor, got %d", got)
	}
	if got := usageFloorFromMeta(map[string]any{
		"usage": map[string]any{
			"prompt_tokens":     11,
			"completion_tokens": 7,
		},
	}); got != 18 {
		t.Fatalf("expected prompt+completion floor, got %d", got)
	}
}
