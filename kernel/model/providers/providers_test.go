package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"google.golang.org/genai"
)

func jsonArgs(v map[string]any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func TestListModelsRequiresRegistration(t *testing.T) {
	factory := NewFactory()
	if got := factory.ListModels(); len(got) != 0 {
		t.Fatalf("expected empty model list, got %v", got)
	}
	if _, err := factory.NewByAlias("deepseek/deepseek-chat"); err == nil {
		t.Fatalf("expected unknown alias error without registration")
	}

	cfg := Config{
		Alias:               "deepseek/deepseek-chat",
		Provider:            "deepseek",
		API:                 APIDeepSeek,
		Model:               "deepseek-chat",
		BaseURL:             "https://api.deepseek.com/v1",
		ContextWindowTokens: 64000,
		Auth: AuthConfig{
			Type:  AuthAPIKey,
			Token: "secret",
		},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register provider config: %v", err)
	}
	list := factory.ListModels()
	if len(list) != 1 || list[0] != cfg.Alias {
		t.Fatalf("unexpected list models: %v", list)
	}
}

func TestFactoryRequiresTokenFromConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-token-should-be-ignored")

	factory := NewFactory()
	cfg := Config{
		Alias:    "openai/gpt-4o-mini",
		Provider: "openai",
		API:      APIOpenAI,
		Model:    "gpt-4o-mini",
		BaseURL:  "https://api.openai.com/v1",
		Auth: AuthConfig{
			Type:     AuthAPIKey,
			TokenEnv: "OPENAI_API_KEY",
		},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register provider config: %v", err)
	}
	_, err := factory.NewByAlias(cfg.Alias)
	if err == nil {
		t.Fatalf("expected missing token error")
	}
	if !strings.Contains(err.Error(), "auth token is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompatStream_PropagatesSSEErrorsWithoutTurnComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {invalid-json}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var (
		gotErr       error
		turnComplete bool
	)
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			gotErr = err
			continue
		}
		if resp != nil && resp.TurnComplete {
			turnComplete = true
		}
	}
	if gotErr == nil {
		t.Fatalf("expected stream error, got nil")
	}
	if turnComplete {
		t.Fatalf("did not expect turn_complete on stream error")
	}
}

func TestOpenAICompatStream_DoesNotApplyRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  50 * time.Millisecond,
	}, "token")

	var (
		gotErr    error
		finalText string
	)
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			gotErr = err
			continue
		}
		if resp != nil && resp.TurnComplete {
			finalText = resp.Message.Text
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no stream error, got %v", gotErr)
	}
	if finalText != "hello world" {
		t.Fatalf("unexpected final text %q", finalText)
	}
}

func TestOpenAICompatStream_IncludesUsageRequestOptionAndPropagatesUsage(t *testing.T) {
	var includeUsage bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		streamOptions, _ := payload["stream_options"].(map[string]any)
		includeUsage, _ = streamOptions["include_usage"].(bool)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var (
		gotErr error
		usage  model.Usage
	)
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			gotErr = err
			continue
		}
		if resp != nil && resp.TurnComplete {
			usage = resp.Usage
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no stream error, got %v", gotErr)
	}
	if !includeUsage {
		t.Fatal("expected stream_options.include_usage=true in request payload")
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestOpenAICompatNonStream_PropagatesFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"truncated"},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var final *model.Response
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
		if resp != nil {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("expected final response")
	}
	if !final.TurnComplete {
		t.Fatal("expected turn complete on terminal non-stream response")
	}
	if final.FinishReason != model.FinishReasonLength {
		t.Fatalf("expected finish reason length, got %q", final.FinishReason)
	}
}

func TestOpenAICompatStream_PropagatesTerminalFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var final *model.Response
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			t.Fatalf("expected no stream error, got %v", err)
		}
		if resp != nil && resp.TurnComplete {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("expected final response")
	}
	if final.Message.Text != "hello world" {
		t.Fatalf("unexpected final text %q", final.Message.Text)
	}
	if final.FinishReason != model.FinishReasonLength {
		t.Fatalf("expected finish reason length, got %q", final.FinishReason)
	}
}

