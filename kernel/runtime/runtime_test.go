package runtime

import (
	"context"
	"iter"
	"sync"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type fixedAgent struct{}

func (a fixedAgent) Name() string { return "fixed" }
func (a fixedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
	}
}

type approvalRequiredAgent struct{}

func (a approvalRequiredAgent) Name() string { return "approval-required" }
func (a approvalRequiredAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		_ = yield
		yield(nil, &toolexec.ApprovalRequiredError{Reason: "host escalation required"})
	}
}

type approvalAbortedAgent struct{}

func (a approvalAbortedAgent) Name() string { return "approval-aborted" }
func (a approvalAbortedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		_ = yield
		yield(nil, &toolexec.ApprovalAbortedError{Reason: "denied"})
	}
}

type assertReadAgent struct {
	t *testing.T
}

func (a assertReadAgent) Name() string { return "assert-read" }
func (a assertReadAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		foundRead := false
		for _, t := range ctx.Tools() {
			if t != nil && t.Name() == "READ" {
				foundRead = true
				break
			}
		}
		if !foundRead {
			a.t.Fatalf("expected runtime to inject READ tool")
		}
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
	}
}

type assertLSPAgent struct {
	t *testing.T
}

func (a assertLSPAgent) Name() string { return "assert-lsp" }
func (a assertLSPAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if _, ok := ctx.Tool("LSP_DIAGNOSTICS"); !ok {
			a.t.Fatalf("expected activated LSP_DIAGNOSTICS tool to be available")
		}
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
	}
}

type lspTestAdapter struct{}

func (a lspTestAdapter) Language() string { return "go" }
func (a lspTestAdapter) BuildToolSet(ctx context.Context, req lspbroker.ActivateRequest) (*lspbroker.ToolSet, error) {
	_ = ctx
	diagnosticsTool, err := tool.NewFunction[struct{}, struct{}]("LSP_DIAGNOSTICS", "test", func(ctx context.Context, args struct{}) (struct{}, error) {
		_ = ctx
		_ = args
		return struct{}{}, nil
	})
	if err != nil {
		return nil, err
	}
	return &lspbroker.ToolSet{
		ID:       "lsp:" + req.Language,
		Language: req.Language,
		Tools:    []tool.Tool{diagnosticsTool},
	}, nil
}

type blockingAgent struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (a *blockingAgent) Name() string { return "blocking" }
func (a *blockingAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		a.once.Do(func() {
			close(a.started)
		})
		<-a.release
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil)
	}
}

