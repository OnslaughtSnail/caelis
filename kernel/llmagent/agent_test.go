package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/delegation"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

type testCtx struct {
	context.Context
	session  *session.Session
	history  []*session.Event
	llm      model.LLM
	tools    []tool.Tool
	toolMap  map[string]tool.Tool
	policies []policy.Hook
	runner   delegation.Runner
}

func (c *testCtx) Session() *session.Session { return c.session }
func (c *testCtx) Events() session.Events    { return session.NewEvents(c.history) }
func (c *testCtx) ReadonlyState() session.ReadonlyState {
	return session.NewReadonlyState(nil)
}
func (c *testCtx) Model() model.LLM   { return c.llm }
func (c *testCtx) Tools() []tool.Tool { return c.tools }
func (c *testCtx) Tool(name string) (tool.Tool, bool) {
	t, ok := c.toolMap[name]
	return t, ok
}
func (c *testCtx) Policies() []policy.Hook { return c.policies }
func (c *testCtx) SubagentRunner() delegation.Runner {
	return c.runner
}
func (c *testCtx) recordVisibleEvent(ev *session.Event) {
	if ev == nil {
		return
	}
	cp := *ev
	c.history = append(c.history, &cp)
}

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

type capabilityNamedTool struct {
	namedTool
	capability toolcap.Capability
}

func (t capabilityNamedTool) Capability() toolcap.Capability {
	return t.capability
}

type captureCapabilityHook struct {
	before []toolcap.Capability
	after  []toolcap.Capability
}

func (h *captureCapabilityHook) Name() string { return "capture_capability" }
func (h *captureCapabilityHook) BeforeModel(ctx context.Context, in policy.ModelInput) (policy.ModelInput, error) {
	_ = ctx
	return in, nil
}
func (h *captureCapabilityHook) BeforeTool(ctx context.Context, in policy.ToolInput) (policy.ToolInput, error) {
	_ = ctx
	h.before = append(h.before, in.Capability)
	return in, nil
}
func (h *captureCapabilityHook) AfterTool(ctx context.Context, out policy.ToolOutput) (policy.ToolOutput, error) {
	_ = ctx
	h.after = append(h.after, out.Capability)
	return out, nil
}
func (h *captureCapabilityHook) BeforeOutput(ctx context.Context, out policy.Output) (policy.Output, error) {
	_ = ctx
	return out, nil
}

type captureToolCallInfoHook struct {
	before []toolexec.ToolCallInfo
	after  []toolexec.ToolCallInfo
}

func (h *captureToolCallInfoHook) Name() string { return "capture_tool_call_info" }
func (h *captureToolCallInfoHook) BeforeModel(ctx context.Context, in policy.ModelInput) (policy.ModelInput, error) {
	_ = ctx
	return in, nil
}
func (h *captureToolCallInfoHook) BeforeTool(ctx context.Context, in policy.ToolInput) (policy.ToolInput, error) {
	info, _ := toolexec.ToolCallInfoFromContext(ctx)
	h.before = append(h.before, info)
	return in, nil
}
func (h *captureToolCallInfoHook) AfterTool(ctx context.Context, out policy.ToolOutput) (policy.ToolOutput, error) {
	info, _ := toolexec.ToolCallInfoFromContext(ctx)
	h.after = append(h.after, info)
	return out, nil
}
func (h *captureToolCallInfoHook) BeforeOutput(ctx context.Context, out policy.Output) (policy.Output, error) {
	_ = ctx
	return out, nil
}

type requireApprovalHook struct{}

func (h requireApprovalHook) Name() string { return "require_approval_hook" }
func (h requireApprovalHook) BeforeModel(ctx context.Context, in policy.ModelInput) (policy.ModelInput, error) {
	_ = ctx
	return in, nil
}
func (h requireApprovalHook) BeforeTool(ctx context.Context, in policy.ToolInput) (policy.ToolInput, error) {
	_ = ctx
	in.Decision = policy.DecisionWithRoute(policy.Decision{
		Effect: policy.DecisionEffectRequireApproval,
		Reason: "approval required by policy hook",
	}, policy.DecisionRouteHost)
	return in, nil
}
func (h requireApprovalHook) AfterTool(ctx context.Context, out policy.ToolOutput) (policy.ToolOutput, error) {
	_ = ctx
	return out, nil
}
func (h requireApprovalHook) BeforeOutput(ctx context.Context, out policy.Output) (policy.Output, error) {
	_ = ctx
	return out, nil
}

type denyToolHook struct{}