func TestOpenAICompatRequest_IncludesMaxTokens(t *testing.T) {
	var gotMax float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		got, _ := payload["max_tokens"].(float64)
		gotMax = got
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider:     "openai-compatible",
		Model:        "test-model",
		BaseURL:      server.URL,
		Timeout:      2 * time.Second,
		MaxOutputTok: 2048,
	}, "token")

	var gotErr error
	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no generate error, got %v", gotErr)
	}
	if gotMax != 2048 {
		t.Fatalf("expected max_tokens=2048, got %v", gotMax)
	}
}

func TestOpenRouterRequest_AppliesConfiguredHeaders(t *testing.T) {
	var gotReferer string
	var gotTitle string
	var gotModel string
	var gotModels []any
	var gotRoute string
	var gotTransforms []any
	var gotProvider map[string]any
	var gotPlugins []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		gotModel, _ = payload["model"].(string)
		gotModels, _ = payload["models"].([]any)
		gotRoute, _ = payload["route"].(string)
		gotTransforms, _ = payload["transforms"].([]any)
		gotProvider, _ = payload["provider"].(map[string]any)
		gotPlugins, _ = payload["plugins"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"ok","reasoning":"thinking..."}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	llm := newOpenRouter(Config{
		Provider: "openrouter",
		API:      APIOpenRouter,
		Model:    "openrouter/healer-alpha",
		BaseURL:  server.URL,
		Headers: map[string]string{
			"HTTP-Referer": "https://example.com/app",
			"X-Title":      "caelis",
		},
		OpenRouter: OpenRouterConfig{
			Models:     []string{"openrouter/openai/gpt-4o-mini", "openrouter/anthropic/claude-sonnet-4"},
			Route:      "fallback",
			Transforms: []string{"middle-out"},
			Provider: map[string]any{
				"allow_fallbacks": true,
			},
			Plugins: []map[string]any{
				{"id": "web"},
			},
		},
		Timeout: 2 * time.Second,
	}, "token")

	var finalReasoning string
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
		if resp != nil && resp.TurnComplete {
			finalReasoning = resp.Message.Reasoning
		}
	}
	if gotReferer != "https://example.com/app" || gotTitle != "caelis" {
		t.Fatalf("expected configured headers, got referer=%q title=%q", gotReferer, gotTitle)
	}
	if gotModel != "openrouter/healer-alpha" {
		t.Fatalf("expected native openrouter model id preserved, got %q", gotModel)
	}
	if len(gotModels) != 2 {
		t.Fatalf("expected native openrouter models list, got %#v", gotModels)
	}
	if gotModels[0] != "openai/gpt-4o-mini" || gotModels[1] != "anthropic/claude-sonnet-4" {
		t.Fatalf("expected routed model ids normalized for request payload, got %#v", gotModels)
	}
	if gotRoute != "fallback" {
		t.Fatalf("expected native openrouter route, got %q", gotRoute)
	}
	if len(gotTransforms) != 1 || gotTransforms[0] != "middle-out" {
		t.Fatalf("expected native openrouter transforms, got %#v", gotTransforms)
	}
	if value, _ := gotProvider["allow_fallbacks"].(bool); !value {
		t.Fatalf("expected native openrouter provider preferences, got %#v", gotProvider)
	}
	if len(gotPlugins) != 1 {
		t.Fatalf("expected native openrouter plugins, got %#v", gotPlugins)
	}
	if finalReasoning != "thinking..." {
		t.Fatalf("expected native openrouter reasoning field, got %q", finalReasoning)
	}
}

func TestOpenRouterStream_PropagatesTerminalFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"step 1\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\" done\"},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := newOpenRouter(Config{
		Provider: "openrouter",
		API:      APIOpenRouter,
		Model:    "openrouter/test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var final *model.Response
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			t.Fatalf("expected no stream error, got %v", err)
		}
		if resp != nil && resp.TurnComplete {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("expected final response")
	}
	if final.FinishReason != model.FinishReasonToolCalls {
		t.Fatalf("expected tool_calls finish reason, got %q", final.FinishReason)
	}
}

func TestOpenAICompatNonStream_AppliesRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  50 * time.Millisecond,
	}, "token")

	var gotErr error
	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !strings.Contains(strings.ToLower(gotErr.Error()), "context deadline exceeded") {
		t.Fatalf("expected context deadline exceeded, got %v", gotErr)
	}
}

