package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// Log chunk handling — inline commit architecture
// ---------------------------------------------------------------------------

func (m *Model) handleLogChunk(chunk string) (tea.Model, tea.Cmd) {
	if chunk == "" {
		return m, nil
	}

	chunk = tuikit.SanitizeLogText(chunk)
	normalized := strings.ReplaceAll(strings.ReplaceAll(chunk, "\r\n", "\n"), "\r", "\n")

	m.streamLine += normalized

	for {
		idx := strings.IndexByte(m.streamLine, '\n')
		if idx < 0 {
			break
		}
		line := m.streamLine[:idx]
		m.streamLine = m.streamLine[idx+1:]
		if strings.TrimSpace(line) != "" && m.transientBlockID != "" && m.transientRemove && !isTransientWarningLine(line) {
			m.removeTransientLogLine()
		}
		if m.tryMergeMutationSummaryLine(line) {
			if strings.TrimSpace(line) != "" {
				m.finalizeAssistantBlock()
				m.finalizeReasoningBlock()
				m.lastFinalAnswer = ""
			}
			continue
		}
		if m.consumeActivityLine(line) {
			if strings.TrimSpace(line) != "" {
				m.finalizeAssistantBlock()
				m.lastFinalAnswer = ""
			}
			continue
		}
		if strings.TrimSpace(line) != "" {
			m.finalizeAssistantBlock()
			m.finalizeReasoningBlock()
			m.lastFinalAnswer = ""
		}
		m.commitLine(line)
	}

	m.syncViewportContent()
	return m, nil
}

func (m *Model) tryMergeMutationSummaryLine(line string) bool {
	merged, ok := mergedMutationToolLine(m.lastCommittedRaw, line)
	if !ok || m.doc.Len() == 0 {
		return false
	}
	last := m.doc.Last()
	if last == nil {
		return false
	}
	tb, ok := last.(*TranscriptBlock)
	if !ok {
		return false
	}
	style := tuikit.DetectLineStyleWithContext(merged, m.lastCommittedStyle)
	tb.Raw = merged
	tb.Style = style
	m.lastCommittedStyle = style
	m.lastCommittedRaw = merged
	m.hasCommittedLine = true
	return true
}

func mergedMutationToolLine(previous string, current string) (string, bool) {
	prevTrimmed := strings.TrimSpace(previous)
	currTrimmed := strings.TrimSpace(current)
	if prevTrimmed == "" || currTrimmed == "" {
		return "", false
	}
	if !strings.HasPrefix(prevTrimmed, "▸ ") || !strings.HasPrefix(currTrimmed, "✓ ") {
		return "", false
	}
	prevRest := strings.TrimSpace(strings.TrimPrefix(prevTrimmed, "▸ "))
	currRest := strings.TrimSpace(strings.TrimPrefix(currTrimmed, "✓ "))
	prevParts := strings.SplitN(prevRest, " ", 2)
	currParts := strings.SplitN(currRest, " ", 2)
	if len(prevParts) != 2 || len(currParts) != 2 {
		return "", false
	}
	toolName := strings.ToUpper(strings.TrimSpace(prevParts[0]))
	if toolName != "PATCH" && toolName != "WRITE" {
		return "", false
	}
	if !strings.EqualFold(toolName, strings.TrimSpace(currParts[0])) {
		return "", false
	}
	summary := strings.TrimSpace(currParts[1])
	fields := strings.Fields(summary)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "+") || !strings.HasPrefix(fields[1], "-") {
		return "", false
	}
	return prevTrimmed + " " + summary, true
}

func (m *Model) finalizeAssistantBlock() {
	m.activeAssistantID = ""
}

func (m *Model) discardActiveAssistantStream() {
	m.streamLine = ""
	m.lastFinalAnswer = ""
	// Remove active assistant block from doc.
	if m.activeAssistantID != "" {
		m.doc.Remove(m.activeAssistantID)
		m.activeAssistantID = ""
	}
	// Remove active reasoning block from doc.
	if m.activeReasoningID != "" {
		m.doc.Remove(m.activeReasoningID)
		m.activeReasoningID = ""
	}
	m.syncViewportContent()
}

func normalizeStreamKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "reasoning", "thinking":
		return "reasoning"
	default:
		return "answer"
	}
}

