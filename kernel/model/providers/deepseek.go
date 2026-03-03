package providers

import (
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func newDeepSeek(cfg Config, token string) model.LLM {
	llm := newOpenAICompat(cfg, token)
	llm.options.IncludeReasoningContent = true
	llm.options.EmitEmptyReasoningForToolCall = true
	llm.options.ApplyReasoning = applyThinkingReasoning
	return llm
}

// thinkingModeMinTokens is the minimum max_tokens value required for DeepSeek
// thinking mode. The API defaults to 32K and allows up to 64K; sending a lower
// limit truncates the reasoning chain.
const thinkingModeMinTokens = 32768

func applyThinkingReasoning(payload *openAICompatRequest, cfg model.ReasoningConfig) {
	if payload == nil {
		return
	}
	if cfg.Enabled == nil {
		return
	}
	state := "disabled"
	if *cfg.Enabled {
		state = "enabled"
		// Thinking mode needs a larger token budget. If the current limit is
		// absent or below the API's default (32K), bump it up so the reasoning
		// chain is not prematurely truncated.
		if payload.MaxTokens <= 0 || payload.MaxTokens < thinkingModeMinTokens {
			payload.MaxTokens = thinkingModeMinTokens
		}
	}
	payload.Thinking = &openAIThinking{Type: state}
	payload.Reasoning = nil
	payload.ReasoningEffort = ""
}
