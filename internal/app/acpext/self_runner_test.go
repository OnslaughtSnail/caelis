package acpext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/app/acpadapter"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	kernelpolicy "github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

func newTestACPAdapterFactory(rt *runtime.Runtime, store session.Store, execRT toolexec.Runtime, workspaceRoot string, ag agent.Agent, llm model.LLM, extraTools []tool.Tool) AdapterFactory {
	return func(conn *internalacp.Conn) (internalacp.Adapter, error) {
		return acpadapter.New(acpadapter.Config{
			Runtime:           rt,
			Store:             store,
			Model:             llm,
			AppName:           "app",
			UserID:            "u",
			WorkspaceRoot:     workspaceRoot,
			BuildSystemPrompt: func(string) (string, error) { return "test self acp prompt", nil },
			NewAgent: func(bool, string, string, internalacp.AgentSessionConfig) (agent.Agent, error) {
				return ag, nil
			},
			NewSessionResources: func(_ context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				execRuntimeACP := internalacp.NewRuntime(execRT, conn, sessionID, workspaceRoot, sessionCWD, caps, modeResolver)
				tools, err := tool.RebindRuntime(extraTools, execRuntimeACP)
				if err != nil {
					return nil, err
				}
				return &internalacp.SessionResources{
					Runtime: execRuntimeACP,
					Tools:   tools,
				}, nil
			},
			EnablePlan:      true,
			EnableSelfSpawn: true,
		})
	}
}

func TestSelfACPSpawnCreatesDelegationReference(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPSpawnLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		Input: "delegate please",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: "wk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}
	events, err := store.ListEvents(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      delegations[0].ChildSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if delegations[0].ChildSessionID == "parent" || delegations[0].ChildSessionID == "" {
		t.Fatalf("expected delegated child session id, got %q", delegations[0].ChildSessionID)
	}
	found := false
	for _, ev := range events {
		if ev != nil && ev.Message.TextContent() == "child done" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected child assistant output in canonical self session, got %+v", events)
	}
}

func TestSelfACPSpawnUsesProvidedAdapterFactory(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	sentinel := errors.New("adapter factory invoked")
	var factoryCalled bool
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		NewAdapter: func(*internalacp.Conn) (internalacp.Adapter, error) {
			factoryCalled = true
			return nil, sentinel
		},
	})
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected self ACP subagent runner")
	}
	_, err = runner.RunSubagent(context.Background(), agent.SubagentRunRequest{
		Agent:  "self",
		Prompt: "child task",
		Yield:  time.Second,
	})
	if !factoryCalled {
		t.Fatal("expected self ACP runner to use provided adapter factory")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected adapter factory error %v, got %v", sentinel, err)
	}
}

func TestACPSpawnLeavesChildSessionInDefaultMode(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPSpawnLLM{}
	adapterFactory := func(conn *internalacp.Conn) (internalacp.Adapter, error) {
		return acpadapter.New(acpadapter.Config{
			Runtime:       rt,
			Store:         store,
			Model:         llm,
			AppName:       "app",
			UserID:        "u",
			WorkspaceRoot: "/workspace",
			SessionModes: []internalacp.SessionMode{
				{ID: sessionmode.DefaultMode, Name: "Default"},
				{ID: sessionmode.PlanMode, Name: "Plan"},
				{ID: sessionmode.FullMode, Name: "Full Access"},
			},
			DefaultModeID: sessionmode.DefaultMode,
			BuildSystemPrompt: func(string) (string, error) {
				return "test self acp prompt", nil
			},
			NewAgent: func(bool, string, string, internalacp.AgentSessionConfig) (agent.Agent, error) {
				return ag, nil
			},
			NewSessionResources: func(_ context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				execRuntimeACP := internalacp.NewRuntime(execRT, conn, sessionID, "/workspace", sessionCWD, caps, modeResolver)
				return &internalacp.SessionResources{Runtime: execRuntimeACP}, nil
			},
			EnablePlan:      true,
			EnableSelfSpawn: true,
		})
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    adapterFactory,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), parent, sessionmode.StoreSnapshot(map[string]any{}, sessionmode.FullMode)); err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		Input: "delegate please",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: "wk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}

	childState, err := store.SnapshotState(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      delegations[0].ChildSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionmode.LoadSnapshot(childState); got != sessionmode.DefaultMode {
		t.Fatalf("expected child session mode %q, got %q", sessionmode.DefaultMode, got)
	}
}