func TestRuntime_Run(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		events = append(events, ev)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events (running,user,assistant,completed), got %d", len(events))
	}
	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 4 {
		t.Fatalf("expected persisted 4 events, got %d", len(listed))
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusCompleted) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_ApprovalRequiredLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-approval-required",
		Input:     "hello",
		Agent:     approvalRequiredAgent{},
		Model:     llm,
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected approval required error")
	}
	if !toolexec.IsErrorCode(gotErr, toolexec.ErrorCodeApprovalRequired) {
		t.Fatalf("expected approval required code, got %q (%v)", toolexec.ErrorCodeOf(gotErr), gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusWaitingApproval) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_ApprovalAbortedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-approval-aborted",
		Input:     "hello",
		Agent:     approvalAbortedAgent{},
		Model:     llm,
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected approval aborted error")
	}
	if !toolexec.IsErrorCode(gotErr, toolexec.ErrorCodeApprovalAborted) {
		t.Fatalf("expected approval aborted code, got %q (%v)", toolexec.ErrorCodeOf(gotErr), gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusInterrupted) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_PreAgentSetupFailureAppendsFailedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	badTool, err := tool.NewFunction[struct{}, struct{}](
		tool.ReadToolName,
		"reserved",
		func(ctx context.Context, args struct{}) (struct{}, error) {
			_ = ctx
			_ = args
			return struct{}{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-setup-failed",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		Tools:     []tool.Tool{badTool},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected setup failure")
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusFailed) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_InjectsCoreReadTool(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-read",
		Input:     "hello",
		Agent:     assertReadAgent{t: t},
		Model:     llm,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
}

func TestRuntime_RestoreActivatedLSPToolsFromHistory(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-lsp"}
	_, err := store.GetOrCreate(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	err = store.AppendEvent(context.Background(), sess, &session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_activate_1",
				Name: "LSP_ACTIVATE",
				Result: map[string]any{
					"language":   "go",
					"toolset_id": "lsp:go",
					"activated":  true,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	broker := lspbroker.New()
	if err := broker.RegisterAdapter(lspTestAdapter{}); err != nil {
		t.Fatal(err)
	}
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-lsp",
		Input:     "hello",
		Agent:     assertLSPAgent{t: t},
		Model:     llm,
		LSPBroker: broker,
		LSPActivationTools: []string{
			"LSP_ACTIVATE",
		},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
}

func TestRuntime_AutoActivateLSPTools(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	broker := lspbroker.New()
	if err := broker.RegisterAdapter(lspTestAdapter{}); err != nil {
		t.Fatal(err)
	}
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:         "app",
		UserID:          "u",
		SessionID:       "s-lsp-auto",
		Input:           "hello",
		Agent:           assertLSPAgent{t: t},
		Model:           llm,
		LSPBroker:       broker,
		AutoActivateLSP: []string{"go"},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
}

func TestRuntime_ContextUsage(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-usage",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	usage, err := rt.ContextUsage(context.Background(), UsageRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-usage",
		Model:     llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.WindowTokens <= 0 {
		t.Fatalf("expected positive window tokens, got %d", usage.WindowTokens)
	}
	if usage.CurrentTokens <= 0 {
		t.Fatalf("expected positive current tokens, got %d", usage.CurrentTokens)
	}
	if usage.Ratio <= 0 {
		t.Fatalf("expected positive ratio, got %f", usage.Ratio)
	}
}

func TestRuntime_ContextUsage_MissingSessionReturnsEmpty(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	usage, err := rt.ContextUsage(context.Background(), UsageRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "missing",
		Model:     llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CurrentTokens != 0 || usage.EventCount != 0 {
		t.Fatalf("expected empty usage for missing session, got %+v", usage)
	}
}

func TestRuntime_RunState_MissingSessionReturnsNone(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.HasLifecycle {
		t.Fatalf("expected missing session run state to be empty, got %+v", state)
	}
}

func TestRuntime_Run_SessionSingleFlight(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	release := make(chan struct{})
	agent1 := &blockingAgent{
		started: make(chan struct{}),
		release: release,
	}
	firstRunDone := make(chan struct{})
	go func() {
		defer close(firstRunDone)
		for _, runErr := range rt.Run(context.Background(), RunRequest{
			AppName:   "app",
			UserID:    "u",
			SessionID: "s-single-flight",
			Input:     "hello",
			Agent:     agent1,
			Model:     llm,
		}) {
			if runErr != nil {
				return
			}
		}
	}()
	<-agent1.started

	var gotErr error
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-single-flight",
		Input:     "hello2",
		Agent:     fixedAgent{},
		Model:     llm,
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected session busy error for concurrent run")
	}
	if !IsSessionBusy(gotErr) {
		t.Fatalf("expected session busy error, got %v", gotErr)
	}

	close(release)
	<-firstRunDone
}

func lifecycleStatuses(events []*session.Event) []string {
	out := make([]string, 0, 2)
	for _, ev := range events {
		if ev == nil || ev.Meta == nil {
			continue
		}
		kind, _ := ev.Meta[metaKind].(string)
		if kind != metaKindLifecycle {
			continue
		}
		payload, _ := ev.Meta[MetaLifecycle].(map[string]any)
		status, _ := payload["status"].(string)
		if status == "" {
			continue
		}
		out = append(out, status)
	}
	return out
}