func (h denyToolHook) Name() string { return "deny_tool_hook" }
func (h denyToolHook) BeforeModel(ctx context.Context, in policy.ModelInput) (policy.ModelInput, error) {
	_ = ctx
	return in, nil
}
func (h denyToolHook) BeforeTool(ctx context.Context, in policy.ToolInput) (policy.ToolInput, error) {
	_ = ctx
	in.Decision = policy.Decision{Effect: policy.DecisionEffectDeny, Reason: "denied by hook"}
	return in, nil
}
func (h denyToolHook) AfterTool(ctx context.Context, out policy.ToolOutput) (policy.ToolOutput, error) {
	_ = ctx
	return out, nil
}
func (h denyToolHook) BeforeOutput(ctx context.Context, out policy.Output) (policy.Output, error) {
	_ = ctx
	return out, nil
}

type echoArgs struct {
	Text string `json:"text"`
}

type echoResp struct {
	Echo string `json:"echo"`
}

func eventIsPartialEvent(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	raw, ok := ev.Meta["partial"]
	flag, ok := raw.(bool)
	return ok && flag
}

func jsonArgs(v map[string]any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
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
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "echo", Args: jsonArgs(map[string]any{"text": "hello"})}}}}, nil
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

func TestLLMAgent_ExposesToolCapabilityToPolicies(t *testing.T) {
	capTool := capabilityNamedTool{
		namedTool: namedTool{
			name: "cap_tool",
			run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
				_ = ctx
				_ = args
				return map[string]any{"ok": true}, nil
			},
		},
		capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationExec},
			Risk:       toolcap.RiskHigh,
		},
	}
	hook := &captureCapabilityHook{}
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "cap_tool",
					Args: "{}",
				}},
			}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{capTool},
		toolMap: map[string]tool.Tool{"cap_tool": capTool},
		policies: []policy.Hook{
			hook,
		},
	}
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if len(hook.before) == 0 {
		t.Fatal("expected before-tool policy to capture capability")
	}
	if len(hook.after) == 0 {
		t.Fatal("expected after-tool policy to capture capability")
	}
	if !hook.before[0].HasOperation(toolcap.OperationExec) || hook.before[0].Risk != toolcap.RiskHigh {
		t.Fatalf("unexpected before capability: %#v", hook.before[0])
	}
	if !hook.after[0].HasOperation(toolcap.OperationExec) || hook.after[0].Risk != toolcap.RiskHigh {
		t.Fatalf("unexpected after capability: %#v", hook.after[0])
	}
}

func TestLLMAgent_ExposesToolCallInfoAcrossPolicyLifecycle(t *testing.T) {
	hook := &captureToolCallInfoHook{}
	toolSeen := toolexec.ToolCallInfo{}
	infoTool := namedTool{
		name: "info_tool",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = args
			toolSeen, _ = toolexec.ToolCallInfoFromContext(ctx)
			return map[string]any{"ok": true}, nil
		},
	}
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-info-1",
					Name: "info_tool",
					Args: "{}",
				}},
			}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{infoTool},
		toolMap: map[string]tool.Tool{"info_tool": infoTool},
		policies: []policy.Hook{
			hook,
		},
	}
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if len(hook.before) != 1 {
		t.Fatalf("expected one before-tool capture, got %d", len(hook.before))
	}
	if hook.before[0].ID != "call-info-1" || hook.before[0].Name != "info_tool" {
		t.Fatalf("unexpected before-tool call info: %#v", hook.before[0])
	}
	if toolSeen.ID != "call-info-1" || toolSeen.Name != "info_tool" {
		t.Fatalf("unexpected tool-run call info: %#v", toolSeen)
	}
	if len(hook.after) != 1 {
		t.Fatalf("expected one after-tool capture, got %d", len(hook.after))
	}
	if hook.after[0].ID != "call-info-1" || hook.after[0].Name != "info_tool" {
		t.Fatalf("unexpected after-tool call info: %#v", hook.after[0])
	}
}

