package tuiapp

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func (m *Model) handleACPProjection(msg tuievents.ACPProjectionMsg) (tea.Model, tea.Cmd) {
	switch msg.Scope {
	case tuievents.ACPProjectionParticipant:
		return m.handleParticipantACPProjection(msg)
	case tuievents.ACPProjectionSubagent:
		return m.handleSubagentACPProjection(msg)
	default:
		return m, nil
	}
}

func (m *Model) handleParticipantACPProjection(msg tuievents.ACPProjectionMsg) (tea.Model, tea.Cmd) {
	sessionID := strings.TrimSpace(msg.ScopeID)
	if sessionID == "" {
		return m, nil
	}
	if _, text, final, ok := acpProjectionStreamPayload(msg); ok {
		block := m.ensureParticipantTurnBlock(sessionID, msg.Actor)
		if block != nil && !msg.OccurredAt.IsZero() && (block.StartedAt.IsZero() || msg.OccurredAt.Before(block.StartedAt)) {
			block.StartedAt = msg.OccurredAt
		}
		return m.handleParticipantTurnStream(sessionID, msg.Stream, msg.Actor, text, final)
	}
	if msg.HasPlanUpdate {
		block := m.ensureParticipantTurnBlock(sessionID, msg.Actor)
		if block == nil {
			return m, nil
		}
		if !msg.OccurredAt.IsZero() && (block.StartedAt.IsZero() || msg.OccurredAt.Before(block.StartedAt)) {
			block.StartedAt = msg.OccurredAt
		}
		if state := strings.ToLower(strings.TrimSpace(block.Status)); state == "initializing" || state == "prompting" {
			block.Status = "running"
		}
		entries := make([]planEntryState, 0, len(msg.PlanEntries))
		for _, entry := range msg.PlanEntries {
			entries = append(entries, planEntryState{Content: entry.Content, Status: entry.Status})
		}
		block.UpdatePlan(entries)
		return m, m.requestStreamViewportSync()
	}
	args, output, final, err, ok := acpProjectionToolPayload(msg)
	if !ok {
		return m, nil
	}
	block := m.ensureParticipantTurnBlock(sessionID, msg.Actor)
	if block == nil {
		return m, nil
	}
	if !msg.OccurredAt.IsZero() && (block.StartedAt.IsZero() || msg.OccurredAt.Before(block.StartedAt)) {
		block.StartedAt = msg.OccurredAt
	}
	if state := strings.ToLower(strings.TrimSpace(block.Status)); state == "initializing" || state == "prompting" {
		block.Status = "running"
	}
	block.UpdateTool(msg.ToolCallID, msg.ToolName, args, output, final, err)
	return m, m.requestStreamViewportSync()
}

func (m *Model) handleSubagentACPProjection(msg tuievents.ACPProjectionMsg) (tea.Model, tea.Cmd) {
	spawnID := strings.TrimSpace(msg.ScopeID)
	if spawnID == "" {
		return m, nil
	}
	sessionKey, state := m.ensureSubagentSessionState(spawnID, "", "")
	panel := m.ensureSubagentPanelBlock(spawnID, "", "", "", "", false)
	if state == nil || panel == nil {
		return m, nil
	}
	if !msg.OccurredAt.IsZero() && (state.StartedAt.IsZero() || msg.OccurredAt.Before(state.StartedAt)) {
		state.StartedAt = msg.OccurredAt
	}
	switch {
	case strings.EqualFold(state.Status, "waiting_approval"):
		state.Status = "running"
	case isTerminalSubagentState(state.Status):
		state.ReviveFromTerminal()
	}
	panel.bindSession(state)
	if kind, text, final, ok := acpProjectionStreamPayload(msg); ok {
		if final {
			panel.ReplaceFinalStreamChunk(kind, text)
		} else {
			panel.AppendStreamChunk(kind, text)
		}
		m.reviveSubagentPanel(panel, false)
		m.syncSubagentSessionPanels(sessionKey)
		return m, m.requestStreamViewportSync()
	}
	if msg.HasPlanUpdate {
		entries := make([]planEntryState, 0, len(msg.PlanEntries))
		for _, entry := range msg.PlanEntries {
			entries = append(entries, planEntryState{Content: entry.Content, Status: entry.Status})
		}
		state.UpdatePlan(entries)
		m.reviveSubagentPanel(panel, false)
		m.syncSubagentSessionPanels(sessionKey)
		return m, m.requestStreamViewportSync()
	}
	stream, chunk, final, ok := acpProjectionSubagentToolPayload(msg)
	if ok {
		state.UpdateToolCall(msg.ToolCallID, msg.ToolName, acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs), stream, chunk, final)
		m.reviveSubagentPanel(panel, false)
		m.syncSubagentSessionPanels(sessionKey)
		return m, m.requestStreamViewportSync()
	}
	return m, nil
}

func acpProjectionStreamPayload(msg tuievents.ACPProjectionMsg) (SubagentEventKind, string, bool, bool) {
	if strings.TrimSpace(msg.Stream) == "" {
		return 0, "", false, false
	}
	hasDelta := msg.DeltaText != ""
	hasFull := msg.FullText != ""
	text := msg.DeltaText
	if !hasDelta {
		text = msg.FullText
	}
	text = tuikit.SanitizeLogText(text)
	if text == "" {
		return 0, "", false, false
	}
	kind := SEAssistant
	if normalizeStreamKind(msg.Stream) == "reasoning" {
		kind = SEReasoning
	}
	return kind, text, !hasDelta && hasFull, true
}

func acpProjectionToolPayload(msg tuievents.ACPProjectionMsg) (args string, output string, final bool, err bool, ok bool) {
	callID := strings.TrimSpace(msg.ToolCallID)
	toolName := strings.TrimSpace(msg.ToolName)
	if callID == "" || toolName == "" {
		return "", "", false, false, false
	}
	args = acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs)
	status := strings.ToLower(strings.TrimSpace(msg.ToolStatus))
	switch status {
	case "", "in_progress", "running":
		return args, "", false, false, true
	case "completed", "failed":
		output = acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus)
		return args, output, true, status == "failed", true
	default:
		return "", "", false, false, false
	}
}

func acpProjectionSubagentToolPayload(msg tuievents.ACPProjectionMsg) (stream string, chunk string, final bool, ok bool) {
	callID := strings.TrimSpace(msg.ToolCallID)
	toolName := strings.TrimSpace(msg.ToolName)
	if callID == "" || toolName == "" {
		return "", "", false, false
	}
	status := strings.ToLower(strings.TrimSpace(msg.ToolStatus))
	switch status {
	case "completed":
		return "stdout", acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), true, true
	case "failed":
		return "stderr", acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), true, true
	case "", "in_progress", "running":
		if msg.ToolResult == nil {
			return "", "", false, true
		}
		stream = strings.TrimSpace(firstNonEmpty(asString(msg.ToolResult["stream"]), "stdout"))
		if stream == "" {
			stream = "stdout"
		}
		return stream, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), false, true
	default:
		return "", "", false, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
