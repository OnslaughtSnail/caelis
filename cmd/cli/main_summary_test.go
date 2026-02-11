package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestSummarizeToolResponse_ReadUsesFileNameAndRange(t *testing.T) {
	got := summarizeToolResponse("READ", map[string]any{
		"path":       "/tmp/work/project/main.go",
		"start_line": 3,
		"end_line":   12,
		"has_more":   false,
	})
	if !strings.Contains(got, "read main.go lines 3-12") {
		t.Fatalf("unexpected read summary: %q", got)
	}
	if strings.Contains(got, "/tmp/work/project") {
		t.Fatalf("expected basename-only summary, got %q", got)
	}
}

func TestSummarizeToolResponse_ReadTruncatedShowsNextOffset(t *testing.T) {
	got := summarizeToolResponse("READ", map[string]any{
		"path":        "/tmp/a.txt",
		"start_line":  1,
		"end_line":    200,
		"has_more":    true,
		"next_offset": 4096,
	})
	if !strings.Contains(got, "truncated") || !strings.Contains(got, "next_offset=4096") {
		t.Fatalf("unexpected truncated read summary: %q", got)
	}
}

func TestSummarizeToolResponse_PatchIncludesMetadataPreview(t *testing.T) {
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
	if !strings.Contains(got, "edited a.txt (replaced=1/1)") {
		t.Fatalf("unexpected patch summary header: %q", got)
	}
	if !strings.Contains(got, "\n  @@ -5,1 +5,1 @@\n  --- old\n  +++ new\n  -old\n  +new") {
		t.Fatalf("expected indented patch preview, got %q", got)
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

func TestSummarizeToolResponse_PatchBuildsPreviewFromCallArgs(t *testing.T) {
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
	if !strings.Contains(got, "edited a.txt (replaced=1/1)") {
		t.Fatalf("unexpected patch summary header: %q", got)
	}
	if !strings.Contains(got, "\n  --- old\n  +++ new\n  -line1\n  -old\n  +line1\n  +new") {
		t.Fatalf("expected preview generated from call args, got %q", got)
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
					Args: map[string]any{
						"path": "a.txt",
						"old":  "alpha",
						"new":  "beta",
					},
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
	if !strings.Contains(rendered, "edited a.txt (replaced=1/1)") {
		t.Fatalf("expected patch summary in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, "--- old") || !strings.Contains(rendered, "+beta") {
		t.Fatalf("expected diff preview rendered from event args, got %q", rendered)
	}
}
