package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type geminiLLM struct {
	name                string
	provider            string
	baseURL             string
	token               string
	client              *http.Client
	contextWindowTokens int
}

func newGemini(cfg Config, token string) model.LLM {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &geminiLLM{
		name:                cfg.Model,
		provider:            cfg.Provider,
		baseURL:             strings.TrimRight(cfg.BaseURL, "/"),
		token:               token,
		client:              &http.Client{Timeout: timeout},
		contextWindowTokens: cfg.ContextWindowTokens,
	}
}

func (l *geminiLLM) Name() string {
	return l.name
}

func (l *geminiLLM) ContextWindowTokens() int {
	return l.contextWindowTokens
}

func (l *geminiLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		if req == nil {
			yield(nil, fmt.Errorf("model: request is nil"))
			return
		}

		system, contents := toGeminiContents(req.Messages)
		payload := geminiRequest{
			Contents: contents,
			Tools:    toGeminiTools(req.Tools),
		}
		if strings.TrimSpace(system) != "" {
			payload.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: system}},
			}
		}
		if req.Reasoning.Enabled != nil || req.Reasoning.BudgetTokens > 0 {
			thinking := geminiThinkingConfig{}
			if req.Reasoning.BudgetTokens > 0 {
				thinking.ThinkingBudget = req.Reasoning.BudgetTokens
			} else if req.Reasoning.Enabled != nil && !*req.Reasoning.Enabled {
				thinking.ThinkingBudget = 0
			}
			payload.GenerationConfig = &geminiGenerationConfig{
				ThinkingConfig: &thinking,
			}
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}

		method := "generateContent"
		if req.Stream {
			method = "streamGenerateContent"
		}
		endpoint := fmt.Sprintf("%s/models/%s:%s?key=%s", l.baseURL, l.name, method, url.QueryEscape(l.token))
		if req.Stream {
			endpoint += "&alt=sse"
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

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
			var out geminiResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				yield(nil, err)
				return
			}
			msg, usage, err := geminiResponseToMessage(out)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&model.Response{
				Message:      msg,
				TurnComplete: true,
				Model:        l.name,
				Provider:     l.provider,
				Usage:        usage,
			}, nil)
			return
		}

		acc := geminiAccumulator{
			role:      model.RoleAssistant,
			toolCalls: []model.ToolCall{},
		}
		var usage model.Usage
		if err := readSSE(resp.Body, func(data []byte) error {
			var out geminiResponse
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}
			msg, chunkUsage, err := geminiResponseToMessage(out)
			if err != nil {
				return err
			}
			usage = chunkUsage
			if msg.Role != "" {
				acc.role = msg.Role
			}
			if strings.TrimSpace(msg.Text) != "" {
				acc.text.WriteString(msg.Text)
				if !yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						Text: msg.Text,
					},
					Partial:      true,
					TurnComplete: false,
					Model:        l.name,
					Provider:     l.provider,
				}, nil) {
					return errStopSSE
				}
			}
			if len(msg.ToolCalls) > 0 {
				acc.toolCalls = append(acc.toolCalls, msg.ToolCalls...)
			}
			return nil
		}); err != nil {
			yield(nil, err)
			return
		}

		yield(&model.Response{
			Message: model.Message{
				Role:      acc.role,
				Text:      acc.text.String(),
				ToolCalls: dedupToolCalls(acc.toolCalls),
			},
			Partial:      false,
			TurnComplete: true,
			Model:        l.name,
			Provider:     l.provider,
			Usage:        usage,
		}, nil)
	}
}

