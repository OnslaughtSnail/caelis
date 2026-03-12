package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type openAICompatLLM struct {
	name                string
	provider            string
	baseURL             string
	token               string
	client              *http.Client
	requestTimeout      time.Duration
	maxOutputTok        int
	contextWindowTokens int
	options             openAICompatOptions
}

type openAICompatOptions struct {
	IncludeReasoningContent       bool
	EmitEmptyReasoningForToolCall bool
	ApplyReasoning                func(*openAICompatRequest, model.ReasoningConfig)
}

func defaultOpenAICompatOptions() openAICompatOptions {
	return openAICompatOptions{
		ApplyReasoning: applyOpenAIReasoning,
	}
}

func newOpenAICompat(cfg Config, token string) *openAICompatLLM {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	llm := &openAICompatLLM{
		name:                cfg.Model,
		provider:            cfg.Provider,
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		token:               token,
		client:              &http.Client{},
		requestTimeout:      timeout,
		maxOutputTok:        cfg.MaxOutputTok,
		contextWindowTokens: cfg.ContextWindowTokens,
		options:             defaultOpenAICompatOptions(),
	}
	return llm
}

func (l *openAICompatLLM) Name() string {
	return l.name
}

func (l *openAICompatLLM) ContextWindowTokens() int {
	return l.contextWindowTokens
}

func (l *openAICompatLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		if req == nil {
			yield(nil, fmt.Errorf("model: request is nil"))
			return
		}
		payload := openAICompatRequest{
			Model:     l.name,
			Messages:  l.fromKernelMessages(req.Messages),
			Tools:     fromKernelTools(req.Tools),
			Stream:    req.Stream,
			MaxTokens: l.maxOutputTok,
		}
		if req.Stream {
			payload.StreamOptions = &openAICompatStreamOptions{IncludeUsage: true}
		}
		if l.options.ApplyReasoning != nil {
			l.options.ApplyReasoning(&payload, req.Reasoning)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}

		runCtx := ctx
		cancel := func() {}
		// For streaming SSE, rely on caller context cancellation to avoid hard timeout
		// cutting off long-running responses.
		if !req.Stream && l.requestTimeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, l.requestTimeout)
		}
		defer cancel()

		httpReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+l.token)

		resp, err := l.client.Do(httpReq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			yield(nil, statusError(resp))
			return
		}

		if !req.Stream {
			var out openAICompatResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				yield(nil, err)
				return
			}
			if len(out.Choices) == 0 {
				yield(nil, fmt.Errorf("model: empty choices"))
				return
			}
			msg, err := toKernelMessage(out.Choices[0].Message)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&model.Response{
				Message:      msg,
				TurnComplete: true,
				Model:        out.Model,
				Provider:     l.provider,
				Usage: model.Usage{
					PromptTokens:     out.Usage.PromptTokens,
					CompletionTokens: out.Usage.CompletionTokens,
					TotalTokens:      out.Usage.TotalTokens,
				},
			}, nil)
			return
		}

		acc := openAIStreamAccumulator{
			role:      model.RoleAssistant,
			toolCalls: map[int]*openAICompatToolCall{},
		}
		var usage model.Usage
		stopped := false
		if err := readSSE(resp.Body, func(data []byte) error {
			var chunk openAICompatStreamChunk
			if err := json.Unmarshal(data, &chunk); err != nil {
				return err
			}
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 || chunk.Usage.TotalTokens > 0 {
				usage = model.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}
			if len(chunk.Choices) == 0 {
				return nil
			}
			delta := chunk.Choices[0].Delta
			if strings.TrimSpace(delta.Role) != "" {
				acc.role = model.Role(delta.Role)
			}
			if text, ok := delta.Content.(string); ok && text != "" {
				acc.text.WriteString(text)
				if !yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						Text: text,
					},
					Partial:      true,
					TurnComplete: false,
					Model:        chunk.Model,
					Provider:     l.provider,
				}, nil) {
					stopped = true
					return errStopSSE
				}
			}
			if strings.TrimSpace(delta.ReasoningContent) != "" {
				acc.reasoning.WriteString(delta.ReasoningContent)
				if !yield(&model.Response{
					Message: model.Message{
						Role:      model.RoleAssistant,
						Reasoning: delta.ReasoningContent,
					},
					Partial:      true,
					TurnComplete: false,
					Model:        chunk.Model,
					Provider:     l.provider,
				}, nil) {
					stopped = true
					return errStopSSE
				}
			}
			for _, tc := range delta.ToolCalls {
				entry := acc.toolCalls[tc.Index]
				if entry == nil {
					entry = &openAICompatToolCall{}
					acc.toolCalls[tc.Index] = entry
				}
				if tc.ID != "" {
					entry.ID = tc.ID
				}
				if tc.Function.Name != "" {
					entry.Function.Name = tc.Function.Name
				}
				entry.Function.Arguments += tc.Function.Arguments
			}
			return nil
		}); err != nil {
			yield(nil, err)
			return
		}
		if stopped {
			return
		}
		finalMsg, err := acc.message()
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&model.Response{
			Message:      finalMsg,
			Partial:      false,
			TurnComplete: true,
			Model:        l.name,
			Provider:     l.provider,
			Usage:        usage,
		}, nil)
	}
}