func (m *Model) handleStreamBlock(kind string, text string, final bool) (tea.Model, tea.Cmd) {
	streamKind := normalizeStreamKind(kind)
	if m.activeActivityID != "" && streamKind == "answer" && strings.TrimSpace(text) != "" {
		m.finalizeActivityBlock()
	}
	if text == "" && !(streamKind == "reasoning" && final) {
		return m, nil
	}

	if streamKind == "reasoning" {
		return m.handleReasoningStream(text, final)
	}
	return m.handleAnswerStream(text, final)
}

func (m *Model) handleAnswerStream(text string, final bool) (tea.Model, tea.Cmd) {
	if final && m.activeAssistantID == "" {
		normalized := strings.TrimSpace(text)
		if normalized != "" && normalized == m.lastFinalAnswer {
			return m, nil
		}
	}

	if m.activeAssistantID == "" {
		block := NewAssistantBlock()
		block.Raw = text
		m.doc.Append(block)
		m.activeAssistantID = block.BlockID()
		m.hasCommittedLine = true
		m.lastCommittedStyle = tuikit.LineStyleAssistant
		m.lastCommittedRaw = "* "
		if final {
			m.activeAssistantID = ""
			m.lastFinalAnswer = strings.TrimSpace(text)
		}
		m.syncViewportContent()
		return m, nil
	}

	block := m.doc.Find(m.activeAssistantID)
	if block == nil {
		m.activeAssistantID = ""
		return m, nil
	}
	ab := block.(*AssistantBlock)
	ab.Raw = mergeStreamChunk(ab.Raw, text, final)
	if final {
		m.activeAssistantID = ""
		m.lastFinalAnswer = strings.TrimSpace(ab.Raw)
	}
	m.lastCommittedStyle = tuikit.LineStyleAssistant
	m.lastCommittedRaw = "* "
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleReasoningStream(text string, final bool) (tea.Model, tea.Cmd) {
	if final {
		if m.activeReasoningID != "" {
			m.doc.Remove(m.activeReasoningID)
			m.activeReasoningID = ""
			m.refreshHistoryTailState()
			m.syncViewportContent()
		}
		return m, nil
	}

	if m.activeReasoningID == "" {
		block := NewReasoningBlock()
		block.Raw = text
		m.doc.Append(block)
		m.activeReasoningID = block.BlockID()
		m.hasCommittedLine = true
		m.lastCommittedStyle = tuikit.LineStyleReasoning
		m.lastCommittedRaw = "│ "
		m.syncViewportContent()
		return m, nil
	}

	block := m.doc.Find(m.activeReasoningID)
	if block == nil {
		m.activeReasoningID = ""
		return m, nil
	}
	rb := block.(*ReasoningBlock)
	rb.Raw = mergeStreamChunk(rb.Raw, text, final)
	m.lastCommittedStyle = tuikit.LineStyleReasoning
	m.lastCommittedRaw = "│ "
	m.syncViewportContent()
	return m, nil
}

const minReplayLen = 16

func mergeStreamChunk(existing string, incoming string, final bool) string {
	if final {
		incoming = strings.TrimSpace(incoming)
		if incoming == "" {
			return existing
		}
		return incoming
	}
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	if len(incoming) >= minReplayLen && strings.HasPrefix(existing, incoming) {
		return existing
	}
	return existing + incoming
}

func (m *Model) finalizeReasoningBlock() {
	m.activeReasoningID = ""
}

func (m *Model) handleDiffBlock(msg tuievents.DiffBlockMsg) (tea.Model, tea.Cmd) {
	m.flushStream()
	m.finalizeActivityBlock()
	m.finalizeAssistantBlock()
	m.finalizeReasoningBlock()
	block := NewDiffBlock(msg)
	m.doc.Append(block)
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleToolStreamMsg(msg tuievents.TaskStreamMsg) (tea.Model, tea.Cmd) {
	m.finalizeActivityBlock()
	toolName := strings.TrimSpace(msg.Label)
	if toolName == "" {
		toolName = strings.TrimSpace(msg.Tool)
	}
	if strings.EqualFold(toolName, "SPAWN") {
		return m, nil
	}
	nextKey := m.toolOutputKey(msg)

	// When a TaskID is present and this isn't a fresh tool invocation
	// (Reset=true), the message is a follow-up to an existing background
	// task (watch, wait, write, cancel).  Only update the panel that was
	// created by the original BASH call — never create a new one.
	var panel *BashPanelBlock
	if strings.TrimSpace(msg.TaskID) != "" && !msg.Reset {
		panel = m.findBashPanelBlock(nextKey)
	} else {
		panel = m.ensureBashPanelBlock(nextKey, toolName, strings.TrimSpace(msg.CallID), msg.Reset)
	}
	if panel == nil {
		return m, nil
	}
	if strings.TrimSpace(msg.Chunk) != "" {
		m.appendBashPanelChunk(panel, strings.TrimSpace(msg.Stream), msg.Chunk)
	}
	m.applyBashPanelState(panel, msg.State, msg.Final)
	if msg.Final && isTerminalToolOutputState(panel.State) {
		panel.Active = false
		if isInlineBashPanel(panel) {
			panel.Expanded = false
			m.syncInlineBashAnchorState(panel)
		}
		m.syncViewportContent()
		return m, nil
	}
	m.syncViewportContent()
	return m, nil
}

// renderAssistantBlockLines renders assistant content for use in block Render.
// Kept as a Model method for access to theme and viewport width.
func (m *Model) renderAssistantBlockLines(raw string) []string {
	nls, plainRows := buildNarrativeRows(raw)
	if len(plainRows) == 0 {
		return []string{tuikit.ColorizeLogLine("* ", tuikit.LineStyleAssistant, m.theme)}
	}
	lines := make([]string, len(plainRows))
	for i, pr := range plainRows {
		plain := pr
		if i == 0 {
			plain = "* " + pr
		}
		lines[i] = styleNarrativeLine(plain, nls[i].Kind, tuikit.LineStyleAssistant, m.theme)
	}
	return lines
}

func (m *Model) renderReasoningBlockLines(raw string) []string {
	nls, plainRows := buildNarrativeRows(raw)
	if len(plainRows) == 0 {
		return []string{tuikit.ColorizeLogLine("· ", tuikit.LineStyleReasoning, m.theme)}
	}
	lines := make([]string, len(plainRows))
	for i, pr := range plainRows {
		prefix := "  "
		if i == 0 {
			prefix = "· "
		}
		plain := prefix + pr
		lines[i] = styleNarrativeLine(plain, nls[i].Kind, tuikit.LineStyleReasoning, m.theme)
	}
	return lines
}

func (m *Model) resetConversationView() {
	m.flushStream()
	m.activeAssistantID = ""
	m.activeReasoningID = ""
	m.activeActivityID = ""
	m.transientBlockID = ""
	m.toolOutputBlockIDs = nil
	m.subagentBlockIDs = nil
	m.pendingToolAnchors = nil
	m.callAnchorIndex = nil
	m.taskOriginCallID = nil
	m.doc.Clear()
	m.viewportStyledLines = m.viewportStyledLines[:0]
	m.viewportPlainLines = m.viewportPlainLines[:0]
	m.hasCommittedLine = false
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.lastFinalAnswer = ""
	m.transientIsRetry = false
	m.pendingQueue = nil
	m.hintEntries = nil
	m.hint = ""
	m.runStartedAt = time.Time{}
	m.lastRunDuration = 0
	m.hasLastRunDuration = false
	m.clearSelection()
	m.clearInputSelection()
	m.userScrolledUp = false
	if m.cfg.ShowWelcomeCard {
		if m.viewport.Width() > 0 {
			m.appendWelcomeCard()
			m.welcomeCardPending = false
		} else {
			m.welcomeCardPending = true
		}
	}
	m.syncViewportContent()
}

func (m *Model) refreshHistoryTailState() {
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.hasCommittedLine = false
	blocks := m.doc.Blocks()
	for i := len(blocks) - 1; i >= 0; i-- {
		tb, ok := blocks[i].(*TranscriptBlock)
		if !ok {
			// Non-transcript blocks (assistant, diff, etc.) count as committed content.
			m.hasCommittedLine = true
			continue
		}
		raw := tb.Raw
		if strings.TrimSpace(raw) == "" {
			continue
		}
		m.lastCommittedRaw = raw
		m.lastCommittedStyle = tuikit.DetectLineStyle(raw)
		m.hasCommittedLine = true
		return
	}
}

// commitLine colorizes one complete line and appends it to the document.
func (m *Model) commitLine(line string) {
	if strings.TrimSpace(line) == "" && !m.hasCommittedLine {
		return
	}

	style := tuikit.DetectLineStyleWithContext(line, m.lastCommittedStyle)
	isEphemeralWarn := isTransientWarningLine(line)
	isRetry := tuikit.IsRetryLine(line) && !isEphemeralWarn
	isWarn := !isRetry && style == tuikit.LineStyleWarn

	// --- Transient log replacement ---
	if isRetry && m.transientBlockID != "" && m.transientIsRetry {
		if tb := m.findTranscriptBlock(m.transientBlockID); tb != nil {
			tb.Raw = line
			tb.Style = style
			m.lastCommittedStyle = style
			m.lastCommittedRaw = line
			m.transientRemove = false
			m.syncViewportContent()
			return
		}
	}
	if isWarn && m.transientBlockID != "" && !m.transientIsRetry {
		if tb := m.findTranscriptBlock(m.transientBlockID); tb != nil {
			tb.Raw = line
			tb.Style = style
			m.lastCommittedStyle = style
			m.lastCommittedRaw = line
			m.transientRemove = isEphemeralWarn
			m.syncViewportContent()
			return
		}
	}

	if m.transientBlockID != "" && m.transientRemove {
		m.removeTransientLogLine()
	}

	m.transientBlockID = ""
	m.transientRemove = false

	if m.hasCommittedLine {
		m.insertSpacing(style, line)
	}

	block := NewTranscriptBlock(line, style)
	m.doc.Append(block)

	// Track tool call start lines as anchor points for panel insertion.
	if style == tuikit.LineStyleTool {
		if toolName, ok := extractToolCallName(line); ok && panelProducingTools[toolName] {
			m.pendingToolAnchors = append(m.pendingToolAnchors, toolAnchor{
				blockID:  block.BlockID(),
				toolName: toolName,
			})
		}
	}

	if isRetry {
		m.transientBlockID = block.BlockID()
		m.transientIsRetry = true
		m.transientRemove = false
	} else if isWarn {
		m.transientBlockID = block.BlockID()
		m.transientIsRetry = false
		m.transientRemove = isEphemeralWarn
	}

	m.lastCommittedStyle = style
	m.lastCommittedRaw = line
	m.hasCommittedLine = true
}

func (m *Model) findTranscriptBlock(id string) *TranscriptBlock {
	b := m.doc.Find(id)
	if b == nil {
		return nil
	}
	tb, ok := b.(*TranscriptBlock)
	if !ok {
		return nil
	}
	return tb
}

func isTransientWarningLine(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(ansi.Strip(line)))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "rate limit") || strings.Contains(normalized, "too many requests") {
		return true
	}
	if strings.Contains(normalized, "retrying in") && strings.Contains(normalized, "waiting longer before retrying") {
		return true
	}
	return false
}

