package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestRuntime_SessionEvents_FilterLifecycleAndLimit(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "user-1",
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleUser, "hi"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, lifecycleEvent(sess, RunLifecycleStatusRunning, "run", nil)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "assistant-1",
		Time:    time.Now().Add(time.Second),
		Message: model.NewTextMessage(model.RoleAssistant, "hello"),
	}); err != nil {
		t.Fatal(err)
	}
	events, err := rt.SessionEvents(context.Background(), SessionEventsRequest{
		AppName:          "app",
		UserID:           "u",
		SessionID:        "s",
		Limit:            1,
		IncludeLifecycle: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID != "assistant-1" {
		t.Fatalf("expected last non-lifecycle event assistant-1, got %q", events[0].ID)
	}
}

func TestRuntime_SessionEvents_MissingSessionReturnsEmpty(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	events, err := rt.SessionEvents(context.Background(), SessionEventsRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected empty result, got %d", len(events))
	}
}