func TestGeminiStream_DoesNotApplyRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/test-model:streamGenerateContent") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"!\"}]}}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":3}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  50 * time.Millisecond,
	}, "token")

	var (
		gotErr    error
		finalText string
	)
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			gotErr = err
			continue
		}
		if resp != nil && resp.TurnComplete {
			finalText = resp.Message.Text
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no stream error, got %v", gotErr)
	}
	if finalText != "hello!" {
		t.Fatalf("unexpected final text %q", finalText)
	}
}

func TestGeminiStream_EmitsReasoningChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/test-model:streamGenerateContent") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"think-1\",\"thought\":true},{\"text\":\"hello\"}]}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"think-2\",\"thought\":true},{\"text\":\"!\"}]}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	var (
		reasoningChunks []string
		finalReasoning  string
		finalText       string
	)
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
		Reasoning: model.ReasoningConfig{
			Effort: "high",
		},
	}) {
		if err != nil {
			t.Fatalf("expected no stream error, got %v", err)
		}
		if resp == nil {
			continue
		}
		if resp.Partial && strings.TrimSpace(resp.Message.Reasoning) != "" {
			reasoningChunks = append(reasoningChunks, strings.TrimSpace(resp.Message.Reasoning))
		}
		if resp.TurnComplete {
			finalReasoning = strings.TrimSpace(resp.Message.Reasoning)
			finalText = strings.TrimSpace(resp.Message.Text)
		}
	}
	if strings.Join(reasoningChunks, "|") != "think-1|think-2" {
		t.Fatalf("unexpected reasoning chunks: %v", reasoningChunks)
	}
	if finalReasoning != "think-1think-2" {
		t.Fatalf("unexpected final reasoning %q", finalReasoning)
	}
	if finalText != "hello!" {
		t.Fatalf("unexpected final text %q", finalText)
	}
}

func TestGeminiRequest_IncludesMaxOutputTokens(t *testing.T) {
	var gotMax float64
	var gotThinkingLevel string
	var gotIncludeThoughts bool
	var gotThinkingBudget any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/test-model:generateContent") {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		if cfg, ok := payload["generationConfig"].(map[string]any); ok {
			gotMax, _ = cfg["maxOutputTokens"].(float64)
			if thinking, ok := cfg["thinkingConfig"].(map[string]any); ok {
				gotThinkingLevel, _ = thinking["thinkingLevel"].(string)
				gotIncludeThoughts, _ = thinking["includeThoughts"].(bool)
				gotThinkingBudget = thinking["thinkingBudget"]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider:     "gemini",
		Model:        "test-model",
		BaseURL:      server.URL,
		Timeout:      2 * time.Second,
		MaxOutputTok: 3072,
	}, "token")

	var gotErr error
	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
		Reasoning: model.ReasoningConfig{
			Effort: "high",
		},
	}) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr != nil {
		t.Fatalf("expected no generate error, got %v", gotErr)
	}
	if gotMax != 3072 {
		t.Fatalf("expected generationConfig.maxOutputTokens=3072, got %v", gotMax)
	}
	if gotThinkingLevel != "HIGH" {
		t.Fatalf("expected generationConfig.thinkingConfig.thinkingLevel=HIGH, got %q", gotThinkingLevel)
	}
	if !gotIncludeThoughts {
		t.Fatalf("expected generationConfig.thinkingConfig.includeThoughts=true")
	}
	if gotThinkingBudget != nil {
		t.Fatalf("expected thinkingBudget omitted, got %v", gotThinkingBudget)
	}
}

