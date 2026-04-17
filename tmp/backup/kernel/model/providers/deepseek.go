package providers

import (
	"strings"

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

const (
	deepSeekChatDefaultMaxTokens = 4096
	deepSeekChatMaxTokens        = 8192
	deepSeekReasonerMaxTokens    = 65536
	deepSeekAdaptiveThinkingType = "adaptive"
	deepSeekReasonerModel        = "deepseek-reasoner"
)

func applyThinkingReasoning(payload *openAICompatRequest, cfg model.ReasoningConfig) {
	if payload == nil {
		return
	}
	if !deepSeekModelSupportsThinking(payload.Model) {
		clearDeepSeekReasoningFields(payload)
		payload.MaxTokens = clampDeepSeekChatMaxTokens(payload.MaxTokens)
		return
	}
	effort := strings.ToLower(strings.TrimSpace(cfg.Effort))
	switch effort {
	case "":
		payload.Thinking = &openAIThinking{Type: deepSeekAdaptiveThinkingType}
		clearDeepSeekReasoningFields(payload)
		payload.MaxTokens = clampDeepSeekReasonerMaxTokens(payload.MaxTokens)
	case "none":
		applyToggleThinkingReasoning(payload, cfg)
		payload.MaxTokens = clampDeepSeekChatMaxTokens(payload.MaxTokens)
	default:
		applyToggleThinkingReasoning(payload, cfg)
		// Thinking mode needs a larger token budget. If the current limit is
		// absent or below the API's default (32K), bump it up so the reasoning
		// chain is not prematurely truncated.
		payload.MaxTokens = clampDeepSeekReasonerMaxTokens(payload.MaxTokens)
	}
}

func clearDeepSeekReasoningFields(payload *openAICompatRequest) {
	if payload == nil {
		return
	}
	payload.Reasoning = nil
	payload.ReasoningEffort = ""
}

func deepSeekModelSupportsThinking(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), deepSeekReasonerModel)
}

func clampDeepSeekChatMaxTokens(current int) int {
	switch {
	case current <= 0:
		return deepSeekChatDefaultMaxTokens
	case current > deepSeekChatMaxTokens:
		return deepSeekChatMaxTokens
	default:
		return current
	}
}

func clampDeepSeekReasonerMaxTokens(current int) int {
	switch {
	case current <= 0:
		return thinkingModeMinTokens
	case current < thinkingModeMinTokens:
		return thinkingModeMinTokens
	case current > deepSeekReasonerMaxTokens:
		return deepSeekReasonerMaxTokens
	default:
		return current
	}
}
