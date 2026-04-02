package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

type renderEventLane string

const (
	renderLaneLog         renderEventLane = "log"
	renderLaneMainStream  renderEventLane = "main_stream"
	renderLaneToolStream  renderEventLane = "tool_stream"
	renderLaneParticipant renderEventLane = "participant"
	renderLaneSubagent    renderEventLane = "subagent"
	renderLaneUIState     renderEventLane = "ui_state"
	renderLaneLifecycle   renderEventLane = "lifecycle"
	renderLaneOverlay     renderEventLane = "overlay"
	renderLanePrompt      renderEventLane = "prompt"
	renderLaneTick        renderEventLane = "tick"
)

type renderEventPolicy struct {
	lane              renderEventLane
	flushSmoothing    bool
	flushLogChunks    bool
	flushTaskStreams  bool
	dismissHints      bool
	flushDeferredOnly bool
}

func renderEventPolicyFor(msg tea.Msg) (renderEventPolicy, bool) {
	switch typed := msg.(type) {
	case tuievents.LogChunkMsg:
		return renderEventPolicy{lane: renderLaneLog, flushSmoothing: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.AssistantStreamMsg, tuievents.RawDeltaMsg, tuievents.ReasoningStreamMsg:
		return renderEventPolicy{lane: renderLaneMainStream, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.DiffBlockMsg:
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.TaskStreamMsg:
		return renderEventPolicy{lane: renderLaneToolStream, flushLogChunks: true, dismissHints: true}, true
	case tuievents.ParticipantTurnStartMsg:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.ParticipantToolMsg:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.ParticipantStatusMsg:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true}, true
	case tuievents.SubagentStartMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.SubagentStatusMsg, tuievents.SubagentPlanMsg, tuievents.SubagentDoneMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true}, true
	case tuievents.SubagentStreamMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.SubagentToolCallMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.PlanUpdateMsg, tuievents.SetHintMsg, tuievents.SetRunningMsg,
		tuievents.SetStatusMsg, tuievents.SetCommandsMsg, tuievents.AttachmentCountMsg:
		return renderEventPolicy{lane: renderLaneUIState}, true
	case tuievents.ClearHistoryMsg, tuievents.UserMessageMsg, tuievents.TaskResultMsg:
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true, dismissHints: true}, true
	case tuievents.BTWOverlayMsg:
		return renderEventPolicy{lane: renderLaneOverlay}, true
	case tuievents.BTWErrorMsg:
		return renderEventPolicy{lane: renderLaneOverlay}, true
	case tuievents.PromptRequestMsg:
		return renderEventPolicy{lane: renderLanePrompt, flushSmoothing: true, flushLogChunks: true, flushTaskStreams: true}, true
	case frameTickMsg:
		return renderEventPolicyForFrameTick(typed), true
	case tuievents.TickStatusMsg:
		return renderEventPolicy{lane: renderLaneTick, flushDeferredOnly: true}, true
	default:
		return renderEventPolicy{}, false
	}
}

func renderEventPolicyForFrameTick(msg frameTickMsg) renderEventPolicy {
	if msg.kind == frameTickDeferredBatch {
		return renderEventPolicy{lane: renderLaneTick, flushDeferredOnly: true}
	}
	return renderEventPolicy{lane: renderLaneTick}
}

func (m *Model) applyRenderEventPolicy(policy renderEventPolicy) {
	if m == nil {
		return
	}
	if policy.flushDeferredOnly {
		m.flushPendingDeferredBatches()
		return
	}
	if policy.flushSmoothing {
		m.flushAllPendingStreamSmoothing()
	}
	if policy.dismissHints {
		m.dismissMessageHints()
	}
	if policy.flushLogChunks {
		m.flushPendingLogChunks()
	}
	if policy.flushTaskStreams {
		m.flushPendingTaskStreamMsgs()
	}
}

