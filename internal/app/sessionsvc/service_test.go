package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestServiceListDelegationsAndAttach(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child"}
	mustGetOrCreateSession(t, store, parent)
	mustGetOrCreateSession(t, store, child)
	mustAppendEvent(t, store, parent, &session.Event{
		ID:        "ev-parent-1",
		SessionID: parent.ID,
		Message:   model.Message{Role: model.RoleAssistant, Text: "spawned"},
		Meta: map[string]any{
			"parent_session_id":   parent.ID,
			"child_session_id":    child.ID,
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-1",
			"parent_tool_name":    "SPAWN",
		},
	})
	mustAppendEvent(t, store, child, &session.Event{
		ID:        "ev-child-1",
		SessionID: child.ID,
		Message:   model.Message{Role: model.RoleAssistant, Text: "child done"},
	})
	svc, err := New(ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    parent.ID,
		WorkspaceKey: "wk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}
	if delegations[0].ChildSessionID != child.ID || delegations[0].DelegationID != "dlg-1" {
		t.Fatalf("unexpected delegation: %+v", delegations[0])
	}

	loaded, err := svc.AttachSession(context.Background(), AttachSessionRequest{
		SessionRef: SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    parent.ID,
			WorkspaceKey: "wk",
		},
		ChildSessionID: child.ID,
		CWD:            "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID != child.ID {
		t.Fatalf("expected child session %q, got %q", child.ID, loaded.SessionID)
	}
	if len(loaded.Events) != 1 || loaded.Events[0].Message.TextContent() != "child done" {
		t.Fatalf("unexpected child events: %+v", loaded.Events)
	}

	loaded, err = svc.AttachSession(context.Background(), AttachSessionRequest{
		SessionRef: SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    parent.ID,
			WorkspaceKey: "wk",
		},
		DelegationID: "dlg-1",
		CWD:          "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID != child.ID {
		t.Fatalf("expected child session by delegation id, got %q", loaded.SessionID)
	}
}

func TestServiceAttachSessionNotFound(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	mustGetOrCreateSession(t, store, parent)
	svc, err := New(ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AttachSession(context.Background(), AttachSessionRequest{
		SessionRef:     SessionRef{AppName: "app", UserID: "u", SessionID: parent.ID, WorkspaceKey: "wk"},
		ChildSessionID: "missing-child",
	})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("expected session not found, got %v", err)
	}
}

func mustGetOrCreateSession(t *testing.T, store session.Store, sess *session.Session) {
	t.Helper()
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
}

func mustAppendEvent(t *testing.T, store session.Store, sess *session.Session, ev *session.Event) {
	t.Helper()
	if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
		t.Fatal(err)
	}
}

func TestServiceListDelegationsSkipsDuplicates(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child"}
	mustGetOrCreateSession(t, store, parent)
	mustGetOrCreateSession(t, store, child)
	for _, id := range []string{"ev-1", "ev-2"} {
		mustAppendEvent(t, store, parent, &session.Event{
			ID:        id,
			SessionID: parent.ID,
			Message:   model.Message{Role: model.RoleAssistant, Text: "spawned"},
			Meta: map[string]any{
				"parent_session_id":   parent.ID,
				"child_session_id":    child.ID,
				"delegation_id":       "dlg-1",
				"parent_tool_call_id": "call-1",
				"parent_tool_name":    "SPAWN",
			},
		})
	}
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(ServiceConfig{
		Runtime: rt,
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListDelegations(context.Background(), SessionRef{AppName: "app", UserID: "u", SessionID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected deduplicated delegation list, got %d", len(items))
	}
}
