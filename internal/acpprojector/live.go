package acpprojector

import (
	"encoding/json"
	"strings"
	"sync"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type Projection struct {
	SessionID   string
	Event       *session.Event
	PlanEntries []internalacp.PlanEntry
	Stream      string
	DeltaText   string
	FullText    string
	ToolCallID  string
	ToolName    string
	ToolArgs    map[string]any
	ToolResult  map[string]any
	ToolStatus  string
	TerminalID  string
}

type LiveProjector struct {
	mu        sync.Mutex
	assistant string
	reasoning string
	toolCalls map[string]toolCallSnapshot
}

type toolCallSnapshot struct {
	Name string
	Args map[string]any
}

func NewLiveProjector() *LiveProjector {
	return &LiveProjector{toolCalls: map[string]toolCallSnapshot{}}
}

func (p *LiveProjector) Snapshot() (assistant string, reasoning string) {
	if p == nil {
		return "", ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assistant, p.reasoning
}

func (p *LiveProjector) Project(env acpclient.UpdateEnvelope) []Projection {
	if p == nil || env.Update == nil {
		return nil
	}
	sessionID := strings.TrimSpace(env.SessionID)
	switch update := env.Update.(type) {
	case acpclient.ContentChunk:
		if out, ok := p.projectContent(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.ToolCall:
		if out, ok := p.projectToolCall(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.ToolCallUpdate:
		if out, ok := p.projectToolCallUpdate(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.PlanUpdate:
		return []Projection{{
			SessionID:   sessionID,
			PlanEntries: append([]internalacp.PlanEntry(nil), update.Entries...),
		}}
	}
	return nil
}

func (p *LiveProjector) projectContent(sessionID string, update acpclient.ContentChunk) (Projection, bool) {
	text := decodeTextChunk(update.Content)
	if text == "" {
		return Projection{}, false
	}
	ev := &session.Event{Message: model.Message{Role: model.RoleAssistant}}
	stream := ""
	p.mu.Lock()
	defer p.mu.Unlock()
	switch strings.TrimSpace(update.SessionUpdate) {
	case acpclient.UpdateAgentMessage:
		next, delta, changed := MergeNarrativeChunk(p.assistant, text)
		if !changed {
			return Projection{}, false
		}
		p.assistant = next
		stream = "assistant"
		ev.Message = model.NewTextMessage(model.RoleAssistant, delta)
		ev.Meta = map[string]any{"partial": true, "channel": "answer"}
		return Projection{
			SessionID: sessionID,
			Event:     ev,
			Stream:    stream,
			DeltaText: delta,
			FullText:  p.assistant,
		}, true
	case acpclient.UpdateAgentThought:
		next, delta, changed := MergeNarrativeChunk(p.reasoning, text)
		if !changed {
			return Projection{}, false
		}
		p.reasoning = next
		stream = "reasoning"
		ev.Message = model.NewReasoningMessage(model.RoleAssistant, delta, model.ReasoningVisibilityVisible)
		ev.Meta = map[string]any{"partial": true, "channel": "reasoning"}
		return Projection{
			SessionID: sessionID,
			Event:     ev,
			Stream:    stream,
			DeltaText: delta,
			FullText:  p.reasoning,
		}, true
	default:
		return Projection{}, false
	}
}

func (p *LiveProjector) projectToolCall(sessionID string, update acpclient.ToolCall) (Projection, bool) {
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return Projection{}, false
	}
	name, args := toolCallShape(update.Title, update.Kind, update.RawInput)
	p.mu.Lock()
	p.toolCalls[callID] = toolCallSnapshot{Name: name, Args: cloneMap(args)}
	p.mu.Unlock()
	return Projection{
		SessionID:  sessionID,
		ToolCallID: callID,
		ToolName:   name,
		ToolArgs:   cloneMap(args),
		Event: &session.Event{
			Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
				ID:   callID,
				Name: name,
				Args: marshalToolInput(args),
			}}, ""),
		},
	}, true
}

func (p *LiveProjector) projectToolCallUpdate(sessionID string, update acpclient.ToolCallUpdate) (Projection, bool) {
	status := strings.ToLower(strings.TrimSpace(derefString(update.Status)))
	if status != internalacp.ToolStatusInProgress && status != internalacp.ToolStatusCompleted && status != internalacp.ToolStatusFailed {
		return Projection{}, false
	}
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return Projection{}, false
	}
	p.mu.Lock()
	snap := p.toolCalls[callID]
	if status == internalacp.ToolStatusCompleted || status == internalacp.ToolStatusFailed {
		delete(p.toolCalls, callID)
	}
	p.mu.Unlock()
	name := strings.TrimSpace(snap.Name)
	args := cloneMap(snap.Args)
	if title := strings.TrimSpace(derefString(update.Title)); title != "" || strings.TrimSpace(derefString(update.Kind)) != "" || update.RawInput != nil {
		name, args = toolCallShape(derefString(update.Title), derefString(update.Kind), update.RawInput)
	}
	if name == "" {
		name = "TOOL"
	}
	result := resultMap(update.RawOutput)
	return Projection{
		SessionID:  sessionID,
		ToolCallID: callID,
		ToolName:   name,
		ToolArgs:   cloneMap(args),
		ToolResult: cloneMap(result),
		ToolStatus: status,
		TerminalID: toolCallTerminalID(update.Content),
		Event: &session.Event{
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:     callID,
				Name:   name,
				Result: result,
			}),
		},
	}, true
}

func decodeTextChunk(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var chunk struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(chunk.Type), "text") {
		return ""
	}
	return chunk.Text
}

func toolCallShape(title string, kind string, rawInput any) (string, map[string]any) {
	name := "TOOL"
	title = strings.TrimSpace(title)
	if title != "" {
		if fields := strings.Fields(title); len(fields) > 0 {
			name = strings.ToUpper(strings.TrimSpace(fields[0]))
		}
	} else if strings.TrimSpace(kind) != "" {
		name = strings.ToUpper(strings.TrimSpace(kind))
	}
	args := map[string]any{}
	if title != "" {
		args["_acp_title"] = title
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		args["_acp_kind"] = kind
	}
	switch typed := rawInput.(type) {
	case nil:
	case map[string]any:
		for key, value := range typed {
			args[key] = value
		}
	default:
		args["_acp_raw_input"] = typed
	}
	return name, args
}

func marshalToolInput(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(data)
}

func resultMap(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return cloneMap(typed)
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil && out != nil {
			return out
		}
	default:
		return map[string]any{"result": typed}
	}
	return map[string]any{}
}

func toolCallTerminalID(items []acpclient.ToolCallContent) string {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "terminal") && strings.TrimSpace(item.TerminalID) != "" {
			return strings.TrimSpace(item.TerminalID)
		}
	}
	return ""
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
