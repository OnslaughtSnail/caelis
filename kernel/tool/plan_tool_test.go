package tool

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessionmem "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestPlanTool_UpdatesSessionState(t *testing.T) {
	store := sessionmem.New()
	sess := &session.Session{AppName: "app", UserID: "user", ID: "session"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	toolImpl, err := NewPlanTool()
	if err != nil {
		t.Fatal(err)
	}
	ctx := session.WithStateContext(context.Background(), sess, store)
	_, err = toolImpl.Run(ctx, map[string]any{
		"entries": []map[string]any{
			{"content": "Inspect repo", "status": "completed"},
			{"content": "Implement fix", "status": "in_progress"},
			{"content": "Run tests", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := state["plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected persisted plan payload, got %#v", state["plan"])
	}
	var items []map[string]any
	if err := convertViaJSON(payload["entries"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 plan entries, got %#v", items)
	}
}
