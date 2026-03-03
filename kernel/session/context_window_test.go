package session

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestContextWindowEvents_NoCompaction(t *testing.T) {
	events := []*Event{
		{ID: "u1", Message: model.Message{Role: model.RoleUser, Text: "one"}},
		{ID: "a1", Message: model.Message{Role: model.RoleAssistant, Text: "two"}},
	}
	window := ContextWindowEvents(events)
	if len(window) != 2 {
		t.Fatalf("expected full window length=2, got %d", len(window))
	}
	if window[0].ID != "u1" || window[1].ID != "a1" {
		t.Fatalf("unexpected ids: %s, %s", window[0].ID, window[1].ID)
	}
}

func TestContextWindowEvents_LegacyCompactionFallback(t *testing.T) {
	events := []*Event{
		{ID: "old", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{
			ID:      "compact",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new", Message: model.Message{Role: model.RoleUser, Text: "new"}},
	}
	window := ContextWindowEvents(events)
	if len(window) != 2 {
		t.Fatalf("expected fallback window length=2, got %d", len(window))
	}
	if window[0].ID != "compact" || window[1].ID != "new" {
		t.Fatalf("unexpected ids: %s, %s", window[0].ID, window[1].ID)
	}
}

func TestContextWindowEvents_UsesLatestCompactionWindow(t *testing.T) {
	events := []*Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old user"}},
		{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old assistant"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail user"}},
		{ID: "tail_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "tail assistant"}},
		{
			ID:      "compact_1",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new_user", Message: model.Message{Role: model.RoleUser, Text: "new user"}},
	}
	window := ContextWindowEvents(events)
	if len(window) != 2 {
		t.Fatalf("expected latest-compaction window length=2, got %d", len(window))
	}
	ids := []string{window[0].ID, window[1].ID}
	expected := []string{"compact_1", "new_user"}
	for i := range expected {
		if ids[i] != expected[i] {
			t.Fatalf("unexpected id at %d: got %s want %s", i, ids[i], expected[i])
		}
	}
}
