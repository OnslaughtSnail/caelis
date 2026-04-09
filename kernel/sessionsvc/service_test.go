package sessionsvc

import (
	"context"
	"fmt"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestServiceListDelegations(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child"}
	mustGetOrCreateSession(t, store, parent)
	mustGetOrCreateSession(t, store, child)
	mustAppendEvent(t, store, parent, &session.Event{
		ID:        "ev-parent-1",
		SessionID: parent.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "spawned"),
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
		Message:   model.NewTextMessage(model.RoleAssistant, "child done"),
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
			Message:   model.NewTextMessage(model.RoleAssistant, "spawned"),
			Meta: map[string]any{
				"parent_session_id":   parent.ID,
				"child_session_id":    child.ID,
				"delegation_id":       "dlg-1",
				"parent_tool_call_id": "call-1",
				"parent_tool_name":    "SPAWN",
			},
		})
	}
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
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

func TestIsSessionNotFoundUsesErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("wrapped: %w", session.ErrSessionNotFound)
	if !isSessionNotFound(wrapped) {
		t.Fatal("expected wrapped session.ErrSessionNotFound to match")
	}
	if isSessionNotFound(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Fatal("did not expect unrelated wrapped error to match")
	}
	if isSessionNotFound(fmt.Errorf("prefix %s suffix", session.ErrSessionNotFound.Error())) {
		t.Fatal("did not expect substring-only match")
	}
}

func TestTrySetActiveIsAtomicPerSession(t *testing.T) {
	svc, err := New(ServiceConfig{
		Store:   inmemory.New(),
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.trySetActive("session-1", &activeTurn{}) {
		t.Fatal("expected first reservation to succeed")
	}
	if svc.trySetActive("session-1", &activeTurn{}) {
		t.Fatal("expected second reservation to fail")
	}
	svc.clearActive("session-1")
	if !svc.trySetActive("session-1", &activeTurn{}) {
		t.Fatal("expected reservation after clear to succeed")
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