func TestLLMAgent_RetriesModelRequestAndSucceeds(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	oldRateLimitMaxRetries := rateLimitRequestMaxRetries
	oldRateLimitBaseDelay := rateLimitRetryBaseDelay
	oldRateLimitMaxDelay := rateLimitRetryMaxDelay
	modelRequestMaxRetries = 5
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	rateLimitRequestMaxRetries = 7
	rateLimitRetryBaseDelay = 5 * time.Millisecond
	rateLimitRetryMaxDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
		rateLimitRequestMaxRetries = oldRateLimitMaxRetries
		rateLimitRetryBaseDelay = oldRateLimitBaseDelay
		rateLimitRetryMaxDelay = oldRateLimitMaxDelay
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
	var retryWarnings []string
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if notice, ok := session.EventNotice(ev); ok {
			if !session.IsUIOnly(ev) {
				t.Fatalf("expected retry warning to be ui-only, got meta=%v", ev.Meta)
			}
			retryWarnings = append(retryWarnings, notice.Text)
		}
		last = ev
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", attempts)
	}
	if len(retryWarnings) != 2 {
		t.Fatalf("expected 2 retry warnings, got %v", retryWarnings)
	}
	if !strings.Contains(retryWarnings[0], "retrying in") {
		t.Fatalf("expected retry warning text, got %v", retryWarnings)
	}
	if last == nil || strings.TrimSpace(last.Message.Text) != "done" {
		t.Fatalf("unexpected final message: %#v", last)
	}
}

func TestLLMAgent_RetryExhaustedReturnsError(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	oldRateLimitMaxRetries := rateLimitRequestMaxRetries
	oldRateLimitBaseDelay := rateLimitRetryBaseDelay
	oldRateLimitMaxDelay := rateLimitRetryMaxDelay
	modelRequestMaxRetries = 5
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	rateLimitRequestMaxRetries = 7
	rateLimitRetryBaseDelay = 5 * time.Millisecond
	rateLimitRetryMaxDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
		rateLimitRequestMaxRetries = oldRateLimitMaxRetries
		rateLimitRetryBaseDelay = oldRateLimitBaseDelay
		rateLimitRetryMaxDelay = oldRateLimitMaxDelay
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

func TestLLMAgent_RateLimitRetriesUseDedicatedPolicy(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	oldRateLimitMaxRetries := rateLimitRequestMaxRetries
	oldRateLimitBaseDelay := rateLimitRetryBaseDelay
	oldRateLimitMaxDelay := rateLimitRetryMaxDelay
	modelRequestMaxRetries = 5
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	rateLimitRequestMaxRetries = 7
	rateLimitRetryBaseDelay = 120 * time.Millisecond
	rateLimitRetryMaxDelay = 250 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
		rateLimitRequestMaxRetries = oldRateLimitMaxRetries
		rateLimitRetryBaseDelay = oldRateLimitBaseDelay
		rateLimitRetryMaxDelay = oldRateLimitMaxDelay
	})

	attempts := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		_ = req
		attempts++
		if attempts <= 2 {
			return nil, errors.New(`model: http status 429 body={"detail":"用户请求TPM超限，请减少tokens后重试","error":{"type":"USER_TPM_RATELIMITING"}}`)
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
	var retryWarnings []string
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if notice, ok := session.EventNotice(ev); ok {
			retryWarnings = append(retryWarnings, notice.Text)
		}
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", attempts)
	}
	if len(retryWarnings) != 2 {
		t.Fatalf("expected 2 retry warnings, got %v", retryWarnings)
	}
	if !strings.Contains(retryWarnings[0], "hit rate limits") {
		t.Fatalf("expected rate-limit warning text, got %v", retryWarnings)
	}
	if !strings.Contains(retryWarnings[0], "(1/7)") {
		t.Fatalf("expected dedicated retry budget in warning, got %v", retryWarnings)
	}
	if !strings.Contains(retryWarnings[0], "HTTP 429 / Too Many Requests") {
		t.Fatalf("expected generic 429 warning context, got %v", retryWarnings)
	}
	if strings.Contains(retryWarnings[0], `body={`) {
		t.Fatalf("expected summarized warning without raw body, got %v", retryWarnings)
	}
	if strings.Contains(retryWarnings[0], "USER_TPM_RATELIMITING") || strings.Contains(retryWarnings[0], "用户请求TPM超限") {
		t.Fatalf("expected no vendor-specific body detail in warning, got %v", retryWarnings)
	}
}

func TestRetryDelayForAttempt_UsesConfiguredBounds(t *testing.T) {
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	modelRetryBaseDelay = time.Second
	modelRetryMaxDelay = 3 * time.Minute
	t.Cleanup(func() {
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
	})

	if got := retryDelayForAttempt(0); got != time.Second {
		t.Fatalf("retry 0: want %s, got %s", time.Second, got)
	}
	if got := retryDelayForAttempt(1); got != 2*time.Second {
		t.Fatalf("retry 1: want %s, got %s", 2*time.Second, got)
	}
	if got := retryDelayForAttempt(7); got != 128*time.Second {
		t.Fatalf("retry 7: want %s, got %s", 128*time.Second, got)
	}
	if got := retryDelayForAttempt(8); got != 3*time.Minute {
		t.Fatalf("retry 8: want %s, got %s", 3*time.Minute, got)
	}
	if got := retryDelayForAttempt(32); got != 3*time.Minute {
		t.Fatalf("retry max cap: want %s, got %s", 3*time.Minute, got)
	}
}