type openAICompatRequest struct {
	Model           string                     `json:"model"`
	Messages        []openAICompatReqMsg       `json:"messages"`
	Tools           []openAICompatTool         `json:"tools,omitempty"`
	Stream          bool                       `json:"stream"`
	StreamOptions   *openAICompatStreamOptions `json:"stream_options,omitempty"`
	MaxTokens       int                        `json:"max_tokens,omitempty"`
	ReasoningEffort string                     `json:"reasoning_effort,omitempty"`
	Reasoning       *openAIReasoning           `json:"reasoning,omitempty"`
	Thinking        *openAIThinking            `json:"thinking,omitempty"`
}

type openAICompatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAICompatMsg struct {
	Role             string                 `json:"role"`
	Content          any                    `json:"content,omitempty"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCallID       string                 `json:"tool_call_id,omitempty"`
	ToolCalls        []openAICompatToolCall `json:"tool_calls,omitempty"`
}

type openAICompatReqMsg struct {
	Role             string                 `json:"role"`
	Content          any                    `json:"content,omitempty"`
	ReasoningContent *string                `json:"reasoning_content,omitempty"`
	ToolCallID       string                 `json:"tool_call_id,omitempty"`
	ToolCalls        []openAICompatToolCall `json:"tool_calls,omitempty"`
}

type openAIReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type openAIThinking struct {
	Type string `json:"type"`
}

type openAICompatTool struct {
	Type     string                   `json:"type"`
	Function openAICompatFunctionDecl `json:"function"`
}

type openAICompatFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAICompatToolCall struct {
	ID       string                   `json:"id"`
	Index    int                      `json:"index,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function openAICompatCallFunction `json:"function"`
}

type openAICompatCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAICompatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message openAICompatMsg `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAICompatStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta openAICompatMsg `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIStreamAccumulator struct {
	role      model.Role
	text      strings.Builder
	reasoning strings.Builder
	toolCalls map[int]*openAICompatToolCall
}

func (a *openAIStreamAccumulator) message() (model.Message, error) {
	msg := model.Message{
		Role:      a.role,
		Text:      a.text.String(),
		Reasoning: a.reasoning.String(),
	}
	if len(a.toolCalls) == 0 {
		return msg, nil
	}
	keys := make([]int, 0, len(a.toolCalls))
	for idx := range a.toolCalls {
		keys = append(keys, idx)
	}
	sort.Ints(keys)
	for _, idx := range keys {
		tc := a.toolCalls[idx]
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	return msg, nil
}

func (l *openAICompatLLM) fromKernelMessages(messages []model.Message) []openAICompatReqMsg {
	out := make([]openAICompatReqMsg, 0, len(messages))
	seenToolCalls := map[string]struct{}{}
	for _, m := range messages {
		// OpenAI-compatible APIs reject role=tool messages that do not carry
		// a tool_call_id. Skip malformed history entries.
		if m.Role == model.RoleTool && m.ToolResponse == nil {
			continue
		}
		for _, call := range m.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if callID != "" {
				seenToolCalls[callID] = struct{}{}
			}
		}
		// OpenAI-compatible APIs require tool messages to carry a non-empty
		// tool_call_id that references a preceding assistant tool call.
		// Resume/legacy histories may contain incomplete tool responses; skip
		// these invalid entries to avoid hard request failures.
		if m.ToolResponse != nil {
			respID := strings.TrimSpace(m.ToolResponse.ID)
			if respID == "" {
				continue
			}
			if _, ok := seenToolCalls[respID]; !ok {
				continue
			}
		}
		out = append(out, l.fromKernelMessage(m))
	}
	return out
}

