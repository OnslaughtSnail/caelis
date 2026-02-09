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
	return &openAICompatLLM{
		name:                cfg.Model,
		provider:            cfg.Provider,
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		token:               token,
		client:              &http.Client{Timeout: timeout},
		contextWindowTokens: cfg.ContextWindowTokens,
		options:             defaultOpenAICompatOptions(),
	}
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
			Model:    l.name,
			Messages: l.fromKernelMessages(req.Messages),
			Tools:    fromKernelTools(req.Tools),
			Stream:   req.Stream,
		}
		if l.options.ApplyReasoning != nil {
			l.options.ApplyReasoning(&payload, req.Reasoning)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", bytes.NewReader(raw))
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
	Model           string               `json:"model"`
	Messages        []openAICompatReqMsg `json:"messages"`
	Tools           []openAICompatTool   `json:"tools,omitempty"`
	Stream          bool                 `json:"stream"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Reasoning       *openAIReasoning     `json:"reasoning,omitempty"`
	Thinking        *openAIThinking      `json:"thinking,omitempty"`
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
		args := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return model.Message{}, err
			}
		}
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}
	return msg, nil
}

func (l *openAICompatLLM) fromKernelMessages(messages []model.Message) []openAICompatReqMsg {
	out := make([]openAICompatReqMsg, 0, len(messages))
	for _, m := range messages {
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
			raw, _ := json.Marshal(c.Args)
			calls = append(calls, openAICompatToolCall{
				ID:   c.ID,
				Type: "function",
				Function: openAICompatCallFunction{
					Name:      c.Name,
					Arguments: string(raw),
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
		args := map[string]any{}
		if strings.TrimSpace(c.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(c.Function.Arguments), &args); err != nil {
				return model.Message{}, err
			}
		}
		out.ToolCalls = append(out.ToolCalls, model.ToolCall{
			ID:   c.ID,
			Name: c.Function.Name,
			Args: args,
		})
	}
	return out, nil
}
