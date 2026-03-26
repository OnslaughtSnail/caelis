package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessioninmemory "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type stubSubagentRunner struct {
	runResult     agent.SubagentRunResult
	inspectResult agent.SubagentRunResult
	inspectErr    error
}

type trackingSubagentRunner struct {
	runCalls       int
	lastRunRequest agent.SubagentRunRequest
	runResult      agent.SubagentRunResult
	inspectCalls   int
	inspectResults []agent.SubagentRunResult
}

type stubTaskController struct {
	cancelCalls int
}

func (s stubSubagentRunner) RunSubagent(context.Context, agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	return s.runResult, nil
}

func (s stubSubagentRunner) InspectSubagent(context.Context, string) (agent.SubagentRunResult, error) {
	return s.inspectResult, s.inspectErr
}

func (s *trackingSubagentRunner) RunSubagent(_ context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	s.runCalls++
	s.lastRunRequest = req
	return s.runResult, nil
}

func (s *trackingSubagentRunner) InspectSubagent(context.Context, string) (agent.SubagentRunResult, error) {
	if s.inspectCalls < len(s.inspectResults) {
		result := s.inspectResults[s.inspectCalls]
		s.inspectCalls++
		return result, nil
	}
	s.inspectCalls++
	if len(s.inspectResults) == 0 {
		return agent.SubagentRunResult{}, nil
	}
	return s.inspectResults[len(s.inspectResults)-1], nil
}

func (s *stubTaskController) Wait(context.Context, *task.Record, time.Duration) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskController) Status(context.Context, *task.Record) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskController) Write(context.Context, *task.Record, string, time.Duration) (task.Snapshot, error) {
	return task.Snapshot{}, nil
}

func (s *stubTaskController) Cancel(_ context.Context, record *task.Record) (task.Snapshot, error) {
	s.cancelCalls++
	record.WithLock(func(one *task.Record) {
		one.Running = false
		one.State = task.StateCancelled
	})
	return task.Snapshot{State: task.StateCancelled}, nil
}

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
		Kind:           task.KindSpawn,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "spawn job",
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

func TestTaskManager_CleanupTurnSkipsDetachedSpawnTasks(t *testing.T) {
	registry := task.NewRegistry(task.RegistryConfig{})
	controller := &stubTaskController{}
	record := registry.Create(task.KindSpawn, "spawned child", controller, true, true)
	record.CleanupOnTurnEnd = false
	record.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"}

	manager := newTaskManager(nil, nil, registry, nil, &sessionContext{appName: "app", userID: "u", sessionID: "parent"}, RunRequest{}, nil)
	manager.trackTurnTask(record.ID)
	manager.cleanupTurn(context.Background())

	if controller.cancelCalls != 0 {
		t.Fatalf("expected detached spawn task to survive turn cleanup, got %d cancel calls", controller.cancelCalls)
	}
	if !record.Running {
		t.Fatalf("expected detached spawn task to remain running after cleanup, got state=%q", record.State)
	}
}

func TestTaskManager_CleanupTurnCancelsAttachedTasks(t *testing.T) {
	registry := task.NewRegistry(task.RegistryConfig{})
	controller := &stubTaskController{}
	record := registry.Create(task.KindBash, "sleep 30", controller, true, true)
	record.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"}

	manager := newTaskManager(nil, nil, registry, nil, &sessionContext{appName: "app", userID: "u", sessionID: "parent"}, RunRequest{}, nil)
	manager.trackTurnTask(record.ID)
	manager.cleanupTurn(context.Background())

	if controller.cancelCalls != 1 {
		t.Fatalf("expected attached task to be cancelled once during cleanup, got %d", controller.cancelCalls)
	}
	if record.Running {
		t.Fatalf("expected attached task to stop running after cleanup, got state=%q", record.State)
	}
}

