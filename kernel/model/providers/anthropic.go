package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type anthropicLLM struct {
	name                string
	provider            string
	baseURL             string
	token               string
	client              *http.Client
	requestTimeout      time.Duration
	maxOutputTok        int
	contextWindowTokens int
}

func newAnthropic(cfg Config, token string) model.LLM {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	maxTok := cfg.MaxOutputTok
	if maxTok <= 0 {
		maxTok = 1024
	}
	return &anthropicLLM{
		name:                cfg.Model,
		provider:            cfg.Provider,
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		token:               token,
		client:              &http.Client{},
		requestTimeout:      timeout,
		maxOutputTok:        maxTok,
		contextWindowTokens: cfg.ContextWindowTokens,
	}
}

func (l *anthropicLLM) Name() string {
	return l.name
}

func (l *anthropicLLM) ContextWindowTokens() int {
	return l.contextWindowTokens
}

func (l *anthropicLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		if req == nil {
			yield(nil, fmt.Errorf("model: request is nil"))
			return
		}
		system, messages := toAnthropicMessages(req.Messages)
		payload := anthropicRequest{
			Model:     l.name,
			System:    system,
			Messages:  messages,
			Tools:     toAnthropicTools(req.Tools),
			MaxTokens: l.maxOutputTok,
			Stream:    false,
		}
		if effort := strings.ToLower(strings.TrimSpace(req.Reasoning.Effort)); effort != "" && effort != "none" {
			budget := req.Reasoning.BudgetTokens
			if budget <= 0 {
				budget = 512
			}
			payload.Thinking = &anthropicThinking{
				Type:         "enabled",
				BudgetTokens: budget,
			}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}
		runCtx := ctx
		cancel := func() {}
		if l.requestTimeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, l.requestTimeout)
		}
		defer cancel()

		httpReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, l.baseURL+"/messages", bytes.NewReader(raw))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", l.token)
		httpReq.Header.Set("anthropic-version", "2023-06-01")

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
		var out anthropicResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			yield(nil, err)
			return
		}

		msg := model.Message{Role: model.RoleAssistant}
		textParts := make([]string, 0, len(out.Content))
		for _, part := range out.Content {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					textParts = append(textParts, part.Text)
				}
			case "tool_use":
				msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
					ID:   part.ID,
					Name: part.Name,
					Args: toolArgsRaw(part.Input),
				})
			}
		}
		msg.Text = strings.TrimSpace(strings.Join(textParts, "\n"))
		yield(&model.Response{
			Message:      msg,
			TurnComplete: true,
			Model:        out.Model,
			Provider:     l.provider,
			Usage: model.Usage{
				PromptTokens:     out.Usage.InputTokens,
				CompletionTokens: out.Usage.OutputTokens,
				TotalTokens:      out.Usage.InputTokens + out.Usage.OutputTokens,
			},
		}, nil)
	}
}

type anthropicRequest struct {
	Model     string              `json:"model"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicToolDecl `json:"tools,omitempty"`
	Thinking  *anthropicThinking  `json:"thinking,omitempty"`
	MaxTokens int                 `json:"max_tokens"`
	Stream    bool                `json:"stream"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicMsgPart `json:"content"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded
}

type anthropicMsgPart struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     map[string]any        `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
}

type anthropicToolDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text,omitempty"`
		ID    string         `json:"id,omitempty"`
		Name  string         `json:"name,omitempty"`
		Input map[string]any `json:"input,omitempty"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func toAnthropicTools(tools []model.ToolDefinition) []anthropicToolDecl {
	out := make([]anthropicToolDecl, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicToolDecl{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return out
}

func toAnthropicMessages(messages []model.Message) (string, []anthropicMessage) {
	systemLines := make([]string, 0, 2)
	out := make([]anthropicMessage, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case model.RoleSystem:
			if strings.TrimSpace(m.Text) != "" {
				systemLines = append(systemLines, m.Text)
			}
		case model.RoleUser:
			var parts []anthropicMsgPart
			if len(m.ContentParts) > 0 {
				for _, cp := range m.ContentParts {
					switch cp.Type {
					case model.ContentPartText:
						parts = append(parts, anthropicMsgPart{Type: "text", Text: cp.Text})
					case model.ContentPartImage:
						parts = append(parts, anthropicMsgPart{
							Type: "image",
							Source: &anthropicImageSource{
								Type:      "base64",
								MediaType: cp.MimeType,
								Data:      cp.Data,
							},
						})
					}
				}
			} else {
				parts = []anthropicMsgPart{{Type: "text", Text: m.Text}}
			}
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: parts,
			})
		case model.RoleAssistant:
			parts := make([]anthropicMsgPart, 0, len(m.ToolCalls)+1)
			if strings.TrimSpace(m.Text) != "" {
				parts = append(parts, anthropicMsgPart{Type: "text", Text: m.Text})
			}
			for _, call := range m.ToolCalls {
				parts = append(parts, anthropicMsgPart{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Name,
					Input: toolArgsMap(call.Args),
				})
			}
			if len(parts) > 0 {
				out = append(out, anthropicMessage{Role: "assistant", Content: parts})
			}
		case model.RoleTool:
			if m.ToolResponse == nil {
				continue
			}
			raw, _ := json.Marshal(m.ToolResponse.Result)
			out = append(out, anthropicMessage{
				Role: "user",
				Content: []anthropicMsgPart{{
					Type:      "tool_result",
					ToolUseID: m.ToolResponse.ID,
					Content:   string(raw),
				}},
			})
		}
	}

	return strings.Join(systemLines, "\n\n"), out
}