func TestGeminiRequest_Pre3UsesThinkingBudget(t *testing.T) {
	var gotThinkingLevel string
	var gotThinkingBudget float64
	var gotIncludeThoughts bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-2.5-flash:generateContent") {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		if cfg, ok := payload["generationConfig"].(map[string]any); ok {
			if thinking, ok := cfg["thinkingConfig"].(map[string]any); ok {
				gotThinkingLevel, _ = thinking["thinkingLevel"].(string)
				gotThinkingBudget, _ = thinking["thinkingBudget"].(float64)
				gotIncludeThoughts, _ = thinking["includeThoughts"].(bool)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "gemini-2.5-flash",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:    false,
		Reasoning: model.ReasoningConfig{Effort: "high"},
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
	}

	if gotThinkingLevel != "" {
		t.Fatalf("expected thinkingLevel omitted for pre-3 model, got %q", gotThinkingLevel)
	}
	if gotThinkingBudget != 8192 {
		t.Fatalf("expected thinkingBudget=8192 for high effort, got %v", gotThinkingBudget)
	}
	if !gotIncludeThoughts {
		t.Fatalf("expected includeThoughts=true for enabled reasoning")
	}
}

func TestGeminiRequest_Pre3DisableReasoningUsesZeroBudget(t *testing.T) {
	var gotThinkingBudget float64
	var gotIncludeThoughts bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		if cfg, ok := payload["generationConfig"].(map[string]any); ok {
			if thinking, ok := cfg["thinkingConfig"].(map[string]any); ok {
				gotThinkingBudget, _ = thinking["thinkingBudget"].(float64)
				gotIncludeThoughts, _ = thinking["includeThoughts"].(bool)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
		Reasoning: model.ReasoningConfig{
			Effort: "none",
		},
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
	}

	if gotThinkingBudget != 0 {
		t.Fatalf("expected thinkingBudget=0 when reasoning disabled, got %v", gotThinkingBudget)
	}
	if gotIncludeThoughts {
		t.Fatalf("expected includeThoughts=false when reasoning disabled")
	}
}

func TestGeminiRequest_BaseURLWithVersionPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "test-model",
		BaseURL:  server.URL + "/v1beta",
		Timeout:  2 * time.Second,
	}, "token")

	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
	}

	if gotPath != "/v1beta/models/test-model:generateContent" {
		t.Fatalf("unexpected request path %q", gotPath)
	}
}

func TestGeminiRequest_XHighEffortFallsBackToHighLevel(t *testing.T) {
	var gotThinkingLevel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		if cfg, ok := payload["generationConfig"].(map[string]any); ok {
			if thinking, ok := cfg["thinkingConfig"].(map[string]any); ok {
				gotThinkingLevel, _ = thinking["thinkingLevel"].(string)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	llm := newGemini(Config{
		Provider: "gemini",
		Model:    "test-model",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "token")

	for _, err := range llm.Generate(context.Background(), &model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:    false,
		Reasoning: model.ReasoningConfig{Effort: "xhigh"},
	}) {
		if err != nil {
			t.Fatalf("expected no generate error, got %v", err)
		}
	}

	if gotThinkingLevel != "HIGH" {
		t.Fatalf("expected xhigh fallback to HIGH, got %q", gotThinkingLevel)
	}
}

func TestFromToOpenAIMessage(t *testing.T) {
	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "gpt-4o-mini",
		BaseURL:  "https://api.openai.com/v1",
		Timeout:  time.Second,
	}, "token")
	in := model.Message{
		Role:      model.RoleAssistant,
		Reasoning: "thinking...",
		ToolCalls: []model.ToolCall{{
			ID:   "c1",
			Name: "echo",
			Args: jsonArgs(map[string]any{"text": "hello"}),
		}},
	}
	raw := llm.fromKernelMessage(in)
	if raw.ReasoningContent != nil {
		t.Fatalf("did not expect reasoning_content in generic openai-compatible request")
	}
	back, err := toKernelMessage(openAICompatMsg{
		Role:       raw.Role,
		Content:    raw.Content,
		ToolCallID: raw.ToolCallID,
		ToolCalls:  raw.ToolCalls,
		ReasoningContent: func() string {
			if raw.ReasoningContent == nil {
				return ""
			}
			return *raw.ReasoningContent
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(back.ToolCalls))
	}
	if back.ToolCalls[0].Name != "echo" {
		t.Fatalf("unexpected tool name %q", back.ToolCalls[0].Name)
	}
	if back.Reasoning != "" {
		t.Fatalf("expected no reasoning in generic openai-compatible roundtrip, got %q", back.Reasoning)
	}
}

func TestToKernelMessage_OpenAICompatKeepsRawToolArgsOnDecodeFailure(t *testing.T) {
	msg, err := toKernelMessage(openAICompatMsg{
		Role: "assistant",
		ToolCalls: []openAICompatToolCall{
			{
				ID:   "c1",
				Type: "function",
				Function: openAICompatCallFunction{
					Name:      "WRITE",
					Arguments: `{"path":`,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected no hard parse error, got %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if got := strings.TrimSpace(msg.ToolCalls[0].Args); got == "" {
		t.Fatalf("expected raw args kept, got %#v", msg.ToolCalls[0])
	}
}

func TestDeepSeekThinkingPayload(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider: "deepseek",
		Model:    "deepseek-reasoner",
		BaseURL:  "https://api.deepseek.com/v1",
		Timeout:  time.Second,
	}, "token").(*openAICompatLLM)
	req := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: jsonArgs(map[string]any{"text": "hi"}),
				}},
			},
		},
		Reasoning: model.ReasoningConfig{Effort: "high"},
	}
	payload := openAICompatRequest{
		Model:    "deepseek-reasoner",
		Messages: llm.fromKernelMessages(req.Messages),
	}
	llm.options.ApplyReasoning(&payload, req.Reasoning)
	if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
		t.Fatalf("expected deepseek thinking config, got %#v", payload.Thinking)
	}
	if payload.Reasoning != nil {
		t.Fatalf("did not expect OpenAI reasoning block for deepseek payload")
	}
	if len(payload.Messages) != 1 || payload.Messages[0].ReasoningContent == nil {
		t.Fatalf("expected reasoning_content field for deepseek tool-call message")
	}
	if got := *payload.Messages[0].ReasoningContent; got != "" {
		t.Fatalf("expected empty reasoning_content for tool-call loop, got %q", got)
	}
	// When thinking is enabled the payload MaxTokens must be at least 32K so
	// the reasoning chain is not prematurely truncated.
	if payload.MaxTokens < thinkingModeMinTokens {
		t.Fatalf("expected MaxTokens >= %d when thinking enabled, got %d",
			thinkingModeMinTokens, payload.MaxTokens)
	}
}

