package sessionsvc

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
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

type switchingExecRuntime struct {
	mu      sync.RWMutex
	current toolexec.Runtime
}

func newSwitchingExecRuntime(rt toolexec.Runtime) *switchingExecRuntime {
	return &switchingExecRuntime{current: rt}
}

func (r *switchingExecRuntime) Set(next toolexec.Runtime) {
	r.mu.Lock()
	r.current = next
	r.mu.Unlock()
}

func (r *switchingExecRuntime) Current() toolexec.Runtime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

func (r *switchingExecRuntime) PermissionMode() toolexec.PermissionMode {
	if current := r.Current(); current != nil {
		return current.PermissionMode()
	}
	return toolexec.PermissionModeDefault
}

func (r *switchingExecRuntime) SandboxType() string {
	if current := r.Current(); current != nil {
		return current.SandboxType()
	}
	return ""
}

func (r *switchingExecRuntime) SandboxPolicy() toolexec.SandboxPolicy {
	if current := r.Current(); current != nil {
		return current.SandboxPolicy()
	}
	return toolexec.SandboxPolicy{}
}

func (r *switchingExecRuntime) FallbackToHost() bool {
	if current := r.Current(); current != nil {
		return current.FallbackToHost()
	}
	return false
}

func (r *switchingExecRuntime) FallbackReason() string {
	if current := r.Current(); current != nil {
		return current.FallbackReason()
	}
	return ""
}
func (r *switchingExecRuntime) Diagnostics() toolexec.SandboxDiagnostics {
	if current := r.Current(); current != nil {
		return current.Diagnostics()
	}
	return toolexec.SandboxDiagnostics{}
}

func (r *switchingExecRuntime) FileSystem() toolexec.FileSystem {
	if current := r.Current(); current != nil {
		return current.FileSystem()
	}
	return nil
}

func (r *switchingExecRuntime) State() toolexec.RuntimeState {
	if current := r.Current(); current != nil {
		return current.State()
	}
	return toolexec.RuntimeState{}
}

func (r *switchingExecRuntime) Execute(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	if current := r.Current(); current != nil {
		return current.Execute(ctx, req)
	}
	return toolexec.CommandResult{}, fmt.Errorf("runtime unavailable")
}

func (r *switchingExecRuntime) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	if current := r.Current(); current != nil {
		return current.Start(ctx, req)
	}
	return nil, fmt.Errorf("runtime unavailable")
}

func (r *switchingExecRuntime) OpenSession(ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	if current := r.Current(); current != nil {
		return current.OpenSession(ref)
	}
	return nil, fmt.Errorf("runtime unavailable")
}

func (r *switchingExecRuntime) DecideRoute(command string, sandboxPermission toolexec.SandboxPermission) toolexec.CommandDecision {
	if current := r.Current(); current != nil {
		return current.DecideRoute(command, sandboxPermission)
	}
	return toolexec.CommandDecision{}
}

type serviceScriptedLLM struct {
	mu    sync.Mutex
	calls [][]*model.Response
}

func (s *serviceScriptedLLM) Name() string { return "scripted-service" }

func (s *serviceScriptedLLM) Generate(_ context.Context, _ *model.Request) iter.Seq2[*model.StreamEvent, error] {
	s.mu.Lock()
	var batch []*model.Response
	if len(s.calls) > 0 {
		batch = s.calls[0]
		s.calls = s.calls[1:]
	}
	s.mu.Unlock()
	return func(yield func(*model.StreamEvent, error) bool) {
		for _, one := range batch {
			if one != nil && !one.TurnComplete {
				clone := *one
				clone.TurnComplete = true
				one = &clone
			}
			if !yield(model.StreamEventFromResponse(one), nil) {
				return
			}
		}
	}
}

type closeableAsyncRunner struct {
	mu      sync.Mutex
	closed  bool
	calls   []toolexec.CommandRequest
	session string
	result  toolexec.CommandResult
}