func TestPermissionRequestHandler_PrefersToolAuthorizerForFileEdits(t *testing.T) {
	approver := &recordingApprover{allow: false}
	authorizer := &recordingToolAuthorizer{allow: true}
	ctx := toolexec.WithApprover(context.Background(), approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, authorizer)
	title := "PATCH /tmp/demo.txt"
	kind := internalacp.ToolKindEdit

	runner := &selfACPSubagentRunner{}
	handler := runner.permissionRequestHandler(ctx, "self", func() string { return "child" }, runtime.DelegationMetadata{DelegationID: "delegation-1"}, "/workspace", nil, nil)
	resp, err := handler(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child",
		ToolCall: acpclient.ToolCallUpdate{
			ToolCallID: "call-patch-1",
			Title:      &title,
			Kind:       &kind,
			RawInput: map[string]any{
				"path": "/tmp/demo.txt",
			},
		},
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject", Kind: "reject_once"},
		},
	})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("expected tool authorizer to be used once, got %d", authorizer.calls)
	}
	if approver.calls != 0 {
		t.Fatalf("expected command approver to be skipped, got %d calls", approver.calls)
	}
	if authorizer.last.Path != "/tmp/demo.txt" {
		t.Fatalf("unexpected authorization path %q", authorizer.last.Path)
	}
	var outcome struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(resp.Outcome, &outcome); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	if outcome.Outcome != "selected" || outcome.OptionID != "allow_once" {
		t.Fatalf("unexpected outcome %+v", outcome)
	}
}

func TestPermissionRequestHandler_InvokesApprovalWatchdogHooks(t *testing.T) {
	approver := &recordingApprover{allow: true}
	ctx := toolexec.WithApprover(context.Background(), approver)
	title := "BASH echo hi"
	kind := "run"
	var started, done int

	runner := &selfACPSubagentRunner{}
	handler := runner.permissionRequestHandler(
		ctx,
		"self",
		func() string { return "child" },
		runtime.DelegationMetadata{DelegationID: "delegation-1"},
		"/workspace",
		func() { started++ },
		func() { done++ },
	)
	_, err := handler(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child",
		ToolCall: acpclient.ToolCallUpdate{
			ToolCallID: "call-run-1",
			Title:      &title,
			Kind:       &kind,
			RawInput: map[string]any{
				"command": "echo hi",
			},
		},
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject", Kind: "reject_once"},
		},
	})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if started != 1 || done != 1 {
		t.Fatalf("expected approval hooks to run once, got started=%d done=%d", started, done)
	}
}

func TestSelfACPSpawnBridgesLiveChildSessionUpdates(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPSpawnLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	runCtx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	runResult, err := svc.RunTurn(runCtx, sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		Input: "delegate please",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	live := append([]sessionstream.Update(nil), updates...)
	mu.Unlock()

	var sawChildText bool
	var childTextCount int
	for _, update := range live {
		if update.Event == nil || strings.TrimSpace(update.SessionID) == "" || update.SessionID == "parent" {
			continue
		}
		if strings.TrimSpace(update.Event.Message.TextContent()) != "child done" {
			continue
		}
		meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
		if !ok || strings.TrimSpace(meta.ParentToolName) != tool.SpawnToolName {
			t.Fatalf("expected bridged child update to preserve SPAWN lineage, got %+v", update.Event.Meta)
		}
		sawChildText = true
		childTextCount++
	}
	if !sawChildText {
		t.Fatalf("expected live bridged child assistant update, got %+v", live)
	}
	if childTextCount != 1 {
		t.Fatalf("expected child assistant update to be bridged once, got %d updates: %+v", childTextCount, live)
	}
}

func TestSelfACPSubagentRunner_UsesCurrentTaskWriteLineageWhenReusingChildSession(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-existing"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-child-existing",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "child already ran"),
		Meta: map[string]any{
			"parent_session_id":   parent.ID,
			"child_session_id":    child.ID,
			"delegation_id":       "dlg-original",
			"parent_tool_call_id": "call-spawn-original",
			"parent_tool_name":    tool.SpawnToolName,
		},
	}); err != nil {
		t.Fatal(err)
	}

	runner := &selfACPSubagentRunner{
		runtime: rt,
		store:   store,
		parent:  parent,
	}
	meta := runner.delegationMetadata(
		runtime.WithSubagentContinuation(toolexec.WithToolCallInfo(context.Background(), tool.TaskToolName, "call-task-write")),
		child.ID,
	)
	if meta.ParentToolCall != "call-task-write" {
		t.Fatalf("expected current TASK write call id, got %q", meta.ParentToolCall)
	}
	if meta.ParentToolName != tool.TaskToolName {
		t.Fatalf("expected current TASK tool preserved, got %q", meta.ParentToolName)
	}
	if meta.DelegationID == "dlg-original" || meta.DelegationID == "" {
		t.Fatalf("expected fresh delegation id for continuation, got %q", meta.DelegationID)
	}
}

func TestSelfACPSubagentRunner_InspectFallsBackToPersistedState(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-running"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	lifecycle := runtime.LifecycleEvent(child, runtime.RunLifecycleStatusWaitingApproval, "self_acp", nil)
	if lifecycle == nil {
		t.Fatal("expected lifecycle event")
	}
	lifecycle = annotateDelegationEvent(lifecycle, runtime.DelegationMetadata{
		ParentSessionID: parent.ID,
		ChildSessionID:  child.ID,
		DelegationID:    "dlg-running",
	})
	if err := store.AppendEvent(context.Background(), child, lifecycle); err != nil {
		t.Fatal(err)
	}

	runner := &selfACPSubagentRunner{
		runtime: rt,
		store:   store,
		parent:  parent,
		shared: &sharedACPSubagentState{
			tracker: newRemoteSubagentTracker(),
		},
	}
	got, err := runner.InspectSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(runtime.RunLifecycleStatusWaitingApproval) || !got.Running || !got.ApprovalPending {
		t.Fatalf("expected waiting approval fallback result, got %+v", got)
	}
	if got.DelegationID != "dlg-running" {
		t.Fatalf("expected delegation id from persisted state, got %+v", got)
	}
}