func TestRetryDelayForAttemptWithBounds_UsesRateLimitBounds(t *testing.T) {
	if got := retryDelayForAttemptWithBounds(0, 5*time.Second, 3*time.Minute); got != 5*time.Second {
		t.Fatalf("rate-limit retry 0: want %s, got %s", 5*time.Second, got)
	}
	if got := retryDelayForAttemptWithBounds(1, 5*time.Second, 3*time.Minute); got != 10*time.Second {
		t.Fatalf("rate-limit retry 1: want %s, got %s", 10*time.Second, got)
	}
	if got := retryDelayForAttemptWithBounds(5, 5*time.Second, 3*time.Minute); got != 160*time.Second {
		t.Fatalf("rate-limit retry 5: want %s, got %s", 160*time.Second, got)
	}
	if got := retryDelayForAttemptWithBounds(6, 5*time.Second, 3*time.Minute); got != 3*time.Minute {
		t.Fatalf("rate-limit retry cap: want %s, got %s", 3*time.Minute, got)
	}
}

func TestRetryWarningText_RateLimitIsFriendly(t *testing.T) {
	text := retryWarningText(
		1,
		7,
		5*time.Second,
		errors.New(`model: http status 429 body={"detail":"用户请求TPM超限，请减少tokens后重试","error":{"type":"USER_TPM_RATELIMITING"}}`),
	)
	if !strings.Contains(text, "hit rate limits") {
		t.Fatalf("expected friendly rate-limit warning, got %q", text)
	}
	if !strings.Contains(text, "HTTP 429 / Too Many Requests") {
		t.Fatalf("expected generic 429 context in warning, got %q", text)
	}
	if strings.Contains(text, `body={`) || strings.Contains(text, "USER_TPM_RATELIMITING") || strings.Contains(text, "用户请求TPM超限") {
		t.Fatalf("expected no vendor-specific body dump in warning, got %q", text)
	}
}

func TestLLMAgent_PartialStreamInterruptionWarnsAndSkipsRetry(t *testing.T) {
	attempts := 0
	llm := newSeqLLM("fake", func(req *model.Request) []seqResult {
		_ = req
		attempts++
		return []seqResult{
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "hello"},
					Partial:      true,
					TurnComplete: false,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
			{err: errors.New("unexpected EOF while reading stream")},
		}
	})
	ag, err := New(Config{Name: "test", EmitPartialEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		toolMap: map[string]tool.Tool{},
	}
	var (
		warnings []string
		partials []string
		gotErr   error
	)
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			continue
		}
		if ev == nil {
			continue
		}
		if notice, ok := session.EventNotice(ev); ok {
			warnings = append(warnings, notice.Text)
		}
		if eventIsPartialEvent(ev) {
			partials = append(partials, ev.Message.Text)
		}
	}
	if gotErr == nil {
		t.Fatal("expected interrupted response error")
	}
	if attempts != 1 {
		t.Fatalf("expected no automatic retry after partial output, got %d attempts", attempts)
	}
	if len(partials) != 1 || partials[0] != "hello" {
		t.Fatalf("expected one partial assistant chunk, got %#v", partials)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1], "automatic retry was skipped") {
		t.Fatalf("expected interrupted-response warning, got %#v", warnings)
	}
	if !strings.Contains(warnings[len(warnings)-1], "/continue") {
		t.Fatalf("expected recovery hint in warning, got %#v", warnings)
	}
}

func TestLLMAgent_PartialStreamCancellationStaysSilent(t *testing.T) {
	attempts := 0
	llm := newSeqLLM("fake", func(req *model.Request) []seqResult {
		_ = req
		attempts++
		return []seqResult{
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "hello"},
					Partial:      true,
					TurnComplete: false,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
			{err: context.Canceled},
		}
	})
	ag, err := New(Config{Name: "test", EmitPartialEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		toolMap: map[string]tool.Tool{},
	}
	var (
		warnings []string
		gotErr   error
	)
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			continue
		}
		if notice, ok := session.EventNotice(ev); ok {
			warnings = append(warnings, notice.Text)
		}
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("expected canceled error, got %v", gotErr)
	}
	if attempts != 1 {
		t.Fatalf("expected no retry after cancel, got %d attempts", attempts)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no interruption warning on cancel, got %#v", warnings)
	}
}