func (m *Model) removeTransientLogLine() {
	if m.transientBlockID == "" {
		return
	}
	m.doc.Remove(m.transientBlockID)
	m.transientBlockID = ""
	m.transientRemove = false
	m.refreshHistoryTailState()
}

func (m *Model) insertSpacing(style tuikit.LineStyle, line string) {
	if m.doc.Len() == 0 {
		return
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	if strings.TrimSpace(m.lastCommittedRaw) == "" {
		return
	}
	// Check if last block already produces empty content.
	last := m.doc.Last()
	if last != nil {
		if tb, ok := last.(*TranscriptBlock); ok && strings.TrimSpace(tb.Raw) == "" {
			return
		}
	}
	if shouldInsertBlockGap(m.lastCommittedStyle, style) {
		m.doc.Append(NewSpacerBlock())
	}
}

func shouldInsertBlockGap(prev tuikit.LineStyle, current tuikit.LineStyle) bool {
	if prev == tuikit.LineStyleDefault || current == tuikit.LineStyleDefault {
		return false
	}
	if current == tuikit.LineStyleUser {
		return true
	}
	return false
}

// flushStream commits any remaining partial line in the stream buffer.
func (m *Model) flushStream() {
	if strings.TrimSpace(m.streamLine) == "" {
		m.streamLine = ""
		return
	}
	m.commitLine(m.streamLine)
	m.streamLine = ""
}