func TestSelfACPSubagentRunner_FailedResultPersistsLifecycle(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	runner := &selfACPSubagentRunner{
		store:  store,
		parent: parent,
		shared: &sharedACPSubagentState{
			tracker: newRemoteSubagentTracker(),
		},
	}
	cause := errors.New("prompt failed")
	_, err := runner.failedResult(context.Background(), "child-failed", true, runtime.DelegationMetadata{
		ParentSessionID: parent.ID,
		ChildSessionID:  "child-failed",
		DelegationID:    "dlg-failed",
	}, "self", 0, 0, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("expected original cause, got %v", err)
	}
	events, err := store.ListEvents(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      "child-failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one persisted failure event, got %d", len(events))
	}
	info, ok := runtime.LifecycleFromEvent(events[0])
	if !ok || info.Status != runtime.RunLifecycleStatusFailed {
		t.Fatalf("expected failed lifecycle event, got %+v", events[0])
	}
	if meta, ok := runtime.DelegationMetadataFromEvent(events[0]); !ok || meta.DelegationID != "dlg-failed" {
		t.Fatalf("expected delegation metadata on failure event, got %+v", events[0].Meta)
	}
}

func TestSelfACPSubagentRunner_PermissionApprovalEmitsRunningLifecycle(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-approval"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	runner := &selfACPSubagentRunner{
		store:  store,
		parent: parent,
		shared: &sharedACPSubagentState{
			tracker: newRemoteSubagentTracker(),
		},
	}
	approver := &recordingApprover{allow: true}
	streamed := make(chan sessionstream.Update, 1)
	ctx := sessionstream.WithStreamer(
		toolexec.WithApprover(context.Background(), approver),
		sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
			streamed <- update
		}),
	)
	handler := runner.permissionRequestHandler(ctx, "copilot", func() string { return child.ID }, runtime.DelegationMetadata{
		ParentSessionID: parent.ID,
		ChildSessionID:  child.ID,
		ParentToolCall:  "call-task-write-1",
		ParentToolName:  tool.TaskToolName,
		DelegationID:    "dlg-approval",
	}, "", nil, nil)
	title := "ECHO echo hi"
	kind := "echo"
	resp, err := handler(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: child.ID,
		ToolCall: acpclient.ToolCallUpdate{
			ToolCallID: "call-echo-1",
			Title:      &title,
			Kind:       &kind,
			RawInput: map[string]any{
				"command": "echo hi",
			},
		},
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
		},
	})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if len(resp.Outcome) == 0 {
		t.Fatal("expected non-empty permission outcome")
	}
	select {
	case update := <-streamed:
		if update.SessionID != child.ID {
			t.Fatalf("expected lifecycle update for child session %q, got %q", child.ID, update.SessionID)
		}
		info, ok := runtime.LifecycleFromEvent(update.Event)
		if !ok || info.Status != runtime.RunLifecycleStatusRunning {
			t.Fatalf("expected running lifecycle event, got %+v", update.Event)
		}
		meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
		if !ok || meta.ParentToolCall != "call-task-write-1" || meta.ParentToolName != tool.TaskToolName {
			t.Fatalf("expected running lifecycle event to preserve parent tool lineage, got %+v", update.Event)
		}
	default:
		t.Fatal("expected running lifecycle sessionstream update after approval")
	}
	events, err := store.ListEvents(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected persisted running lifecycle event")
	}
	info, ok := runtime.LifecycleFromEvent(events[len(events)-1])
	if !ok || info.Status != runtime.RunLifecycleStatusRunning {
		t.Fatalf("expected last persisted event to be running lifecycle, got %+v", events[len(events)-1])
	}
	meta, ok := runtime.DelegationMetadataFromEvent(events[len(events)-1])
	if !ok || meta.ParentToolCall != "call-task-write-1" || meta.ParentToolName != tool.TaskToolName {
		t.Fatalf("expected persisted lifecycle event to preserve parent tool lineage, got %+v", events[len(events)-1])
	}
}

func TestPermissionRequestHandler_FullAccessAutoAllowsKnownSingleUseOption(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), parent, sessionmode.StoreSnapshot(map[string]any{}, sessionmode.FullMode)); err != nil {
		t.Fatal(err)
	}
	approver := &recordingApprover{allow: false}
	ctx := toolexec.WithApprover(context.Background(), approver)
	runner := &selfACPSubagentRunner{
		store:  store,
		parent: parent,
		shared: &sharedACPSubagentState{tracker: newRemoteSubagentTracker()},
	}

	handler := runner.permissionRequestHandler(ctx, "copilot", func() string { return "child" }, runtime.DelegationMetadata{DelegationID: "dlg-auto"}, "/workspace", nil, nil)
	resp, err := handler(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child",
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
		},
	})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if got := acpclient.PermissionSelectedOptionID(resp); got != "allow_once" {
		t.Fatalf("expected allow_once, got %q", got)
	}
	if approver.calls != 0 {
		t.Fatalf("expected auto-allow to bypass approver, got %d calls", approver.calls)
	}
}

