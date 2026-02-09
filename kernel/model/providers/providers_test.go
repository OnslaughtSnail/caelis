package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

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
			Args: map[string]any{"text": "hello"},
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

func TestDeepSeekThinkingPayload(t *testing.T) {
	llm := newDeepSeek(Config{
		Provider: "deepseek",
		Model:    "deepseek-chat",
		BaseURL:  "https://api.deepseek.com/v1",
		Timeout:  time.Second,
	}, "token").(*openAICompatLLM)
	enabled := true
	req := &model.Request{
		Messages: []model.Message{
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "c1",
					Name: "echo",
					Args: map[string]any{"text": "hi"},
				}},
			},
		},
		Reasoning: model.ReasoningConfig{Enabled: &enabled, Effort: "high"},
	}
	payload := openAICompatRequest{
		Model:    "deepseek-chat",
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
}

func TestXiaomiProviderUsesThinkingPayload(t *testing.T) {
	llm := newXiaomi(Config{
		Provider: "xiaomi",
		Model:    "mimo",
		BaseURL:  "https://api.xiaomimimo.com/v1",
		Timeout:  time.Second,
	}, "token").(*openAICompatLLM)
	enabled := true
	payload := openAICompatRequest{
		Model: "mimo",
		Messages: llm.fromKernelMessages([]model.Message{
			{Role: model.RoleUser, Text: "hello"},
		}),
	}
	llm.options.ApplyReasoning(&payload, model.ReasoningConfig{Enabled: &enabled, Effort: "high"})
	if payload.Thinking == nil || payload.Thinking.Type != "enabled" {
		t.Fatalf("expected xiaomi thinking payload, got %#v", payload.Thinking)
	}
	if payload.Reasoning != nil || payload.ReasoningEffort != "" {
		t.Fatalf("did not expect openai reasoning fields for xiaomi payload")
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
				Args: map[string]any{"text": "x"},
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
	system, msgs := toGeminiContents([]model.Message{
		{Role: model.RoleSystem, Text: "sys"},
		{Role: model.RoleUser, Text: "u"},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:               "call1",
				Name:             "echo",
				Args:             map[string]any{"text": "x"},
				ThoughtSignature: "sig-1",
			}},
		},
	})
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
	if parts[0].ThoughtSignature != "sig-1" {
		t.Fatalf("expected thought signature propagated, got %q", parts[0].ThoughtSignature)
	}
}

func TestGeminiMessageTransform_SkipsToolCallWithoutThoughtSignature(t *testing.T) {
	_, msgs := toGeminiContents([]model.Message{
		{
			Role: model.RoleAssistant,
			Text: "tool planned",
			ToolCalls: []model.ToolCall{{
				ID:   "call1",
				Name: "BASH",
				Args: map[string]any{"command": "ls"},
			}},
		},
	})
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
	msg, _, err := geminiResponseToMessage(geminiResponse{
		Candidates: []struct {
			Content geminiContent `json:"content"`
		}{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{
							ThoughtSignature: "sig-call-1",
							FunctionCall: &geminiFunctionCall{
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
	if msg.ToolCalls[0].ThoughtSignature != "sig-call-1" {
		t.Fatalf("expected thought signature kept, got %q", msg.ToolCalls[0].ThoughtSignature)
	}
}

func TestGeminiFunctionCallUnmarshal_SupportsSnakeCaseThoughtSignature(t *testing.T) {
	var call geminiFunctionCall
	if err := json.Unmarshal([]byte(`{"name":"BASH","args":{"command":"ls"},"thought_signature":"sig-snake-1"}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.ThoughtSignature != "sig-snake-1" {
		t.Fatalf("expected snake thought signature, got %q", call.ThoughtSignature)
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
							"thoughtSignature":"sig-part-1"
						}
					]
				}
			}
		]
	}`)
	var out geminiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	msg, _, err := geminiResponseToMessage(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ThoughtSignature != "sig-part-1" {
		t.Fatalf("expected part-level thought signature, got %q", msg.ToolCalls[0].ThoughtSignature)
	}
}

func TestDedupToolCalls_MergesLateThoughtSignature(t *testing.T) {
	calls := dedupToolCalls([]model.ToolCall{
		{
			ID:   "BASH",
			Name: "BASH",
			Args: map[string]any{"command": "ls"},
		},
		{
			ID:               "BASH",
			Name:             "BASH",
			Args:             map[string]any{"command": "ls -la"},
			ThoughtSignature: "sig-late-1",
		},
	})
	if len(calls) != 1 {
		t.Fatalf("expected 1 merged call, got %d", len(calls))
	}
	if calls[0].ThoughtSignature != "sig-late-1" {
		t.Fatalf("expected merged thought signature, got %q", calls[0].ThoughtSignature)
	}
	if calls[0].Args["command"] != "ls -la" {
		t.Fatalf("expected latest args merged, got %v", calls[0].Args["command"])
	}
}
