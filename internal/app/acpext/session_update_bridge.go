package acpext

import (
	"context"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
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
	projector      *acpprojector.LiveProjector
}

func newACPSessionUpdateBridge(meta runtime.DelegationMetadata, agentName string, childSessionID string, childCWD string, tracker *remoteSubagentTracker, _ func(), _ func()) *acpSessionUpdateBridge {
	return &acpSessionUpdateBridge{
		meta:           meta,
		childSessionID: strings.TrimSpace(childSessionID),
		agentName:      strings.TrimSpace(agentName),
		childCWD:       strings.TrimSpace(childCWD),
		tracker:        tracker,
		projector:      acpprojector.NewLiveProjector(),
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
	for _, item := range b.projector.Project(env) {
		if strings.TrimSpace(item.SessionID) != "" {
			sessionID = strings.TrimSpace(item.SessionID)
		}
		b.emitProjection(ctx, sessionID, item)
	}
}

func (b *acpSessionUpdateBridge) emitProjection(ctx context.Context, sessionID string, item acpprojector.Projection) {
	if b == nil {
		return
	}
	if len(item.PlanEntries) > 0 {
		if b.tracker != nil {
			b.tracker.markRunning(b.agentName, sessionID, b.meta.DelegationID, b.childCWD)
		}
		return
	}
	if item.Event == nil {
		return
	}
	switch item.Stream {
	case "assistant":
		if b.tracker != nil {
			b.tracker.updateAssistant(b.agentName, sessionID, item.FullText)
		}
	case "reasoning":
		if b.tracker != nil {
			b.tracker.updateReasoning(b.agentName, sessionID, item.FullText)
		}
	}
	if b.tracker != nil {
		b.tracker.markRunning(b.agentName, sessionID, b.meta.DelegationID, b.childCWD)
		if strings.TrimSpace(item.ToolName) != "" {
			b.tracker.updateTool(b.agentName, sessionID, item.ToolName)
		}
		if strings.TrimSpace(item.ToolCallID) != "" && strings.TrimSpace(item.ToolStatus) == "" {
			b.tracker.beginToolCall(b.agentName, sessionID)
		}
		if item.ToolStatus == "completed" || item.ToolStatus == "failed" {
			b.tracker.endToolCall(b.agentName, sessionID)
		}
	}
	if item.ToolStatus == "in_progress" && strings.TrimSpace(item.TerminalID) != "" {
		return
	}
	b.emitCanonical(ctx, sessionID, item.Event)
}

func (b *acpSessionUpdateBridge) FlushAssistant(_ context.Context) {
	if b == nil {
		return
	}
	text, reasoning := b.projector.Snapshot()
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
	sessionstream.Emit(ctx, sessionID, annotateAgentEventMeta(annotateDelegationEvent(ev, b.meta), b.agentName))
}

func annotateAgentEventMeta(ev *session.Event, agentName string) *session.Event {
	if ev == nil {
		return nil
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return ev
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta["agent_id"] = agentName
	ev.Meta["_ui_agent"] = agentName
	return ev
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

func toolCallTerminalID(items []acpclient.ToolCallContent) string {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "terminal") && strings.TrimSpace(item.TerminalID) != "" {
			return strings.TrimSpace(item.TerminalID)
		}
	}
	return ""
}