func TestLLMAgent_EmitsWhitespaceOnlyPartialChunks(t *testing.T) {
	llm := newSeqLLM("fake", func(req *model.Request) []seqResult {
		_ = req
		return []seqResult{
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "## Heading"},
					Partial:      true,
					TurnComplete: false,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "\n\n"},
					Partial:      true,
					TurnComplete: false,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "- item"},
					Partial:      true,
					TurnComplete: false,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
			{
				resp: &model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "## Heading\n\n- item"},
					TurnComplete: true,
					Model:        "fake",
					Provider:     "test-provider",
				},
			},
		}
	})
	ag, err := New(Config{Name: "test", EmitPartialEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testCtx{
		Context: context.Background(),
		session: &session.Session{AppName: "a", UserID: "u", ID: "s"},
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		toolMap: map[string]tool.Tool{},
	}

	var partials []string
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if ev == nil || !eventIsPartialEvent(ev) {
			continue
		}
		partials = append(partials, ev.Message.Text)
	}

	want := []string{"## Heading", "\n\n", "- item"}
	if len(partials) != len(want) {
		t.Fatalf("expected %d partials, got %#v", len(want), partials)
	}
	for i := range want {
		if partials[i] != want[i] {
			t.Fatalf("unexpected partials: got %#v want %#v", partials, want)
		}
	}
}

func TestLLMAgent_EmptyResponseRetriesWhenNothingWasShown(t *testing.T) {
	oldMaxRetries := modelRequestMaxRetries
	oldBaseDelay := modelRetryBaseDelay
	oldMaxDelay := modelRetryMaxDelay
	modelRequestMaxRetries = 2
	modelRetryBaseDelay = time.Millisecond
	modelRetryMaxDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		modelRequestMaxRetries = oldMaxRetries
		modelRetryBaseDelay = oldBaseDelay
		modelRetryMaxDelay = oldMaxDelay
	})

	attempts := 0
	llm := newSeqLLM("fake", func(req *model.Request) []seqResult {
		_ = req
		attempts++
		if attempts <= 2 {
			return nil
		}
		return []seqResult{{
			resp: &model.Response{
				Message:      model.Message{Role: model.RoleAssistant, Text: "done"},
				TurnComplete: true,
				Model:        "fake",
				Provider:     "test-provider",
			},
		}}
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
		toolMap: map[string]tool.Tool{},
	}
	var warnings []string
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatalf("expected retry to recover empty responses, got %v", runErr)
		}
		if notice, ok := session.EventNotice(ev); ok {
			warnings = append(warnings, notice.Text)
		}
	}
	if attempts != 3 {
		t.Fatalf("expected retries for empty responses, got %d attempts", attempts)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected two retry warnings for empty responses, got %#v", warnings)
	}
}

func TestLLMAgent_InvalidRawToolArgsFailsRun(t *testing.T) {
	// With an empty call ID, arg-parse errors cannot be fed back to the model
	// and must cause a hard error.
	toolCalled := false
	echoTool := namedTool{
		name: "echo",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled = true
			return map[string]any{"ok": true}, nil
		},
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "",
					Name: "echo",
					Args: `not valid json`,
				}},
			}}, nil
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
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo": echoTool},
	}
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			if !strings.Contains(runErr.Error(), "invalid tool call") {
				t.Fatalf("unexpected error: %v", runErr)
			}
			if toolCalled {
				t.Fatal("expected tool not to execute when raw args are invalid")
			}
			return
		}
	}
	t.Fatal("expected invalid raw args error")
}

func TestLLMAgent_InvalidArgsWithIDReturnedAsToolResponse(t *testing.T) {
	// Even non-truncated invalid JSON (e.g. "not valid json") should be fed
	// back to the model as a tool response when the call ID is present.
	toolCalled := false
	echoTool := namedTool{
		name: "echo",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled = true
			return map[string]any{"ok": true}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: `not valid json`,
				}},
			}}, nil
		case model.RoleTool:
			errText := fmt.Sprint(last.ToolResponse.Result["error"])
			if !strings.Contains(errText, `invalid tool call "echo" arguments`) {
				return nil, fmt.Errorf("unexpected error: %q", errText)
			}
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "recovered"}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo": echoTool},
	}
	events := make([]*session.Event, 0, 8)
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatalf("expected no hard error, got %v", runErr)
		}
		events = append(events, ev)
	}
	if toolCalled {
		t.Fatal("expected echo tool not to run when args are invalid")
	}
	foundRecovery := false
	for _, ev := range events {
		if ev != nil && strings.TrimSpace(ev.Message.Text) == "recovered" {
			foundRecovery = true
			break
		}
	}
	if !foundRecovery {
		t.Fatal("expected model to recover from invalid args error")
	}
}