func fromKernelTools(tools []model.ToolDefinition) []openAICompatTool {
	out := make([]openAICompatTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openAICompatTool{
			Type: "function",
			Function: openAICompatFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

func (l *openAICompatLLM) fromKernelMessage(m model.Message) openAICompatReqMsg {
	if m.ToolResponse != nil {
		raw, _ := json.Marshal(m.ToolResponse.Result)
		return openAICompatReqMsg{
			Role:       string(model.RoleTool),
			ToolCallID: m.ToolResponse.ID,
			Content:    string(raw),
		}
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]openAICompatToolCall, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			raw := strings.TrimSpace(c.Args)
			if raw == "" {
				raw = "{}"
			}
			calls = append(calls, openAICompatToolCall{
				ID:   c.ID,
				Type: "function",
				Function: openAICompatCallFunction{
					Name:      c.Name,
					Arguments: raw,
				},
			})
		}
		content := any(nil)
		if m.Text != "" {
			content = m.Text
		}
		return openAICompatReqMsg{
			Role:             string(m.Role),
			Content:          content,
			ReasoningContent: l.reasoningContentField(m.Reasoning, true),
			ToolCalls:        calls,
		}
	}
	if m.Role == model.RoleUser && len(m.ContentParts) > 0 {
		parts := make([]openAIContentPart, 0, len(m.ContentParts))
		for _, cp := range m.ContentParts {
			switch cp.Type {
			case model.ContentPartText:
				parts = append(parts, openAIContentPart{Type: "text", Text: cp.Text})
			case model.ContentPartImage:
				parts = append(parts, openAIContentPart{
					Type:     "image_url",
					ImageURL: &openAIImageURL{URL: fmt.Sprintf("data:%s;base64,%s", cp.MimeType, cp.Data)},
				})
			}
		}
		return openAICompatReqMsg{
			Role:    string(m.Role),
			Content: parts,
		}
	}
	return openAICompatReqMsg{
		Role:             string(m.Role),
		Content:          m.Text,
		ReasoningContent: l.reasoningContentField(m.Reasoning, false),
	}
}

func (l *openAICompatLLM) reasoningContentField(reasoning string, hasToolCalls bool) *string {
	if l == nil || !l.options.IncludeReasoningContent {
		return nil
	}
	if strings.TrimSpace(reasoning) != "" {
		return &reasoning
	}
	if hasToolCalls && l.options.EmitEmptyReasoningForToolCall {
		empty := ""
		return &empty
	}
	return nil
}

func applyToggleThinkingReasoning(payload *openAICompatRequest, cfg model.ReasoningConfig) {
	if payload == nil {
		return
	}
	effort := strings.ToLower(strings.TrimSpace(cfg.Effort))
	if effort == "" {
		return
	}
	state := "enabled"
	if effort == "none" {
		state = "disabled"
	}
	payload.Thinking = &openAIThinking{Type: state}
	payload.Reasoning = nil
	payload.ReasoningEffort = ""
}

func applyOpenAIReasoning(payload *openAICompatRequest, cfg model.ReasoningConfig) {
	if payload == nil {
		return
	}
	effort := strings.TrimSpace(cfg.Effort)
	if effort == "" {
		return
	}
	payload.Reasoning = &openAIReasoning{Effort: effort}
	// Keep this for compatibility with some OpenAI-compatible gateways.
	payload.ReasoningEffort = effort
}

func toKernelMessage(m openAICompatMsg) (model.Message, error) {
	out := model.Message{
		Role:      model.Role(m.Role),
		Reasoning: m.ReasoningContent,
	}
	if text, ok := m.Content.(string); ok {
		out.Text = text
	}
	if len(m.ToolCalls) == 0 {
		return out, nil
	}
	for _, c := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, model.ToolCall{
			ID:   c.ID,
			Name: c.Function.Name,
			Args: c.Function.Arguments,
		})
	}
	return out, nil
}
