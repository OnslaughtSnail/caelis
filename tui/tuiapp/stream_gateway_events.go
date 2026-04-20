package tuiapp

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	"github.com/OnslaughtSnail/caelis/tui/tuikit"
)

func (m *Model) handleGatewayEventEnvelope(env appgateway.EventEnvelope) (tea.Model, tea.Cmd) {
	if env.Err != nil {
		model, cmd := m.handleTaskResultMsg(TaskResultMsg{Err: env.Err})
		return model, cmd
	}
	ev := env.Event
	switch ev.Kind {
	case appgateway.EventKindUserMessage:
		if text := strings.TrimSpace(gatewayUserText(ev)); text != "" {
			return m.handleUserMessageMsg(UserMessageMsg{Text: text}), nil
		}
		return m, nil
	case appgateway.EventKindAssistantMessage:
		return m.handleGatewayNarrativeEvent(ev)
	case appgateway.EventKindToolCall, appgateway.EventKindToolResult, appgateway.EventKindPlanUpdate:
		return m.handleGatewayACPProjectionEvent(ev)
	case appgateway.EventKindApprovalRequested:
		return m.handleGatewayApprovalEvent(ev)
	case appgateway.EventKindParticipant:
		return m.handleGatewayParticipantEvent(ev)
	case appgateway.EventKindLifecycle, appgateway.EventKindSessionLifecycle:
		return m.handleGatewayLifecycleEvent(ev)
	case appgateway.EventKindNotice, appgateway.EventKindSystemMessage:
		return m.appendGatewayTranscript(gatewayNoticeText(ev))
	default:
		return m, nil
	}
}

func (m *Model) handleGatewayNarrativeEvent(ev appgateway.Event) (tea.Model, tea.Cmd) {
	payload := ev.Narrative
	if payload == nil {
		return m, nil
	}
	switch payload.Role {
	case appgateway.NarrativeRoleUser:
		if text := strings.TrimSpace(payload.Text); text != "" {
			return m.handleUserMessageMsg(UserMessageMsg{Text: text}), nil
		}
		return m, nil
	case appgateway.NarrativeRoleAssistant:
		return m.handleGatewayAssistantNarrative(ev)
	case appgateway.NarrativeRoleSystem, appgateway.NarrativeRoleNotice:
		return m.appendGatewayTranscript(payload.Text)
	default:
		return m, nil
	}
}