func TestLLMAgent_AnyToolTruncatedArgsReturnedAsToolResponse(t *testing.T) {
	toolCalled := false
	bashTool := namedTool{
		name: "BASH",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled = true
			return map[string]any{"ok": true}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "BASH",
					Args: `{"command":"echo hello`,
				}},
			}}, nil
		case model.RoleTool:
			if last.ToolResponse == nil {
				return nil, fmt.Errorf("expected tool response from agent")
			}
			errText := fmt.Sprint(last.ToolResponse.Result["error"])
			if !strings.Contains(errText, `invalid tool call "BASH" arguments`) {
				return nil, fmt.Errorf("unexpected tool error text: %q", errText)
			}
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "ok, retrying differently"}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{bashTool},
		toolMap: map[string]tool.Tool{"BASH": bashTool},
	}
	events := make([]*session.Event, 0, 8)
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatalf("expected no hard error, got %v", runErr)
		}
		events = append(events, ev)
	}
	if toolCalled {
		t.Fatal("expected BASH tool not to run when args are truncated")
	}
	foundToolResult := false
	for _, ev := range events {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		if ev.Message.ToolResponse.Name != "BASH" {
			continue
		}
		errText := fmt.Sprint(ev.Message.ToolResponse.Result["error"])
		if strings.Contains(errText, `invalid tool call "BASH" arguments`) {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Fatalf("expected BASH parse error returned as tool response, events=%#v", events)
	}
}

func TestLLMAgent_WriteTruncatedArgsReturnedAsToolResponse(t *testing.T) {
	toolCalled := false
	writeTool := namedTool{
		name: "WRITE",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled = true
			return map[string]any{"ok": true}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "WRITE",
					Args: `{"path":"index.html","content":"<html>`,
				}},
			}}, nil
		case model.RoleTool:
			if last.ToolResponse == nil {
				return nil, fmt.Errorf("expected tool response from agent")
			}
			errText := fmt.Sprint(last.ToolResponse.Result["error"])
			if !strings.Contains(errText, `invalid tool call "WRITE" arguments`) {
				return nil, fmt.Errorf("unexpected tool error text: %q", errText)
			}
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "switching to chunked strategy"}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{writeTool},
		toolMap: map[string]tool.Tool{"WRITE": writeTool},
	}
	events := make([]*session.Event, 0, 8)
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatalf("expected no hard error, got %v", runErr)
		}
		events = append(events, ev)
	}
	if toolCalled {
		t.Fatal("expected WRITE tool not to run when args are truncated")
	}
	foundToolResult := false
	for _, ev := range events {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		if ev.Message.ToolResponse.Name != "WRITE" {
			continue
		}
		errText := fmt.Sprint(ev.Message.ToolResponse.Result["error"])
		if strings.Contains(errText, `invalid tool call "WRITE" arguments`) {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Fatalf("expected WRITE parse error returned as tool response, events=%#v", events)
	}
}

func TestLLMAgent_RawToolArgsCompatibilityParsing(t *testing.T) {
	toolCalled := false
	echoTool := namedTool{
		name: "echo",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			toolCalled = true
			if args["text"] != "hello" {
				return nil, fmt.Errorf("unexpected parsed args: %#v", args)
			}
			return map[string]any{"echo": "hello"}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		if step == 1 {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: "```json\n{\"text\":\"hello\"}\n```",
				}},
			}}, nil
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
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo": echoTool},
	}
	var last *session.Event
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		last = ev
	}
	if !toolCalled {
		t.Fatal("expected tool to be called")
	}
	if last == nil || strings.TrimSpace(last.Message.Text) != "done" {
		t.Fatalf("unexpected final event: %#v", last)
	}
}