func TestPermissionRequestHandler_FullAccessUnknownOptionsFallbackToInteractiveApproval(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), parent, sessionmode.StoreSnapshot(map[string]any{}, sessionmode.FullMode)); err != nil {
		t.Fatal(err)
	}
	approver := &recordingApprover{allow: true}
	ctx := toolexec.WithApprover(context.Background(), approver)
	runner := &selfACPSubagentRunner{
		store:  store,
		parent: parent,
		shared: &sharedACPSubagentState{tracker: newRemoteSubagentTracker()},
	}

	handler := runner.permissionRequestHandler(ctx, "unknown-agent", func() string { return "child" }, runtime.DelegationMetadata{DelegationID: "dlg-fallback"}, "/workspace", nil, nil)
	resp, err := handler(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child",
		Options: []acpclient.PermissionOption{
			{OptionID: "approve", Name: "Approve"},
			{OptionID: "reject", Name: "Reject"},
		},
	})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if got := acpclient.PermissionSelectedOptionID(resp); got != "approve" {
		t.Fatalf("expected fallback approver to select approve, got %q", got)
	}
	if approver.calls != 1 {
		t.Fatalf("expected one approver call, got %d", approver.calls)
	}
	if !approver.lastInteractive {
		t.Fatal("expected fallback approval to require interactive approval")
	}
}

func TestSelfACPSubagentRunner_ReturnsSessionHandleWhenPromptTimeoutHitsAfterReady(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &timeoutAwareACPSpawnLLM{
		started:  make(chan struct{}, 1),
		canceled: make(chan struct{}, 1),
	}
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
	})
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected self ACP subagent runner")
	}

	result, err := runner.RunSubagent(context.Background(), agent.SubagentRunRequest{
		Agent:   "self",
		Prompt:  "slow child",
		Yield:   250 * time.Millisecond,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected timeout to degrade into a tracked child state, got %v", err)
	}
	if strings.TrimSpace(result.SessionID) == "" {
		t.Fatalf("expected timed-out child to preserve session handle, got %+v", result)
	}
	if strings.TrimSpace(result.DelegationID) == "" {
		t.Fatalf("expected delegation id on timeout recovery, got %+v", result)
	}
	if result.Timeout != 50*time.Millisecond {
		t.Fatalf("expected timeout to round-trip, got %s", result.Timeout)
	}
}

func TestSelfACPSubagentRunner_CancelsRemotePromptOnLocalTimeout(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &timeoutAwareACPSpawnLLM{
		started:  make(chan struct{}, 1),
		canceled: make(chan struct{}, 1),
	}
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
	})
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected self ACP subagent runner")
	}

	done := make(chan error, 1)
	go func() {
		_, runErr := runner.RunSubagent(context.Background(), agent.SubagentRunRequest{
			Agent:   "self",
			Prompt:  "slow child",
			Yield:   250 * time.Millisecond,
			Timeout: 50 * time.Millisecond,
		})
		done <- runErr
	}()

	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("expected child prompt to start")
	}
	select {
	case <-llm.canceled:
	case <-time.After(time.Second):
		t.Fatal("expected local timeout to cancel the remote child prompt")
	}
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("expected timeout cancellation to remain recoverable, got %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("expected RunSubagent to return after timeout")
	}
}