func (m *Model) deferredBatchingEnabled() bool {
	return m != nil && m.cfg.StreamTickInterval > 0
}

func (m *Model) dispatchRenderEvent(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	policy, ok := renderEventPolicyFor(msg)
	if !ok {
		return m, nil, false
	}
	m.applyRenderEventPolicy(policy)

	switch typed := msg.(type) {
	case tuievents.LogChunkMsg:
		if !m.deferredBatchingEnabled() {
			model, cmd := m.handleLogChunk(typed.Chunk)
			return model, cmd, true
		}
		if !m.queueLogChunk(typed.Chunk) {
			return m, nil, true
		}
		return m, m.ensureDeferredBatchTick(), true

	case tuievents.AssistantStreamMsg:
		if m.cfg.FrameBatchMainStream {
			model, cmd := m.enqueueMainDelta(typed.Kind, typed.Actor, typed.Text, typed.Final)
			return model, cmd, true
		}
		model, cmd := m.handleStreamBlock(typed.Kind, typed.Actor, typed.Text, typed.Final)
		return model, cmd, true

	case tuievents.RawDeltaMsg:
		model, cmd := m.handleRawDelta(typed)
		return model, cmd, true

	case tuievents.ReasoningStreamMsg:
		if m.cfg.FrameBatchMainStream {
			model, cmd := m.enqueueMainDelta("reasoning", typed.Actor, typed.Text, typed.Final)
			return model, cmd, true
		}
		model, cmd := m.handleStreamBlock("reasoning", typed.Actor, typed.Text, typed.Final)
		return model, cmd, true

	case tuievents.DiffBlockMsg:
		model, cmd := m.handleDiffBlock(typed)
		return model, cmd, true

	case tuievents.TaskStreamMsg:
		if m.deferredBatchingEnabled() && shouldBatchTaskStreamMsg(typed) {
			m.queueTaskStreamMsg(typed)
			return m, m.ensureDeferredBatchTick(), true
		}
		model, cmd := m.handleToolStreamMsg(typed)
		return model, cmd, true

	case tuievents.ParticipantTurnStartMsg:
		model, cmd := m.handleParticipantTurnStart(typed)
		return model, cmd, true
	case tuievents.ParticipantToolMsg:
		model, cmd := m.handleParticipantToolMsg(typed)
		return model, cmd, true
	case tuievents.ParticipantStatusMsg:
		model, cmd := m.handleParticipantStatusMsg(typed)
		return model, cmd, true

	case tuievents.SubagentStartMsg:
		model, cmd := m.handleSubagentStart(typed)
		return model, cmd, true
	case tuievents.SubagentStatusMsg:
		model, cmd := m.handleSubagentStatus(typed)
		return model, cmd, true
	case tuievents.SubagentStreamMsg:
		model, cmd := m.handleSubagentStream(typed)
		return model, cmd, true
	case tuievents.SubagentToolCallMsg:
		model, cmd := m.handleSubagentToolCall(typed)
		return model, cmd, true
	case tuievents.SubagentPlanMsg:
		model, cmd := m.handleSubagentPlan(typed)
		return model, cmd, true
	case tuievents.SubagentDoneMsg:
		model, cmd := m.handleSubagentDone(typed)
		return model, cmd, true

	case tuievents.PlanUpdateMsg:
		return m.handlePlanUpdateMsg(typed), nil, true
	case tuievents.SetHintMsg:
		model, cmd := m.handleSetHintMsg(typed)
		return model, cmd, true
	case tuievents.SetRunningMsg:
		return m.handleSetRunningMsg(typed), nil, true
	case tuievents.SetStatusMsg:
		return m.handleSetStatusMsg(typed), nil, true
	case tuievents.SetCommandsMsg:
		return m.handleSetCommandsMsg(typed), nil, true
	case tuievents.AttachmentCountMsg:
		return m.handleAttachmentCountMsg(typed), nil, true

	case tuievents.ClearHistoryMsg:
		m.resetConversationView()
		return m, nil, true
	case tuievents.UserMessageMsg:
		return m.handleUserMessageMsg(typed), nil, true
	case tuievents.TaskResultMsg:
		model, cmd := m.handleTaskResultMsg(typed)
		return model, cmd, true

	case tuievents.BTWOverlayMsg:
		model, cmd := m.handleBTWDelta(typed.Text, typed.Final)
		return model, cmd, true
	case tuievents.BTWErrorMsg:
		return m.handleBTWErrorMsg(typed), nil, true

	case tuievents.PromptRequestMsg:
		m.enqueuePrompt(typed)
		m.ensureViewportLayout()
		return m, nil, true

	case frameTickMsg:
		legacyBroadcast := typed.kind == ""
		var cmds []tea.Cmd
		if legacyBroadcast || typed.kind == frameTickDeferredBatch {
			m.deferredBatchTickScheduled = false
		}
		if legacyBroadcast || typed.kind == frameTickOffscreen {
			hadOffscreenTick := m.offscreenViewportTickScheduled
			m.offscreenViewportTickScheduled = false
			if hadOffscreenTick {
				cmds = append(cmds, m.flushPendingOffscreenViewportSync(typed.at))
			}
		}
		if legacyBroadcast || typed.kind == frameTickStreamSmoothing {
			cmds = append(cmds, m.drainPendingStreamSmoothing(typed.at))
		}
		if legacyBroadcast || typed.kind == frameTickPanelAnimation {
			cmds = append(cmds, m.advancePanelAnimations(typed.at))
		}
		if legacyBroadcast || typed.kind == frameTickScrollbarVisible {
			cmds = append(cmds, m.advanceScrollbarVisibility(typed.at))
		}
		return m, tea.Batch(cmds...), true
	case tuievents.TickStatusMsg:
		model, cmd := m.handleStatusTickMsg()
		return model, cmd, true
	default:
		return m, nil, false
	}
}

