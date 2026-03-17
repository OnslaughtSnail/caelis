package runtime

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// ----- shouldPersistEvent tests -----

func TestShouldPersistEvent_NilEvent(t *testing.T) {
	if shouldPersistEvent(nil, false) {
		t.Fatal("nil event must not be persisted")
	}
	if shouldPersistEvent(nil, true) {
		t.Fatal("nil event must not be persisted even with persistPartial=true")
	}
}

func TestShouldPersistEvent_NormalEventPersists(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-1",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
	}
	if !shouldPersistEvent(ev, false) {
		t.Fatal("normal assistant event must be persisted")
	}
	if !shouldPersistEvent(ev, true) {
		t.Fatal("normal assistant event must be persisted with persistPartial=true")
	}
}

func TestShouldPersistEvent_SkipsOverlayEvent(t *testing.T) {
	ev := session.MarkOverlay(&session.Event{
		ID:      "ev-overlay",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "ephemeral"},
	})
	if shouldPersistEvent(ev, false) {
		t.Fatal("overlay event must not be persisted")
	}
	if shouldPersistEvent(ev, true) {
		t.Fatal("overlay event must not be persisted even with persistPartial=true")
	}
}

func TestShouldPersistEvent_SkipsLifecycleEvent(t *testing.T) {
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	ev := lifecycleEvent(sess, RunLifecycleStatusRunning, "run", nil)
	if shouldPersistEvent(ev, false) {
		t.Fatal("lifecycle event must not be persisted")
	}
	if shouldPersistEvent(ev, true) {
		t.Fatal("lifecycle event must not be persisted even with persistPartial=true")
	}
}

func TestShouldPersistEvent_PartialWithFlagFalse(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-partial",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "partial"},
	}
	session.SetEventType(ev, session.EventTypePartialAnswer)
	if shouldPersistEvent(ev, false) {
		t.Fatal("partial event must not be persisted when persistPartial=false")
	}
}

func TestShouldPersistEvent_PartialWithFlagTrue(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-partial",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "partial"},
	}
	session.SetEventType(ev, session.EventTypePartialAnswer)
	if !shouldPersistEvent(ev, true) {
		t.Fatal("partial event must be persisted when persistPartial=true")
	}
}

func TestShouldPersistEvent_PartialReasoningWithFlagFalse(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-partial-reasoning",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "thinking"},
	}
	session.SetEventType(ev, session.EventTypePartialReasoning)
	if shouldPersistEvent(ev, false) {
		t.Fatal("partial reasoning event must not be persisted when persistPartial=false")
	}
}

func TestShouldPersistEvent_OverlayPartialNeverPersists(t *testing.T) {
	ev := session.MarkOverlay(&session.Event{
		ID:      "ev-overlay-partial",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "overlay partial"},
	})
	session.SetEventType(ev, session.EventTypeOverlayPartialAnswer)
	if shouldPersistEvent(ev, true) {
		t.Fatal("overlay partial event must not be persisted even with persistPartial=true")
	}
}

func TestShouldPersistEvent_UserEventPersists(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-user",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleUser, Text: "question"},
	}
	if !shouldPersistEvent(ev, false) {
		t.Fatal("user event must be persisted")
	}
}

func TestShouldPersistEvent_ToolResponsePersists(t *testing.T) {
	ev := &session.Event{
		ID:   "ev-tool",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "call-1",
				Name:   "read",
				Result: map[string]any{"content": "file data"},
			},
		},
	}
	if !shouldPersistEvent(ev, false) {
		t.Fatal("tool response event must be persisted")
	}
}

// ----- isDurableReplayEvent tests -----

func TestIsDurableReplayEvent_NilEvent(t *testing.T) {
	if isDurableReplayEvent(nil, false) {
		t.Fatal("nil event must not be durable replay")
	}
	if isDurableReplayEvent(nil, true) {
		t.Fatal("nil event must not be durable replay with persistPartial=true")
	}
}

func TestIsDurableReplayEvent_NormalEventIsDurable(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-1",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
	}
	if !isDurableReplayEvent(ev, false) {
		t.Fatal("normal assistant event must be durable replay")
	}
}

func TestIsDurableReplayEvent_PartialFilteredEvenWhenPersisted(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-partial",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "partial"},
	}
	session.SetEventType(ev, session.EventTypePartialAnswer)
	// Even when persistPartial=true (shouldPersistEvent returns true),
	// isDurableReplayEvent must still filter partials.
	if isDurableReplayEvent(ev, true) {
		t.Fatal("partial event must not be durable replay even when persistPartial=true")
	}
}

func TestIsDurableReplayEvent_UIOnlyNotDurable(t *testing.T) {
	ev := session.MarkUIOnly(&session.Event{
		ID:      "ev-ui",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleSystem, Text: "notice"},
	})
	if isDurableReplayEvent(ev, false) {
		t.Fatal("ui-only event must not be durable replay")
	}
}

func TestIsDurableReplayEvent_OverlayNotDurable(t *testing.T) {
	ev := session.MarkOverlay(&session.Event{
		ID:      "ev-overlay",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleAssistant, Text: "overlay"},
	})
	if isDurableReplayEvent(ev, false) {
		t.Fatal("overlay event must not be durable replay")
	}
}

func TestIsDurableReplayEvent_LifecycleNotDurable(t *testing.T) {
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	ev := lifecycleEvent(sess, RunLifecycleStatusCompleted, "run", nil)
	if isDurableReplayEvent(ev, false) {
		t.Fatal("lifecycle event must not be durable replay")
	}
}