func TestSelfACPSubagentRunner_CallerTimeoutDoesNotCancelDetachedChild(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-existing"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &controlledACPSpawnLLM{
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		finished: make(chan struct{}, 1),
	}
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
	})
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected self ACP subagent runner")
	}

	runCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := runner.RunSubagent(runCtx, agent.SubagentRunRequest{
			Agent:     "self",
			SessionID: child.ID,
			Prompt:    "slow child",
			Yield:     5 * time.Second,
		})
		done <- runErr
	}()

	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("expected child prompt to start")
	}
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.DeadlineExceeded) {
			t.Fatalf("expected caller timeout from RunSubagent, got %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("expected RunSubagent to stop waiting after caller timeout")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, inspectErr := runner.InspectSubagent(context.Background(), child.ID)
		if inspectErr == nil && got.Running && got.State == string(runtime.RunLifecycleStatusRunning) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := runner.InspectSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.State != string(runtime.RunLifecycleStatusRunning) {
		t.Fatalf("expected child to remain running after caller timeout, got %+v", got)
	}

	close(llm.release)
	select {
	case <-llm.finished:
	case <-time.After(time.Second):
		t.Fatal("expected detached child to keep running and finish after release")
	}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, inspectErr := runner.InspectSubagent(context.Background(), child.ID)
		if inspectErr == nil && !got.Running && got.State == string(runtime.RunLifecycleStatusCompleted) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err = runner.InspectSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("expected child to finish after release, got %+v", got)
}

func TestACPTransportRunSubagent_FailsPromptWhenPeerDisconnectsAfterPartialOutput(t *testing.T) {
	origStart := startACPClient
	startACPClient = func(ctx context.Context, cfg acpclient.Config) (*acpclient.Client, error) {
		serverReader, clientWriter := io.Pipe()
		clientReader, serverWriter := io.Pipe()
		client, err := acpclient.StartLoopback(ctx, cfg, clientReader, clientWriter)
		if err != nil {
			return nil, err
		}
		serverConn := internalacp.NewConn(serverReader, serverWriter)
		go func() {
			_ = serverConn.Serve(ctx, func(_ context.Context, msg internalacp.Message) (any, *internalacp.RPCError) {
				switch msg.Method {
				case internalacp.MethodInitialize:
					return internalacp.InitializeResponse{}, nil
				case internalacp.MethodSessionNew:
					return internalacp.NewSessionResponse{SessionID: "child-acp-1"}, nil
				case internalacp.MethodSessionPrompt:
					_ = serverConn.Notify(internalacp.MethodSessionUpdate, internalacp.SessionNotification{
						SessionID: "child-acp-1",
						Update: mustMarshalRaw(acpclient.ContentChunk{
							SessionUpdate: acpclient.UpdateAgentMessage,
							Content:       mustMarshalRaw(acpclient.TextChunk{Type: "text", Text: "partial from codex"}),
						}),
					})
					_ = serverWriter.Close()
					_ = serverReader.Close()
					<-ctx.Done()
					return nil, nil
				default:
					return nil, &internalacp.RPCError{Code: -32601, Message: "method not found"}
				}
			}, nil)
		}()
		return client, nil
	}
	defer func() { startACPClient = origStart }()

	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		ResolveAgentRegistry: func() (*appagents.Registry, error) {
			return appagents.NewRegistry(appagents.Descriptor{
				ID:        "codex",
				Name:      "codex",
				Transport: appagents.TransportACP,
				Command:   "fake-codex",
			}), nil
		},
	})
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected ACP subagent runner")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = runner.RunSubagent(ctx, agent.SubagentRunRequest{
		Agent:  "codex",
		Prompt: "child task",
		Yield:  200 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "connection closed before response") {
		t.Fatalf("expected abrupt ACP disconnect to fail prompt, got %v", err)
	}

	events, err := store.ListEvents(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      "child-acp-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected failed child lifecycle to be persisted")
	}
	info, ok := runtime.LifecycleFromEvent(events[len(events)-1])
	if !ok || info.Status != runtime.RunLifecycleStatusFailed {
		t.Fatalf("expected failed lifecycle after ACP disconnect, got %+v", events[len(events)-1])
	}
}

func TestDelegatedSelfChildSessionCannotSpawnExternalACPChild(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPNestedExternalSpawnLLM{}
	workspace := t.TempDir()
	registryResolver := func() (*appagents.Registry, error) {
		return appagents.NewRegistry(appagents.Descriptor{
			ID:        "codex",
			Name:      "codex",
			Transport: appagents.TransportACP,
			Command:   "fake-codex",
		}), nil
	}
	var subagentFactory runtime.SubagentRunnerFactory
	adapterFactory := func(conn *internalacp.Conn) (internalacp.Adapter, error) {
		return acpadapter.New(acpadapter.Config{
			Runtime:               rt,
			Store:                 store,
			Model:                 llm,
			AppName:               "app",
			UserID:                "u",
			WorkspaceRoot:         workspace,
			BuildSystemPrompt:     func(string) (string, error) { return "nested external prompt", nil },
			NewAgent:              func(bool, string, string, internalacp.AgentSessionConfig) (agent.Agent, error) { return ag, nil },
			EnablePlan:            true,
			EnableSelfSpawn:       true,
			SubagentRunnerFactory: subagentFactory,
			NewSessionResources: func(_ context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				execRuntimeACP := internalacp.NewRuntime(execRT, conn, sessionID, workspace, sessionCWD, caps, modeResolver)
				return &internalacp.SessionResources{Runtime: execRuntimeACP}, nil
			},
		})
	}
	subagentFactory = NewACPSubagentRunnerFactory(Config{
		Store:                store,
		WorkspaceRoot:        workspace,
		WorkspaceCWD:         workspace,
		ClientRuntime:        execRT,
		ResolveAgentRegistry: registryResolver,
		NewAdapter:           adapterFactory,
	})
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:               rt,
		Store:                 store,
		AppName:               "app",
		UserID:                "u",
		WorkspaceCWD:          workspace,
		Execution:             execRT,
		EnableSelfSpawn:       true,
		SubagentRunnerFactory: subagentFactory,
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: workspace,
		},
		Input: "delegate nested codex",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected only first-level self delegation, got %d", len(delegations))
	}
	child := &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      delegations[0].ChildSessionID,
	}
	events, err := store.ListEvents(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	var sawUnknownSpawnTool, sawChildDone bool
	eventSummaries := make([]string, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		summary := strings.TrimSpace(ev.Message.TextContent())
		if resp := ev.Message.ToolResponse(); resp != nil {
			summary = fmt.Sprintf("tool=%s state=%s task=%s output=%s", resp.Name, strings.TrimSpace(stringValue(resp.Result["state"])), strings.TrimSpace(stringValue(resp.Result["task_id"])), strings.TrimSpace(stringValue(resp.Result["output"])))
		}
		eventSummaries = append(eventSummaries, summary)
		if strings.TrimSpace(ev.Message.TextContent()) == "child observed codex complete" {
			sawChildDone = true
		}
		if resp := ev.Message.ToolResponse(); resp != nil && resp.Name == tool.SpawnToolName && strings.Contains(strings.TrimSpace(stringValue(resp.Result["error"])), `unknown tool "SPAWN"`) {
			sawUnknownSpawnTool = true
		}
	}
	if !sawUnknownSpawnTool {
		t.Fatalf("expected delegated self child to see SPAWN as unavailable, got %v", eventSummaries)
	}
	if !sawChildDone {
		t.Fatalf("expected child assistant follow-up after nested codex rejection, got %v", eventSummaries)
	}
}

