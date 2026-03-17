package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuidiff"
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

	// Sanitize incoming text.
	chunk = tuikit.SanitizeLogText(chunk)
	normalized := strings.ReplaceAll(strings.ReplaceAll(chunk, "\r\n", "\n"), "\r", "\n")

	m.streamLine += normalized

	// Commit all complete lines (those terminated by \n).
	for {
		idx := strings.IndexByte(m.streamLine, '\n')
		if idx < 0 {
			break
		}
		line := m.streamLine[:idx]
		m.streamLine = m.streamLine[idx+1:]
		if strings.TrimSpace(line) != "" && m.transientLogIdx >= 0 && m.transientRemove && !isTransientWarningLine(line) {
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
			// Non-stream log lines (tool calls/results/system lines) delimit
			// assistant streaming blocks. Without this, reasoning can keep
			// accumulating across tool turns.
			m.finalizeAssistantBlock()
			m.finalizeReasoningBlock()
			m.lastFinalAnswer = ""
		}
		m.commitLine(line)
	}

	m.syncViewportContent()
	return m, m.maybeStartClosingToolOutputFades()
}

func (m *Model) tryMergeMutationSummaryLine(line string) bool {
	merged, ok := mergedMutationToolLine(m.lastCommittedRaw, line)
	if !ok || len(m.historyLines) == 0 {
		return false
	}
	style := tuikit.DetectLineStyleWithContext(merged, m.lastCommittedStyle)
	colored := tuikit.ColorizeLogLine(merged, style, m.theme)
	colored = tuikit.LineExtraGutter(style) + colored
	m.historyLines[len(m.historyLines)-1] = colored
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
	if m.assistantBlock == nil {
		return
	}
	m.assistantBlock = nil
}

func (m *Model) discardActiveAssistantStream() {
	m.streamLine = ""
	m.lastFinalAnswer = ""
	first, second := &m.assistantBlock, &m.reasoningBlock
	if m.assistantBlock != nil && m.reasoningBlock != nil && m.reasoningBlock.start > m.assistantBlock.start {
		first, second = &m.reasoningBlock, &m.assistantBlock
	}
	m.discardAssistantBlock(first)
	m.discardAssistantBlock(second)
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
	if m.activityBlock != nil && streamKind == "answer" && strings.TrimSpace(text) != "" {
		m.finalizeActivityBlock()
	}
	if text == "" && !(streamKind == "reasoning" && final) {
		return m, nil
	}
	blockStyle := tuikit.LineStyleAssistant
	blockMarker := "* "
	renderAssistant := func(raw string) []string {
		return m.renderAssistantBlockLines(raw)
	}

	var activeBlock **assistantBlockState
	if streamKind == "reasoning" {
		blockStyle = tuikit.LineStyleReasoning
		blockMarker = "│ "
		activeBlock = &m.reasoningBlock
	} else {
		activeBlock = &m.assistantBlock
	}
	if streamKind == "answer" && final && *activeBlock == nil {
		normalized := strings.TrimSpace(text)
		if normalized != "" && normalized == m.lastFinalAnswer {
			// Drop duplicated terminal answer events.
			return m, nil
		}
	}
	if streamKind == "reasoning" && final {
		if *activeBlock != nil {
			m.discardAssistantBlock(activeBlock)
			m.refreshHistoryTailState()
			m.syncViewportContent()
		}
		return m, m.maybeStartClosingToolOutputFades()
	}
	if *activeBlock == nil {
		start := len(m.historyLines)
		lines := m.renderReasoningBlockLines(text)
		if streamKind == "answer" {
			lines = renderAssistant(text)
		}
		m.historyLines = append(m.historyLines, lines...)
		*activeBlock = &assistantBlockState{
			start: start,
			end:   start + len(lines),
			raw:   text,
		}
		m.hasCommittedLine = true
		m.lastCommittedStyle = blockStyle
		m.lastCommittedRaw = blockMarker
		if final {
			*activeBlock = nil
			if streamKind == "answer" {
				m.lastFinalAnswer = strings.TrimSpace(text)
			}
		}
		m.syncViewportContent()
		return m, m.maybeStartClosingToolOutputFades()
	}
	block := *activeBlock
	block.raw = mergeStreamChunk(block.raw, text, final)
	lines := m.renderReasoningBlockLines(block.raw)
	if streamKind == "answer" {
		lines = renderAssistant(block.raw)
	}
	m.replaceHistoryRange(block.start, block.end, lines)
	block.end = block.start + len(lines)
	if final {
		*activeBlock = nil
		if streamKind == "answer" {
			m.lastFinalAnswer = strings.TrimSpace(block.raw)
		}
	}
	m.lastCommittedStyle = blockStyle
	m.lastCommittedRaw = blockMarker
	m.syncViewportContent()
	return m, m.maybeStartClosingToolOutputFades()
}