func TestDeepSeekThinkingPayload_SmallMaxTokensBumped(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider:     "deepseek",
		Model:        "deepseek-reasoner",
		BaseURL:      "https://api.deepseek.com/v1",
		Timeout:      time.Second,
		MaxOutputTok: 8192, // smaller than thinking min – must be bumped
	}, "token").(*openAICompatLLM)
	req := &model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Reasoning: model.ReasoningConfig{Effort: "medium"},
	}
	payload := openAICompatRequest{
		Model:     "deepseek-reasoner",
		Messages:  llm.fromKernelMessages(req.Messages),
		MaxTokens: llm.maxOutputTok, // 8192 from config
	}
	llm.options.ApplyReasoning(&payload, req.Reasoning)
	if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
		t.Fatalf("expected thinking enabled")
	}
	if payload.MaxTokens < thinkingModeMinTokens {
		t.Fatalf("expected MaxTokens bumped to >= %d, got %d",
			thinkingModeMinTokens, payload.MaxTokens)
	}
}

func TestDeepSeekThinkingPayload_AdaptiveUsesReasonerRange(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider:     "deepseek",
		Model:        "deepseek-reasoner",
		BaseURL:      "https://api.deepseek.com/v1",
		Timeout:      time.Second,
		MaxOutputTok: 70000,
	}, "token").(*openAICompatLLM)
	req := &model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Reasoning: model.ReasoningConfig{},
	}
	payload := openAICompatRequest{
		Model:     "deepseek-reasoner",
		Messages:  llm.fromKernelMessages(req.Messages),
		MaxTokens: llm.maxOutputTok,
	}
	llm.options.ApplyReasoning(&payload, req.Reasoning)
	if payload.Thinking == nil || payload.Thinking.Type != deepSeekAdaptiveThinkingType {
		t.Fatalf("expected thinking adaptive")
	}
	if payload.MaxTokens != deepSeekReasonerMaxTokens {
		t.Fatalf("expected MaxTokens capped to %d for adaptive thinking, got %d", deepSeekReasonerMaxTokens, payload.MaxTokens)
	}
}