func TestSelfACPSpawn_ListAndGlobUseChildWorkspace(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "one.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "two.md"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	listTool, err := toolfs.NewListWithRuntime(execRT)
	if err != nil {
		t.Fatal(err)
	}
	globTool, err := toolfs.NewGlobWithRuntime(execRT)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPListGlobLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    workspace,
		Execution:       execRT,
		Tools:           []tool.Tool{listTool, globTool},
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  workspace,
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, workspace, ag, llm, []tool.Tool{listTool, globTool}),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: workspace,
		},
		Input: "delegate list glob",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}
	loadedEvents, err := store.ListEvents(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      delegations[0].ChildSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawList, sawGlob, sawCompleted bool
	for _, ev := range loadedEvents {
		if ev == nil {
			continue
		}
		if info, ok := runtime.LifecycleFromEvent(ev); ok && info.Status == runtime.RunLifecycleStatusCompleted {
			sawCompleted = true
			continue
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
		switch resp.Name {
		case toolfs.ListToolName:
			if got := intValue(resp.Result["count"]); got < 2 {
				t.Fatalf("expected LIST count >= 2, got %#v", resp.Result)
			}
			if filepath.Clean(stringValue(resp.Result["path"])) != workspace {
				t.Fatalf("expected LIST path %q, got %#v", workspace, resp.Result)
			}
			sawList = true
		case toolfs.GlobToolName:
			if got := intValue(resp.Result["count"]); got != 1 {
				t.Fatalf("expected GLOB count 1, got %#v", resp.Result)
			}
			sawGlob = true
		}
	}
	if !sawList || !sawGlob {
		t.Fatalf("expected LIST and GLOB tool responses, sawList=%v sawGlob=%v", sawList, sawGlob)
	}
	if !sawCompleted {
		t.Fatalf("expected child session to persist completed lifecycle, got %+v", loadedEvents)
	}
}

func TestSelfACPSpawnRejectsNestedSelfSpawnWithoutBreakingChildSession(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "one.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	listTool, err := toolfs.NewListWithRuntime(execRT)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPNestedSelfSpawnLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    workspace,
		Execution:       execRT,
		Tools:           []tool.Tool{listTool},
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  workspace,
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, workspace, ag, llm, []tool.Tool{listTool}),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: workspace,
		},
		Input: "delegate nested self",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected only the first-level child delegation, got %d", len(delegations))
	}
	child := &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      delegations[0].ChildSessionID,
	}
	values, err := store.SnapshotState(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	if !internalacp.IsDelegatedChild(anyMap(anyMap(values["acp"])["meta"])) {
		t.Fatalf("expected delegated child marker in child session state, got %#v", values)
	}

	events, err := store.ListEvents(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	var sawNestedSpawnError, sawList, sawRecovered, sawRunnerFailure bool
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if strings.TrimSpace(ev.Message.TextContent()) == "child recovered" {
			sawRecovered = true
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
		errText := strings.TrimSpace(stringValue(resp.Result["error"]))
		switch resp.Name {
		case tool.SpawnToolName:
			if strings.Contains(errText, `unknown tool "SPAWN"`) {
				sawNestedSpawnError = true
			}
		case toolfs.ListToolName:
			sawList = true
		}
		if strings.Contains(errText, "task manager") || strings.Contains(errText, "session-busy") || strings.Contains(errText, "runner closed") {
			sawRunnerFailure = true
		}
	}
	if !sawNestedSpawnError {
		t.Fatalf("expected nested self spawn rejection in child events, got %+v", events)
	}
	if !sawList {
		t.Fatalf("expected child to keep executing tools after nested spawn rejection, got %+v", events)
	}
	if !sawRecovered {
		t.Fatalf("expected child assistant follow-up after nested spawn rejection, got %+v", events)
	}
	if sawRunnerFailure {
		t.Fatalf("unexpected stale runner/task manager failure after nested spawn rejection, got %+v", events)
	}
}

type testACPSpawnLLM struct{}

type testACPListGlobLLM struct{}

type testACPNestedSelfSpawnLLM struct{}

type testACPNestedExternalSpawnLLM struct{}

type timeoutAwareACPSpawnLLM struct {
	started  chan struct{}
	canceled chan struct{}
}

type controlledACPSpawnLLM struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

type fakeTerminalOutputClient struct {
	mu       sync.Mutex
	outputs  []acpclient.TerminalOutputResponse
	released []string
}

func (f *fakeTerminalOutputClient) TerminalOutput(_ context.Context, _, _ string) (acpclient.TerminalOutputResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.outputs) == 0 {
		return acpclient.TerminalOutputResponse{}, nil
	}
	resp := f.outputs[0]
	if len(f.outputs) > 1 {
		f.outputs = f.outputs[1:]
	}
	return resp, nil
}

