package runtime

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestBuildRecoveryEvents_GeneratesToolInterrupt(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID:   "call_1",
						Name: "READ",
						Args: map[string]any{"path": "/tmp/a.txt"},
					},
				},
			},
		},
	}

	recovery := buildRecoveryEvents(input)
	if len(recovery) != 1 {
		t.Fatalf("expected 1 recovery event, got %d", len(recovery))
	}
	ev := recovery[0]
	if ev.Message.Role != model.RoleTool {
		t.Fatalf("expected role=tool, got %q", ev.Message.Role)
	}
	if ev.Message.ToolResponse == nil || ev.Message.ToolResponse.ID != "call_1" {
		t.Fatalf("expected tool response for call_1, got %+v", ev.Message.ToolResponse)
	}
	if ev.Meta[metaKind] != metaKindRecovery {
		t.Fatalf("expected meta kind %q, got %#v", metaKindRecovery, ev.Meta[metaKind])
	}
}

func TestBuildRecoveryEvents_SkipsClosedToolCalls(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID:   "call_1",
						Name: "READ",
						Args: map[string]any{"path": "/tmp/a.txt"},
					},
				},
			},
		},
		{
			ID:   "tool_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:   "call_1",
					Name: "READ",
					Result: map[string]any{
						"path": "/tmp/a.txt",
					},
				},
			},
		},
	}

	recovery := buildRecoveryEvents(input)
	if len(recovery) != 0 {
		t.Fatalf("expected no recovery event, got %d", len(recovery))
	}
}

func TestBuildRecoveryEvents_UsesLastCompactionWindow(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_old",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID:   "call_old",
						Name: "READ",
						Args: map[string]any{"path": "/tmp/old.txt"},
					},
				},
			},
		},
		{
			ID:   "compaction",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "summary",
			},
			Meta: map[string]any{
				metaKind: metaKindCompaction,
			},
		},
		{
			ID:   "assistant_new",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "after compaction",
			},
		},
	}

	recovery := buildRecoveryEvents(input)
	if len(recovery) != 0 {
		t.Fatalf("expected no recovery event after compaction window reset, got %d", len(recovery))
	}
}