func TestDeepSeekThinkingPayload_DisabledCapsToChatRange(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider:     "deepseek",
		Model:        "deepseek-reasoner",
		BaseURL:      "https://api.deepseek.com/v1",
		Timeout:      time.Second,
		MaxOutputTok: 32768,
	}, "token").(*openAICompatLLM)
	req := &model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Reasoning: model.ReasoningConfig{Effort: "none"},
	}
	payload := openAICompatRequest{
		Model:     "deepseek-reasoner",
		Messages:  llm.fromKernelMessages(req.Messages),
		MaxTokens: llm.maxOutputTok,
	}
	llm.options.ApplyReasoning(&payload, req.Reasoning)
	if payload.Thinking == nil || payload.Thinking.Type != "disabled" {
		t.Fatalf("expected thinking disabled")
	}
	if payload.MaxTokens != deepSeekChatMaxTokens {
		t.Fatalf("expected MaxTokens capped to %d when thinking is disabled, got %d", deepSeekChatMaxTokens, payload.MaxTokens)
	}
}

func TestDeepSeekChatIgnoresReasoningAndCapsTokens(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		BaseURL:      "https://api.deepseek.com/v1",
		Timeout:      time.Second,
		MaxOutputTok: 64000,
	}, "token").(*openAICompatLLM)
	payload := openAICompatRequest{
		Model:     "deepseek-chat",
		Messages:  llm.fromKernelMessages([]model.Message{{Role: model.RoleUser, Text: "hi"}}),
		MaxTokens: llm.maxOutputTok,
	}
	llm.options.ApplyReasoning(&payload, model.ReasoningConfig{Effort: "high"})
	if payload.Thinking != nil {
		t.Fatalf("did not expect thinking payload for deepseek-chat, got %#v", payload.Thinking)
	}
	if payload.MaxTokens != deepSeekChatMaxTokens {
		t.Fatalf("expected MaxTokens capped to %d for deepseek-chat, got %d", deepSeekChatMaxTokens, payload.MaxTokens)
	}
}

func TestMimoProviderUsesThinkingPayload(t *testing.T) {
	llm := newMimo(Config{
		Provider: "xiaomi",
		Model:    "mimo",
		BaseURL:  "https://api.xiaomimimo.com/v1",
		Timeout:  time.Second,
	}, "token").(*openAICompatLLM)
	payload := openAICompatRequest{
		Model: "mimo",
		Messages: llm.fromKernelMessages([]model.Message{
			{Role: model.RoleUser, Text: "hello"},
		}),
	}
	llm.options.ApplyReasoning(&payload, model.ReasoningConfig{Effort: "high"})
	if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
		t.Fatalf("expected mimo thinking payload, got %#v", payload.Thinking)
	}
	if payload.Reasoning != nil || payload.ReasoningEffort != "" {
		t.Fatalf("did not expect openai reasoning fields for mimo payload")
	}
}

func TestVolcengineCodingPlanReasoningDisabledSendsThinkingDisabled(t *testing.T) {
	llm := newVolcengineCodingPlan(Config{
		Provider: "volcengine",
		Model:    "doubao-seed-2.0-pro",
		BaseURL:  "https://ark.cn-beijing.volces.com/api/coding/v3",
		Timeout:  time.Second,
	}, "token").(*openAICompatLLM)
	payload := openAICompatRequest{
		Model: "doubao-seed-2.0-pro",
		Messages: llm.fromKernelMessages([]model.Message{
			{Role: model.RoleUser, Text: "hello"},
		}),
	}
	llm.options.ApplyReasoning(&payload, model.ReasoningConfig{Effort: "none"})
	if payload.Thinking == nil || payload.Thinking.Type != "disabled" {
		t.Fatalf("expected volcengine coding plan payload to disable thinking explicitly, got %#v", payload.Thinking)
	}
	if payload.Reasoning != nil || payload.ReasoningEffort != "" {
		t.Fatalf("did not expect openai reasoning fields for volcengine coding plan payload")
	}
}

