package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
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
	dismissHints      bool
	flushDeferredOnly bool
}

func renderEventPolicyFor(msg tea.Msg) (renderEventPolicy, bool) {
	switch typed := msg.(type) {
	case appgateway.EventEnvelope:
		return renderEventPolicyForGatewayEnvelope(typed), true
	case TranscriptEventsMsg:
		return renderEventPolicyForTranscriptEvents(typed), true
	case LogChunkMsg:
		return renderEventPolicy{lane: renderLaneLog, flushSmoothing: true, dismissHints: true}, true
	case AssistantStreamMsg, RawDeltaMsg, ReasoningStreamMsg:
		return renderEventPolicy{lane: renderLaneMainStream, flushLogChunks: true, dismissHints: true}, true
	case ParticipantStatusMsg:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true}, true
	case ACPProjectionMsg:
		switch typed.Scope {
		case ACPProjectionMain:
			return renderEventPolicy{lane: renderLaneMainStream, flushSmoothing: true, flushLogChunks: true, dismissHints: true}, true
		case ACPProjectionParticipant:
			return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, dismissHints: true}, true
		case ACPProjectionSubagent:
			return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, dismissHints: true}, true
		default:
			return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true}, true
		}
	case SubagentStartMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, dismissHints: true}, true
	case SubagentStatusMsg, SubagentDoneMsg:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true}, true
	case PlanUpdateMsg, SetHintMsg, SetRunningMsg,
		SetStatusMsg, SetCommandsMsg, AttachmentCountMsg:
		return renderEventPolicy{lane: renderLaneUIState}, true
	case ClearHistoryMsg, UserMessageMsg, TaskResultMsg:
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, dismissHints: true}, true
	case BTWOverlayMsg:
		return renderEventPolicy{lane: renderLaneOverlay}, true
	case BTWErrorMsg:
		return renderEventPolicy{lane: renderLaneOverlay}, true
	case PromptRequestMsg:
		return renderEventPolicy{lane: renderLanePrompt, flushSmoothing: true, flushLogChunks: true}, true
	case frameTickMsg:
		return renderEventPolicyForFrameTick(typed), true
	case TickStatusMsg:
		return renderEventPolicy{lane: renderLaneTick, flushDeferredOnly: true}, true
	default:
		return renderEventPolicy{}, false
	}
}

