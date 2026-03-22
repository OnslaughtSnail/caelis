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

type openRouterLLM struct {
	name                string
	provider            string
	baseURL             string
	token               string
	headers             map[string]string
	client              *http.Client
	requestTimeout      time.Duration
	maxOutputTok        int
	contextWindowTokens int
	options             openAICompatOptions
	config              OpenRouterConfig
}

type openRouterRequest struct {
	Model           string                     `json:"model,omitempty"`
	Models          []string                   `json:"models,omitempty"`
	Route           string                     `json:"route,omitempty"`
	Messages        []openRouterReqMsg         `json:"messages"`
	Tools           []openAICompatTool         `json:"tools,omitempty"`
	Stream          bool                       `json:"stream"`
	StreamOptions   *openAICompatStreamOptions `json:"stream_options,omitempty"`
	MaxTokens       int                        `json:"max_tokens,omitempty"`
	ReasoningEffort string                     `json:"reasoning_effort,omitempty"`
	Reasoning       *openAIReasoning           `json:"reasoning,omitempty"`
	Transforms      []string                   `json:"transforms,omitempty"`
	Provider        map[string]any             `json:"provider,omitempty"`
	Plugins         []map[string]any           `json:"plugins,omitempty"`
}

type openRouterMsg struct {
	Role             string                 `json:"role"`
	Content          any                    `json:"content,omitempty"`
	Reasoning        string                 `json:"reasoning,omitempty"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCallID       string                 `json:"tool_call_id,omitempty"`
	ToolCalls        []openAICompatToolCall `json:"tool_calls,omitempty"`
}

type openRouterReqMsg struct {
	Role             string                 `json:"role"`
	Content          any                    `json:"content,omitempty"`
	Reasoning        *string                `json:"reasoning,omitempty"`
	ReasoningContent *string                `json:"reasoning_content,omitempty"`
	ToolCallID       string                 `json:"tool_call_id,omitempty"`
	ToolCalls        []openAICompatToolCall `json:"tool_calls,omitempty"`
}

type openRouterResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      openRouterMsg `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openRouterStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta        openRouterMsg `json:"delta"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openRouterStreamAccumulator struct {
	role      model.Role
	text      strings.Builder
	reasoning strings.Builder
	toolCalls map[int]*openAICompatToolCall
}

func newOpenRouter(cfg Config, token string) model.LLM {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &openRouterLLM{
		name:                cfg.Model,
		provider:            cfg.Provider,
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		token:               token,
		headers:             cloneHeaders(cfg.Headers),
		client:              &http.Client{},
		requestTimeout:      timeout,
		maxOutputTok:        cfg.MaxOutputTok,
		contextWindowTokens: cfg.ContextWindowTokens,
		options:             defaultOpenAICompatOptions(),
		config:              cloneOpenRouterConfig(cfg.OpenRouter),
	}
}

func (l *openRouterLLM) Name() string {
	return l.name
}

func (l *openRouterLLM) ProviderName() string {
	return l.provider
}

func (l *openRouterLLM) ContextWindowTokens() int {
	return l.contextWindowTokens
}

func (l *openRouterLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		if req == nil {
			yield(nil, fmt.Errorf("model: request is nil"))
			return
		}
		payload := openRouterRequest{
			Model:      normalizeOpenRouterModelID(l.name),
			Models:     normalizeOpenRouterModelIDs(l.config.Models),
			Route:      strings.TrimSpace(l.config.Route),
			Messages:   l.fromKernelMessages(req.Messages),
			Tools:      fromKernelTools(req.Tools),
			Stream:     req.Stream,
			MaxTokens:  l.maxOutputTok,
			Transforms: cloneStringSlice(l.config.Transforms),
			Provider:   cloneAnyMap(l.config.Provider),
			Plugins:    cloneMapSlice(l.config.Plugins),
		}
		if req.Stream {
			payload.StreamOptions = &openAICompatStreamOptions{IncludeUsage: true}
		}
		if l.options.ApplyReasoning != nil {
			base := openAICompatRequest{
				Model:           payload.Model,
				Messages:        nil,
				Tools:           payload.Tools,
				Stream:          payload.Stream,
				StreamOptions:   payload.StreamOptions,
				MaxTokens:       payload.MaxTokens,
				ReasoningEffort: payload.ReasoningEffort,
				Reasoning:       payload.Reasoning,
			}
			l.options.ApplyReasoning(&base, req.Reasoning)
			payload.MaxTokens = base.MaxTokens
			payload.ReasoningEffort = base.ReasoningEffort
			payload.Reasoning = base.Reasoning
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}

		runCtx := ctx
		cancel := func() {}
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
		applyDefaultAuthHeader(httpReq, Config{API: APIOpenRouter, Provider: l.provider}, l.token, false)
		applyConfiguredHeaders(httpReq, l.headers)

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
			var out openRouterResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				yield(nil, err)
				return
			}
			if len(out.Choices) == 0 {
				yield(nil, fmt.Errorf("model: empty choices"))
				return
			}
			msg, err := openRouterToKernelMessage(out.Choices[0].Message)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&model.Response{
				Message:      msg,
				TurnComplete: true,
				FinishReason: normalizeOpenAICompatFinishReason(out.Choices[0].FinishReason),
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

		acc := openRouterStreamAccumulator{
			role:      model.RoleAssistant,
			toolCalls: map[int]*openAICompatToolCall{},
		}
		var usage model.Usage
		finishReason := model.FinishReasonUnknown
		stopped := false
		if err := readSSE(resp.Body, func(data []byte) error {
			var chunk openRouterStreamChunk
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
			if one := normalizeOpenAICompatFinishReason(chunk.Choices[0].FinishReason); one != model.FinishReasonUnknown {
				finishReason = one
			}
			delta := chunk.Choices[0].Delta
			if strings.TrimSpace(delta.Role) != "" {
				acc.role = model.Role(delta.Role)
			}
			if text, ok := delta.Content.(string); ok && text != "" {
				acc.text.WriteString(text)
				if !yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: text},
					Partial:      true,
					TurnComplete: false,
					Model:        chunk.Model,
					Provider:     l.provider,
				}, nil) {
					stopped = true
					return errStopSSE
				}
			}
			reasoning := firstNonEmpty(delta.Reasoning, delta.ReasoningContent)
			if strings.TrimSpace(reasoning) != "" {
				acc.reasoning.WriteString(reasoning)
				if !yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Reasoning: reasoning},
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
			TurnComplete: true,
			FinishReason: finishReason,
			Model:        l.name,
			Provider:     l.provider,
			Usage:        usage,
		}, nil)
	}
}

func (a *openRouterStreamAccumulator) message() (model.Message, error) {
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

func (l *openRouterLLM) fromKernelMessages(messages []model.Message) []openRouterReqMsg {
	out := make([]openRouterReqMsg, 0, len(messages))
	seenToolCalls := map[string]struct{}{}
	for _, m := range messages {
		if m.Role == model.RoleTool && m.ToolResponse == nil {
			continue
		}
		for _, call := range m.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if callID != "" {
				seenToolCalls[callID] = struct{}{}
			}
		}
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

func (l *openRouterLLM) fromKernelMessage(m model.Message) openRouterReqMsg {
	if m.ToolResponse != nil {
		raw, _ := json.Marshal(m.ToolResponse.Result)
		return openRouterReqMsg{
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
		return openRouterReqMsg{
			Role:      string(m.Role),
			Content:   content,
			Reasoning: l.reasoningField(m.Reasoning, true),
			ToolCalls: calls,
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
		return openRouterReqMsg{
			Role:    string(m.Role),
			Content: parts,
		}
	}
	return openRouterReqMsg{
		Role:      string(m.Role),
		Content:   m.Text,
		Reasoning: l.reasoningField(m.Reasoning, false),
	}
}

func (l *openRouterLLM) reasoningField(reasoning string, hasToolCalls bool) *string {
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

func openRouterToKernelMessage(m openRouterMsg) (model.Message, error) {
	out := model.Message{
		Role:      model.Role(m.Role),
		Reasoning: firstNonEmpty(m.Reasoning, m.ReasoningContent),
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

func cloneOpenRouterConfig(in OpenRouterConfig) OpenRouterConfig {
	return OpenRouterConfig{
		Models:     cloneStringSlice(in.Models),
		Route:      strings.TrimSpace(in.Route),
		Provider:   cloneAnyMap(in.Provider),
		Transforms: cloneStringSlice(in.Transforms),
		Plugins:    cloneMapSlice(in.Plugins),
	}
}

func normalizeOpenRouterModelID(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	const providerPrefix = "openrouter/"
	if strings.HasPrefix(strings.ToLower(value), providerPrefix) {
		remainder := strings.TrimSpace(value[len(providerPrefix):])
		if strings.Contains(remainder, "/") {
			return remainder
		}
	}
	return value
}

func normalizeOpenRouterModelIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, one := range in {
		if normalized := normalizeOpenRouterModelID(one); normalized != "" {
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, one := range in {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		out = append(out, one)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMapSlice(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, one := range in {
		if cloned := cloneAnyMap(one); len(cloned) > 0 {
			out = append(out, cloned)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
