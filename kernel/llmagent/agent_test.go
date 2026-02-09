package llmagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type testCtx struct {
	context.Context
	session  *session.Session
	history  []*session.Event
	llm      model.LLM
	tools    []tool.Tool
	toolMap  map[string]tool.Tool
	policies []policy.Hook
}

func (c *testCtx) Session() *session.Session { return c.session }
func (c *testCtx) History() []*session.Event { return c.history }
func (c *testCtx) Model() model.LLM          { return c.llm }
func (c *testCtx) Tools() []tool.Tool        { return c.tools }
func (c *testCtx) Tool(name string) (tool.Tool, bool) {
	t, ok := c.toolMap[name]
	return t, ok
}
func (c *testCtx) Policies() []policy.Hook { return c.policies }

type namedTool struct {
	name string
	run  func(context.Context, map[string]any) (map[string]any, error)
}

func (t namedTool) Name() string        { return t.name }
func (t namedTool) Description() string { return t.name }
func (t namedTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{Name: t.name, Description: t.name, Parameters: map[string]any{"type": "object"}}
}
func (t namedTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	if t.run == nil {
		return map[string]any{}, nil
	}
	return t.run(ctx, args)
}

type echoArgs struct {
	Text string `json:"text"`
}

type echoResp struct {
	Echo string `json:"echo"`
}

func TestLLMAgent_ToolLoop(t *testing.T) {
	echoTool, err := tool.NewFunction[echoArgs, echoResp]("echo", "echo", func(ctx context.Context, args echoArgs) (echoResp, error) {
		_ = ctx
		return echoResp{Echo: args.Text}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "echo", Args: map[string]any{"text": "hello"}}}}}, nil
		}
		if last.Role == model.RoleTool {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
		}
		return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "fallback"}}, nil
	})

	ag, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo": echoTool},
	}

	var events []*session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		events = append(events, ev)
	}
	if len(events) < 3 {
		t.Fatalf("expected >= 3 events, got %d", len(events))
	}
	if events[1].Message.ToolResponse == nil {
		t.Fatalf("expected tool response event")
	}
	if events[len(events)-1].Message.Text != "done" {
		t.Fatalf("unexpected final text: %q", events[len(events)-1].Message.Text)
	}
}

func TestLLMAgent_ToolResultTruncation(t *testing.T) {
	echoTool, err := tool.NewFunction[struct{}, struct {
		Out string `json:"out"`
	}]("echo_big", "echo big", func(ctx context.Context, args struct{}) (struct {
		Out string `json:"out"`
	}, error) {
		_ = ctx
		_ = args
		return struct {
			Out string `json:"out"`
		}{Out: strings.Repeat("x", 12000)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo_big",
					Args: map[string]any{},
				}},
			}}, nil
		}
		if last.Role == model.RoleTool {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
		}
		return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "fallback"}}, nil
	})

	ag, err := New(Config{
		Name:           "test",
		ToolTruncation: tool.TruncationPolicy{MaxTokens: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo_big": echoTool},
	}

	var toolEvent *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if ev != nil && ev.Message.ToolResponse != nil {
			toolEvent = ev
			break
		}
	}
	if toolEvent == nil {
		t.Fatal("expected tool response event")
	}
	meta, ok := toolEvent.Message.ToolResponse.Result["_tool_truncation"].(map[string]any)
	if !ok {
		t.Fatalf("expected _tool_truncation meta, got: %#v", toolEvent.Message.ToolResponse.Result)
	}
	if meta["truncated"] != true {
		t.Fatalf("expected truncated meta true, got: %#v", meta["truncated"])
	}
}