// minReplayLen is the minimum byte length for an incoming chunk to be
// considered a replayed older cumulative snapshot.  Short delta tokens
// (e.g. "#", "- ", "**", "\n") frequently coincide with the opening
// characters of the accumulated text and must not be dropped.
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
	if strings.HasPrefix(incoming, existing) {
		// Cumulative stream chunk: incoming includes all previous text.
		return incoming
	}
	// Only treat as a replayed older cumulative snapshot when the incoming
	// text is long enough to be a credible replay, not a short delta token
	// that coincidentally matches the opening characters of the buffer.
	if len(incoming) >= minReplayLen && strings.HasPrefix(existing, incoming) {
		return existing
	}
	return existing + incoming
}

func (m *Model) discardAssistantBlock(block **assistantBlockState) {
	if block == nil || *block == nil {
		return
	}
	current := *block
	start := current.start
	end := current.end
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(m.historyLines) {
		start = len(m.historyLines)
	}
	if end > len(m.historyLines) {
		end = len(m.historyLines)
	}
	if end > start {
		m.replaceHistoryRange(start, end, nil)
		m.shiftAnchoredBlocks(end, start-end, "")
	}
	*block = nil
}

func (m *Model) finalizeReasoningBlock() {
	if m.reasoningBlock == nil {
		return
	}
	m.reasoningBlock = nil
}

func (m *Model) handleDiffBlock(msg tuievents.DiffBlockMsg) (tea.Model, tea.Cmd) {
	m.flushStream()
	m.finalizeActivityBlock()
	m.finalizeAssistantBlock()
	m.finalizeReasoningBlock()
	start := len(m.historyLines)
	lines := m.renderDiffBlockLines(msg)
	m.historyLines = append(m.historyLines, lines...)
	m.diffBlocks = append(m.diffBlocks, diffBlockState{
		start: start,
		end:   start + len(lines),
		msg:   msg,
	})
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.syncViewportContent()
	return m, m.maybeStartClosingToolOutputFades()
}

func (m *Model) handleToolStreamMsg(msg tuievents.TaskStreamMsg) (tea.Model, tea.Cmd) {
	m.finalizeActivityBlock()
	toolName := strings.TrimSpace(msg.Label)
	if toolName == "" {
		toolName = strings.TrimSpace(msg.Tool)
	}
	nextKey := m.toolOutputKey(msg)
	panel := m.ensureToolOutputPanel(nextKey, toolName, strings.TrimSpace(msg.CallID), msg.Reset)
	if panel == nil {
		return m, nil
	}
	m.applyToolOutputState(panel, msg.State, msg.Final)
	if msg.Final {
		cmd := m.beginFinalizeToolOutputBlock(panel)
		if cmd == nil {
			cmd = m.maybeStartClosingToolOutputFades()
		}
		return m, cmd
	}
	if strings.TrimSpace(msg.Chunk) == "" {
		m.syncAnchoredToolOutputBlock(panel)
		return m, nil
	}
	m.appendToolOutputChunk(panel, strings.TrimSpace(msg.Stream), msg.Chunk)
	m.syncAnchoredToolOutputBlock(panel)
	return m, nil
}

func (m *Model) replaceHistoryRange(start int, end int, replacement []string) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(m.historyLines) {
		start = len(m.historyLines)
	}
	if end > len(m.historyLines) {
		end = len(m.historyLines)
	}
	head := append([]string(nil), m.historyLines[:start]...)
	head = append(head, replacement...)
	m.historyLines = append(head, m.historyLines[end:]...)
}

func (m *Model) shiftAnchoredBlocks(threshold, delta int, skipKey string) {
	if delta == 0 {
		return
	}
	if m.assistantBlock != nil && m.assistantBlock.start >= threshold {
		m.assistantBlock.start += delta
		m.assistantBlock.end += delta
	}
	if m.reasoningBlock != nil && m.reasoningBlock.start >= threshold {
		m.reasoningBlock.start += delta
		m.reasoningBlock.end += delta
	}
	for i := range m.diffBlocks {
		if m.diffBlocks[i].start >= threshold {
			m.diffBlocks[i].start += delta
			m.diffBlocks[i].end += delta
		}
	}
	if m.activityBlock != nil && m.activityBlock.start >= threshold {
		m.activityBlock.start += delta
		m.activityBlock.end += delta
	}
	for key, panel := range m.toolOutputs {
		if key == skipKey || panel == nil {
			continue
		}
		if panel.start >= threshold {
			panel.start += delta
			panel.end += delta
		}
	}
}

