package llmagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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

func TestLLMAgent_RetriesModelRequestAndSucceeds(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	modelRequestMaxRetries = 5
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
	})

	attempts := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		_ = req
		attempts++
		if attempts <= 2 {
			return nil, errors.New("temporary upstream failure")
		}
		return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
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
	if attempts != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", attempts)
	}
	if last == nil || strings.TrimSpace(last.Message.Text) != "done" {
		t.Fatalf("unexpected final message: %#v", last)
	}
}

func TestLLMAgent_RetryExhaustedReturnsError(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	modelRequestMaxRetries = 5
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
	})

	attempts := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		_ = req
		attempts++
		return nil, errors.New("upstream unavailable")
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
		tools:   nil,
		toolMap: map[string]tool.Tool{},
	}
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			if !strings.Contains(runErr.Error(), "failed after 5 retries") {
				t.Fatalf("unexpected retry error: %v", runErr)
			}
			if attempts != 6 {
				t.Fatalf("expected 6 attempts (initial + 5 retries), got %d", attempts)
			}
			return
		}
	}
	t.Fatal("expected retry exhausted error")
}

func TestLLMAgent_StopsWhenApprovalIsCanceled(t *testing.T) {
	cancelTool := namedTool{
		name: "needs_approval",
		run: func(context.Context, map[string]any) (map[string]any, error) {
			return nil, &toolexec.ApprovalAbortedError{Reason: "approval denied"}
		},
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "needs_approval",
					Args: map[string]any{},
				}},
			}}, nil
		}
		t.Fatalf("agent should stop after approval cancel, got last role=%s", last.Role)
		return nil, nil
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
		tools:   []tool.Tool{cancelTool},
		toolMap: map[string]tool.Tool{"needs_approval": cancelTool},
	}

	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			if !toolexec.IsApprovalAborted(runErr) {
				t.Fatalf("expected approval aborted error, got %v", runErr)
			}
			return
		}
		if ev != nil && ev.Message.ToolResponse != nil {
			t.Fatalf("expected no tool response after cancel, got %+v", ev.Message.ToolResponse)
		}
	}
	t.Fatal("expected run to fail with approval canceled error")
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

func TestToMessages_StripsUIOnlyToolResultKeys(t *testing.T) {
	history := []*session.Event{
		{
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:   "call_1",
					Name: "PATCH",
					Result: map[string]any{
						"path":        "a.txt",
						"_ui_preview": "--- old\n+++ new",
						"metadata": map[string]any{
							"preview": "hidden",
						},
						"nested": map[string]any{
							"_ui_note": "internal",
							"ok":       true,
						},
					},
				},
			},
		},
	}
	msgs := toMessages(history, "sys")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	result := msgs[1].ToolResponse.Result
	if _, exists := result["_ui_preview"]; exists {
		t.Fatalf("expected _ui_preview to be stripped, got %#v", result)
	}
	if _, exists := result["metadata"]; exists {
		t.Fatalf("expected metadata to be stripped, got %#v", result)
	}
	nested, _ := result["nested"].(map[string]any)
	if _, exists := nested["_ui_note"]; exists {
		t.Fatalf("expected nested _ui_note to be stripped, got %#v", nested)
	}
}

func TestLLMAgent_DoesNotSendUIOnlyToolFieldsToModel(t *testing.T) {
	previewTool := namedTool{
		name: "preview_tool",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			return map[string]any{
				"value":       "ok",
				"_ui_preview": "--- old\n+++ new",
				"metadata": map[string]any{
					"preview": "hidden",
				},
			}, nil
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
					ID:   "c1",
					Name: "preview_tool",
					Args: map[string]any{},
				}},
			}}, nil
		case 2:
			last := req.Messages[len(req.Messages)-1]
			if last.ToolResponse == nil {
				return nil, fmt.Errorf("expected tool response in second request")
			}
			if _, exists := last.ToolResponse.Result["_ui_preview"]; exists {
				return nil, fmt.Errorf("unexpected _ui_preview in model-visible tool response")
			}
			if _, exists := last.ToolResponse.Result["metadata"]; exists {
				return nil, fmt.Errorf("unexpected metadata in model-visible tool response")
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{previewTool},
		toolMap: map[string]tool.Tool{"preview_tool": previewTool},
	}

	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
}

func TestLLMAgent_AddsDefaultMetadataToToolResults(t *testing.T) {
	toolWithMinimalResult := namedTool{
		name: "minimal_tool",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			return map[string]any{"value": "ok"}, nil
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
					ID:   "c1",
					Name: "minimal_tool",
					Args: map[string]any{},
				}},
			}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{toolWithMinimalResult},
		toolMap: map[string]tool.Tool{"minimal_tool": toolWithMinimalResult},
	}

	var toolEvent *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if ev != nil && ev.Message.ToolResponse != nil {
			toolEvent = ev
		}
	}
	if toolEvent == nil {
		t.Fatal("expected tool response event")
	}
	meta, ok := toolEvent.Message.ToolResponse.Result["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map in tool result, got %#v", toolEvent.Message.ToolResponse.Result["metadata"])
	}
	if len(meta) != 0 {
		t.Fatalf("expected empty default metadata map, got %#v", meta)
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

func TestLLMAgent_IgnoresMaxStepsLimit(t *testing.T) {
	turn := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		turn++
		if turn == 1 {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: map[string]any{"text": "loop"},
				}},
			}}, nil
		}
		return &model.Response{Message: model.Message{
			Role: model.RoleAssistant, Text: "done",
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

func TestLLMAgent_AllowsNegativeMaxSteps(t *testing.T) {
	_, err := New(Config{Name: "test", MaxSteps: -1})
	if err != nil {
		t.Fatalf("expected negative max steps to be ignored, got %v", err)
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