func TestIsDurableReplayEvent_UserEventIsDurable(t *testing.T) {
	ev := &session.Event{
		ID:      "ev-user",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleUser, Text: "hello"},
	}
	if !isDurableReplayEvent(ev, false) {
		t.Fatal("user event must be durable replay")
	}
}

func TestIsDurableReplayEvent_ToolResponseIsDurable(t *testing.T) {
	ev := &session.Event{
		ID:   "ev-tool",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "call-1",
				Name:   "read",
				Result: map[string]any{"content": "data"},
			},
		},
	}
	if !isDurableReplayEvent(ev, false) {
		t.Fatal("tool response event must be durable replay")
	}
}

// ----- streamResyncEvent tests -----

func TestStreamResyncEvent_IsUIOnly(t *testing.T) {
	ev := streamResyncEvent()
	if ev == nil {
		t.Fatal("streamResyncEvent must return non-nil")
	}
	if !session.IsUIOnly(ev) {
		t.Fatal("stream resync event must be UI-only")
	}
	if ev.Message.Role != model.RoleSystem {
		t.Fatalf("expected system role, got %q", ev.Message.Role)
	}
	kind, _ := ev.Meta["kind"].(string)
	if kind != "stream_resync" {
		t.Fatalf("expected kind=stream_resync, got %q", kind)
	}
}

func TestStreamResyncEvent_NeverPersisted(t *testing.T) {
	ev := streamResyncEvent()
	if shouldPersistEvent(ev, true) {
		t.Fatal("stream resync event must never be persisted")
	}
}

// ----- buildRecoveryEvents additional tests -----

func TestBuildRecoveryEvents_MultipleDanglingCalls(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_multi",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call_a", Name: "READ", Args: `{"path":"/a"}`},
					{ID: "call_b", Name: "WRITE", Args: `{"path":"/b","content":"x"}`},
					{ID: "call_c", Name: "BASH", Args: `{"command":"ls"}`},
				},
			},
		},
	}
	recovery := buildRecoveryEvents(input)
	if len(recovery) != 3 {
		t.Fatalf("expected 3 recovery events for 3 dangling calls, got %d", len(recovery))
	}
	seen := map[string]bool{}
	for _, ev := range recovery {
		if ev.Message.Role != model.RoleTool {
			t.Fatalf("expected role=tool, got %q", ev.Message.Role)
		}
		if ev.Message.ToolResponse == nil {
			t.Fatal("expected non-nil tool response")
		}
		seen[ev.Message.ToolResponse.ID] = true
		result := ev.Message.ToolResponse.Result
		if result == nil {
			t.Fatal("expected non-nil result map")
		}
		if result["interrupted"] != true {
			t.Fatal("expected interrupted=true in recovery result")
		}
		if ev.Meta[metaKind] != metaKindRecovery {
			t.Fatalf("expected meta kind=%q, got %v", metaKindRecovery, ev.Meta[metaKind])
		}
	}
	for _, id := range []string{"call_a", "call_b", "call_c"} {
		if !seen[id] {
			t.Fatalf("missing recovery event for %s", id)
		}
	}
}

func TestBuildRecoveryEvents_PartiallyResolvedCalls(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "READ", Args: `{"path":"/a"}`},
					{ID: "call_2", Name: "WRITE", Args: `{"path":"/b","content":"x"}`},
				},
			},
		},
		{
			ID:   "tool_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:     "call_1",
					Name:   "READ",
					Result: map[string]any{"content": "data"},
				},
			},
		},
	}
	recovery := buildRecoveryEvents(input)
	if len(recovery) != 1 {
		t.Fatalf("expected 1 recovery event for 1 dangling call, got %d", len(recovery))
	}
	if recovery[0].Message.ToolResponse.ID != "call_2" {
		t.Fatalf("expected recovery for call_2, got %q", recovery[0].Message.ToolResponse.ID)
	}
}

func TestBuildRecoveryEvents_EmptyInput(t *testing.T) {
	if got := buildRecoveryEvents(nil); len(got) != 0 {
		t.Fatalf("expected nil/empty for nil input, got %d", len(got))
	}
	if got := buildRecoveryEvents([]*session.Event{}); len(got) != 0 {
		t.Fatalf("expected nil/empty for empty input, got %d", len(got))
	}
}

func TestBuildRecoveryEvents_RecoveryEventCarriesToolMeta(t *testing.T) {
	input := []*session.Event{
		{
			ID:   "assistant_1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call_x", Name: "BASH", Args: `{"command":"sleep 30"}`},
				},
			},
		},
	}
	recovery := buildRecoveryEvents(input)
	if len(recovery) != 1 {
		t.Fatalf("expected 1 recovery event, got %d", len(recovery))
	}
	detail, ok := recovery[0].Meta[metaKindRecovery].(map[string]any)
	if !ok {
		t.Fatal("expected recovery meta detail map")
	}
	if detail["type"] != "dangling_tool_call" {
		t.Fatalf("expected type=dangling_tool_call, got %v", detail["type"])
	}
	if detail["tool_call_id"] != "call_x" {
		t.Fatalf("expected tool_call_id=call_x, got %v", detail["tool_call_id"])
	}
	if detail["tool_name"] != "BASH" {
		t.Fatalf("expected tool_name=BASH, got %v", detail["tool_name"])
	}
	if detail["tool_args"] != `{"command":"sleep 30"}` {
		t.Fatalf("expected tool_args preserved, got %v", detail["tool_args"])
	}
}