func (m *Model) renderAssistantBlockLines(raw string) []string {
	rendered, isMarkdown := renderNarrativeMarkdown(raw, maxInt(20, m.viewport.Width()), m.theme)
	if rendered == "" {
		return []string{tuikit.ColorizeLogLine("* ", tuikit.LineStyleAssistant, m.theme)}
	}
	if !isMarkdown {
		rendered = normalizePlainBlockText(ansi.Strip(rendered))
	}
	lines := trimLeadingBlankLines(strings.Split(rendered, "\n"))
	if len(lines) > 0 {
		lines[0] = tuikit.ColorizeLogLine("* "+lines[0], tuikit.LineStyleAssistant, m.theme)
	}
	if isMarkdown {
		return lines
	}
	for i := range lines {
		if i == 0 {
			continue
		}
		lines[i] = tuikit.ColorizeLogLine(lines[i], tuikit.LineStyleAssistant, m.theme)
	}
	return lines
}

func trimLeadingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m *Model) renderReasoningBlockLines(raw string) []string {
	rendered, isMarkdown := renderNarrativeMarkdown(raw, maxInt(20, m.viewport.Width()), m.theme)
	if rendered == "" {
		return []string{tuikit.ColorizeLogLine("· ", tuikit.LineStyleReasoning, m.theme)}
	}
	rendered = normalizePlainBlockText(ansi.Strip(rendered))
	lines := trimLeadingBlankLines(strings.Split(rendered, "\n"))
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		prefix := "  "
		if i == 0 {
			prefix = "· "
		}
		line = prefix + line
		if isMarkdown {
			lines[i] = m.theme.ReasoningStyle().Render(tuikit.LinkifyText(line, m.theme.LinkStyle()))
			continue
		}
		lines[i] = tuikit.ColorizeLogLine(line, tuikit.LineStyleReasoning, m.theme)
	}
	return lines
}

