package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"strings"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

// Factory constructs baseline chat agents from one runtime.AgentSpec.
type Factory struct {
	SystemPrompt string
}

// Agent is the minimal model-backed chat agent.
type Agent struct {
	name         string
	model        sdkmodel.LLM
	tools        []sdktool.Tool
	systemPrompt string
	reasoning    sdkmodel.ReasoningConfig
}

// New returns one concrete chat agent.
func New(name string, model sdkmodel.LLM, systemPrompt string) (*Agent, error) {
	if model == nil {
		return nil, errors.New("sdk/runtime/agents/chat: model is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "chat"
	}
	return &Agent{
		name:         name,
		model:        model,
		systemPrompt: strings.TrimSpace(systemPrompt),
	}, nil
}

// NewWithTools returns one chat agent with builtin tool access.
func NewWithTools(name string, model sdkmodel.LLM, tools []sdktool.Tool, systemPrompt string) (*Agent, error) {
	agent, err := New(name, model, systemPrompt)
	if err != nil {
		return nil, err
	}
	agent.tools = append([]sdktool.Tool(nil), tools...)
	return agent, nil
}

// NewAgent constructs one chat agent from one runtime.AgentSpec.
func (f Factory) NewAgent(_ context.Context, spec sdkruntime.AgentSpec) (sdkruntime.Agent, error) {
	systemPrompt := ""
	if raw, ok := spec.Metadata["system_prompt"].(string); ok {
		systemPrompt = strings.TrimSpace(raw)
	}
	if systemPrompt == "" {
		systemPrompt = strings.TrimSpace(f.SystemPrompt)
	}
	agent, err := NewWithTools(spec.Name, spec.Model, spec.Tools, systemPrompt)
	if err != nil {
		return nil, err
	}
	agent.reasoning = reasoningFromMetadata(spec.Metadata)
	return agent, nil
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Run(ctx sdkruntime.Context) iter.Seq2[*sdksession.Event, error] {
	return func(yield func(*sdksession.Event, error) bool) {
		messages := messagesFromContext(ctx)
		for step := 0; step < 8; step++ {
			request := &sdkmodel.Request{
				Messages:  messages,
				Tools:     sdktool.ModelSpecs(a.tools),
				Reasoning: a.reasoning,
				Stream:    false,
			}
			request.Instructions = append(request.Instructions, instructionsFromContext(ctx, a.systemPrompt)...)

			final, err := collectFinalResponse(ctx, a.model, request)
			if err != nil {
				yield(nil, err)
				return
			}

			assistantMessage := sdkmodel.CloneMessage(final.Message)
			calls := assistantMessage.ToolCalls()
			if len(calls) == 0 {
				assistantEvent := modelResponseEvent(assistantMessage, final)
				if !yield(assistantEvent, nil) {
					return
				}
				messages = append(messages, assistantMessage)
				return
			}
			toolCallEvents := modelToolCallEvents(assistantMessage, final)
			for _, event := range toolCallEvents {
				if !yield(event, nil) {
					return
				}
			}
			messages = append(messages, assistantMessage)
			for _, call := range calls {
				toolMessage, toolEvent, err := a.executeToolCall(ctx, call)
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(toolEvent, nil) {
					return
				}
				messages = append(messages, toolMessage)
			}
		}
		yield(nil, errors.New("sdk/runtime/agents/chat: tool loop exceeded max steps"))
	}
}

func reasoningFromMetadata(meta map[string]any) sdkmodel.ReasoningConfig {
	var reasoning sdkmodel.ReasoningConfig
	if raw, ok := meta["reasoning_effort"].(string); ok {
		reasoning.Effort = strings.TrimSpace(raw)
	}
	switch raw := meta["reasoning_budget_tokens"].(type) {
	case int:
		reasoning.BudgetTokens = raw
	case int64:
		reasoning.BudgetTokens = int(raw)
	case float64:
		reasoning.BudgetTokens = int(raw)
	}
	return reasoning
}

func collectFinalResponse(ctx context.Context, model sdkmodel.LLM, req *sdkmodel.Request) (*sdkmodel.Response, error) {
	var final *sdkmodel.Response
	for event, err := range model.Generate(ctx, req) {
		if err != nil {
			return nil, err
		}
		if event != nil && event.Response != nil && event.TurnComplete {
			final = event.Response
		}
	}
	if final == nil {
		return nil, errors.New("sdk/runtime/agents/chat: model returned no final response")
	}
	return final, nil
}

func modelResponseEvent(message sdkmodel.Message, resp *sdkmodel.Response) *sdksession.Event {
	out := &sdksession.Event{
		Type:    sdksession.EventTypeOf(&sdksession.Event{Message: &message}),
		Message: &message,
		Text:    message.TextContent(),
		Protocol: &sdksession.EventProtocol{
			UpdateType: string(sdksession.ProtocolUpdateTypeAgentMessage),
		},
	}
	if resp != nil {
		out.Meta = map[string]any{
			"model":             strings.TrimSpace(resp.Model),
			"provider":          strings.TrimSpace(resp.Provider),
			"finish_reason":     string(resp.FinishReason),
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
	}
	return out
}

func modelToolCallEvents(message sdkmodel.Message, resp *sdkmodel.Response) []*sdksession.Event {
	calls := message.ToolCalls()
	if len(calls) == 0 {
		return nil
	}
	out := make([]*sdksession.Event, 0, len(calls))
	baseMeta := map[string]any{}
	if resp != nil {
		baseMeta["model"] = strings.TrimSpace(resp.Model)
		baseMeta["provider"] = strings.TrimSpace(resp.Provider)
		baseMeta["finish_reason"] = string(resp.FinishReason)
		baseMeta["prompt_tokens"] = resp.Usage.PromptTokens
		baseMeta["completion_tokens"] = resp.Usage.CompletionTokens
		baseMeta["total_tokens"] = resp.Usage.TotalTokens
	}
	for i, call := range calls {
		event := &sdksession.Event{
			Type: sdksession.EventTypeToolCall,
			Protocol: &sdksession.EventProtocol{
				UpdateType: string(sdksession.ProtocolUpdateTypeToolCall),
				ToolCall: &sdksession.ProtocolToolCall{
					ID:       strings.TrimSpace(call.ID),
					Name:     strings.TrimSpace(call.Name),
					Kind:     toolKindForName(call.Name),
					Title:    toolCallTitle(call),
					Status:   "pending",
					RawInput: mustObject(call.Args),
				},
			},
			Meta: maps.Clone(baseMeta),
		}
		if i == 0 {
			event.Message = &message
			event.Text = message.TextContent()
		}
		out = append(out, event)
	}
	return out
}

func (a *Agent) executeToolCall(ctx context.Context, call sdkmodel.ToolCall) (sdkmodel.Message, *sdksession.Event, error) {
	tool, ok := a.lookupTool(call.Name)
	if !ok {
		message := toolResultMessage(call, sdktool.Result{
			ID:      call.ID,
			Name:    call.Name,
			IsError: true,
			Content: []sdkmodel.Part{sdkmodel.NewJSONPart(mustJSON(map[string]any{"error": fmt.Sprintf("tool %q not found", call.Name)}))},
		})
		return message, &sdksession.Event{
			Type:    sdksession.EventTypeToolResult,
			Message: &message,
			Text:    message.TextContent(),
			Protocol: &sdksession.EventProtocol{
				UpdateType: string(sdksession.ProtocolUpdateTypeToolUpdate),
				ToolCall: &sdksession.ProtocolToolCall{
					ID:        strings.TrimSpace(call.ID),
					Name:      strings.TrimSpace(call.Name),
					Kind:      toolKindForName(call.Name),
					Title:     toolCallTitle(call),
					Status:    "failed",
					RawInput:  mustObject(call.Args),
					RawOutput: map[string]any{"error": fmt.Sprintf("tool %q not found", call.Name)},
				},
			},
			Meta: map[string]any{
				"tool_name":    strings.TrimSpace(call.Name),
				"tool_call_id": strings.TrimSpace(call.ID),
				"is_error":     true,
			},
		}, nil
	}

	result, err := tool.Call(ctx, sdktool.Call{
		ID:    strings.TrimSpace(call.ID),
		Name:  strings.TrimSpace(call.Name),
		Input: json.RawMessage(strings.TrimSpace(call.Args)),
	})
	if err != nil {
		result = sdktool.Result{
			ID:      strings.TrimSpace(call.ID),
			Name:    strings.TrimSpace(call.Name),
			IsError: true,
			Content: []sdkmodel.Part{sdkmodel.NewJSONPart(mustJSON(map[string]any{"error": err.Error()}))},
		}
	}
	message := toolResultMessage(call, result)
	event := &sdksession.Event{
		Type:    sdksession.EventTypeToolResult,
		Message: &message,
		Text:    message.TextContent(),
		Protocol: &sdksession.EventProtocol{
			UpdateType: string(sdksession.ProtocolUpdateTypeToolUpdate),
			ToolCall: &sdksession.ProtocolToolCall{
				ID:        strings.TrimSpace(call.ID),
				Name:      strings.TrimSpace(call.Name),
				Kind:      toolKindForName(call.Name),
				Title:     toolCallTitle(call),
				Status:    toolCallStatus(result),
				RawInput:  mustObject(call.Args),
				RawOutput: maps.Clone(result.Meta),
			},
		},
		Meta: mergeEventMeta(
			map[string]any{
				"tool_name":    strings.TrimSpace(call.Name),
				"tool_call_id": strings.TrimSpace(call.ID),
				"is_error":     result.IsError,
			},
			result.Meta,
		),
	}
	return message, event, nil
}

func (a *Agent) lookupTool(name string) (sdktool.Tool, bool) {
	name = strings.TrimSpace(strings.ToUpper(name))
	for _, item := range a.tools {
		if item == nil {
			continue
		}
		if strings.TrimSpace(strings.ToUpper(item.Definition().Name)) == name {
			return item, true
		}
	}
	return nil, false
}

func toolResultMessage(call sdkmodel.ToolCall, result sdktool.Result) sdkmodel.Message {
	if len(result.Content) == 0 {
		result.Content = []sdkmodel.Part{sdkmodel.NewJSONPart(mustJSON(result.Meta))}
	}
	parts := sdkmodel.CloneParts(result.Content)
	if len(parts) == 0 {
		parts = []sdkmodel.Part{sdkmodel.NewJSONPart(mustJSON(map[string]any{}))}
	}
	return sdkmodel.Message{
		Role: sdkmodel.RoleTool,
		Parts: []sdkmodel.Part{{
			Kind: sdkmodel.PartKindToolResult,
			ToolResult: &sdkmodel.ToolResultPart{
				ToolUseID: strings.TrimSpace(firstNonEmpty(result.ID, call.ID)),
				Name:      strings.TrimSpace(firstNonEmpty(result.Name, call.Name)),
				Content:   parts,
				IsError:   result.IsError,
			},
		}},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mustJSON(value map[string]any) json.RawMessage {
	if value == nil {
		value = map[string]any{}
	}
	raw, _ := json.Marshal(value)
	return raw
}

func mustObject(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func toolKindForName(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "READ":
		return "read"
	case "WRITE", "PATCH":
		return "edit"
	case "SEARCH", "GLOB", "LIST":
		return "search"
	case "PLAN":
		return "other"
	case "BASH", "TASK":
		return "execute"
	default:
		return "other"
	}
}

func toolCallTitle(call sdkmodel.ToolCall) string {
	name := strings.TrimSpace(call.Name)
	args := mustObject(call.Args)
	switch strings.ToUpper(name) {
	case "READ", "WRITE", "PATCH", "SEARCH", "LIST", "GLOB":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return fmt.Sprintf("%s %s", name, strings.TrimSpace(path))
		}
	case "BASH":
		if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
			return fmt.Sprintf("BASH %s", strings.TrimSpace(command))
		}
	case "TASK":
		action, _ := args["action"].(string)
		taskID, _ := args["task_id"].(string)
		if strings.TrimSpace(action) != "" && strings.TrimSpace(taskID) != "" {
			return fmt.Sprintf("TASK %s %s", strings.TrimSpace(action), strings.TrimSpace(taskID))
		}
	}
	return name
}

func toolCallStatus(result sdktool.Result) string {
	if state, _ := result.Meta["state"].(string); strings.TrimSpace(state) != "" {
		switch strings.TrimSpace(state) {
		case "running", "waiting_input", "waiting_approval":
			return strings.TrimSpace(state)
		}
	}
	if result.IsError {
		return "failed"
	}
	return "completed"
}

func mergeEventMeta(parts ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, part := range parts {
		for key, value := range part {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func messagesFromContext(ctx sdkruntime.Context) []sdkmodel.Message {
	if ctx == nil {
		return nil
	}
	out := make([]sdkmodel.Message, 0, ctx.Events().Len())
	for event := range ctx.Events().All() {
		if !sdksession.IsInvocationVisibleEvent(event) || event == nil || event.Message == nil {
			continue
		}
		out = append(out, sdkmodel.CloneMessage(*event.Message))
	}
	return out
}

func instructionsFromContext(ctx sdkruntime.Context, systemPrompt string) []sdkmodel.Part {
	out := make([]sdkmodel.Part, 0, 1)
	if strings.TrimSpace(systemPrompt) != "" {
		out = append(out, sdkmodel.NewTextPart(strings.TrimSpace(systemPrompt)))
	}
	return out
}

// Metadata returns one stable agent metadata map for upstream assembly.
func Metadata(systemPrompt string) map[string]any {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return nil
	}
	return map[string]any{"system_prompt": systemPrompt}
}

// CloneMetadata returns one shallow metadata copy.
func CloneMetadata(values map[string]any) map[string]any {
	return maps.Clone(values)
}
