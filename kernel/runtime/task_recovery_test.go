package runtime

import (
	"context"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessionstore "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskstore "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
)

func TestRuntime_ReconcileSession_InterruptsStaleBashTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{Store: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := sessions.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-bash-1",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "sleep 30",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			taskSpecCommand: "sleep 30",
		},
		Result: map[string]any{
			"session_id": "proc-1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := rt.ReconcileSession(context.Background(), ReconcileSessionRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 task entry, got %d", len(entries))
	}
	got, err := tasks.Get(context.Background(), "t-bash-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != task.StateInterrupted || got.Running {
		t.Fatalf("expected interrupted bash task, got state=%q running=%v", got.State, got.Running)
	}
	if got.Result["interrupted"] != true {
		t.Fatalf("expected interrupted result marker, got %#v", got.Result)
	}
}

func TestRuntime_ReconcileSession_InterruptsStaleDelegateChild(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{Store: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child"}
	if _, err := sessions.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.GetOrCreate(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if err := sessions.ReplaceState(context.Background(), child, runStateSnapshot(RunState{
		HasLifecycle: true,
		Status:       RunLifecycleStatusRunning,
		Phase:        "run",
		UpdatedAt:    time.Now(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-delegate-1",
		Kind:           task.KindDelegate,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "inspect repo",
		State:          task.StateRunning,
		Running:        true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			taskSpecPrompt:       "inspect repo",
			taskSpecChildSession: "child",
			taskSpecDelegationID: "dlg-1",
		},
		Result: map[string]any{
			"child_session_id": "child",
			"delegation_id":    "dlg-1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := rt.ReconcileSession(context.Background(), ReconcileSessionRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 task entry, got %d", len(entries))
	}
	gotTask, err := tasks.Get(context.Background(), "t-delegate-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != task.StateInterrupted || gotTask.Running {
		t.Fatalf("expected interrupted delegate task, got state=%q running=%v", gotTask.State, gotTask.Running)
	}
	childState, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if childState.Status != RunLifecycleStatusInterrupted {
		t.Fatalf("expected child run state interrupted, got %q", childState.Status)
	}
}

func TestRuntime_ReconcileSession_ReattachesRecoverableBashTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{Store: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := sessions.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-bash-recover",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "sleep 30",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			taskSpecCommand:       "sleep 30",
			taskSpecWorkdir:       "/workspace",
			taskSpecRoute:         string(toolexec.ExecutionRouteHost),
			taskSpecExecSessionID: "proc-1",
		},
		Result: map[string]any{
			"session_id": "proc-1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	execRuntime := taskTestRuntime{host: &stubAsyncTaskRunner{
		status: toolexec.SessionStatus{
			ID:        "proc-1",
			Command:   "sleep 30",
			Dir:       "/workspace",
			State:     toolexec.SessionStateRunning,
			StartTime: time.Now(),
		},
	}}
	entries, err := rt.ReconcileSession(context.Background(), ReconcileSessionRequest{
		AppName:     "app",
		UserID:      "u",
		SessionID:   "parent",
		ExecRuntime: execRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 task entry, got %d", len(entries))
	}
	got, err := tasks.Get(context.Background(), "t-bash-recover")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != task.StateRunning || !got.Running {
		t.Fatalf("expected recovered bash task to remain running, got state=%q running=%v", got.State, got.Running)
	}
	if got.Result["session_id"] != "proc-1" || got.Result["interrupted"] != nil {
		t.Fatalf("unexpected recovered result payload: %#v", got.Result)
	}
}

func TestRuntime_ReconcileSession_KeepsLiveDelegateTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{Store: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	record := &task.Record{
		ID:             "t-live-1",
		Kind:           task.KindDelegate,
		Title:          "live task",
		State:          task.StateRunning,
		Running:        true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	rt.taskRegistry.Put(record)
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-live-1",
		Kind:           task.KindDelegate,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "live task",
		State:          task.StateRunning,
		Running:        true,
		SupportsCancel: true,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := rt.ReconcileSession(context.Background(), ReconcileSessionRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 task entry, got %d", len(entries))
	}
	gotTask, err := tasks.Get(context.Background(), "t-live-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != task.StateRunning || !gotTask.Running {
		t.Fatalf("expected live task to remain running, got state=%q running=%v", gotTask.State, gotTask.Running)
	}
}