func normalizePlainBlockText(rendered string) string {
	lines := trimLeadingBlankLines(strings.Split(rendered, "\n"))
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderDiffBlockLines(msg tuievents.DiffBlockMsg) []string {
	model := tuidiff.BuildModel(tuidiff.Payload{
		Tool:      msg.Tool,
		Path:      msg.Path,
		Created:   msg.Created,
		Hunk:      msg.Hunk,
		Old:       msg.Old,
		New:       msg.New,
		Preview:   msg.Preview,
		Truncated: msg.Truncated,
	})
	wrapWidth := maxInt(40, m.viewport.Width())
	return tuidiff.Render(model, wrapWidth, m.theme)
}

func (m *Model) rerenderDiffBlocks() {
	if len(m.diffBlocks) == 0 {
		return
	}
	for i := range m.diffBlocks {
		block := &m.diffBlocks[i]
		lines := m.renderDiffBlockLines(block.msg)
		oldLen := block.end - block.start
		m.replaceHistoryRange(block.start, block.end, lines)
		block.end = block.start + len(lines)
		delta := len(lines) - oldLen
		if delta == 0 {
			continue
		}
		m.shiftAnchoredBlocks(block.end-delta, delta, "")
	}
}

func (m *Model) recolorCommittedHistory() {
	if m == nil {
		return
	}
	prevStyle := tuikit.LineStyleDefault
	hasCommitted := false
	for i, line := range m.historyLines {
		raw := ansi.Strip(line)
		if strings.TrimSpace(raw) == "" {
			m.historyLines[i] = ""
			continue
		}
		style := tuikit.DetectLineStyleWithContext(raw, prevStyle)
		colored := tuikit.ColorizeLogLine(raw, style, m.theme)
		colored = tuikit.LineExtraGutter(style) + colored
		m.historyLines[i] = colored
		prevStyle = style
		hasCommitted = true
	}
	m.hasCommittedLine = hasCommitted
	if !hasCommitted {
		m.lastCommittedStyle = tuikit.LineStyleDefault
		m.lastCommittedRaw = ""
		return
	}
	m.refreshHistoryTailState()
}

func (m *Model) rerenderStreamBlock(block *assistantBlockState, kind string) {
	if m == nil || block == nil {
		return
	}
	lines := m.renderReasoningBlockLines(block.raw)
	if normalizeStreamKind(kind) == "answer" {
		lines = m.renderAssistantBlockLines(block.raw)
	}
	oldLen := block.end - block.start
	m.replaceHistoryRange(block.start, block.end, lines)
	block.end = block.start + len(lines)
	delta := len(lines) - oldLen
	if delta != 0 {
		m.shiftAnchoredBlocks(block.end-delta, delta, "")
	}
}

func (m *Model) resetConversationView() {
	m.flushStream()
	m.assistantBlock = nil
	m.reasoningBlock = nil
	m.clearActivityBlock()
	m.clearToolOutputPanels()
	m.diffBlocks = m.diffBlocks[:0]
	m.historyLines = m.historyLines[:0]
	m.viewportStyledLines = m.viewportStyledLines[:0]
	m.viewportPlainLines = m.viewportPlainLines[:0]
	m.hasCommittedLine = false
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.lastFinalAnswer = ""
	m.transientLogIdx = -1
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
	for i := len(m.historyLines) - 1; i >= 0; i-- {
		raw := ansi.Strip(m.historyLines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}
		m.lastCommittedRaw = raw
		m.lastCommittedStyle = tuikit.DetectLineStyle(raw)
		m.hasCommittedLine = true
		return
	}
}

// commitLine colorizes one complete line and appends it to the history buffer.
//
// Transient log replacement rules:
//   - Retry lines replace the previous retry line in-place (status-update style).
//   - Consecutive warn lines replace the previous warn line in-place.
//   - Error lines are always appended (never replaced).
//   - Assistant narrative and other content are immutable.
//
// Spacing rules (from layout.go tokens):
//   - Conversation turns: SpaceTurnGap
//   - Log↔narrative boundary: SpaceLogBlockGap
//   - Consecutive tool calls: SpaceToolGap
//
// User and log lines receive extra left gutter via LineExtraGutter().
func (m *Model) commitLine(line string) {
	if strings.TrimSpace(line) == "" && !m.hasCommittedLine {
		return // skip leading blank lines
	}

	style := tuikit.DetectLineStyleWithContext(line, m.lastCommittedStyle)
	isEphemeralWarn := isTransientWarningLine(line)
	isRetry := tuikit.IsRetryLine(line) && !isEphemeralWarn
	isWarn := !isRetry && style == tuikit.LineStyleWarn

	// --- Transient log replacement ---
	if isRetry && m.transientLogIdx >= 0 && m.transientIsRetry {
		// Replace previous retry in-place.
		colored := tuikit.ColorizeLogLine(line, style, m.theme)
		colored = tuikit.LineExtraGutter(style) + colored
		m.historyLines[m.transientLogIdx] = colored
		m.lastCommittedStyle = style
		m.lastCommittedRaw = line
		m.transientRemove = false
		m.syncViewportContent()
		return
	}
	if isWarn && m.transientLogIdx >= 0 && !m.transientIsRetry {
		// Replace previous consecutive warn in-place.
		colored := tuikit.ColorizeLogLine(line, style, m.theme)
		colored = tuikit.LineExtraGutter(style) + colored
		m.historyLines[m.transientLogIdx] = colored
		m.lastCommittedStyle = style
		m.lastCommittedRaw = line
		m.transientRemove = isEphemeralWarn
		m.syncViewportContent()
		return
	}

	if m.transientLogIdx >= 0 && m.transientRemove {
		m.removeTransientLogLine()
	}

	// Leaving a transient slot — clear tracking.
	m.transientLogIdx = -1
	m.transientRemove = false

	// Keep the transcript compact; region spacing is handled outside the viewport.
	if m.hasCommittedLine {
		m.insertSpacing(style, line)
	}

	colored := tuikit.ColorizeLogLine(line, style, m.theme)
	colored = tuikit.LineExtraGutter(style) + colored
	m.historyLines = append(m.historyLines, colored)

	// Mark new transient slot for retry or warn.
	if isRetry {
		m.transientLogIdx = len(m.historyLines) - 1
		m.transientIsRetry = true
		m.transientRemove = false
	} else if isWarn {
		m.transientLogIdx = len(m.historyLines) - 1
		m.transientIsRetry = false
		m.transientRemove = isEphemeralWarn
	}

	m.lastCommittedStyle = style
	m.lastCommittedRaw = line
	m.hasCommittedLine = true
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
	idx := m.transientLogIdx
	if idx < 0 || idx >= len(m.historyLines) {
		return
	}
	m.replaceHistoryRange(idx, idx+1, nil)
	if idx < len(m.historyLines) {
		m.shiftAnchoredBlocks(idx+1, -1, "")
	}
	m.refreshHistoryTailState()
	m.transientLogIdx = -1
	m.transientRemove = false
}

func (m *Model) insertSpacing(style tuikit.LineStyle, line string) {
	if len(m.historyLines) == 0 {
		return
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	if strings.TrimSpace(m.lastCommittedRaw) == "" {
		return
	}
	if strings.TrimSpace(m.historyLines[len(m.historyLines)-1]) == "" {
		return
	}
	if shouldInsertBlockGap(m.lastCommittedStyle, style) {
		m.historyLines = append(m.historyLines, "")
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