func TestTaskManager_InterruptTurnCancelsDetachedSpawnTasks(t *testing.T) {
	registry := task.NewRegistry(task.RegistryConfig{})
	spawnController := &stubTaskController{}
	spawnRecord := registry.Create(task.KindSpawn, "spawned child", spawnController, true, true)
	spawnRecord.CleanupOnTurnEnd = false
	spawnRecord.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"}

	bashController := &stubTaskController{}
	bashRecord := registry.Create(task.KindBash, "sleep 30", bashController, true, true)
	bashRecord.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"}

	manager := newTaskManager(nil, nil, registry, nil, &sessionContext{appName: "app", userID: "u", sessionID: "parent"}, RunRequest{}, nil)
	manager.trackTurnTask(spawnRecord.ID)
	manager.trackTurnTask(bashRecord.ID)
	manager.interruptTurn(context.Background())

	if spawnController.cancelCalls != 1 {
		t.Fatalf("expected detached spawn task to be cancelled once during interrupt cleanup, got %d", spawnController.cancelCalls)
	}
	if spawnRecord.Running {
		t.Fatalf("expected detached spawn task to stop running after interrupt cleanup, got state=%q", spawnRecord.State)
	}
	if bashController.cancelCalls != 0 {
		t.Fatalf("expected interrupt cleanup to leave non-spawn tasks alone, got %d cancel calls", bashController.cancelCalls)
	}
}