func TestLLMAgent_DuplicateToolCallFailsWithoutToolResponse(t *testing.T) {
	toolCalled := 0
	echoTool := namedTool{
		name: "echo",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled++
			return map[string]any{"echo": "hello"}, nil
		},
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: jsonArgs(map[string]any{"text": "hello"}),
				}},
			}}, nil
		case model.RoleTool:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: jsonArgs(map[string]any{"text": "hello"}),
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{echoTool},
		toolMap: map[string]tool.Tool{"echo": echoTool},
	}

	events := make([]*session.Event, 0, 8)
	var gotErr error
	for ev, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected duplicate tool call error")
	}
	if !strings.Contains(gotErr.Error(), "duplicate tool call detected") {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	if toolCalled != 2 {
		t.Fatalf("expected tool to run twice before duplicate guard, got %d", toolCalled)
	}
	if len(events) == 0 {
		t.Fatal("expected assistant/tool events before failure")
	}
	last := events[len(events)-1]
	if last.Message.Role != model.RoleTool {
		t.Fatalf("expected last event to be tool response for duplicate, got %#v", last.Message)
	}
	if last.Message.ToolResponse == nil {
		t.Fatal("expected tool response event on duplicate failure")
	}
	errStr, _ := last.Message.ToolResponse.Result["error"].(string)
	if !strings.Contains(errStr, "duplicate") {
		t.Fatalf("expected duplicate error in tool response result, got %q", errStr)
	}
}

func TestLLMAgent_NonConsecutiveSameToolCallIsNotDuplicate(t *testing.T) {
	// BASH→READ→BASH with same args should NOT trigger duplicate detection.
	bashCalled := 0
	readCalled := 0
	bashTool := namedTool{
		name: "BASH",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			bashCalled++
			return map[string]any{"output": "ok"}, nil
		},
	}
	readTool := namedTool{
		name: "READ",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			readCalled++
			return map[string]any{"content": "file"}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		switch step {
		case 1: // initial: call BASH
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "c1", Name: "BASH",
					Args: jsonArgs(map[string]any{"cmd": "ls"}),
				}},
			}}, nil
		case 2: // after BASH result: call READ (different tool)
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "c2", Name: "READ",
					Args: jsonArgs(map[string]any{"path": "/tmp/f"}),
				}},
			}}, nil
		case 3: // after READ result: call BASH again with same args
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "c3", Name: "BASH",
					Args: jsonArgs(map[string]any{"cmd": "ls"}),
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{bashTool, readTool},
		toolMap: map[string]tool.Tool{"BASH": bashTool, "READ": readTool},
	}

	var gotErr error
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no error for non-consecutive same tool calls, got: %v", gotErr)
	}
	if bashCalled != 2 {
		t.Fatalf("expected BASH to be called 2 times, got %d", bashCalled)
	}
	if readCalled != 1 {
		t.Fatalf("expected READ to be called 1 time, got %d", readCalled)
	}
}

func TestLLMAgent_TaskPollingIsNotTreatedAsDuplicate(t *testing.T) {
	taskCalled := 0
	taskTool := namedTool{
		name: "TASK",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			taskCalled++
			return map[string]any{
				"task_id": "t-1",
				"state":   "running",
				"running": true,
			}, nil
		},
	}

	step := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		step++
		switch step {
		case 1, 2, 3:
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   fmt.Sprintf("task-call-%d", step),
					Name: "TASK",
					Args: jsonArgs(map[string]any{"action": "wait", "task_id": "t-1", "yield_time_ms": 5000}),
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "hi"}}},
		llm:     llm,
		tools:   []tool.Tool{taskTool},
		toolMap: map[string]tool.Tool{"TASK": taskTool},
	}

	var gotErr error
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no duplicate error for TASK polling, got: %v", gotErr)
	}
	if taskCalled != 3 {
		t.Fatalf("expected TASK to run 3 times, got %d", taskCalled)
	}
}

func TestLLMAgent_PropagatesPolicyDecisionToToolContext(t *testing.T) {
	toolCalled := false
	hostRouteSeen := false
	approvalEffectSeen := false
	checkTool := namedTool{
		name: "check_ctx",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = args
			toolCalled = true
			decision, ok := policy.ToolDecisionFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("missing policy decision in tool context")
			}
			if decision.Effect == policy.DecisionEffectRequireApproval {
				approvalEffectSeen = true
			}
			if route, ok := policy.DecisionRouteFromMetadata(decision); ok && route == policy.DecisionRouteHost {
				hostRouteSeen = true
			}
			return map[string]any{"ok": true}, nil
		},
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "check_ctx",
					Args: "{}",
				}},
			}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{checkTool},
		toolMap: map[string]tool.Tool{"check_ctx": checkTool},
		policies: []policy.Hook{
			requireApprovalHook{},
		},
	}
	for _, runErr := range ag.Run(ctx) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if !toolCalled {
		t.Fatal("expected tool to be called")
	}
	if !approvalEffectSeen {
		t.Fatal("expected require_approval effect in tool context")
	}
	if !hostRouteSeen {
		t.Fatal("expected host route metadata in tool context decision")
	}
}

