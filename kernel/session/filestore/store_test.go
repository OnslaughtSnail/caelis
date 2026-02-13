package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestStore_AppendAndList(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e1", Message: model.Message{Role: model.RoleUser, Text: "hi"}}
	if err := store.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestStore_ListEvents_CompatibleWithConcatenatedJSONObjects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(root, "app", "u", "s", "events.jsonl")
	raw := `{"ID":"e1","SessionID":"s","Message":{"Role":"user","Text":"a"}}{"ID":"e2","SessionID":"s","Message":{"Role":"assistant","Text":"b"}}`
	if err := os.WriteFile(eventsPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "e1" || events[1].ID != "e2" {
		t.Fatalf("unexpected event ids: %s, %s", events[0].ID, events[1].ID)
	}
}

func TestStore_SessionOnlyLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := NewWithOptions(root, Options{Layout: LayoutSessionOnly})
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e1", Message: model.Message{Role: model.RoleUser, Text: "hi"}}
	if err := store.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "s", "events.jsonl")); err != nil {
		t.Fatalf("expected session-only events path to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "u", "s", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("did not expect namespaced events path, err=%v", err)
	}
}

func TestStore_RejectsPathTraversalInSessionKeys(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	bad := &session.Session{AppName: "app", UserID: "u", ID: "../escape"}
	if _, err := store.GetOrCreate(context.Background(), bad); err == nil {
		t.Fatalf("expected path traversal session id to be rejected")
	}
	if err := store.AppendEvent(context.Background(), bad, &session.Event{
		ID:      "e1",
		Message: model.Message{Role: model.RoleUser, Text: "x"},
	}); err == nil {
		t.Fatalf("expected append with path traversal session id to fail")
	}
	if _, err := store.ListEvents(context.Background(), bad); err == nil {
		t.Fatalf("expected list with path traversal session id to fail")
	}
	if _, err := store.SnapshotState(context.Background(), bad); err == nil {
		t.Fatalf("expected snapshot with path traversal session id to fail")
	}
}

func TestStore_ListContextWindowEvents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{ID: "old", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{
			ID:      "compact",
			Message: model.Message{Role: model.RoleSystem, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new", Message: model.Message{Role: model.RoleUser, Text: "new"}},
	}
	for _, ev := range events {
		if err := store.AppendEvent(context.Background(), s, ev); err != nil {
			t.Fatal(err)
		}
	}

	window, err := store.ListContextWindowEvents(context.Background(), s)
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
