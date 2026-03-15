package eventview

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestAgentVisible_UsesLatestContextWindowWithoutLifecycle(t *testing.T) {
	now := time.Now()
	events := []*session.Event{
		{ID: "old-user", Time: now, Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{ID: "life-1", Time: now, Message: model.Message{Role: model.RoleSystem}, Meta: map[string]any{"kind": "lifecycle"}},
		{ID: "compact", Time: now, Message: model.Message{Role: model.RoleUser, Text: "summary"}, Meta: map[string]any{"kind": "compaction"}},
		{ID: "life-2", Time: now, Message: model.Message{Role: model.RoleSystem}, Meta: map[string]any{"kind": "lifecycle"}},
		{ID: "new-user", Time: now, Message: model.Message{Role: model.RoleUser, Text: "new"}},
	}

	got := AgentVisible(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 visible events, got %d", len(got))
	}
	if got[0].ID != "compact" || got[1].ID != "new-user" {
		t.Fatalf("unexpected visible event ids: %q, %q", got[0].ID, got[1].ID)
	}
}

func TestMessages_SkipsUIOnlyAndRuntimeSystemNotices(t *testing.T) {
	now := time.Now()
	uiOnly := session.MarkNotice(&session.Event{
		ID:   "ui-only",
		Time: now,
	}, session.NoticeLevelWarn, "retrying in 1s")
	events := session.NewEvents([]*session.Event{
		{ID: "user", Time: now, Message: model.Message{Role: model.RoleUser, Text: "hi"}},
		uiOnly,
		{ID: "warn-old", Time: now, Message: model.Message{Role: model.RoleSystem, Text: "warn: old persisted warning"}},
		{ID: "note-old", Time: now, Message: model.Message{Role: model.RoleSystem, Text: "note: old persisted note"}},
		{ID: "assistant", Time: now, Message: model.Message{Role: model.RoleAssistant, Text: "done"}},
	})

	got := Messages(events, "sys", nil)
	if len(got) != 3 {
		t.Fatalf("expected system prompt + user + assistant only, got %+v", got)
	}
	if got[0].Role != model.RoleSystem || got[0].Text != "sys" {
		t.Fatalf("unexpected system prompt: %+v", got[0])
	}
	if got[1].Role != model.RoleUser || got[1].Text != "hi" {
		t.Fatalf("unexpected user message: %+v", got[1])
	}
	if got[2].Role != model.RoleAssistant || got[2].Text != "done" {
		t.Fatalf("unexpected assistant message: %+v", got[2])
	}
}

func TestPendingToolCalls_ReturnsOnlyUnmatchedCallsInOrder(t *testing.T) {
	now := time.Now()
	events := []*session.Event{
		{
			ID:   "assistant-1",
			Time: now,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call-b", Name: "BASH", Args: "{\"cmd\":\"pwd\"}"},
					{ID: "call-a", Name: "READ", Args: "{\"path\":\"a.txt\"}"},
				},
			},
		},
		{
			ID:   "tool-a",
			Time: now,
			Message: model.Message{
				Role:         model.RoleTool,
				ToolResponse: &model.ToolResponse{ID: "call-a", Name: "READ", Result: map[string]any{"ok": true}},
			},
		},
		{
			ID:   "assistant-2",
			Time: now,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call-c", Name: "WRITE", Args: "{\"path\":\"b.txt\"}"},
				},
			},
		},
	}

	got := PendingToolCalls(session.NewEvents(events))
	if len(got) != 2 {
		t.Fatalf("expected 2 pending tool calls, got %d", len(got))
	}
	if got[0].ID != "call-b" || got[1].ID != "call-c" {
		t.Fatalf("unexpected pending order: %+v", got)
	}
}
