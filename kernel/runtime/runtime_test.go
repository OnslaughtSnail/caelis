package runtime

import (
	"context"
	"iter"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toollsp "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/lsp"
)

type fixedAgent struct{}

func (a fixedAgent) Name() string { return "fixed" }
func (a fixedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
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
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected persisted 2 events, got %d", len(listed))
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
				Name: toollsp.ActivateToolName,
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
