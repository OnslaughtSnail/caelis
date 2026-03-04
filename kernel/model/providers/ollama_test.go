package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// ---------------------------------------------------------------------------
// Ollama provider helpers
// ---------------------------------------------------------------------------

func TestIsOllamaProvider(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"ollama", true},
		{"Ollama", true},
		{"OLLAMA", true},
		{" ollama ", true},
		{"openai", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isOllamaProvider(tc.input)
		if got != tc.want {
			t.Errorf("isOllamaProvider(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Factory: register & create Ollama
// ---------------------------------------------------------------------------

func TestOllamaRegisterAndCreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"qwen2.5:7b","choices":[{"message":{"role":"assistant","content":"hello from ollama"}}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`)
	}))
	defer server.Close()

	factory := NewFactory()
	cfg := Config{
		Alias:    "ollama/qwen2.5:7b",
		Provider: "ollama",
		API:      APIOllama,
		Model:    "qwen2.5:7b",
		BaseURL:  server.URL,
		Auth: AuthConfig{
			Type: AuthNone,
		},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register ollama config: %v", err)
	}
	llm, err := factory.NewByAlias("ollama/qwen2.5:7b")
	if err != nil {
		t.Fatalf("create ollama LLM: %v", err)
	}
	if llm.Name() != "qwen2.5:7b" {
		t.Fatalf("unexpected model name: %q", llm.Name())
	}

	// Verify the model can generate a response.
	var gotText string
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   false,
	}) {
		if err != nil {
			t.Fatalf("generate error: %v", err)
		}
		if resp != nil && resp.TurnComplete {
			gotText = resp.Message.Text
		}
	}
	if gotText != "hello from ollama" {
		t.Fatalf("unexpected response text: %q", gotText)
	}
}

func TestOllamaAuthNoneAllowsEmptyToken(t *testing.T) {
	factory := NewFactory()
	cfg := Config{
		Alias:    "ollama/test-model",
		Provider: "ollama",
		API:      APIOllama,
		Model:    "test-model",
		BaseURL:  "http://localhost:11434",
		Auth: AuthConfig{
			Type: AuthNone,
		},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register should succeed with AuthNone: %v", err)
	}
	// NewByAlias should not fail due to empty token.
	llm, err := factory.NewByAlias("ollama/test-model")
	if err != nil {
		t.Fatalf("NewByAlias should not fail with AuthNone and empty token: %v", err)
	}
	if llm == nil {
		t.Fatal("expected non-nil LLM")
	}
}

func TestOllamaBaseURLGetsV1Suffix(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`)
	}))
	defer server.Close()

	llm := newOllama(Config{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  server.URL, // no /v1 suffix
	}, "")

	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
	}) {
		if err != nil {
			t.Fatalf("generate error: %v", err)
		}
		_ = resp
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %q", gotPath)
	}
}

func TestOllamaBaseURLDoesNotDoubleV1(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`)
	}))
	defer server.Close()

	llm := newOllama(Config{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  server.URL + "/v1", // already has /v1 suffix
	}, "")

	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
	}) {
		if err != nil {
			t.Fatalf("generate error: %v", err)
		}
		_ = resp
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions, got %q", gotPath)
	}
}

// ---------------------------------------------------------------------------
// Discovery: Ollama /api/tags
// ---------------------------------------------------------------------------

func TestDiscoverOllamaModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{
			"models": []map[string]any{
				{
					"name":  "qwen2.5:7b",
					"model": "qwen2.5:7b",
					"details": map[string]any{
						"family":         "qwen2",
						"parameter_size": "7.6B",
					},
				},
				{
					"name":  "llama3.1:8b",
					"model": "llama3.1:8b",
					"details": map[string]any{
						"family":         "llama",
						"parameter_size": "8.0B",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), Config{
		API:     APIOllama,
		BaseURL: server.URL,
		Auth:    AuthConfig{Type: AuthNone},
	})
	if err != nil {
		t.Fatalf("discover ollama models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	// Models should be sorted by name.
	if names[0] != "llama3.1:8b" || names[1] != "qwen2.5:7b" {
		t.Fatalf("unexpected model names: %v", names)
	}
}

func TestDiscoverOllamaModelsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[]}`)
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), Config{
		API:     APIOllama,
		BaseURL: server.URL,
		Auth:    AuthConfig{Type: AuthNone},
	})
	if err != nil {
		t.Fatalf("discover should not error on empty list: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected empty model list, got %d", len(models))
	}
}

func TestDiscoverOllamaModelsWithV1BaseURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"qwen2.5:7b"}]}`)
	}))
	defer server.Close()

	models, err := DiscoverModels(context.Background(), Config{
		API:     APIOllama,
		BaseURL: server.URL + "/v1",
		Auth:    AuthConfig{Type: AuthNone},
	})
	if err != nil {
		t.Fatalf("discover with /v1 base url: %v", err)
	}
	if gotPath != "/api/tags" {
		t.Fatalf("expected request path /api/tags, got %q", gotPath)
	}
	if len(models) != 1 || models[0].Name != "qwen2.5:7b" {
		t.Fatalf("unexpected discovered models: %#v", models)
	}
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

func TestOllamaStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"model\":\"qwen2.5:7b\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: {\"model\":\"qwen2.5:7b\",\"choices\":[{\"delta\":{\"content\":\" World\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	llm := newOllama(Config{
		Provider: "ollama",
		Model:    "qwen2.5:7b",
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
	}, "")

	var parts []string
	for resp, err := range llm.Generate(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Text: "hi"}},
		Stream:   true,
	}) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		if resp != nil && resp.Partial {
			parts = append(parts, resp.Message.Text)
		}
	}
	got := strings.Join(parts, "")
	if got != "Hello World" {
		t.Fatalf("unexpected streamed text: %q", got)
	}
}

// ---------------------------------------------------------------------------
// resolveToken with AuthNone
// ---------------------------------------------------------------------------

func TestResolveTokenAuthNone(t *testing.T) {
	token, err := resolveToken(AuthConfig{Type: AuthNone})
	if err != nil {
		t.Fatalf("resolveToken with AuthNone should not error: %v", err)
	}
	if token != "" {
		t.Fatalf("expected empty token, got %q", token)
	}
}

func TestResolveTokenAuthNoneWithOptionalToken(t *testing.T) {
	token, err := resolveToken(AuthConfig{Type: AuthNone, Token: "optional-key"})
	if err != nil {
		t.Fatalf("resolveToken with AuthNone and optional token should not error: %v", err)
	}
	if token != "optional-key" {
		t.Fatalf("expected optional-key, got %q", token)
	}
}