func (f *fakeTerminalOutputClient) TerminalRelease(_ context.Context, _, terminalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, terminalID)
	return nil
}

func intPtr(value int) *int { return &value }

func (l *testACPSpawnLLM) Name() string { return "test-acp-spawn" }

func (l *testACPSpawnLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate please":
				args, _ := json.Marshal(map[string]any{"prompt": "child task", "yield_time_ms": 0})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-1", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			case "child task":
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "child done"),
					TurnComplete: true,
				}), nil)
				return
			}
		case model.RoleTool:
			if resp := last.ToolResponse(); resp != nil && resp.Name == tool.SpawnToolName {
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "delegated complete"),
					TurnComplete: true,
				}), nil)
				return
			}
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func (l *testACPListGlobLLM) Name() string { return "test-acp-list-glob" }

func (l *testACPNestedSelfSpawnLLM) Name() string { return "test-acp-nested-self-spawn" }

func (l *testACPNestedExternalSpawnLLM) Name() string { return "test-acp-nested-external-spawn" }

func (l *timeoutAwareACPSpawnLLM) Name() string { return "test-acp-timeout" }

func (l *controlledACPSpawnLLM) Name() string { return "test-acp-controlled" }

func TestTerminalBridgeManager_StreamsTerminalOutputIntoSessionUpdates(t *testing.T) {
	client := &fakeTerminalOutputClient{
		outputs: []acpclient.TerminalOutputResponse{
			{Output: "[10s] heartbeat 1/2\n"},
			{
				Output: "[10s] heartbeat 1/2\n[20s] heartbeat 2/2\n",
				ExitStatus: &acpclient.TerminalExitStatus{
					ExitCode: intPtr(0),
				},
			},
		},
	}
	meta := runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child-1",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}
	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx, cancel := context.WithCancel(sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	})))
	defer cancel()
	manager := &terminalBridgeManager{}
	tracker := newRemoteSubagentTracker()
	title := "BASH python long_job.py"
	kind := "execute"
	manager.observe(ctx, client, tracker, "child-1", "self", meta, acpclient.ToolCallUpdate{
		ToolCallID: "call-bash-1",
		Title:      &title,
		Kind:       &kind,
		Status:     strPtr("in_progress"),
		Content: []acpclient.ToolCallContent{{
			Type:       "terminal",
			TerminalID: "term-child-1",
		}},
	})
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(updates)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	manager.stopAll()

	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 streamed terminal updates, got %#v", updates)
	}
	first := updates[0].Event.Message.ToolResponse()
	second := updates[1].Event.Message.ToolResponse()
	if first == nil || second == nil {
		t.Fatalf("expected tool responses, got %#v", updates)
	}
	if got := first.Result["stdout"]; !strings.Contains(got.(string), "heartbeat 1/2") {
		t.Fatalf("expected first terminal delta, got %#v", first.Result)
	}
	if got := second.Result["stdout"]; !strings.Contains(got.(string), "heartbeat 2/2") {
		t.Fatalf("expected second terminal delta, got %#v", second.Result)
	}
	state, ok := tracker.inspect("self", "child-1")
	if !ok {
		t.Fatal("expected terminal bridge to update tracker state")
	}
	if state.ProgressSeq <= 0 {
		t.Fatalf("expected tracker progress seq to advance, got %+v", state)
	}
	if !strings.Contains(state.LatestOutput, "heartbeat 2/2") {
		t.Fatalf("expected tracker latest output to include terminal progress, got %+v", state)
	}
}

