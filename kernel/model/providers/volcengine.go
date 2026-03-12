package providers

import "github.com/OnslaughtSnail/caelis/kernel/model"

func newVolcengine(cfg Config, token string) model.LLM {
	llm := newOpenAICompat(cfg, token)
	llm.options.IncludeReasoningContent = true
	llm.options.EmitEmptyReasoningForToolCall = true
	llm.options.ApplyReasoning = applyVolcengineThinkingReasoning
	return llm
}
