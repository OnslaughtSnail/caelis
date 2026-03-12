package providers

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func newVolcengineCodingPlan(cfg Config, token string) model.LLM {
	llm := newOpenAICompat(cfg, token)
	llm.options.IncludeReasoningContent = true
	llm.options.EmitEmptyReasoningForToolCall = true
	llm.options.ApplyReasoning = applyVolcengineThinkingReasoning
	return llm
}

func applyVolcengineThinkingReasoning(payload *openAICompatRequest, cfg model.ReasoningConfig) {
	if payload == nil {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(cfg.Effort))
	state := "auto"
	switch effort {
	case "none":
		state = "disabled"
	case "":
		state = "auto"
	default:
		state = "enabled"
	}
	payload.Thinking = &openAIThinking{Type: state}
	payload.Reasoning = nil
	payload.ReasoningEffort = ""
}