var errTestRunnerClosed = errors.New("test async runner is closed")

func (r *closeableAsyncRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (r *closeableAsyncRunner) StartAsync(_ context.Context, req toolexec.CommandRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errTestRunnerClosed
	}
	r.calls = append(r.calls, req)
	if r.session == "" {
		r.session = "bash-session-1"
	}
	return r.session, nil
}

func (r *closeableAsyncRunner) WriteInput(string, []byte) error { return nil }

func (r *closeableAsyncRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) ([]byte, []byte, int64, int64, error) {
	_ = sessionID
	_ = stderrMarker
	r.mu.Lock()
	defer r.mu.Unlock()
	stdout := []byte(r.result.Stdout)
	start := int(stdoutMarker)
	if start < 0 || start > len(stdout) {
		start = 0
	}
	return stdout[start:], []byte(r.result.Stderr), int64(len(stdout)), int64(len(r.result.Stderr)), nil
}

func (r *closeableAsyncRunner) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return toolexec.SessionStatus{
		ID:          r.session,
		State:       toolexec.SessionStateCompleted,
		ExitCode:    r.result.ExitCode,
		StdoutBytes: int64(len(r.result.Stdout)),
		StderrBytes: int64(len(r.result.Stderr)),
	}, nil
}

func (r *closeableAsyncRunner) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return toolexec.CommandResult{}, errTestRunnerClosed
	}
	return r.result, nil
}

func (r *closeableAsyncRunner) TerminateSession(string) error { return nil }

func (r *closeableAsyncRunner) ListSessions() []toolexec.SessionInfo { return nil }

func (r *closeableAsyncRunner) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

func drainTurnErrors(seq iter.Seq2[*session.Event, error]) []error {
	var errs []error
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func TestServiceRunTurnUsesSwappableExecutionRuntime(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := &closeableAsyncRunner{result: toolexec.CommandResult{Stdout: "old", ExitCode: 0}}
	oldRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		HostRunner:     oldRunner,
		SandboxRunner:  oldRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	newRunner := &closeableAsyncRunner{result: toolexec.CommandResult{Stdout: "after-switch", ExitCode: 0}}
	newRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		HostRunner:     newRunner,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(newRuntime) })

	view := newSwitchingExecRuntime(oldRuntime)
	view.Set(newRuntime)
	if err := toolexec.Close(oldRuntime); err != nil {
		t.Fatal(err)
	}

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &serviceScriptedLLM{
		calls: [][]*model.Response{
			{{
				Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
					ID:   "call-bash-1",
					Name: "BASH",
					Args: `{"command":"echo after-switch"}`,
				}}, ""),
			}},
			{{
				Message:      model.NewTextMessage(model.RoleAssistant, "done"),
				TurnComplete: true,
			}},
		},
	}

	svc, err := New(ServiceConfig{
		Runtime:      rt,
		Store:        store,
		AppName:      "app",
		UserID:       "u",
		Execution:    view,
		WorkspaceCWD: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := svc.RunTurn(context.Background(), RunTurnRequest{
		SessionRef: SessionRef{AppName: "app", UserID: "u", SessionID: "s-bash"},
		Input:      "run bash",
		Agent:      ag,
		Model:      llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Handle.Close() }()

	if errs := drainTurnErrors(run.Handle.Events()); len(errs) > 0 {
		t.Fatalf("unexpected run errors: %v", errs)
	}
	if len(newRunner.calls) != 1 {
		t.Fatalf("expected switched runtime runner to execute once, got %d", len(newRunner.calls))
	}
	if got := newRunner.calls[0].Command; got != "echo after-switch" {
		t.Fatalf("unexpected switched runtime command %q", got)
	}
	if len(oldRunner.calls) != 0 {
		t.Fatalf("expected closed runtime not to receive calls, got %d", len(oldRunner.calls))
	}
}