func TestLLMAgent_DenyDecisionSkipsToolExecution(t *testing.T) {
	toolCalled := false
	checkTool := namedTool{
		name: "check_ctx",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			toolCalled = true
			return map[string]any{"ok": true}, nil
		},
	}

	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "check_ctx",
					Args: "{}",
				}},
			}}, nil
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
		history: []*session.Event{{Message: model.Message{Role: model.RoleUser, Text: "run"}}},
		llm:     llm,
		tools:   []tool.Tool{checkTool},
		toolMap: map[string]tool.Tool{"check_ctx": checkTool},
		policies: []policy.Hook{
			denyToolHook{},
		},
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
	if toolCalled {
		t.Fatal("expected tool execution to be skipped by deny decision")
	}
	if toolEvent == nil || toolEvent.Message.ToolResponse == nil {
		t.Fatal("expected tool response event")
	}
	if got := fmt.Sprint(toolEvent.Message.ToolResponse.Result["error"]); !strings.Contains(got, "denied by policy") {
		t.Fatalf("expected denial error in tool response, got %q", got)
	}
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
					Args: "{}",
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
					Args: "{}",
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

func TestToMessagesWithSanitizer_UsesCustomSanitizer(t *testing.T) {
	history := []*session.Event{
		{
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:   "call_1",
					Name: "PATCH",
					Result: map[string]any{
						"path":     "a.txt",
						"metadata": map[string]any{"preview": "visible"},
					},
				},
			},
		},
	}
	keepAll := func(input map[string]any) map[string]any {
		return input
	}
	msgs := toMessagesWithSanitizer(history, "sys", keepAll)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if _, exists := msgs[1].ToolResponse.Result["metadata"]; !exists {
		t.Fatalf("expected metadata to be preserved with custom sanitizer")
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
					Args: "{}",
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
					Args: "{}",
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

func TestLLMAgent_AddsErrorCodeToToolResultMetadata(t *testing.T) {
	codedErrTool := namedTool{
		name: "coded_tool",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			return nil, &toolexec.ApprovalRequiredError{Reason: "needs approval"}
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
					Name: "coded_tool",
					Args: "{}",
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
		tools:   []tool.Tool{codedErrTool},
		toolMap: map[string]tool.Tool{"coded_tool": codedErrTool},
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
		t.Fatalf("expected metadata map, got %#v", toolEvent.Message.ToolResponse.Result["metadata"])
	}
	if meta["error_code"] != string(toolexec.ErrorCodeApprovalRequired) {
		t.Fatalf("expected error_code %q, got %#v", toolexec.ErrorCodeApprovalRequired, meta["error_code"])
	}
}

func TestLLMAgent_DoesNotAddErrorCodeForUncodedErrors(t *testing.T) {
	plainErrTool := namedTool{
		name: "plain_tool",
		run: func(ctx context.Context, args map[string]any) (map[string]any, error) {
			_ = ctx
			_ = args
			return nil, errors.New("plain failure")
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
					Name: "plain_tool",
					Args: "{}",
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
		tools:   []tool.Tool{plainErrTool},
		toolMap: map[string]tool.Tool{"plain_tool": plainErrTool},
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
		t.Fatalf("expected metadata map, got %#v", toolEvent.Message.ToolResponse.Result["metadata"])
	}
	if _, exists := meta["error_code"]; exists {
		t.Fatalf("expected no error_code for plain errors, got %#v", meta["error_code"])
	}
}

func TestLLMAgent_RefreshesToolDeclarationsAfterActivation(t *testing.T) {
	dynamicTool := namedTool{name: "LSP_DIAGNOSTICS"}
	activationTool := namedTool{
		name: "ENABLE_EXTRA_TOOLS",
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
					Name: "ENABLE_EXTRA_TOOLS",
					Args: jsonArgs(map[string]any{}),
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

func TestLLMAgent_CompletesMultiTurnToolLoop(t *testing.T) {
	turn := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		turn++
		if turn == 1 {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: jsonArgs(map[string]any{"text": "loop"}),
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
	ag, err := New(Config{Name: "test"})
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

func TestLLMAgent_AllowsUnlimitedToolLoopByDefault(t *testing.T) {
	turn := 0
	llm := newTestLLM("fake", func(req *model.Request) (*model.Response, error) {
		turn++
		if turn == 1 {
			return &model.Response{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: jsonArgs(map[string]any{"text": "ok"}),
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
	ag, err := New(Config{Name: "test"})
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
	ag, err := New(Config{Name: "test"})
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
