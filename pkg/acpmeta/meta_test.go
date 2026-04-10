package acpmeta

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessioninmemory "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestSessionMetaStateHelpers(t *testing.T) {
	state := StoreSessionMeta(nil, WithModelAlias(nil, "gpt-5.4"))
	meta := SessionMetaFromState(state)
	if got := ModelAlias(meta); got != "gpt-5.4" {
		t.Fatalf("expected model alias persisted, got %q", got)
	}
}

func TestUpdateSessionMetaPersistsThroughStore(t *testing.T) {
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if err := UpdateSessionMeta(ctx, store, sess, func(meta map[string]any) map[string]any {
		return WithDelegatedChild(WithModelAlias(meta, "sonnet"), true)
	}); err != nil {
		t.Fatal(err)
	}

	meta, err := SessionMetaFromStore(ctx, store, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got := ModelAlias(meta); got != "sonnet" {
		t.Fatalf("expected model alias sonnet, got %q", got)
	}
	if !IsDelegatedChild(meta) {
		t.Fatalf("expected delegated child meta, got %#v", meta)
	}
}

func TestControllerSessionStateHelpers(t *testing.T) {
	state := StoreControllerSession(nil, ControllerSession{
		AgentID:   "codex",
		SessionID: "remote-1",
	})
	ref := ControllerSessionFromState(state)
	if ref.AgentID != "codex" || ref.SessionID != "remote-1" {
		t.Fatalf("unexpected controller session %+v", ref)
	}

	state = StoreControllerSession(state, ControllerSession{})
	ref = ControllerSessionFromState(state)
	if ref != (ControllerSession{}) {
		t.Fatalf("expected cleared controller session, got %+v", ref)
	}
}

func TestUpdateControllerSessionPersistsThroughStore(t *testing.T) {
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-controller"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if err := UpdateControllerSession(ctx, store, sess, func(ref ControllerSession) ControllerSession {
		ref.AgentID = "copilot"
		ref.SessionID = "remote-2"
		return ref
	}); err != nil {
		t.Fatal(err)
	}

	ref, err := ControllerSessionFromStore(ctx, store, sess)
	if err != nil {
		t.Fatal(err)
	}
	if ref.AgentID != "copilot" || ref.SessionID != "remote-2" {
		t.Fatalf("unexpected persisted controller session %+v", ref)
	}
}
