package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
)

func TestTaskManager_StatusReturnsPersistedCancelledTaskAcrossTurns(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-cancelled",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "sleep 30",
		State:          task.StateCancelled,
		Running:        false,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Result: map[string]any{
			"state":         string(task.StateCancelled),
			"latest_output": "cancelled by runtime cleanup",
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	manager := newTaskManager(nil, nil, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)
	snapshot, err := manager.Status(context.Background(), task.ControlRequest{TaskID: entry.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != task.StateCancelled || snapshot.Running {
		t.Fatalf("expected persisted cancelled snapshot, got state=%q running=%v result=%#v", snapshot.State, snapshot.Running, snapshot.Result)
	}
	if got := snapshot.Result["state"]; got != string(task.StateCancelled) {
		t.Fatalf("expected cancelled result state, got %#v", got)
	}
}

func TestTaskManager_WaitReturnsPersistedCancelledTaskAcrossTurns(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-cancelled-wait",
		Kind:           task.KindDelegate,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "delegate job",
		State:          task.StateCancelled,
		Running:        false,
		SupportsInput:  false,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Result: map[string]any{
			"state": string(task.StateCancelled),
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	manager := newTaskManager(nil, nil, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)
	snapshot, err := manager.Wait(context.Background(), task.ControlRequest{TaskID: entry.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != task.StateCancelled || snapshot.Running {
		t.Fatalf("expected persisted cancelled snapshot from wait, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
}
