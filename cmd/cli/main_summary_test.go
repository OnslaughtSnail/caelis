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

func TestSummarizeToolResponse_DelegateRendersAssistantWithoutChildID(t *testing.T) {
	got := summarizeToolResponse("DELEGATE", map[string]any{
		"child_session_id": "s-1234567890ab",
		"summary":          "## Done\n\n- item",
	})
	if strings.Contains(got, "child=") {
		t.Fatalf("did not expect child session id in delegate summary: %q", got)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("expected delegate summary to render on the next line, got %q", got)
	}
	if !strings.Contains(got, "Done") {
		t.Fatalf("unexpected delegate summary: %q", got)
	}
}

func TestSummarizeToolResponse_DelegateRunningIncludesLatestOutput(t *testing.T) {
	got := summarizeToolResponse("DELEGATE", map[string]any{
		"task_id":       "t-1234567890ab",
		"running":       true,
		"latest_output": "line-1\nline-2",
	})
	if !strings.Contains(got, "task=t-12345678 running") || !strings.Contains(got, "line-2") {
		t.Fatalf("unexpected delegate running summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskWaitRendersFriendlySummary(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id":       "t-1234567890ab",
		"state":         "running",
		"running":       true,
		"latest_output": "line-1\nline-2",
	}, map[string]any{
		"action":        "wait",
		"task_id":       "t-1234567890ab",
		"yield_time_ms": 5000,
	})
	if got != "-> Waited 5 s" {
		t.Fatalf("unexpected task wait summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskStatusRendersFriendlyState(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"task_id": "t-1234567890ab",
		"state":   "waiting_input",
		"running": false,
	}, map[string]any{
		"action":  "status",
		"task_id": "t-1234567890ab",
	})
	if got != "-> Waiting for input" {
		t.Fatalf("unexpected task status summary: %q", got)
	}
}

func TestSummarizeToolResponse_TaskListRendersFriendlySummary(t *testing.T) {
	got := summarizeToolResponseWithCall("TASK", map[string]any{
		"count": 2,
		"tasks": []any{
			map[string]any{"task_id": "t-1", "state": "running", "running": true},
			map[string]any{"task_id": "t-2", "state": "cancelled", "running": false},
		},
	}, map[string]any{
		"action": "list",
	})
	if got != "-> Listed 2 tasks (1 running)" {
		t.Fatalf("unexpected task list summary: %q", got)
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
					"task_id":       "t-1234567890ab",
					"state":         "running",
					"running":       true,
					"latest_output": "line-1\nline-2",
				},
			},
		},
	}, state)
	rendered := out.String()
	if !strings.Contains(rendered, "✓ Wait -> Waited 5 s") {
		t.Fatalf("expected friendly TASK render, got %q", rendered)
	}
	if strings.Contains(rendered, "t-1234567890ab") || strings.Contains(rendered, "line-2") {
		t.Fatalf("did not expect task id or output preview in TASK render, got %q", rendered)
	}
}

func TestRenderDelegateSummaryPreview_StripsCodeFences(t *testing.T) {
	got := renderDelegateSummaryPreview("## Header\n\n```sh\necho hi\nls\n```\n\nDone")
	if strings.Contains(got, "```") {
		t.Fatalf("expected code fences stripped, got %q", got)
	}
	if strings.Contains(got, "echo hi") || strings.Contains(got, "ls") {
		t.Fatalf("expected fenced block contents hidden, got %q", got)
	}
	if !strings.Contains(got, "Header") || !strings.Contains(got, "Done") {
		t.Fatalf("unexpected delegate preview %q", got)
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
	got := formatToolResultLine("✓ ", "DELEGATE", "\nline-1\nline-2")
	if got != "✓ DELEGATE\nline-1\nline-2\n" {
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
