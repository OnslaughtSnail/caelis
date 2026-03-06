package inmemory

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestStore_ListContextWindowEvents(t *testing.T) {
	store := New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "old",
		Message: model.Message{Role: model.RoleUser, Text: "old"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "compact",
		Message: model.Message{Role: model.RoleSystem, Text: "summary"},
		Meta: map[string]any{
			"kind": "compaction",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "new",
		Message: model.Message{Role: model.RoleUser, Text: "new"},
	}); err != nil {
		t.Fatal(err)
	}

	window, err := store.ListContextWindowEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 {
		t.Fatalf("expected 2 events in context window, got %d", len(window))
	}
	if window[0].ID != "compact" || window[1].ID != "new" {
		t.Fatalf("unexpected window ids: %s, %s", window[0].ID, window[1].ID)
	}
}

func TestStore_ListContextWindowEvents_UsesLatestCompactionWindow(t *testing.T) {
	store := New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-tail"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old user"}},
		{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old assistant"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail user"}},
		{ID: "tail_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "tail assistant"}},
		{
			ID:      "compact",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new_user", Message: model.Message{Role: model.RoleUser, Text: "new user"}},
	}
	for _, ev := range events {
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}

	window, err := store.ListContextWindowEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 {
		t.Fatalf("expected 2 events in context window, got %d", len(window))
	}
	if window[0].ID != "compact" || window[1].ID != "new_user" {
		t.Fatalf("unexpected window ids: %s, %s", window[0].ID, window[1].ID)
	}
}

func TestStore_ReplaceState_RoundTrip(t *testing.T) {
	store := New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-state"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"runtime.lifecycle": map[string]any{
			"status": "completed",
		},
	}
	if err := store.ReplaceState(context.Background(), sess, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if got["runtime.lifecycle"] == nil {
		t.Fatalf("expected lifecycle state, got %+v", got)
	}
}
