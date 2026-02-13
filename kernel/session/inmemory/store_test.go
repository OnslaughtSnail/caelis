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
