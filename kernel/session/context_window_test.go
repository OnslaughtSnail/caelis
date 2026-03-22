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
	// Compaction event without summarized_to_event_id: falls back to
	// compaction + everything after it (no tail reconstruction).
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

func TestContextWindowEvents_TailReconstructedFromSummarizedToID(t *testing.T) {
	// Store layout: [old_user, old_assistant, tail_user, tail_assistant, compaction]
	// Compaction summarized up to old_assistant. Tail (tail_user, tail_assistant)
	// must appear in the window AFTER the compaction event without being
	// duplicated in the store.
	events := []*Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old answer"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail q"}},
		{ID: "tail_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "tail a"}},
		{
			ID:      "compact_1",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
				"compaction": map[string]any{
					"summarized_to_event_id": "old_assistant",
				},
			},
		},
	}
	window := ContextWindowEvents(events)
	// Expected: [compaction, tail_user, tail_assistant]
	if len(window) != 3 {
		t.Fatalf("expected 3 events (compaction + 2 tail), got %d", len(window))
	}
	expected := []string{"compact_1", "tail_user", "tail_assistant"}
	for i, want := range expected {
		if window[i].ID != want {
			t.Fatalf("window[%d]: got %q, want %q", i, window[i].ID, want)
		}
	}
}

func TestContextWindowEvents_UsesExplicitTailEventIDs(t *testing.T) {
	events := []*Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old answer"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail q"}},
		{ID: "tail_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "tail a"}},
		{
			ID:      "compact_1",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
				"compaction": map[string]any{
					"summarized_to_event_id": "old_assistant",
					"tail_event_ids":         []any{"tail_user", "tail_assistant"},
				},
			},
		},
	}
	window := ContextWindowEvents(events)
	expected := []string{"compact_1", "tail_user", "tail_assistant"}
	if len(window) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(window))
	}
	for i, want := range expected {
		if window[i].ID != want {
			t.Fatalf("window[%d]: got %q, want %q", i, window[i].ID, want)
		}
	}
}

func TestContextWindowEvents_TailPlusPostCompactionEvents(t *testing.T) {
	// After compaction, new events may be appended. The window should be:
	// [compaction, tail..., new events after compaction...]
	events := []*Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail"}},
		{
			ID:      "compact_1",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
				"compaction": map[string]any{
					"summarized_to_event_id": "old_user",
				},
			},
		},
		{ID: "new_after", Message: model.Message{Role: model.RoleUser, Text: "new"}},
	}
	window := ContextWindowEvents(events)
	// Expected: [compaction, tail_user, new_after]
	if len(window) != 3 {
		t.Fatalf("expected 3 events, got %d", len(window))
	}
	expected := []string{"compact_1", "tail_user", "new_after"}
	for i, want := range expected {
		if window[i].ID != want {
			t.Fatalf("window[%d]: got %q, want %q", i, window[i].ID, want)
		}
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
