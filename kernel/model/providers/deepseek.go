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
	}
	payload.Thinking = &openAIThinking{Type: state}
	payload.Reasoning = nil
	payload.ReasoningEffort = ""
}
