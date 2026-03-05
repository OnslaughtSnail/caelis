package providers

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// isOllamaProvider returns true when the provider string identifies Ollama.
func isOllamaProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "ollama")
}

// newOllama returns an OpenAI-compatible LLM client configured for Ollama.
// Ollama exposes an OpenAI-compatible API at /v1, so we ensure the base URL
// includes the /v1 suffix and delegate to the openAICompat implementation.
func newOllama(cfg Config, token string) model.LLM {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	cfg.BaseURL = baseURL
	llm := newOpenAICompat(cfg, token)
	return llm
}