func TestTaskManager_WaitUsesPersistedOutputCursorsAcrossTurns(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-running-bash",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "acpx prompt",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		StdoutCursor:   4,
		Spec: map[string]any{
			taskSpecCommand:       "acpx prompt",
			taskSpecWorkdir:       "/tmp",
			taskSpecRoute:         "host",
			taskSpecExecSessionID: "sess-1",
		},
		Result: map[string]any{
			"state":         string(task.StateRunning),
			"latest_output": "DONE",
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	runner := &stubAsyncTaskRunner{
		stdoutByMarker: map[int64][]byte{
			0: []byte("DONE"),
		},
		status: toolexec.SessionStatus{State: toolexec.SessionStateRunning},
	}
	manager := newTaskManager(nil, taskTestRuntime{host: runner}, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)

	start := time.Now()
	snapshot, err := manager.Wait(context.Background(), task.ControlRequest{
		TaskID: entry.TaskID,
		Yield:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.lastStdoutMarker; got != 4 {
		t.Fatalf("expected wait to resume from persisted stdout cursor, got %d", got)
	}
	if snapshot.Output.Stdout != "" {
		t.Fatalf("did not expect stale stdout replay, got %q", snapshot.Output.Stdout)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("expected wait to block for at least one poll interval, only waited %s", time.Since(start))
	}
}

func TestTaskManager_BashWaitDoesNotReturnEarlyOnOutput(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-bash-output",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "echo hi",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			taskSpecCommand:       "echo hi",
			taskSpecWorkdir:       "/tmp",
			taskSpecRoute:         "host",
			taskSpecExecSessionID: "sess-1",
		},
		Result: map[string]any{
			"state": string(task.StateRunning),
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	runner := &stubAsyncTaskRunner{
		stdoutByMarker: map[int64][]byte{
			0: []byte("hi\n"),
		},
		status: toolexec.SessionStatus{State: toolexec.SessionStateRunning},
	}
	manager := newTaskManager(nil, taskTestRuntime{host: runner}, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)

	start := time.Now()
	snapshot, err := manager.Wait(context.Background(), task.ControlRequest{
		TaskID: entry.TaskID,
		Yield:  250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("expected wait to honor yield window despite output, only waited %s", elapsed)
	}
	if strings.TrimSpace(snapshot.Output.Stdout) != "hi" {
		t.Fatalf("expected collected stdout in snapshot, got %q", snapshot.Output.Stdout)
	}
}

func TestTaskManager_WaitReturnsRetainedOutputWhenNonTTYTaskCompletes(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-bash-complete",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "echo hi",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		StdoutCursor:   3,
		Spec: map[string]any{
			taskSpecCommand:       "echo hi",
			taskSpecWorkdir:       "/tmp",
			taskSpecRoute:         "host",
			taskSpecExecSessionID: "sess-1",
		},
		Result: map[string]any{
			"state": string(task.StateRunning),
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	runner := &stubAsyncTaskRunner{
		stdoutByMarker: map[int64][]byte{
			0: []byte("one\ntwo\n"),
			3: []byte("two\n"),
		},
		status: toolexec.SessionStatus{
			State:               toolexec.SessionStateCompleted,
			ExitCode:            0,
			CaptureCapBytes:     1024,
			StdoutBytes:         8,
			StdoutRetainedBytes: 8,
		},
	}
	manager := newTaskManager(nil, taskTestRuntime{host: runner}, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)

	snapshot, err := manager.Wait(context.Background(), task.ControlRequest{TaskID: entry.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Output.Stdout; got != "one\ntwo\n" {
		t.Fatalf("expected retained stdout on completion, got %q", got)
	}
	meta, _ := snapshot.Result["output_meta"].(map[string]any)
	if got := meta["tty"]; got != false {
		t.Fatalf("expected non-tty output_meta, got %#v", snapshot.Result)
	}
}

func TestTaskManager_WaitSuppressesTTYTranscriptWhenTaskCompletes(t *testing.T) {
	store := taskinmemory.New()
	entry := &task.Entry{
		TaskID:         "t-bash-tty-complete",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "s"},
		Title:          "interactive",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			taskSpecCommand:       "interactive",
			taskSpecWorkdir:       "/tmp",
			taskSpecRoute:         "host",
			taskSpecExecSessionID: "sess-1",
			taskSpecTTY:           true,
		},
		Result: map[string]any{
			"state": string(task.StateRunning),
		},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	runner := &stubAsyncTaskRunner{
		stdoutByMarker: map[int64][]byte{
			0: []byte("name?\nalice\nhello alice\n"),
		},
		status: toolexec.SessionStatus{
			State:               toolexec.SessionStateCompleted,
			TTY:                 true,
			ExitCode:            0,
			CaptureCapBytes:     1024,
			StdoutBytes:         24,
			StdoutRetainedBytes: 24,
		},
	}
	manager := newTaskManager(nil, taskTestRuntime{host: runner}, nil, store, &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)

	snapshot, err := manager.Wait(context.Background(), task.ControlRequest{TaskID: entry.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Output.Stdout != "" || snapshot.Output.Stderr != "" {
		t.Fatalf("expected tty completion to suppress transcript, got %#v", snapshot.Output)
	}
	if got := strings.TrimSpace(snapshot.Result["latest_output"].(string)); !strings.Contains(got, "hello alice") {
		t.Fatalf("expected tty preview in latest_output, got %#v", snapshot.Result)
	}
	meta, _ := snapshot.Result["output_meta"].(map[string]any)
	if got := meta["streamed"]; got != true {
		t.Fatalf("expected tty output_meta.streamed=true, got %#v", snapshot.Result)
	}
}

func TestSubagentTaskController_WaitDoesNotReturnEarlyOnNewEvents(t *testing.T) {
	sessStore := sessioninmemory.New()
	rt, err := New(Config{Store: sessStore})
	if err != nil {
		t.Fatal(err)
	}

	childSess := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	if _, err := sessStore.GetOrCreate(context.Background(), childSess); err != nil {
		t.Fatal(err)
	}
	if err := sessStore.AppendEvent(context.Background(), childSess, lifecycleEvent(childSess, RunLifecycleStatusRunning, "run", nil)); err != nil {
		t.Fatal(err)
	}
	if err := sessStore.AppendEvent(context.Background(), childSess, &session.Event{
		Message: model.NewTextMessage(model.RoleAssistant, "still working"),
		Time:    time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	record := &task.Record{
		ID:      "t-delegate-output",
		Kind:    task.KindSpawn,
		Title:   "spawn job",
		State:   task.StateRunning,
		Running: true,
		Session: task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	controller := &subagentTaskController{
		runtime:      rt,
		appName:      "app",
		userID:       "u",
		sessionID:    "child-1",
		delegationID: "d-1",
		runner: stubSubagentRunner{
			inspectResult: agent.SubagentRunResult{
				SessionID:    "child-1",
				DelegationID: "d-1",
				Agent:        "self",
				State:        string(task.StateRunning),
				Running:      true,
				Assistant:    "still working",
				LogSnapshot:  "still working\n",
			},
		},
	}

	start := time.Now()
	snapshot, err := controller.Wait(context.Background(), record, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("expected spawn wait to honor yield window despite new events, only waited %s; snapshot=%#v", elapsed, snapshot)
	}
	if !snapshot.Running {
		t.Fatalf("expected spawn snapshot to remain running, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
	if got := snapshot.Result["progress_state"]; got != string(task.StateRunning) {
		t.Fatalf("expected subagent progress_state in snapshot result, got %#v", snapshot.Result)
	}
	if _, ok := snapshot.Result["latest_output"]; ok {
		t.Fatalf("did not expect spawn latest_output to leak into snapshot result, got %#v", snapshot.Result)
	}
}

func TestTaskManager_ListFiltersRegistryBySession(t *testing.T) {
	store := taskinmemory.New()
	registry := task.NewRegistry(task.RegistryConfig{})

	current := registry.Create(task.KindSpawn, "current", nil, false, true)
	current.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"}

	other := registry.Create(task.KindBash, "other", nil, true, true)
	other.Session = task.SessionRef{AppName: "app", UserID: "u", SessionID: "child"}

	persisted := &task.Entry{
		TaskID:         "t-persisted-parent",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "persisted parent",
		State:          task.StateCompleted,
		Running:        false,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Result: map[string]any{
			"state": string(task.StateCompleted),
		},
	}
	if err := store.Upsert(context.Background(), persisted); err != nil {
		t.Fatal(err)
	}

	manager := newTaskManager(nil, nil, registry, store, &sessionContext{appName: "app", userID: "u", sessionID: "parent"}, RunRequest{}, nil)
	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 tasks for current session, got %d: %#v", len(items), items)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.TaskID] = true
	}
	if !seen[current.ID] {
		t.Fatalf("expected current session registry task %q in list, got %#v", current.ID, items)
	}
	if !seen[persisted.TaskID] {
		t.Fatalf("expected persisted current session task %q in list, got %#v", persisted.TaskID, items)
	}
	if seen[other.ID] {
		t.Fatalf("did not expect other session task %q in list, got %#v", other.ID, items)
	}
}

func TestTaskManager_StatusRebuildsPersistedSpawnController(t *testing.T) {
	sessStore := sessioninmemory.New()
	taskStore := taskinmemory.New()
	rt, err := New(Config{Store: sessStore, TaskStore: taskStore})
	if err != nil {
		t.Fatal(err)
	}

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child"}
	if _, err := sessStore.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := sessStore.GetOrCreate(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if err := sessStore.ReplaceState(context.Background(), child, runStateSnapshot(RunState{
		HasLifecycle: true,
		Status:       RunLifecycleStatusRunning,
		Phase:        "run",
		UpdatedAt:    time.Now(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-spawn-persisted",
		Kind:           task.KindSpawn,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Title:          "spawned child",
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

	manager := newTaskManager(rt, nil, nil, taskStore, &sessionContext{appName: "app", userID: "u", sessionID: "parent"}, RunRequest{}, stubSubagentRunner{
		inspectResult: agent.SubagentRunResult{
			SessionID:    "child",
			DelegationID: "dlg-1",
			Agent:        "self",
			State:        string(task.StateRunning),
			Running:      true,
		},
	})
	snapshot, err := manager.Status(context.Background(), task.ControlRequest{TaskID: "t-spawn-persisted"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != task.KindSpawn {
		t.Fatalf("expected spawn snapshot kind, got %q", snapshot.Kind)
	}
	if !snapshot.Running || snapshot.State != task.StateRunning {
		t.Fatalf("expected persisted spawn task to remain controllable, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
	if got := snapshot.Result["_ui_child_session_id"]; got != "child" {
		t.Fatalf("expected child session metadata, got %#v", snapshot.Result)
	}
}

func TestSubagentTaskController_StatusInterruptsStaleTrackerLoss(t *testing.T) {
	record := &task.Record{
		ID:          "t-stale-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateRunning,
		Running:     true,
		CreatedAt:   time.Now().Add(-2 * time.Minute),
		HeartbeatAt: time.Now().Add(-2 * time.Minute),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "d-1",
		agent:        "gemini",
		childCWD:     "/tmp",
		runner: stubSubagentRunner{
			inspectErr: context.DeadlineExceeded, // overwritten below
		},
	}
	controller.runner = stubSubagentRunner{
		inspectErr: assertErrString("acpext: delegated child session \"child-1\" is not tracked in this process"),
	}

	snapshot, err := controller.Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Running || snapshot.State != task.StateInterrupted {
		t.Fatalf("expected stale tracker loss to interrupt task, got state=%q running=%v result=%#v", snapshot.State, snapshot.Running, snapshot.Result)
	}
	if got := snapshot.Result["interrupted"]; got != true {
		t.Fatalf("expected interrupted marker in result, got %#v", snapshot.Result)
	}
}

func TestSubagentTaskController_StatusKeepsRecentTrackerLossRunning(t *testing.T) {
	record := &task.Record{
		ID:          "t-recent-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateRunning,
		Running:     true,
		CreatedAt:   time.Now(),
		HeartbeatAt: time.Now(),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "d-1",
		agent:        "gemini",
		childCWD:     "/tmp",
		runner: stubSubagentRunner{
			inspectErr: assertErrString("runtime: delegated child session \"child-1\" not found"),
		},
	}

	snapshot, err := controller.Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || snapshot.State != task.StateRunning {
		t.Fatalf("expected recent tracker loss to stay running during grace window, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
}

func TestSubagentTaskController_StatusKeepsStaleSilentRunRunning(t *testing.T) {
	record := &task.Record{
		ID:          "t-silent-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateRunning,
		Running:     true,
		CreatedAt:   time.Now().Add(-2 * time.Minute),
		HeartbeatAt: time.Now().Add(-2 * time.Minute),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "d-1",
		agent:        "gemini",
		childCWD:     "/tmp",
		runner: stubSubagentRunner{
			inspectResult: agent.SubagentRunResult{
				SessionID: "child-1",
				State:     string(task.StateRunning),
				Running:   true,
				UpdatedAt: time.Now().Add(-2 * time.Minute),
			},
		},
	}

	snapshot, err := controller.Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || snapshot.State != task.StateRunning {
		t.Fatalf("expected quiet running subagent to remain running, got state=%q running=%v result=%#v", snapshot.State, snapshot.Running, snapshot.Result)
	}
}

func TestSubagentTaskController_StatusFailsIdleTimedOutRun(t *testing.T) {
	record := &task.Record{
		ID:          "t-idle-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateRunning,
		Running:     true,
		CreatedAt:   time.Now().Add(-3 * time.Minute),
		HeartbeatAt: time.Now().Add(-3 * time.Minute),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	cancelled := false
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "d-1",
		agent:        "codex",
		childCWD:     "/tmp",
		idleTimeout:  30 * time.Second,
		cancel:       func() { cancelled = true },
		runner: stubSubagentRunner{
			inspectResult: agent.SubagentRunResult{
				SessionID: "child-1",
				State:     string(task.StateRunning),
				Running:   true,
				UpdatedAt: time.Now().Add(-2 * time.Minute),
			},
		},
	}

	snapshot, err := controller.Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Running || snapshot.State != task.StateFailed {
		t.Fatalf("expected idle timed out subagent to fail, got state=%q running=%v result=%#v", snapshot.State, snapshot.Running, snapshot.Result)
	}
	if snapshot.Result["idle_timed_out"] != true {
		t.Fatalf("expected idle timeout marker, got %#v", snapshot.Result)
	}
	if !cancelled {
		t.Fatal("expected idle timeout to trigger cancel")
	}
}

func TestSubagentTaskController_StatusDoesNotIdleTimeoutApprovalWait(t *testing.T) {
	record := &task.Record{
		ID:          "t-approval-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateWaitingApproval,
		Running:     true,
		CreatedAt:   time.Now().Add(-3 * time.Minute),
		HeartbeatAt: time.Now().Add(-3 * time.Minute),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	cancelled := false
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "d-1",
		agent:        "codex",
		childCWD:     "/tmp",
		idleTimeout:  30 * time.Second,
		cancel:       func() { cancelled = true },
		runner: stubSubagentRunner{
			inspectResult: agent.SubagentRunResult{
				SessionID:       "child-1",
				State:           string(task.StateWaitingApproval),
				Running:         true,
				ApprovalPending: true,
				UpdatedAt:       time.Now().Add(-2 * time.Minute),
			},
		},
	}

	snapshot, err := controller.Status(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || snapshot.State != task.StateWaitingApproval {
		t.Fatalf("expected approval-waiting subagent to remain waiting_approval, got state=%q running=%v result=%#v", snapshot.State, snapshot.Running, snapshot.Result)
	}
	if cancelled {
		t.Fatal("did not expect approval wait to trigger idle-timeout cancellation")
	}
	if snapshot.Result["idle_timed_out"] == true {
		t.Fatalf("did not expect idle timeout marker during approval wait, got %#v", snapshot.Result)
	}
}

func TestSubagentTaskController_WriteRejectsRunningSubagent(t *testing.T) {
	record := &task.Record{
		ID:          "t-running-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateRunning,
		Running:     true,
		CreatedAt:   time.Now(),
		HeartbeatAt: time.Now(),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	runner := &trackingSubagentRunner{
		inspectResults: []agent.SubagentRunResult{{
			SessionID: "child-1",
			State:     string(task.StateRunning),
			Running:   true,
			UpdatedAt: time.Now(),
		}},
	}
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "dlg-1",
		agent:        "copilot",
		childCWD:     "/tmp",
		runner:       runner,
	}

	_, err := controller.Write(toolexec.WithToolCallInfo(context.Background(), tool.TaskToolName, "call-task-write"), record, "yes", 0)
	if err == nil || !strings.Contains(err.Error(), "only allowed for completed spawn subagents") {
		t.Fatalf("expected running TASK write rejection, got %v", err)
	}
	if runner.runCalls != 0 {
		t.Fatalf("expected TASK write to stop before RunSubagent, got %d run calls", runner.runCalls)
	}
}

func TestSubagentTaskController_WriteStoresContinuationPanelMetadata(t *testing.T) {
	record := &task.Record{
		ID:          "t-completed-subagent",
		Kind:        task.KindSpawn,
		Title:       "spawn job",
		State:       task.StateCompleted,
		Running:     false,
		CreatedAt:   time.Now().Add(-time.Minute),
		HeartbeatAt: time.Now().Add(-time.Minute),
		Session:     task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
		Spec: map[string]any{
			taskSpecChildSession: "child-1",
			taskSpecDelegationID: "dlg-old",
			taskSpecAgent:        "copilot",
			taskSpecChildCWD:     "/tmp",
		},
	}
	runner := &trackingSubagentRunner{
		inspectResults: []agent.SubagentRunResult{
			{
				SessionID: "child-1",
				State:     string(task.StateCompleted),
				Running:   false,
				UpdatedAt: time.Now().Add(-time.Minute),
			},
			{
				SessionID:    "child-1",
				DelegationID: "dlg-new",
				Agent:        "copilot",
				ChildCWD:     "/tmp",
				State:        string(task.StateRunning),
				Running:      true,
				UpdatedAt:    time.Now(),
			},
		},
		runResult: agent.SubagentRunResult{
			SessionID:    "child-1",
			DelegationID: "dlg-new",
			Agent:        "copilot",
			ChildCWD:     "/tmp",
			State:        string(task.StateRunning),
			Running:      true,
			UpdatedAt:    time.Now(),
		},
	}
	controller := &subagentTaskController{
		sessionID:    "child-1",
		delegationID: "dlg-old",
		agent:        "copilot",
		childCWD:     "/tmp",
		runner:       runner,
	}

	snapshot, err := controller.Write(toolexec.WithToolCallInfo(context.Background(), tool.TaskToolName, "call-task-write"), record, "follow up", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || snapshot.State != task.StateRunning {
		t.Fatalf("expected continued subagent to be running, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
	if got := snapshot.Result["_ui_spawn_id"]; got != "call-task-write" {
		t.Fatalf("expected continuation spawn id from TASK write call, got %#v", snapshot.Result)
	}
	if got := snapshot.Result["_ui_parent_tool_call_id"]; got != "call-task-write" {
		t.Fatalf("expected continuation parent call id, got %#v", snapshot.Result)
	}
	if got := snapshot.Result["_ui_anchor_tool"]; got != SubagentContinuationAnchorTool {
		t.Fatalf("expected WRITE anchor metadata, got %#v", snapshot.Result)
	}
	if runner.lastRunRequest.SessionID != "child-1" {
		t.Fatalf("expected continuation to reuse child session, got %+v", runner.lastRunRequest)
	}
}

type assertErrString string

func (e assertErrString) Error() string { return string(e) }

type taskTestRuntime struct {
	host toolexec.AsyncCommandRunner
}

func (r taskTestRuntime) PermissionMode() toolexec.PermissionMode {
	return toolexec.PermissionModeDefault
}
func (r taskTestRuntime) SandboxType() string                   { return "" }
func (r taskTestRuntime) SandboxPolicy() toolexec.SandboxPolicy { return toolexec.SandboxPolicy{} }
func (r taskTestRuntime) FallbackToHost() bool                  { return false }
func (r taskTestRuntime) FallbackReason() string                { return "" }
func (r taskTestRuntime) FileSystem() toolexec.FileSystem       { return nil }
func (r taskTestRuntime) HostRunner() toolexec.CommandRunner    { return r.host }
func (r taskTestRuntime) SandboxRunner() toolexec.CommandRunner { return nil }
func (r taskTestRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
}

type stubAsyncTaskRunner struct {
	lastStdoutMarker int64
	lastStderrMarker int64
	stdoutByMarker   map[int64][]byte
	stderrByMarker   map[int64][]byte
	status           toolexec.SessionStatus
	sessionID        string
}

func (s *stubAsyncTaskRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (s *stubAsyncTaskRunner) StartAsync(context.Context, toolexec.CommandRequest) (string, error) {
	if strings.TrimSpace(s.sessionID) == "" {
		s.sessionID = "sess-1"
	}
	return s.sessionID, nil
}

func (s *stubAsyncTaskRunner) WriteInput(string, []byte) error { return nil }

func (s *stubAsyncTaskRunner) ReadOutput(_ string, stdoutMarker, stderrMarker int64) ([]byte, []byte, int64, int64, error) {
	s.lastStdoutMarker = stdoutMarker
	s.lastStderrMarker = stderrMarker
	stdout := append([]byte(nil), s.stdoutByMarker[stdoutMarker]...)
	stderr := append([]byte(nil), s.stderrByMarker[stderrMarker]...)
	return stdout, stderr, stdoutMarker + int64(len(stdout)), stderrMarker + int64(len(stderr)), nil
}

func (s *stubAsyncTaskRunner) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	return s.status, nil
}

func (s *stubAsyncTaskRunner) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (s *stubAsyncTaskRunner) TerminateSession(string) error { return nil }

func (s *stubAsyncTaskRunner) ListSessions() []toolexec.SessionInfo { return nil }

func TestTaskManager_StartBashStreamsInitialOutputBeforeYieldReturns(t *testing.T) {
	runner := &stubAsyncTaskRunner{
		stdoutByMarker: map[int64][]byte{
			0: []byte("hi\n"),
		},
		status:    toolexec.SessionStatus{State: toolexec.SessionStateRunning},
		sessionID: "sess-live-1",
	}
	manager := newTaskManager(nil, taskTestRuntime{host: runner}, nil, taskinmemory.New(), &sessionContext{appName: "app", userID: "u", sessionID: "s"}, RunRequest{}, nil)

	var seen []taskstream.Event
	ctx := taskstream.WithStreamer(context.Background(), taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
		seen = append(seen, ev)
	}))
	ctx = toolexec.WithToolCallInfo(ctx, "BASH", "call-bash-1")

	snapshot, err := manager.StartBash(ctx, task.BashStartRequest{
		Command: "printf hi",
		Workdir: "/tmp",
		Route:   string(toolexec.ExecutionRouteHost),
		Yield:   1100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || strings.TrimSpace(snapshot.TaskID) == "" {
		t.Fatalf("expected yielded running snapshot, got %#v", snapshot)
	}
	if len(seen) < 2 {
		t.Fatalf("expected initial taskstream events before yield returns, got %#v", seen)
	}
	if seen[0].CallID != "call-bash-1" || seen[0].TaskID != snapshot.TaskID || seen[0].Reset || seen[0].State != "running" {
		t.Fatalf("unexpected initial reset event: %#v", seen[0])
	}
	foundChunk := false
	for _, ev := range seen {
		if ev.Stream == "stdout" && ev.Chunk == "hi\n" {
			foundChunk = true
			break
		}
	}
	if !foundChunk {
		t.Fatalf("expected stdout chunk in live taskstream, got %#v", seen)
	}
}
