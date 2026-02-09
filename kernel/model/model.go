package model

import (
	"context"
	"iter"
)

// Role identifies message author type.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolDefinition describes a callable tool for model planning.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is a model-emitted tool invocation request.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
	// ThoughtSignature carries provider-specific chain-of-thought signature
	// required by some providers (for example Gemini) to validate tool loops.
	ThoughtSignature string
}

// ToolResponse is a tool execution result returned to model context.
type ToolResponse struct {
	ID     string
	Name   string
	Result map[string]any
}

// Message is a single turn element in model context.
type Message struct {
	Role         Role
	Text         string
	Reasoning    string
	ToolCalls    []ToolCall
	ToolResponse *ToolResponse
}

// ReasoningConfig controls provider reasoning/thinking behavior.
type ReasoningConfig struct {
	// Enabled toggles reasoning mode when supported by provider.
	Enabled *bool
	// BudgetTokens limits provider thinking tokens when supported.
	BudgetTokens int
	// Effort is provider-specific reasoning effort hint, e.g. low|medium|high.
	Effort string
}

// Request is a provider-agnostic model request.
type Request struct {
	Messages  []Message
	Tools     []ToolDefinition
	Stream    bool
	Reasoning ReasoningConfig
}

// Usage reports model token usage (best-effort).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is a provider-agnostic model response chunk.
type Response struct {
	Message      Message
	Partial      bool
	TurnComplete bool
	Usage        Usage
	Model        string
	Provider     string
}

// LLM is the model abstraction used by the kernel.
type LLM interface {
	Name() string
	Generate(context.Context, *Request) iter.Seq2[*Response, error]
}