func TestOpenAICompatEffortReasoningUsesOpenAIReasoningPayload(t *testing.T) {
	llm := newOpenAICompat(Config{
		Provider:      "openai-compatible",
		Model:         "gpt-5",
		BaseURL:       "https://example.com/v1",
		Timeout:       time.Second,
		ReasoningMode: "effort",
	}, "token")
	payload := openAICompatRequest{
		Model: "gpt-5",
		Messages: llm.fromKernelMessages([]model.Message{
			{Role: model.RoleUser, Text: "hello"},
		}),
	}
	llm.options.ApplyReasoning(&payload, model.ReasoningConfig{Effort: "high"})
	if payload.Reasoning == nil || payload.Reasoning.Effort != "high" {
		t.Fatalf("expected effort openai-compatible payload to carry reasoning effort, got %#v", payload.Reasoning)
	}
	if payload.ReasoningEffort != "high" {
		t.Fatalf("expected compatibility reasoning_effort=high, got %q", payload.ReasoningEffort)
	}
	if payload.Thinking != nil {
		t.Fatalf("did not expect thinking payload for effort openai-compatible request")
	}
}

func TestOpenAICompatMessageTransform_SkipsInvalidToolResponses(t *testing.T) {
	llm := newOpenAICompat(Config{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  "https://example.com/v1",
		Timeout:  time.Second,
	}, "token")
	messages := llm.fromKernelMessages([]model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Name: "echo",
				Args: jsonArgs(map[string]any{"text": "x"}),
			}},
		},
		{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "",
				Name:   "echo",
				Result: map[string]any{"echo": "missing-id"},
			},
		},
		{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "call_2",
				Name:   "echo",
				Result: map[string]any{"echo": "unmatched-id"},
			},
		},
		{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "call_1",
				Name:   "echo",
				Result: map[string]any{"echo": "ok"},
			},
		},
		{
			Role: model.RoleTool,
		},
	})
	if len(messages) != 2 {
		t.Fatalf("expected 2 transformed messages, got %d", len(messages))
	}
	if messages[1].Role != string(model.RoleTool) {
		t.Fatalf("expected tool role at index 1, got %q", messages[1].Role)
	}
	if messages[1].ToolCallID != "call_1" {
		t.Fatalf("expected tool_call_id=call_1, got %q", messages[1].ToolCallID)
	}
}

func TestAnthropicMessageTransform(t *testing.T) {
	system, msgs := toAnthropicMessages([]model.Message{
		{Role: model.RoleSystem, Text: "sys"},
		{Role: model.RoleUser, Text: "u"},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call1",
				Name: "echo",
				Args: jsonArgs(map[string]any{"text": "x"}),
			}},
		},
	})
	if system != "sys" {
		t.Fatalf("unexpected system text: %q", system)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected >= 2 messages, got %d", len(msgs))
	}
}

func TestGeminiMessageTransform(t *testing.T) {
	system, msgs, err := toGeminiContents([]model.Message{
		{Role: model.RoleSystem, Text: "sys"},
		{Role: model.RoleUser, Text: "u"},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:               "call1",
				Name:             "echo",
				Args:             jsonArgs(map[string]any{"text": "x"}),
				ThoughtSignature: "sig-1",
			}},
		},
	})
	if err != nil {
		t.Fatalf("toGeminiContents: %v", err)
	}
	if system != "sys" {
		t.Fatalf("unexpected system text: %q", system)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected >= 2 messages, got %d", len(msgs))
	}
	parts := msgs[len(msgs)-1].Parts
	if len(parts) == 0 || parts[0].FunctionCall == nil {
		t.Fatalf("expected function call part in last gemini message")
	}
	if string(parts[0].ThoughtSignature) != "sig-1" {
		t.Fatalf("expected thought signature propagated, got %q", string(parts[0].ThoughtSignature))
	}
}

