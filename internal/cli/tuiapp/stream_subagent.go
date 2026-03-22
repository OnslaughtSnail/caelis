package tuiapp

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

func (m *Model) ensureSubagentPanelBlock(spawnID, attachID, agent, callID string) *SubagentPanelBlock {
	if m.subagentBlockIDs == nil {
		m.subagentBlockIDs = map[string]string{}
	}
	blockID, ok := m.subagentBlockIDs[spawnID]
	if ok {
		block := m.doc.Find(blockID)
		if block != nil {
			if sp, ok := block.(*SubagentPanelBlock); ok {
				if sp.AttachID == "" && strings.TrimSpace(attachID) != "" {
					sp.AttachID = strings.TrimSpace(attachID)
				}
				if sp.Agent == "" && strings.TrimSpace(agent) != "" {
					sp.Agent = strings.TrimSpace(agent)
				}
				if sp.CallID == "" && strings.TrimSpace(callID) != "" {
					sp.CallID = strings.TrimSpace(callID)
				}
				m.syncInlineSubagentAnchorState(sp)
				return sp
			}
		}
	}
	sp := NewSubagentPanelBlock(spawnID, attachID, agent, callID)
	// Anchor panel after its specific tool call line.
	anchorID := m.resolveCallAnchor(callID, "SPAWN")
	if anchorID != "" {
		m.doc.InsertAfter(anchorID, sp)
	} else {
		m.doc.Append(sp)
	}
	m.subagentBlockIDs[spawnID] = sp.BlockID()
	m.syncInlineSubagentAnchorState(sp)
	return sp
}

func (m *Model) handleSubagentStart(msg tuievents.SubagentStartMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, msg.AttachTarget, msg.Agent, msg.CallID)
	if panel.Status == "" {
		panel.Status = "running"
	}
	if !isTerminalSubagentState(panel.Status) {
		panel.Expanded = true
	} else {
		panel.Expanded = false
	}
	m.syncInlineSubagentAnchorState(panel)
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleSubagentStatus(msg tuievents.SubagentStatusMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, "", "", "")
	state := strings.TrimSpace(msg.State)
	if state != "" {
		panel.Status = state
	}
	if isTerminalSubagentState(state) && subagentHasInlineAnchor(m, panel) {
		panel.Expanded = false
	}
	// Create an approval event with context when entering waiting_approval.
	if strings.EqualFold(state, "waiting_approval") {
		panel.AddApprovalEvent(msg.ApprovalTool, msg.ApprovalCommand)
	}
	m.syncInlineSubagentAnchorState(panel)
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleSubagentStream(msg tuievents.SubagentStreamMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, "", "", "")
	switch msg.Stream {
	case "assistant":
		panel.AppendStreamChunk(SEAssistant, msg.Chunk)
	case "reasoning":
		panel.AppendStreamChunk(SEReasoning, msg.Chunk)
	}
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleSubagentToolCall(msg tuievents.SubagentToolCallMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, "", "", "")
	if msg.CallID != "" {
		panel.UpdateToolCall(msg.CallID, msg.ToolName, msg.Args, msg.Stream, msg.Chunk, msg.Final)
	}
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleSubagentPlan(msg tuievents.SubagentPlanMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, "", "", "")
	entries := make([]planEntryState, len(msg.Entries))
	for i, e := range msg.Entries {
		entries[i] = planEntryState{Content: e.Content, Status: e.Status}
	}
	panel.UpdatePlan(entries)
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleSubagentDone(msg tuievents.SubagentDoneMsg) (tea.Model, tea.Cmd) {
	panel := m.ensureSubagentPanelBlock(msg.SpawnID, "", "", "")
	panel.Status = msg.State
	if subagentHasInlineAnchor(m, panel) {
		panel.Expanded = false
	}
	m.syncInlineSubagentAnchorState(panel)
	m.syncViewportContent()
	return m, nil
}

func (m *Model) findInlineSubagentPanelByAnchorBlockID(blockID string) *SubagentPanelBlock {
	blockID = strings.TrimSpace(blockID)
	if blockID == "" {
		return nil
	}
	for _, candidate := range m.subagentBlockIDs {
		panel, _ := m.doc.Find(candidate).(*SubagentPanelBlock)
		if panel == nil {
			continue
		}
		callID := strings.TrimSpace(panel.CallID)
		if callID == "" {
			continue
		}
		if strings.TrimSpace(m.callAnchorIndex[callID]) == blockID {
			return panel
		}
	}
	return nil
}

func (m *Model) syncInlineSubagentAnchorState(panel *SubagentPanelBlock) {
	if m == nil || panel == nil {
		return
	}
	callID := strings.TrimSpace(panel.CallID)
	if callID == "" {
		return
	}
	anchorID := strings.TrimSpace(m.callAnchorIndex[callID])
	if anchorID == "" {
		return
	}
	tb := m.findTranscriptBlock(anchorID)
	if tb == nil {
		return
	}
	tb.Raw = inlineBashAnchorLabel(tb.Raw, panel.Expanded)
}

func subagentHasInlineAnchor(m *Model, panel *SubagentPanelBlock) bool {
	if m == nil || panel == nil {
		return false
	}
	callID := strings.TrimSpace(panel.CallID)
	if callID == "" {
		return false
	}
	return strings.TrimSpace(m.callAnchorIndex[callID]) != ""
}

func isTerminalSubagentState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		return true
	default:
		return false
	}
}
