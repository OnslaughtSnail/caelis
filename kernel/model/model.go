package model

import (
	"context"
	"iter"
	"strings"
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
	// Args preserves provider-originated raw JSON argument text.
	// It is parsed only at execution boundaries.
	Args string
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

// ContentPartType identifies the kind of content in a ContentPart.
type ContentPartType string

const (
	ContentPartText  ContentPartType = "text"
	ContentPartImage ContentPartType = "image"
)

// ContentPart is one segment of a multimodal message.
type ContentPart struct {
	Type     ContentPartType
	Text     string
	MimeType string // e.g. "image/png"
	Data     string // base64-encoded image data
	FileName string // original filename for display
}

// Message is a single turn element in model context.
type Message struct {
	Role         Role
	Text         string
	ContentParts []ContentPart
	Reasoning    string
	ToolCalls    []ToolCall
	ToolResponse *ToolResponse
}

// HasImages returns true if the message contains any image content parts.
func (m Message) HasImages() bool {
	for _, part := range m.ContentParts {
		if part.Type == ContentPartImage {
			return true
		}
	}
	return false
}

// TextContent returns the text content from either ContentParts or the Text
// field, providing backward-compatible text extraction.
func (m Message) TextContent() string {
	if len(m.ContentParts) == 0 {
		return m.Text
	}
	var parts []string
	for _, p := range m.ContentParts {
		if p.Type == ContentPartText && strings.TrimSpace(p.Text) != "" {
			parts = append(parts, p.Text)
		}
	}
	if len(parts) == 0 {
		return m.Text
	}
	return strings.Join(parts, "\n")
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