func (m *Model) handlePlanUpdateMsg(msg tuievents.PlanUpdateMsg) tea.Model {
	m.planEntries = m.planEntries[:0]
	hasIncomplete := false
	for _, item := range msg.Entries {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		if status != "completed" {
			hasIncomplete = true
		}
		m.planEntries = append(m.planEntries, planEntryState{Content: content, Status: status})
	}
	if !hasIncomplete {
		m.planEntries = m.planEntries[:0]
	}
	m.ensureViewportLayout()
	return m
}

func (m *Model) handleSetHintMsg(msg tuievents.SetHintMsg) (tea.Model, tea.Cmd) {
	after := msg.ClearAfter
	if after <= 0 {
		after = systemHintDuration
	}
	return m, m.showHint(msg.Hint, hintOptions{
		priority:       msg.Priority,
		clearOnMessage: msg.ClearOnMessage,
		clearAfter:     after,
	})
}

func (m *Model) handleSetRunningMsg(msg tuievents.SetRunningMsg) tea.Model {
	wasRunning := m.running
	m.running = msg.Running
	if msg.Running && !wasRunning {
		m.startRunningAnimation()
	}
	if !msg.Running {
		m.stopRunningAnimation()
		m.runStartedAt = time.Time{}
	}
	return m
}

func (m *Model) handleSetStatusMsg(msg tuievents.SetStatusMsg) tea.Model {
	if workspace := strings.TrimSpace(msg.Workspace); workspace != "" {
		m.cfg.Workspace = workspace
	}
	if strings.TrimSpace(msg.Model) != "" {
		m.statusModel = msg.Model
	}
	m.statusContext = strings.TrimSpace(msg.Context)
	return m
}

func (m *Model) handleSetCommandsMsg(msg tuievents.SetCommandsMsg) tea.Model {
	m.setCommands(msg.Commands)
	return m
}