func renderEventPolicyForGatewayEnvelope(env appgateway.EventEnvelope) renderEventPolicy {
	if env.Err != nil {
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	}
	switch env.Event.Kind {
	case appgateway.EventKindAssistantMessage, appgateway.EventKindUserMessage:
		return renderEventPolicy{lane: renderLaneMainStream, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case appgateway.EventKindToolCall, appgateway.EventKindToolResult, appgateway.EventKindApprovalRequested:
		return renderEventPolicy{lane: renderLaneToolStream, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case appgateway.EventKindPlanUpdate:
		return renderEventPolicy{lane: renderLaneUIState, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case appgateway.EventKindParticipant:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case appgateway.EventKindLifecycle, appgateway.EventKindSessionLifecycle:
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	default:
		return renderEventPolicy{lane: renderLaneLifecycle, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	}
}

func renderEventPolicyForTranscriptEvents(msg TranscriptEventsMsg) renderEventPolicy {
	if len(msg.Events) == 0 {
		return renderEventPolicy{lane: renderLaneLifecycle}
	}
	hasParticipant := false
	hasSubagent := false
	hasTool := false
	for _, event := range msg.Events {
		switch event.Scope {
		case ACPProjectionSubagent:
			hasSubagent = true
		case ACPProjectionParticipant:
			hasParticipant = true
		}
		if event.Kind == TranscriptEventTool || event.Kind == TranscriptEventApproval {
			hasTool = true
		}
	}
	switch {
	case hasSubagent:
		return renderEventPolicy{lane: renderLaneSubagent, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case hasParticipant:
		return renderEventPolicy{lane: renderLaneParticipant, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	case hasTool:
		return renderEventPolicy{lane: renderLaneToolStream, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	default:
		return renderEventPolicy{lane: renderLaneMainStream, flushSmoothing: true, flushLogChunks: true, dismissHints: true}
	}
}

func renderEventPolicyForFrameTick(msg frameTickMsg) renderEventPolicy {
	if msg.kind == frameTickDeferredBatch {
		return renderEventPolicy{lane: renderLaneTick, flushDeferredOnly: true}
	}
	return renderEventPolicy{lane: renderLaneTick}
}

func (m *Model) applyRenderEventPolicy(policy renderEventPolicy) tea.Cmd {
	if m == nil {
		return nil
	}
	var cmds []tea.Cmd
	if policy.flushDeferredOnly {
		return m.flushPendingDeferredBatches()
	}
	if policy.flushSmoothing {
		m.flushAllPendingStreamSmoothing()
	}
	if policy.dismissHints {
		m.dismissMessageHints()
	}
	if policy.flushLogChunks {
		cmds = append(cmds, m.flushPendingLogChunks())
	}
	return tea.Batch(cmds...)
}

func (m *Model) deferredBatchingEnabled() bool {
	return m != nil && m.cfg.StreamTickInterval > 0
}

func (m *Model) dispatchRenderEvent(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	policy, ok := renderEventPolicyFor(msg)
	if !ok {
		return m, nil, false
	}
	if shouldInvalidateUserDisplayDedup(msg) {
		m.invalidateUserDisplayDedup()
	}
	policyCmd := m.applyRenderEventPolicy(policy)

	switch typed := msg.(type) {
	case appgateway.EventEnvelope:
		model, cmd := m.handleGatewayEventEnvelope(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case TranscriptEventsMsg:
		model, cmd := m.handleTranscriptEventsMsg(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case LogChunkMsg:
		if !m.deferredBatchingEnabled() {
			model, cmd := m.handleLogChunk(typed.Chunk)
			return model, tea.Batch(policyCmd, cmd), true
		}
		if !m.queueLogChunk(typed.Chunk) {
			return m, policyCmd, true
		}
		return m, tea.Batch(policyCmd, m.ensureDeferredBatchTick()), true

	case AssistantStreamMsg:
		if m.cfg.FrameBatchMainStream {
			model, cmd := m.enqueueMainDelta(typed.Kind, typed.Actor, typed.Text, typed.Final)
			return model, tea.Batch(policyCmd, cmd), true
		}
		model, cmd := m.handleStreamBlock(typed.Kind, typed.Actor, typed.Text, typed.Final)
		return model, tea.Batch(policyCmd, cmd), true

	case RawDeltaMsg:
		model, cmd := m.handleRawDelta(typed)
		return model, tea.Batch(policyCmd, cmd), true

	case ReasoningStreamMsg:
		if m.cfg.FrameBatchMainStream {
			model, cmd := m.enqueueMainDelta("reasoning", typed.Actor, typed.Text, typed.Final)
			return model, tea.Batch(policyCmd, cmd), true
		}
		model, cmd := m.handleStreamBlock("reasoning", typed.Actor, typed.Text, typed.Final)
		return model, tea.Batch(policyCmd, cmd), true

	case ParticipantStatusMsg:
		model, cmd := m.handleParticipantStatusMsg(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case ACPProjectionMsg:
		model, cmd := m.handleACPProjection(typed)
		return model, tea.Batch(policyCmd, cmd), true

	case SubagentStartMsg:
		model, cmd := m.handleSubagentStart(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case SubagentStatusMsg:
		model, cmd := m.handleSubagentStatus(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case SubagentDoneMsg:
		model, cmd := m.handleSubagentDone(typed)
		return model, tea.Batch(policyCmd, cmd), true

	case PlanUpdateMsg:
		return m.handlePlanUpdateMsg(typed), policyCmd, true
	case SetHintMsg:
		model, cmd := m.handleSetHintMsg(typed)
		return model, tea.Batch(policyCmd, cmd), true
	case SetRunningMsg:
		return m.handleSetRunningMsg(typed), policyCmd, true
	case SetStatusMsg:
		return m.handleSetStatusMsg(typed), policyCmd, true
	case SetCommandsMsg:
		return m.handleSetCommandsMsg(typed), policyCmd, true
	case AttachmentCountMsg:
		return m.handleAttachmentCountMsg(typed), policyCmd, true

	case ClearHistoryMsg:
		m.resetConversationView()
		return m, policyCmd, true
	case UserMessageMsg:
		return m.handleUserMessageMsg(typed), policyCmd, true
	case TaskResultMsg:
		model, cmd := m.handleTaskResultMsg(typed)
		return model, tea.Batch(policyCmd, cmd), true

	case BTWOverlayMsg:
		model, cmd := m.handleBTWDelta(typed.Text, typed.Final)
		return model, tea.Batch(policyCmd, cmd), true
	case BTWErrorMsg:
		return m.handleBTWErrorMsg(typed), policyCmd, true

	case PromptRequestMsg:
		m.enqueuePrompt(typed)
		m.ensureViewportLayout()
		return m, policyCmd, true

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
		return m, tea.Batch(append(cmds, policyCmd)...), true
	case TickStatusMsg:
		model, cmd := m.handleStatusTickMsg()
		return model, tea.Batch(policyCmd, cmd), true
	default:
		return m, nil, false
	}
}

func (m *Model) invalidateUserDisplayDedup() {
	if m == nil {
		return
	}
	m.userDisplayDedupOK = false
}

func shouldInvalidateUserDisplayDedup(msg tea.Msg) bool {
	switch msg.(type) {
	case AssistantStreamMsg,
		RawDeltaMsg,
		ReasoningStreamMsg,
		TranscriptEventsMsg,
		ParticipantStatusMsg,
		ACPProjectionMsg,
		SubagentStatusMsg,
		SubagentDoneMsg:
		return true
	default:
		return false
	}
}

func (m *Model) handlePlanUpdateMsg(msg PlanUpdateMsg) tea.Model {
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

func (m *Model) handleSetHintMsg(msg SetHintMsg) (tea.Model, tea.Cmd) {
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

func (m *Model) handleSetRunningMsg(msg SetRunningMsg) tea.Model {
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

func (m *Model) handleSetStatusMsg(msg SetStatusMsg) tea.Model {
	if workspace := strings.TrimSpace(msg.Workspace); workspace != "" {
		m.cfg.Workspace = workspace
	}
	if strings.TrimSpace(msg.Model) != "" {
		m.statusModel = msg.Model
	}
	m.statusContext = strings.TrimSpace(msg.Context)
	m.syncWelcomeCardBlock()
	m.syncViewportContent()
	return m
}

func (m *Model) handleSetCommandsMsg(msg SetCommandsMsg) tea.Model {
	m.setCommands(msg.Commands)
	return m
}

func (m *Model) handleAttachmentCountMsg(msg AttachmentCountMsg) tea.Model {
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

func (m *Model) handleUserMessageMsg(msg UserMessageMsg) tea.Model {
	m.dequeuePendingUserMessage(msg.Text)
	m.finalizeActiveMainACPTurn(false, nil)
	m.commitUserDisplayLine(msg.Text)
	m.ensureViewportLayout()
	m.syncViewportContent()
	return m
}

func (m *Model) handleBTWErrorMsg(msg BTWErrorMsg) tea.Model {
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
	m.syncWelcomeCardBlock()
	m.syncViewportContent()
	return m, tickStatusCmd()
}

func (m *Model) handleTaskResultMsg(msg TaskResultMsg) (tea.Model, tea.Cmd) {
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
	m.finalizeActiveMainACPTurn(msg.Interrupted, msg.Err)
	if msg.SuppressTurnDivider {
		m.finalizeActiveParticipantTurn(msg.Interrupted, msg.Err)
	}
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
			errText == PromptErrInterrupt ||
			errText == PromptErrEOF
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