func (m *Model) handleGatewayAssistantNarrative(ev appgateway.Event) (tea.Model, tea.Cmd) {
	payload := ev.Narrative
	if payload == nil {
		return m, nil
	}
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	actor := strings.TrimSpace(payload.Actor)
	occurredAt := ev.OccurredAt
	m.prepareForGatewayStructuredScope(scope)

	var cmds []tea.Cmd
	if reasoning := strings.TrimSpace(payload.ReasoningText); reasoning != "" {
		model, cmd := m.handleACPProjection(ACPProjectionMsg{
			Scope:      scope,
			ScopeID:    scopeID,
			Actor:      actor,
			OccurredAt: occurredAt,
			Stream:     "reasoning",
			DeltaText:  gatewayProjectionDelta(reasoning, payload.Final),
			FullText:   gatewayProjectionFull(reasoning, payload.Final),
		})
		m = model.(*Model)
		cmds = append(cmds, cmd)
	}
	if text := strings.TrimSpace(payload.Text); text != "" {
		model, cmd := m.handleACPProjection(ACPProjectionMsg{
			Scope:      scope,
			ScopeID:    scopeID,
			Actor:      actor,
			OccurredAt: occurredAt,
			Stream:     "answer",
			DeltaText:  gatewayProjectionDelta(text, payload.Final),
			FullText:   gatewayProjectionFull(text, payload.Final),
		})
		m = model.(*Model)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) handleGatewayACPProjectionEvent(ev appgateway.Event) (tea.Model, tea.Cmd) {
	projection, ok := gatewayACPProjectionFromEvent(ev)
	if !ok {
		return m, nil
	}
	m.prepareForGatewayStructuredScope(projection.Scope)
	return m.handleACPProjection(projection)
}

func (m *Model) handleGatewayApprovalEvent(ev appgateway.Event) (tea.Model, tea.Cmd) {
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	toolName, command := gatewayApprovalSummary(ev)
	occurredAt := ev.OccurredAt
	m.prepareForGatewayStructuredScope(scope)

	switch scope {
	case ACPProjectionParticipant:
		return m.handleParticipantStatusMsg(ParticipantStatusMsg{
			SessionID:       scopeID,
			State:           "waiting_approval",
			ApprovalTool:    toolName,
			ApprovalCommand: command,
			OccurredAt:      occurredAt,
		})
	case ACPProjectionSubagent:
		return m.handleSubagentStatus(SubagentStatusMsg{
			SpawnID:         scopeID,
			State:           "waiting_approval",
			ApprovalTool:    toolName,
			ApprovalCommand: command,
			OccurredAt:      occurredAt,
		})
	default:
		block := m.ensureMainACPTurnBlock(scopeID)
		if block == nil {
			return m, nil
		}
		block.SetStatus("waiting_approval", toolName, command, occurredAt)
		return m, m.requestStreamViewportSync()
	}
}

func (m *Model) handleGatewayParticipantEvent(ev appgateway.Event) (tea.Model, tea.Cmd) {
	payload := ev.Participant
	if payload == nil {
		return m, nil
	}
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	action := strings.TrimSpace(payload.Action)
	if action == "" || scopeID == "" {
		return m, nil
	}
	m.prepareForGatewayStructuredScope(scope)
	switch scope {
	case ACPProjectionSubagent:
		return m.handleSubagentStatus(SubagentStatusMsg{
			SpawnID:    scopeID,
			State:      action,
			OccurredAt: ev.OccurredAt,
		})
	default:
		return m.handleParticipantStatusMsg(ParticipantStatusMsg{
			SessionID:  scopeID,
			State:      action,
			OccurredAt: ev.OccurredAt,
		})
	}
}

func (m *Model) handleGatewayLifecycleEvent(ev appgateway.Event) (tea.Model, tea.Cmd) {
	payload := ev.Lifecycle
	if payload == nil {
		return m, nil
	}
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	state := strings.ToLower(strings.TrimSpace(payload.Status))
	if state == "" {
		return m, nil
	}
	m.prepareForGatewayStructuredScope(scope)
	switch scope {
	case ACPProjectionParticipant:
		return m.handleParticipantStatusMsg(ParticipantStatusMsg{
			SessionID:  scopeID,
			State:      state,
			OccurredAt: ev.OccurredAt,
		})
	case ACPProjectionSubagent:
		return m.handleSubagentStatus(SubagentStatusMsg{
			SpawnID:    scopeID,
			State:      state,
			OccurredAt: ev.OccurredAt,
		})
	default:
		block := m.ensureMainACPTurnBlock(scopeID)
		if block == nil {
			return m, nil
		}
		block.SetStatus(state, "", "", ev.OccurredAt)
		return m, m.requestStreamViewportSync()
	}
}

func (m *Model) appendGatewayTranscript(text string) (tea.Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	m.finalizeAssistantBlock()
	m.finalizeReasoningBlock()
	block := NewTranscriptBlock(text, tuikit.DetectLineStyle(text))
	m.doc.Append(block)
	m.hasCommittedLine = true
	m.lastCommittedStyle = block.Style
	m.syncViewportContent()
	return m, nil
}

func (m *Model) prepareForGatewayStructuredScope(scope ACPProjectionScope) {
	switch scope {
	case ACPProjectionMain, ACPProjectionParticipant, ACPProjectionSubagent:
		m.finalizeAssistantBlock()
		m.finalizeReasoningBlock()
	}
}

func gatewayACPProjectionFromEvent(ev appgateway.Event) (ACPProjectionMsg, bool) {
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	occurredAt := ev.OccurredAt

	if payload := ev.ToolCall; payload != nil {
		return ACPProjectionMsg{
			Scope:      scope,
			ScopeID:    scopeID,
			Actor:      strings.TrimSpace(payload.Actor),
			OccurredAt: occurredAt,
			ToolCallID: strings.TrimSpace(payload.CallID),
			ToolName:   strings.TrimSpace(payload.ToolName),
			ToolArgs:   gatewayToolArgsMap(payload.CommandPreview, payload.ArgsText),
			ToolStatus: firstNonEmpty(strings.TrimSpace(payload.Status), "running"),
		}, true
	}
	if payload := ev.ToolResult; payload != nil {
		status := strings.TrimSpace(payload.Status)
		if status == "" {
			if payload.Error {
				status = "failed"
			} else {
				status = "completed"
			}
		}
		return ACPProjectionMsg{
			Scope:      scope,
			ScopeID:    scopeID,
			Actor:      strings.TrimSpace(payload.Actor),
			OccurredAt: occurredAt,
			ToolCallID: strings.TrimSpace(payload.CallID),
			ToolName:   strings.TrimSpace(payload.ToolName),
			ToolArgs:   gatewayToolArgsMap(payload.CommandPreview, ""),
			ToolResult: gatewayToolResultMap(payload.OutputText, payload.Error),
			ToolStatus: status,
		}, true
	}
	if payload := ev.Plan; payload != nil {
		entries := make([]PlanEntry, 0, len(payload.Entries))
		for _, entry := range payload.Entries {
			entries = append(entries, PlanEntry{
				Content: entry.Content,
				Status:  entry.Status,
			})
		}
		return ACPProjectionMsg{
			Scope:         scope,
			ScopeID:       scopeID,
			OccurredAt:    occurredAt,
			PlanEntries:   entries,
			HasPlanUpdate: len(entries) > 0,
		}, len(entries) > 0
	}
	return ACPProjectionMsg{}, false
}

func gatewayEventScope(ev appgateway.Event) ACPProjectionScope {
	if ev.Origin == nil {
		return ACPProjectionMain
	}
	return gatewayProjectionScope(ev.Origin.Scope)
}

func gatewayProjectionScope(scope appgateway.EventScope) ACPProjectionScope {
	switch scope {
	case appgateway.EventScopeParticipant:
		return ACPProjectionParticipant
	case appgateway.EventScopeSubagent:
		return ACPProjectionSubagent
	default:
		return ACPProjectionMain
	}
}

func gatewayEventScopeID(ev appgateway.Event) string {
	if ev.Origin != nil && strings.TrimSpace(ev.Origin.ScopeID) != "" {
		return strings.TrimSpace(ev.Origin.ScopeID)
	}
	if sessionID := strings.TrimSpace(ev.SessionRef.SessionID); sessionID != "" {
		return sessionID
	}
	return strings.TrimSpace(ev.TurnID)
}

func gatewayParticipantID(ev appgateway.Event) string {
	if ev.Origin != nil && strings.TrimSpace(ev.Origin.ParticipantID) != "" {
		return strings.TrimSpace(ev.Origin.ParticipantID)
	}
	switch {
	case ev.Narrative != nil:
		return strings.TrimSpace(ev.Narrative.ParticipantID)
	case ev.ToolCall != nil:
		return strings.TrimSpace(ev.ToolCall.ParticipantID)
	case ev.ToolResult != nil:
		return strings.TrimSpace(ev.ToolResult.ParticipantID)
	case ev.Participant != nil:
		return strings.TrimSpace(ev.Participant.ParticipantID)
	case ev.Lifecycle != nil:
		return strings.TrimSpace(ev.Lifecycle.ParticipantID)
	default:
		return ""
	}
}

func gatewayUserText(ev appgateway.Event) string {
	if ev.Narrative != nil {
		return strings.TrimSpace(ev.Narrative.Text)
	}
	return ""
}

func gatewayNoticeText(ev appgateway.Event) string {
	if ev.Narrative != nil {
		return strings.TrimSpace(ev.Narrative.Text)
	}
	return ""
}

func gatewayApprovalSummary(ev appgateway.Event) (string, string) {
	if ev.ApprovalPayload != nil {
		return strings.TrimSpace(ev.ApprovalPayload.ToolName), strings.TrimSpace(ev.ApprovalPayload.CommandPreview)
	}
	return "", ""
}

func gatewayProjectionDelta(text string, final bool) string {
	if final {
		return ""
	}
	return text
}

func gatewayProjectionFull(text string, final bool) string {
	if final {
		return text
	}
	return ""
}

func gatewayToolArgsMap(commandPreview string, argsText string) map[string]any {
	display := strings.TrimSpace(firstNonEmpty(commandPreview, argsText))
	if display == "" {
		return nil
	}
	return map[string]any{"_display": display}
}

func gatewayToolResultMap(output string, isErr bool) map[string]any {
	output = strings.TrimSpace(output)
	if output == "" {
		if isErr {
			return map[string]any{"summary": "failed"}
		}
		return map[string]any{"summary": "completed"}
	}
	if isErr {
		return map[string]any{"error": output, "summary": output}
	}
	return map[string]any{"summary": output}
}
