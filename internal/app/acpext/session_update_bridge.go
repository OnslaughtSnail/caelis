package acpext

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

// acpSessionUpdateBridge projects ACP child-session updates onto the runtime's
// standard sessionstream channel. Self and future external ACP subagent
// runners should both use this path so the CLI/TUI only consumes one shape.
type acpSessionUpdateBridge struct {
	meta           runtime.DelegationMetadata
	childSessionID string

	mu        sync.Mutex
	assistant string
	reasoning string
	toolCalls map[string]acpToolCall
}

type acpToolCall struct {
	title    string
	kind     string
	rawInput any
}

func newACPSessionUpdateBridge(meta runtime.DelegationMetadata, childSessionID string) *acpSessionUpdateBridge {
	return &acpSessionUpdateBridge{
		meta:           meta,
		childSessionID: strings.TrimSpace(childSessionID),
		toolCalls:      map[string]acpToolCall{},
	}
}

func (b *acpSessionUpdateBridge) Emit(ctx context.Context, env acpclient.UpdateEnvelope) {
	if b == nil || env.Update == nil {
		return
	}
	sessionID := strings.TrimSpace(env.SessionID)
	if sessionID == "" {
		sessionID = b.childSessionID
	}
	switch update := env.Update.(type) {
	case acpclient.ContentChunk:
		b.emitContent(ctx, sessionID, update)
	case acpclient.ToolCall:
		b.emitToolCall(ctx, sessionID, update)
	case acpclient.ToolCallUpdate:
		b.emitToolCallUpdate(ctx, sessionID, update)
	}
}

func (b *acpSessionUpdateBridge) emitContent(ctx context.Context, sessionID string, update acpclient.ContentChunk) {
	text := decodeACPTextChunk(update.Content)
	if text == "" {
		return
	}
	ev := &session.Event{
		Message: model.Message{Role: model.RoleAssistant},
		Meta: map[string]any{
			"partial": true,
		},
	}
	b.mu.Lock()
	switch strings.TrimSpace(update.SessionUpdate) {
	case acpclient.UpdateAgentMessage:
		b.assistant += text
		ev.Message.Text = b.assistant
		ev.Meta["channel"] = string(session.PartialChannelAnswer)
	case acpclient.UpdateAgentThought:
		b.reasoning += text
		ev.Message.Reasoning = b.reasoning
		ev.Meta["channel"] = string(session.PartialChannelReasoning)
	default:
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	sessionstream.Emit(ctx, sessionID, annotateDelegationEvent(ev, b.meta))
}

func (b *acpSessionUpdateBridge) emitToolCall(ctx context.Context, sessionID string, update acpclient.ToolCall) {
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return
	}
	b.mu.Lock()
	b.toolCalls[callID] = acpToolCall{
		title:    strings.TrimSpace(update.Title),
		kind:     strings.TrimSpace(update.Kind),
		rawInput: update.RawInput,
	}
	b.mu.Unlock()
	ev := &session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolCalls: []model.ToolCall{{
				ID:   callID,
				Name: acpToolDisplayName(update.Title, update.Kind),
				Args: marshalACPValue(update.RawInput),
			}},
		},
	}
	sessionstream.Emit(ctx, sessionID, annotateDelegationEvent(ev, b.meta))
}

func (b *acpSessionUpdateBridge) emitToolCallUpdate(ctx context.Context, sessionID string, update acpclient.ToolCallUpdate) {
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return
	}
	status := strings.ToLower(strings.TrimSpace(derefString(update.Status)))
	if status != internalacp.ToolStatusCompleted && status != internalacp.ToolStatusFailed {
		return
	}
	b.mu.Lock()
	snap := b.toolCalls[callID]
	delete(b.toolCalls, callID)
	b.mu.Unlock()
	title := firstNonEmpty(strings.TrimSpace(derefString(update.Title)), snap.title)
	kind := firstNonEmpty(strings.TrimSpace(derefString(update.Kind)), snap.kind)
	ev := &session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     callID,
				Name:   acpToolDisplayName(title, kind),
				Result: acpRawOutputResult(update.RawOutput),
			},
		},
	}
	sessionstream.Emit(ctx, sessionID, annotateDelegationEvent(ev, b.meta))
}

func decodeACPTextChunk(raw json.RawMessage) string {
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

func acpToolDisplayName(title string, kind string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		if fields := strings.Fields(title); len(fields) > 0 {
			return strings.ToUpper(strings.TrimSpace(fields[0]))
		}
	}
	if kind != "" {
		return strings.ToUpper(strings.TrimSpace(kind))
	}
	return "TOOL"
}

func marshalACPValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func acpRawOutputResult(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, one := range typed {
			out[key] = one
		}
		return out
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
