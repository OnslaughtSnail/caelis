package session_test

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

type demoState struct {
	Value string
}

type demoCodec struct{}

func (demoCodec) LoadState(values map[string]any) (demoState, error) {
	state := demoState{}
	if values == nil {
		return state, nil
	}
	text, _ := values["demo"].(string)
	state.Value = text
	return state, nil
}

func (demoCodec) StoreState(values map[string]any, state demoState) (map[string]any, error) {
	if values == nil {
		values = map[string]any{}
	}
	next := make(map[string]any, len(values)+1)
	for key, value := range values {
		next[key] = value
	}
	next["demo"] = state.Value
	return next, nil
}

func TestMapSessionStateStore_SaveAndLoad(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "typed"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), sess, map[string]any{
		"existing": "keep",
	}); err != nil {
		t.Fatal(err)
	}

	typed, err := session.NewMapSessionStateStore[demoState](store, demoCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := typed.Save(context.Background(), sess, demoState{Value: "hello"}); err != nil {
		t.Fatal(err)
	}

	got, err := typed.Load(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "hello" {
		t.Fatalf("expected typed value %q, got %q", "hello", got.Value)
	}

	snapshot, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot["existing"] != "keep" {
		t.Fatalf("expected existing state to be preserved, got %+v", snapshot)
	}
	if snapshot["demo"] != "hello" {
		t.Fatalf("expected typed state in snapshot, got %+v", snapshot)
	}
}
