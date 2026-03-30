package runtime

import (
	"context"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessionstore "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskstore "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
)

func TestRuntime_ReconcileSession_InterruptsStaleBashTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
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

func TestRuntime_ReconcileSession_KeepsRunningSpawnChild(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
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
		TaskID:         "t-spawn-1",
		Kind:           task.KindSpawn,
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
	gotTask, err := tasks.Get(context.Background(), "t-spawn-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != task.StateRunning || !gotTask.Running {
		t.Fatalf("expected running spawn task to remain recoverable, got state=%q running=%v", gotTask.State, gotTask.Running)
	}
	if gotTask.Result["progress_state"] != string(task.StateRunning) {
		t.Fatalf("expected running progress_state, got %#v", gotTask.Result)
	}
}

func TestRuntime_ReconcileSession_RecoversCompletedSpawnChild(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
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
		Status:       RunLifecycleStatusCompleted,
		Phase:        "done",
		UpdatedAt:    time.Now(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-child-final",
		SessionID: child.ID,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleAssistant, "done"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-spawn-complete",
		Kind:           task.KindSpawn,
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
	gotTask, err := tasks.Get(context.Background(), "t-spawn-complete")
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != task.StateCompleted || gotTask.Running {
		t.Fatalf("expected completed spawn task to be recovered, got state=%q running=%v", gotTask.State, gotTask.Running)
	}
	if gotTask.Result["final_result"] != "done" {
		t.Fatalf("expected final result recovered from child session, got %#v", gotTask.Result)
	}
}

func TestRuntime_ReconcileSession_ReattachesRecoverableBashTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
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
	meta, _ := got.Result["output_meta"].(map[string]any)
	if captureCap := meta["capture_cap_bytes"]; captureCap != int64(0) {
		t.Fatalf("expected recovery output_meta, got %#v", got.Result)
	}
}

func TestRuntime_ReconcileSession_ReattachesLegacyACPTerminalBashTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := sessions.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-bash-acp-recover",
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
			taskSpecRoute:         string(toolexec.ExecutionRouteSandbox),
			taskSpecExecSessionID: "term-123",
		},
		Result: map[string]any{
			"session_id": "term-123",
			"route":      string(toolexec.ExecutionRouteSandbox),
		},
	}); err != nil {
		t.Fatal(err)
	}

	terminalRunner := &stubAsyncTaskRunner{
		status: toolexec.SessionStatus{
			ID:        "term-123",
			Command:   "sleep 30",
			Dir:       "/workspace",
			State:     toolexec.SessionStateRunning,
			StartTime: time.Now(),
		},
	}
	execRuntime := taskTestRuntime{
		backends: map[string]toolexec.AsyncCommandRunner{
			"acp_terminal": terminalRunner,
		},
	}
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
	got, err := tasks.Get(context.Background(), "t-bash-acp-recover")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != task.StateRunning || !got.Running {
		t.Fatalf("expected recovered ACP bash task to remain running, got state=%q running=%v", got.State, got.Running)
	}
	if got.Result["session_id"] != "term-123" || got.Result["interrupted"] != nil {
		t.Fatalf("unexpected recovered result payload: %#v", got.Result)
	}
	if got.Result["backend"] != "acp_terminal" {
		t.Fatalf("expected recovered backend acp_terminal, got %#v", got.Result["backend"])
	}
}

func TestRuntime_ReconcileSession_KeepsLiveSpawnTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	record := &task.Record{
		ID:             "t-live-1",
		Kind:           task.KindSpawn,
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
		Kind:           task.KindSpawn,
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

func TestRuntime_ReconcileSession_InterruptsUnknownDelegateTask(t *testing.T) {
	sessions := sessionstore.New()
	tasks := taskstore.New()
	rt, err := New(Config{LogStore: sessions, StateStore: sessions, TaskStore: tasks})
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
		TaskID:         "t-delegate-unknown",
		Kind:           task.Kind("delegate"),
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
	gotTask, err := tasks.Get(context.Background(), "t-delegate-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != task.StateInterrupted || gotTask.Running {
		t.Fatalf("expected unknown delegate task to be interrupted, got state=%q running=%v", gotTask.State, gotTask.Running)
	}
}