func (m *Model) handleAttachmentCountMsg(msg tuievents.AttachmentCountMsg) tea.Model {
	if msg.Count <= 0 {
		m.clearInputAttachments()
		m.dismissVisibleHint()
	} else {
		m.syncAttachmentSummary()
	}
	m.syncTextareaChrome()
	m.ensureViewportLayout()
	return m
}

func (m *Model) handleUserMessageMsg(msg tuievents.UserMessageMsg) tea.Model {
	m.dequeuePendingUserMessage(msg.Text)
	if m.activeActivityID != "" {
		_ = m.finalizeActivityBlock()
	}
	m.commitUserDisplayLine(msg.Text)
	m.ensureViewportLayout()
	m.syncViewportContent()
	return m
}

func (m *Model) handleBTWErrorMsg(msg tuievents.BTWErrorMsg) tea.Model {
	if m.btwOverlay == nil && m.btwDismissed {
		return m
	}
	m.dropPendingStreamSmoothing(streamSmoothingKey("btw", "", "answer", ""))
	m.applyBTWOverlayImmediate(msg.Text, true)
	return m
}

func (m *Model) handleStatusTickMsg() (tea.Model, tea.Cmd) {
	if m.cfg.RefreshWorkspace != nil {
		if workspace := strings.TrimSpace(m.cfg.RefreshWorkspace()); workspace != "" {
			m.cfg.Workspace = workspace
		}
	}
	if m.cfg.RefreshStatus != nil {
		modelText, contextText := m.cfg.RefreshStatus()
		if strings.TrimSpace(modelText) != "" {
			m.statusModel = modelText
		}
		m.statusContext = strings.TrimSpace(contextText)
	}
	return m, tickStatusCmd()
}

func (m *Model) handleTaskResultMsg(msg tuievents.TaskResultMsg) (tea.Model, tea.Cmd) {
	if msg.ContinueRunning {
		if msg.Err != nil {
			m.pendingQueue = nil
			errLine := "error: " + msg.Err.Error()
			m.commitLine(errLine)
			m.ensureViewportLayout()
			m.syncViewportContent()
		}
		return m, nil
	}
	if msg.Interrupted {
		m.discardActiveAssistantStream()
	} else {
		m.flushStream()
		m.finalizeAssistantBlock()
		m.finalizeReasoningBlock()
	}
	if msg.SuppressTurnDivider {
		m.finalizeActiveParticipantTurn(msg.Interrupted, msg.Err)
	}
	_ = m.finalizeActivityBlock()
	if !m.runStartedAt.IsZero() {
		m.lastRunDuration = time.Since(m.runStartedAt)
		m.hasLastRunDuration = true
		m.runStartedAt = time.Time{}
	}
	m.running = false
	m.stopRunningAnimation()
	m.pendingQueue = nil
	m.planEntries = m.planEntries[:0]
	m.clearInputAttachments()
	m.syncTextareaChrome()
	m.clearInputOverlays()
	if msg.Err != nil && !msg.Interrupted {
		errText := strings.TrimSpace(msg.Err.Error())
		isPromptCancel := errText == "cli: input interrupted" ||
			errText == "cli: input eof" ||
			errText == tuievents.PromptErrInterrupt ||
			errText == tuievents.PromptErrEOF
		if !isPromptCancel {
			errLine := "error: " + msg.Err.Error()
			m.commitLine(errLine)
		}
	}
	if m.showTurnDivider && !msg.SuppressTurnDivider && m.doc.Len() > 0 {
		last := m.doc.Last()
		hasContent := false
		if last != nil {
			if tb, ok := last.(*TranscriptBlock); ok {
				hasContent = strings.TrimSpace(tb.Raw) != ""
			} else {
				hasContent = true
			}
		}
		if hasContent {
			m.doc.Append(NewDividerBlock(m.userTurnDividerLabel()))
		}
	}
	m.showTurnDivider = false
	m.ensureViewportLayout()
	m.syncViewportContent()
	if msg.ExitNow {
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}
