package providers

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func isXiaomiProvider(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return provider == "xiaomi" || provider == "mimo"
}

func newXiaomi(cfg Config, token string) model.LLM {
	llm := newOpenAICompat(cfg, token)
	llm.options.IncludeReasoningContent = true
	llm.options.EmitEmptyReasoningForToolCall = true
	llm.options.ApplyReasoning = applyThinkingReasoning
	return llm
}
