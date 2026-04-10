package runstatus

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestStoreSavePreservesUnrelatedState(t *testing.T) {
	ctx := t.Context()
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "user", ID: "run"}
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatalf("GetOrCreate(): %v", err)
	}
	if err := store.ReplaceState(ctx, sess, map[string]any{"existing": "keep"}); err != nil {
		t.Fatalf("ReplaceState(): %v", err)
	}

	lifecycleStore, err := NewStore(store)
	if err != nil {
		t.Fatalf("NewStore(): %v", err)
	}
	now := time.Now().UTC().Round(0)
	if err := lifecycleStore.Save(ctx, sess, State{
		HasLifecycle: true,
		Status:       StatusCompleted,
		Phase:        "run",
		EventID:      "ev_done",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	snapshot, err := store.SnapshotState(ctx, sess)
	if err != nil {
		t.Fatalf("SnapshotState(): %v", err)
	}
	if snapshot["existing"] != "keep" {
		t.Fatalf("existing state lost: %#v", snapshot)
	}

	got, err := lifecycleStore.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("Status=%q, want %q", got.Status, StatusCompleted)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt=%s, want %s", got.UpdatedAt, now)
	}
}