func TestLLMAgent_RefreshesToolDeclarationsAfterActivation(t *testing.T) {
	dynamicTool := namedTool{name: "LSP_DIAGNOSTICS"}
	activationTool := namedTool{
		name: "LSP_ACTIVATE",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			inv, ok := ctx.(*testCtx)
			if !ok {
				return nil, fmt.Errorf("unexpected context type %T", ctx)
			}
			inv.tools = append(inv.tools, dynamicTool)
			inv.toolMap[dynamicTool.Name()] = dynamicTool
			return map[string]any{"language": "go", "activated": true}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		switch step {
		case 1:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "activate_1",
					Name: "LSP_ACTIVATE",
					Args: map[string]any{"language": "go"},
				}},
			}}, nil
		case 2:
			foundDynamic := false
			for _, one := range req.Tools {
				if one.Name == "LSP_DIAGNOSTICS" {
					foundDynamic = true
					break
				}
			}
			if !foundDynamic {
				return nil, fmt.Errorf("dynamic tool schema not found in second model request")
			}
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
		default:
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
		}
	})

	ag, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "activate lsp"}}},
		llm:     llm,
		tools:   []tool.Tool{activationTool},
		toolMap: map[string]tool.Tool{activationTool.Name(): activationTool},
	}

	var last *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		last = ev
	}
	if last == nil || last.Message.Text != "done" {
		t.Fatalf("unexpected final event: %#v", last)
	}
}

func TestLLMAgent_MaxStepsExceeded(t *testing.T) {
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		return &model.Response{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "c1",
				Name: "echo",
				Args: map[string]any{"text": "loop"},
			}},
		}}, nil
	})
	echoTool, err := tool.NewFunction[echoArgs, echoResp]("echo", "echo", func(ctx context.Context, args echoArgs) (echoResp, error) {
		_ = ctx
		return echoResp{Echo: args.Text}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := New(Config{Name: "test", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{echoTool.Name(): echoTool},
	}
	var gotErr error
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "max steps exceeded") {
		t.Fatalf("expected max steps error, got %v", gotErr)
	}
}

func TestLLMAgent_AllowUnlimitedStepsWithZero(t *testing.T) {
	turn := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		turn++
		if turn == 1 {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: map[string]any{"text": "ok"},
				}},
			}}, nil
		}
		return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
	})
	echoTool, err := tool.NewFunction[echoArgs, echoResp]("echo", "echo", func(ctx context.Context, args echoArgs) (echoResp, error) {
		_ = ctx
		return echoResp{Echo: args.Text}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := New(Config{Name: "test", MaxSteps: 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{echoTool.Name(): echoTool},
	}
	var last *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		last = ev
	}
	if last == nil || last.Message.Text != "done" {
		t.Fatalf("unexpected last event: %#v", last)
	}
}

func TestLLMAgent_RejectNegativeMaxSteps(t *testing.T) {
	_, err := New(Config{Name: "test", MaxSteps: -1})
	if err == nil || !strings.Contains(err.Error(), "max_steps") {
		t.Fatalf("expected max_steps validation error, got %v", err)
	}
}

func TestLLMAgent_PersistsModelUsageMeta(t *testing.T) {
	llm := newTestLLM("fake-provider", func(req *model.Request) (*model.Response, error) {
		_ = req
		return &model.Response{
			Message:  model.Message{Role: model.RoleAssistant, Text: "done"},
			Model:    "demo-model",
			Provider: "demo-provider",
			Usage: model.Usage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		}, nil
	})
	ag, err := New(Config{Name: "test", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   nil,
		toolMap: map[string]tool.Tool{},
	}

	var last *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		last = ev
	}
	if last == nil || last.Meta == nil {
		t.Fatalf("expected event meta with usage")
	}
	if last.Meta["model"] != "demo-model" {
		t.Fatalf("unexpected model meta: %#v", last.Meta["model"])
	}
	if last.Meta["provider"] != "demo-provider" {
		t.Fatalf("unexpected provider meta: %#v", last.Meta["provider"])
	}
	usage, ok := last.Meta["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage map, got %#v", last.Meta["usage"])
	}
	if usage["total_tokens"] != 18 {
		t.Fatalf("unexpected total_tokens: %#v", usage["total_tokens"])
	}
}