type geminiAccumulator struct {
	role      model.Role
	text      strings.Builder
	toolCalls []model.ToolCall
}

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type geminiFunctionCall struct {
	Name             string         `json:"name"`
	Args             map[string]any `json:"args"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

func (c *geminiFunctionCall) UnmarshalJSON(data []byte) error {
	type decode struct {
		Name                  string         `json:"name"`
		Args                  map[string]any `json:"args"`
		ThoughtSignature      string         `json:"thoughtSignature"`
		ThoughtSignatureSnake string         `json:"thought_signature"`
	}
	var raw decode
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Name = raw.Name
	c.Args = raw.Args
	c.ThoughtSignature = strings.TrimSpace(raw.ThoughtSignature)
	if c.ThoughtSignature == "" {
		c.ThoughtSignature = strings.TrimSpace(raw.ThoughtSignatureSnake)
	}
	return nil
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func geminiResponseToMessage(out geminiResponse) (model.Message, model.Usage, error) {
	if len(out.Candidates) == 0 {
		return model.Message{}, model.Usage{}, fmt.Errorf("model: empty candidates")
	}
	msg := model.Message{Role: model.RoleAssistant}
	textParts := make([]string, 0, len(out.Candidates[0].Content.Parts))
	for _, part := range out.Candidates[0].Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			thoughtSig := strings.TrimSpace(part.ThoughtSignature)
			if thoughtSig == "" {
				thoughtSig = strings.TrimSpace(part.FunctionCall.ThoughtSignature)
			}
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:               part.FunctionCall.Name,
				Name:             part.FunctionCall.Name,
				Args:             part.FunctionCall.Args,
				ThoughtSignature: thoughtSig,
			})
		}
	}
	msg.Text = strings.TrimSpace(strings.Join(textParts, "\n"))
	usage := model.Usage{
		PromptTokens:     out.UsageMetadata.PromptTokenCount,
		CompletionTokens: out.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      out.UsageMetadata.TotalTokenCount,
	}
	return msg, usage, nil
}

func toGeminiContents(messages []model.Message) (string, []geminiContent) {
	systemLines := make([]string, 0, 2)
	out := make([]geminiContent, 0, len(messages))

	for _, m := range messages {
		switch m.Role {
		case model.RoleSystem:
			if strings.TrimSpace(m.Text) != "" {
				systemLines = append(systemLines, m.Text)
			}
		case model.RoleUser:
			out = append(out, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					Text: m.Text,
				}},
			})
		case model.RoleAssistant:
			parts := make([]geminiPart, 0, len(m.ToolCalls)+1)
			if strings.TrimSpace(m.Text) != "" {
				parts = append(parts, geminiPart{Text: m.Text})
			}
			for _, call := range m.ToolCalls {
				// Gemini tool loop requires thought signature in functionCall parts.
				// Skip legacy tool calls without signature to avoid request rejection.
				if strings.TrimSpace(call.ThoughtSignature) == "" {
					continue
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: call.Name,
						Args: call.Args,
					},
					ThoughtSignature: call.ThoughtSignature,
				})
			}
			if len(parts) > 0 {
				out = append(out, geminiContent{Role: "model", Parts: parts})
			}
		case model.RoleTool:
			if m.ToolResponse == nil {
				continue
			}
			out = append(out, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     m.ToolResponse.Name,
						Response: m.ToolResponse.Result,
					},
				}},
			})
		}
	}
	return strings.Join(systemLines, "\n\n"), out
}

func toGeminiTools(tools []model.ToolDefinition) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]geminiFunctionDecl, 0, len(tools))
	for _, one := range tools {
		declarations = append(declarations, geminiFunctionDecl{
			Name:        one.Name,
			Description: one.Description,
			Parameters:  one.Parameters,
		})
	}
	return []geminiTool{{FunctionDeclarations: declarations}}
}

func dedupToolCalls(calls []model.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	index := map[string]int{}
	out := make([]model.ToolCall, 0, len(calls))
	for _, call := range calls {
		key := callKey(call)
		if pos, exists := index[key]; exists {
			out[pos] = mergeToolCall(out[pos], call)
			continue
		}
		index[key] = len(out)
		out = append(out, call)
	}
	return out
}

func callKey(call model.ToolCall) string {
	callID := strings.TrimSpace(call.ID)
	if callID != "" {
		return callID + "|" + call.Name
	}
	if len(call.Args) == 0 {
		return call.Name
	}
	raw, err := json.Marshal(call.Args)
	if err != nil {
		return call.Name
	}
	return call.Name + "|" + string(raw)
}

func mergeToolCall(oldCall model.ToolCall, newCall model.ToolCall) model.ToolCall {
	merged := oldCall
	if strings.TrimSpace(merged.ID) == "" {
		merged.ID = newCall.ID
	}
	if strings.TrimSpace(newCall.Name) != "" {
		merged.Name = newCall.Name
	}
	if strings.TrimSpace(merged.ThoughtSignature) == "" && strings.TrimSpace(newCall.ThoughtSignature) != "" {
		merged.ThoughtSignature = newCall.ThoughtSignature
	}
	if len(newCall.Args) > 0 {
		if merged.Args == nil {
			merged.Args = map[string]any{}
		}
		for k, v := range newCall.Args {
			merged.Args[k] = v
		}
	}
	return merged
}
