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
	agentName      string
	childCWD       string
	tracker        *remoteSubagentTracker

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

func newACPSessionUpdateBridge(meta runtime.DelegationMetadata, agentName string, childSessionID string, childCWD string, tracker *remoteSubagentTracker) *acpSessionUpdateBridge {
	return &acpSessionUpdateBridge{
		meta:           meta,
		childSessionID: strings.TrimSpace(childSessionID),
		agentName:      strings.TrimSpace(agentName),
		childCWD:       strings.TrimSpace(childCWD),
		tracker:        tracker,
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
	ev := runtimeEventWithPartialMeta()
	b.mu.Lock()
	switch strings.TrimSpace(update.SessionUpdate) {
	case acpclient.UpdateAgentMessage:
		b.assistant += text
		ev.Message = model.NewTextMessage(model.RoleAssistant, b.assistant)
		ev.Meta["channel"] = "answer"
		if b.tracker != nil {
			b.tracker.updateAssistant(b.agentName, sessionID, b.assistant)
		}
	case acpclient.UpdateAgentThought:
		b.reasoning += text
		ev.Message = model.NewReasoningMessage(model.RoleAssistant, b.reasoning, model.ReasoningVisibilityVisible)
		ev.Meta["channel"] = "reasoning"
		if b.tracker != nil {
			b.tracker.updateReasoning(b.agentName, sessionID, b.reasoning)
		}
	default:
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	if b.tracker != nil {
		b.tracker.markRunning(b.agentName, sessionID, b.meta.DelegationID, b.childCWD)
	}
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   callID,
			Name: acpToolDisplayName(update.Title, update.Kind),
			Args: marshalACPToolInput(update.Title, update.Kind, update.RawInput),
		}}, ""),
	}
	if b.tracker != nil {
		b.tracker.markRunning(b.agentName, sessionID, b.meta.DelegationID, b.childCWD)
		b.tracker.updateTool(b.agentName, sessionID, acpToolDisplayName(update.Title, update.Kind))
	}
	b.emitCanonical(ctx, sessionID, ev)
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
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:     callID,
			Name:   acpToolDisplayName(title, kind),
			Result: acpRawOutputResult(update.RawOutput),
		}),
	}
	if b.tracker != nil {
		b.tracker.markRunning(b.agentName, sessionID, b.meta.DelegationID, b.childCWD)
		b.tracker.updateTool(b.agentName, sessionID, acpToolDisplayName(title, kind))
	}
	b.emitCanonical(ctx, sessionID, ev)
}

func (b *acpSessionUpdateBridge) FlushAssistant(ctx context.Context) {
	if b == nil {
		return
	}
	b.mu.Lock()
	text := b.assistant
	reasoning := b.reasoning
	b.mu.Unlock()
	if text == "" && reasoning == "" {
		return
	}
	if b.tracker != nil {
		if text != "" {
			b.tracker.updateAssistant(b.agentName, b.childSessionID, text)
		}
		if reasoning != "" {
			b.tracker.updateReasoning(b.agentName, b.childSessionID, reasoning)
		}
	}
}

func (b *acpSessionUpdateBridge) emitCanonical(ctx context.Context, sessionID string, ev *session.Event) {
	if ev == nil {
		return
	}
	sessionstream.Emit(ctx, sessionID, annotateDelegationEvent(ev, b.meta))
}

func runtimeEventWithPartialMeta() *session.Event {
	return &session.Event{
		Message: model.Message{Role: model.RoleAssistant},
		Meta: map[string]any{
			"partial": true,
		},
	}
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

func marshalACPToolInput(title string, kind string, value any) string {
	payload := map[string]any{}
	if title = strings.TrimSpace(title); title != "" {
		payload["_acp_title"] = title
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		payload["_acp_kind"] = kind
	}
	switch typed := value.(type) {
	case nil:
	case map[string]any:
		for key, one := range typed {
			payload[key] = one
		}
	default:
		payload["_acp_raw_input"] = typed
	}
	if len(payload) == 0 {
		return ""
	}
	raw, err := json.Marshal(payload)
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