func TestGeminiMessageTransform_SkipsToolCallWithoutThoughtSignature(t *testing.T) {
	_, msgs, err := toGeminiContents([]model.Message{
		{
			Role: model.RoleAssistant,
			Text: "tool planned",
			ToolCalls: []model.ToolCall{{
				ID:   "call1",
				Name: "BASH",
				Args: jsonArgs(map[string]any{"command": "ls"}),
			}},
		},
	})
	if err != nil {
		t.Fatalf("toGeminiContents: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Parts) != 1 {
		t.Fatalf("expected only assistant text part, got %d", len(msgs[0].Parts))
	}
	if msgs[0].Parts[0].FunctionCall != nil {
		t.Fatalf("expected tool call without thought signature to be skipped")
	}
}

func TestGeminiResponseToMessage_PreservesThoughtSignature(t *testing.T) {
	msg, _, err := geminiResponseToMessage(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							ThoughtSignature: []byte("sig-call-1"),
							FunctionCall: &genai.FunctionCall{
								Name: "BASH",
								Args: map[string]any{"command": "ls"},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ThoughtSignature == "sig-call-1" {
		t.Fatalf("expected thought signature to be encoded for lossless persistence, got raw %q", msg.ToolCalls[0].ThoughtSignature)
	}
	if got := decodeGeminiThoughtSignature(msg.ToolCalls[0].ThoughtSignature); string(got) != "sig-call-1" {
		t.Fatalf("expected decoded thought signature kept, got %q", string(got))
	}
}

func TestGeminiResponseToMessage_ExtractsReasoningText(t *testing.T) {
	msg, _, err := geminiResponseToMessage(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "thought-1", Thought: true},
						{Text: "answer"},
						{Text: "thought-2", Thought: true},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "answer" {
		t.Fatalf("unexpected answer text %q", msg.Text)
	}
	if msg.Reasoning != "thought-1\nthought-2" {
		t.Fatalf("unexpected reasoning text %q", msg.Reasoning)
	}
}

func TestGeminiResponseToMessage_DoesNotClassifyAnswerTextAsReasoningByThoughtSignature(t *testing.T) {
	msg, _, err := geminiResponseToMessage(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "thought-signature", ThoughtSignature: []byte("sig-thought")},
						{Text: "answer"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "thought-signature\nanswer" {
		t.Fatalf("unexpected answer text %q", msg.Text)
	}
	if msg.Reasoning != "" {
		t.Fatalf("unexpected reasoning text %q", msg.Reasoning)
	}
}

func TestGeminiResponseDecode_PartLevelThoughtSignature(t *testing.T) {
	raw := []byte(`{
		"candidates":[
			{
				"content":{
					"parts":[
						{
							"functionCall":{"name":"BASH","args":{"command":"ls"}},
							"thoughtSignature":"c2lnLXBhcnQtMQ=="
						}
					]
				}
			}
		]
	}`)
	var out genai.GenerateContentResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	msg, _, err := geminiResponseToMessage(&out)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if got := decodeGeminiThoughtSignature(msg.ToolCalls[0].ThoughtSignature); string(got) != "sig-part-1" {
		t.Fatalf("expected part-level thought signature, got %q", string(got))
	}
}

func TestDedupToolCalls_MergesLateThoughtSignature(t *testing.T) {
	calls := dedupToolCalls([]model.ToolCall{
		{
			ID:   "BASH",
			Name: "BASH",
			Args: jsonArgs(map[string]any{"command": "ls"}),
		},
		{
			ID:               "BASH",
			Name:             "BASH",
			Args:             jsonArgs(map[string]any{"command": "ls -la"}),
			ThoughtSignature: "sig-late-1",
		},
	})
	if len(calls) != 1 {
		t.Fatalf("expected 1 merged call, got %d", len(calls))
	}
	if calls[0].ThoughtSignature != "sig-late-1" {
		t.Fatalf("expected merged thought signature, got %q", calls[0].ThoughtSignature)
	}
	if strings.TrimSpace(calls[0].Args) != `{"command":"ls -la"}` {
		t.Fatalf("expected latest args merged, got %v", calls[0].Args)
	}
}

func TestGeminiThoughtSignature_BinaryRoundTrip(t *testing.T) {
	raw := []byte{0x00, 0x01, 0x02, 0xff, 0x20, 0x09}
	encoded := encodeGeminiThoughtSignature(raw)
	if encoded == "" || encoded == string(raw) {
		t.Fatalf("expected non-empty encoded signature, got %q", encoded)
	}
	decoded := decodeGeminiThoughtSignature(encoded)
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("expected decoded signature to match raw bytes")
	}
	legacy := decodeGeminiThoughtSignature("sig-legacy-1")
	if string(legacy) != "sig-legacy-1" {
		t.Fatalf("expected legacy signature compatibility, got %q", string(legacy))
	}
}