func TestTerminalBridgeManager_OnlyPausesWatchdogForActiveTerminalSession(t *testing.T) {
	client := &fakeTerminalOutputClient{
		outputs: []acpclient.TerminalOutputResponse{
			{
				Output: "[10s] heartbeat\n",
				ExitStatus: &acpclient.TerminalExitStatus{
					ExitCode: intPtr(0),
				},
			},
		},
	}
	var starts, stops int
	manager := &terminalBridgeManager{
		onStart: func() { starts++ },
		onStop:  func() { stops++ },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	title := "BASH python long_job.py"
	kind := "execute"
	update := acpclient.ToolCallUpdate{
		ToolCallID: "call-bash-1",
		Title:      &title,
		Kind:       &kind,
		Status:     strPtr("in_progress"),
		Content: []acpclient.ToolCallContent{{
			Type:       "terminal",
			TerminalID: "term-child-1",
		}},
	}
	manager.observe(ctx, client, newRemoteSubagentTracker(), "child-1", "self", runtime.DelegationMetadata{}, update)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if starts == 1 && stops == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if starts != 1 || stops != 1 {
		t.Fatalf("expected one start and one stop callback for terminal-backed tool, got starts=%d stops=%d", starts, stops)
	}
}

func (l *testACPListGlobLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate list glob":
				args, _ := json.Marshal(map[string]any{"prompt": "child list glob", "yield_time_ms": 0})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-1", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			case "child list glob":
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-list-1", Name: toolfs.ListToolName, Args: `{"path":"."}`}}, ""),
					TurnComplete: true,
				}), nil)
				return
			}
		case model.RoleTool:
			resp := last.ToolResponse()
			if resp == nil {
				break
			}
			switch resp.Name {
			case tool.SpawnToolName:
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "delegated complete"),
					TurnComplete: true,
				}), nil)
				return
			case toolfs.ListToolName:
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-glob-1", Name: toolfs.GlobToolName, Args: `{"pattern":"*.txt"}`}}, ""),
					TurnComplete: true,
				}), nil)
				return
			case toolfs.GlobToolName:
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "child done"),
					TurnComplete: true,
				}), nil)
				return
			}
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func (l *testACPNestedSelfSpawnLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate nested self":
				args, _ := json.Marshal(map[string]any{"prompt": "child nested self", "yield_time_ms": 0})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-1", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			case "child nested self":
				args, _ := json.Marshal(map[string]any{"agent": "self", "prompt": "grandchild blocked", "yield_time_ms": 0})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-nested", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			}
		case model.RoleTool:
			resp := last.ToolResponse()
			if resp == nil {
				break
			}
			switch resp.Name {
			case tool.SpawnToolName:
				if errText := strings.TrimSpace(stringValue(resp.Result["error"])); errText != "" {
					yield(model.StreamEventFromResponse(&model.Response{
						Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-list-1", Name: toolfs.ListToolName, Args: `{"path":"."}`}}, ""),
						TurnComplete: true,
					}), nil)
					return
				}
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "delegated complete"),
					TurnComplete: true,
				}), nil)
				return
			case toolfs.ListToolName:
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "child recovered"),
					TurnComplete: true,
				}), nil)
				return
			}
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func (l *testACPNestedExternalSpawnLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate nested codex":
				args, _ := json.Marshal(map[string]any{"prompt": "child nested codex", "yield_time_ms": 0})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-self", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			case "child nested codex":
				args, _ := json.Marshal(map[string]any{"agent": "codex", "prompt": "codex nested task", "yield_time_ms": 1})
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "call-spawn-codex", Name: tool.SpawnToolName, Args: string(args)}}, ""),
					TurnComplete: true,
				}), nil)
				return
			}
		case model.RoleTool:
			resp := last.ToolResponse()
			if resp == nil {
				break
			}
			if resp.Name == tool.SpawnToolName {
				if errText := strings.TrimSpace(stringValue(resp.Result["error"])); errText != "" {
					yield(model.StreamEventFromResponse(&model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "child observed codex complete"),
						TurnComplete: true,
					}), nil)
					return
				}
				output := strings.TrimSpace(stringValue(resp.Result["output"]))
				if strings.Contains(output, "child observed codex complete") {
					yield(model.StreamEventFromResponse(&model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "root observed child complete"),
						TurnComplete: true,
					}), nil)
					return
				}
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "child observed codex complete"),
					TurnComplete: true,
				}), nil)
				return
			}
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func (l *timeoutAwareACPSpawnLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser && last.TextContent() == "slow child" {
			select {
			case l.started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			select {
			case l.canceled <- struct{}{}:
			default:
			}
			yield(nil, ctx.Err())
			return
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func (l *controlledACPSpawnLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser && last.TextContent() == "slow child" {
			select {
			case l.started <- struct{}{}:
			default:
			}
			select {
			case <-l.release:
				select {
				case l.finished <- struct{}{}:
				default:
				}
				yield(model.StreamEventFromResponse(&model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "child done"),
					TurnComplete: true,
				}), nil)
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
			return
		}
		yield(model.StreamEventFromResponse(&model.Response{
			Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
			TurnComplete: true,
		}), nil)
	}
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func anyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func drainTurn(seq iter.Seq2[*session.Event, error]) []error {
	errs := make([]error, 0)
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

type recordingApprover struct {
	allow           bool
	calls           int
	last            toolexec.ApprovalRequest
	lastInteractive bool
}

func (a *recordingApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	a.calls++
	a.last = req
	a.lastInteractive = toolexec.InteractiveApprovalRequired(ctx)
	return a.allow, nil
}

type recordingToolAuthorizer struct {
	allow           bool
	calls           int
	last            kernelpolicy.ToolAuthorizationRequest
	lastInteractive bool
}

func (a *recordingToolAuthorizer) AuthorizeTool(ctx context.Context, req kernelpolicy.ToolAuthorizationRequest) (bool, error) {
	a.calls++
	a.last = req
	a.lastInteractive = toolexec.InteractiveApprovalRequired(ctx)
	return a.allow, nil
}
