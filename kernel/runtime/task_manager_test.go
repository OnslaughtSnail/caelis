package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessioninmemory "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
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

func TestDelegateTaskController_WaitDoesNotReturnEarlyOnNewEvents(t *testing.T) {
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
		Message: model.Message{Role: model.RoleAssistant, Text: "still working"},
		Time:    time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	record := &task.Record{
		ID:      "t-delegate-output",
		Kind:    task.KindDelegate,
		Title:   "delegate job",
		State:   task.StateRunning,
		Running: true,
		Session: task.SessionRef{AppName: "app", UserID: "u", SessionID: "parent"},
	}
	controller := &delegateTaskController{
		runtime:      rt,
		appName:      "app",
		userID:       "u",
		sessionID:    "child-1",
		delegationID: "d-1",
	}

	start := time.Now()
	snapshot, err := controller.Wait(context.Background(), record, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("expected delegate wait to honor yield window despite new events, only waited %s; snapshot=%#v", elapsed, snapshot)
	}
	if !snapshot.Running {
		t.Fatalf("expected delegate snapshot to remain running, got state=%q running=%v", snapshot.State, snapshot.Running)
	}
	if got := snapshot.Result["progress_state"]; got != string(task.StateRunning) {
		t.Fatalf("expected delegate progress_state in snapshot result, got %#v", snapshot.Result)
	}
	if _, ok := snapshot.Result["latest_output"]; ok {
		t.Fatalf("did not expect delegate latest_output to leak into snapshot result, got %#v", snapshot.Result)
	}
}

func TestTaskManager_ListFiltersRegistryBySession(t *testing.T) {
	store := taskinmemory.New()
	registry := task.NewRegistry(task.RegistryConfig{})

	current := registry.Create(task.KindDelegate, "current", nil, false, true)
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
