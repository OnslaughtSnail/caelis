package tuiapp

import (
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuidiff"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	// Detect running hint from tool call lines.
	if strings.HasPrefix(strings.TrimSpace(m.streamLine), "▸ ") {
		parts := strings.SplitN(strings.TrimSpace(m.streamLine), " ", 3)
		if len(parts) >= 2 {
			m.runningHint = parts[1]
		}
	}

	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleAssistantStream(text string, final bool) (tea.Model, tea.Cmd) {
	return m.handleStreamBlock("answer", text, final)
}

func (m *Model) finalizeAssistantBlock() {
	if m.assistantBlock == nil {
		return
	}
	m.assistantBlock = nil
}

func (m *Model) handleReasoningStream(text string, final bool) (tea.Model, tea.Cmd) {
	return m.handleStreamBlock("reasoning", text, final)
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
	if text == "" {
		return m, nil
	}
	streamKind := normalizeStreamKind(kind)
	blockStyle := tuikit.LineStyleAssistant
	blockMarker := "* "
	render := m.renderAssistantBlockLines

	var activeBlock **assistantBlockState
	if streamKind == "reasoning" {
		blockStyle = tuikit.LineStyleReasoning
		blockMarker = "│ "
		render = m.renderReasoningBlockLines
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
	if *activeBlock == nil {
		start := len(m.historyLines)
		lines := render(text)
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
		return m, nil
	}
	block := *activeBlock
	block.raw = mergeStreamChunk(block.raw, text, final)
	lines := render(block.raw)
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
	return m, nil
}

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
		// Cumulative stream chunk.
		return incoming
	}
	if strings.HasPrefix(existing, incoming) {
		// Replayed/duplicated old chunk.
		return existing
	}
	return existing + incoming
}

func (m *Model) finalizeReasoningBlock() {
	if m.reasoningBlock == nil {
		return
	}
	m.reasoningBlock = nil
}

func (m *Model) handleDiffBlock(msg tuievents.DiffBlockMsg) (tea.Model, tea.Cmd) {
	m.flushStream()
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
	return m, nil
}

func (m *Model) handleToolStreamMsg(msg tuievents.TaskStreamMsg) (tea.Model, tea.Cmd) {
	toolName := strings.TrimSpace(msg.Label)
	if toolName == "" {
		toolName = strings.TrimSpace(msg.Tool)
	}
	nextKey := m.toolOutputKey(msg)
	panel := m.ensureToolOutputPanel(nextKey, toolName, strings.TrimSpace(msg.CallID), msg.Reset)
	if panel == nil {
		return m, nil
	}
	if msg.Final {
		if strings.EqualFold(strings.TrimSpace(panel.tool), "BASH") {
			m.finalizeBashToolOutputBlock(panel)
			return m, nil
		}
		panel.active = false
		return m, nil
	}
	if strings.TrimSpace(msg.Chunk) == "" {
		return m, nil
	}
	panel.active = true
	m.appendToolOutputChunk(panel, strings.TrimSpace(msg.Stream), msg.Chunk)
	m.syncAnchoredToolOutputBlock(panel)
	return m, nil
}

func (m *Model) toolOutputKey(msg tuievents.TaskStreamMsg) string {
	if taskID := strings.TrimSpace(msg.TaskID); taskID != "" {
		return taskID
	}
	if callID := strings.TrimSpace(msg.CallID); callID != "" {
		return callID
	}
	toolName := strings.TrimSpace(msg.Label)
	if toolName == "" {
		toolName = strings.TrimSpace(msg.Tool)
	}
	return toolName
}

func (m *Model) ensureToolOutputPanel(key, toolName, callID string, reset bool) *toolOutputState {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if m.toolOutputs == nil {
		m.toolOutputs = map[string]*toolOutputState{}
	}
	panel, ok := m.toolOutputs[key]
	if !ok || reset {
		panel = &toolOutputState{
			key:    key,
			tool:   strings.TrimSpace(toolName),
			callID: strings.TrimSpace(callID),
			start:  len(m.historyLines),
			end:    len(m.historyLines),
		}
		m.toolOutputs[key] = panel
	} else {
		if strings.TrimSpace(panel.tool) == "" {
			panel.tool = strings.TrimSpace(toolName)
		}
		if strings.TrimSpace(panel.callID) == "" {
			panel.callID = strings.TrimSpace(callID)
		}
	}
	return panel
}

func (m *Model) clearToolOutputPanels() {
	m.toolOutputs = nil
}

func (m *Model) appendToolOutputChunk(panel *toolOutputState, stream, chunk string) {
	if panel == nil {
		return
	}
	normalized := tuikit.SanitizeLogText(chunk)
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "\r\n", "\n"), "\r", "\n")
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream == "" {
		stream = "stdout"
	}
	switch stream {
	case "stderr":
		panel.stderrPartial = m.consumeToolOutputChunk(panel, panel.stderrPartial, normalized, stream)
	default:
		panel.stdoutPartial = m.consumeToolOutputChunk(panel, panel.stdoutPartial, normalized, stream)
	}
}

func (m *Model) consumeToolOutputChunk(panel *toolOutputState, partial, chunk, stream string) string {
	if chunk == "" {
		return partial
	}
	buf := partial + chunk
	for {
		idx := strings.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(buf[:idx], "\r")
		buf = buf[idx+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if shouldSkipDelegatePreviewLine(panel, line) {
			continue
		}
		panel.lines = append(panel.lines, toolOutputLine{text: line, stream: stream})
	}
	if len(panel.lines) > toolOutputPreviewLines {
		panel.lines = append([]toolOutputLine(nil), panel.lines[len(panel.lines)-toolOutputPreviewLines:]...)
	}
	return buf
}

func shouldSkipDelegatePreviewLine(panel *toolOutputState, line string) bool {
	if panel == nil || !strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		panel.delegateFence = !panel.delegateFence
		return true
	}
	return panel.delegateFence
}

func (m *Model) currentToolOutputLines(panel *toolOutputState) []toolOutputLine {
	if panel == nil {
		return nil
	}
	content := append([]toolOutputLine(nil), panel.lines...)
	if partial := strings.TrimSpace(panel.stdoutPartial); partial != "" {
		content = append(content, toolOutputLine{text: partial, stream: "stdout"})
	}
	if partial := strings.TrimSpace(panel.stderrPartial); partial != "" {
		content = append(content, toolOutputLine{text: partial, stream: "stderr"})
	}
	filtered := content[:0]
	for _, line := range content {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
			switch strings.ToLower(strings.TrimSpace(line.stream)) {
			case "assistant", "reasoning", "stderr":
			default:
				continue
			}
		}
		filtered = append(filtered, line)
	}
	content = filtered
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		return prioritizeDelegatePreviewLines(content, toolOutputPreviewLines)
	}
	if len(content) > toolOutputPreviewLines {
		content = content[len(content)-toolOutputPreviewLines:]
	}
	return content
}

func (m *Model) renderToolOutputBlockLines(panel *toolOutputState, content []toolOutputLine) []string {
	if len(content) == 0 {
		return nil
	}
	lines := make([]string, 0, len(content))
	panelInnerWidth := maxInt(1, m.viewport.Width-4)
	for _, line := range content {
		text, prefix, style := m.renderToolOutputLine(panel, line)
		availableTextWidth := maxInt(1, panelInnerWidth-displayColumns(prefix))
		if displayColumns(text) > availableTextWidth {
			if availableTextWidth == 1 {
				text = "…"
			} else {
				text = sliceByDisplayColumns(text, 0, availableTextWidth-1) + "…"
			}
		}
		lines = append(lines, style.Width(panelInnerWidth).Render(prefix+text))
	}
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.PanelBorder).
		Padding(0, 1).
		Width(panelInnerWidth)
	return strings.Split(boxStyle.Render(strings.Join(lines, "\n")), "\n")
}

func (m *Model) syncAnchoredToolOutputBlock(panel *toolOutputState) {
	if panel == nil {
		return
	}
	content := m.currentToolOutputLines(panel)
	lines := m.renderToolOutputBlockLines(panel, content)
	oldLen := panel.end - panel.start
	m.replaceHistoryRange(panel.start, panel.end, lines)
	panel.end = panel.start + len(lines)
	delta := len(lines) - oldLen
	if delta != 0 {
		m.shiftAnchoredBlocks(panel.end-delta, delta, panel.key)
	}
	m.syncViewportContent()
}

func (m *Model) finalizeBashToolOutputBlock(panel *toolOutputState) {
	if panel == nil {
		return
	}
	content := m.currentToolOutputLines(panel)
	lines := m.renderFinalToolOutputHistoryLines(panel, content)
	oldLen := panel.end - panel.start
	m.replaceHistoryRange(panel.start, panel.end, lines)
	newEnd := panel.start + len(lines)
	delta := len(lines) - oldLen
	delete(m.toolOutputs, panel.key)
	if delta != 0 {
		m.shiftAnchoredBlocks(newEnd-delta, delta, panel.key)
	}
	m.syncViewportContent()
}

func (m *Model) renderFinalToolOutputHistoryLines(panel *toolOutputState, content []toolOutputLine) []string {
	if panel == nil || len(content) == 0 {
		return nil
	}
	lines := make([]string, 0, len(content))
	for _, line := range content {
		text := strings.TrimSpace(line.text)
		if text == "" {
			continue
		}
		prefix := "  "
		if strings.EqualFold(strings.TrimSpace(line.stream), "stderr") {
			lines = append(lines, m.theme.ErrorStyle().Render(prefix+text))
			continue
		}
		lines = append(lines, tuikit.ColorizeLogLine(prefix+text, tuikit.LineStyleDefault, m.theme))
	}
	return lines
}

func (m *Model) renderToolOutputLine(panel *toolOutputState, line toolOutputLine) (text string, prefix string, style lipgloss.Style) {
	text = strings.TrimSpace(line.text)
	prefix = "  "
	style = lipgloss.NewStyle().Foreground(m.theme.TextPrimary)
	stream := strings.ToLower(strings.TrimSpace(line.stream))
	if stream == "stderr" {
		return text, "! ", m.theme.ErrorStyle()
	}
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		switch stream {
		case "reasoning":
			return text, "· ", m.theme.ReasoningStyle()
		case "assistant":
			return text, "  ", m.theme.AssistantStyle()
		}
	}
	return text, prefix, style
}

func prioritizeDelegatePreviewLines(content []toolOutputLine, limit int) []toolOutputLine {
	if len(content) <= limit || limit <= 0 {
		return content
	}
	selected := make([]toolOutputLine, 0, minInt(limit, len(content)))
	used := make([]bool, len(content))
	for i := len(content) - 1; i >= 0 && len(selected) < limit; i-- {
		switch strings.ToLower(strings.TrimSpace(content[i].stream)) {
		case "assistant", "stderr":
			selected = append(selected, content[i])
			used[i] = true
		}
	}
	for i := len(content) - 1; i >= 0 && len(selected) < limit; i-- {
		if used[i] {
			continue
		}
		selected = append(selected, content[i])
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
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
	trimmed := strings.TrimSpace(raw)
	isMarkdown := looksLikeMarkdown(trimmed)
	rendered := renderAssistantMarkdown(trimmed)
	if rendered == "" {
		return []string{tuikit.ColorizeLogLine("* ", tuikit.LineStyleAssistant, m.theme)}
	}
	lines := strings.Split(rendered, "\n")
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

func (m *Model) renderReasoningBlockLines(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{tuikit.ColorizeLogLine("· ", tuikit.LineStyleReasoning, m.theme)}
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if i == 0 {
			line = "· " + line
		} else {
			line = "  " + line
		}
		lines[i] = tuikit.ColorizeLogLine(line, tuikit.LineStyleReasoning, m.theme)
	}
	return lines
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
	wrapWidth := maxInt(40, m.viewport.Width)
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

func (m *Model) resetConversationView() {
	m.flushStream()
	m.assistantBlock = nil
	m.reasoningBlock = nil
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
	m.runStartedAt = time.Time{}
	m.lastRunDuration = 0
	m.hasLastRunDuration = false
	m.clearSelection()
	m.clearInputSelection()
	m.userScrolledUp = false
	if m.cfg.ShowWelcomeCard {
		if m.viewport.Width > 0 {
			m.appendWelcomeCard()
			m.welcomeCardPending = false
		} else {
			m.welcomeCardPending = true
		}
	}
	m.syncViewportContent()
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
	isRetry := tuikit.IsRetryLine(line)
	isWarn := !isRetry && style == tuikit.LineStyleWarn

	// --- Transient log replacement ---
	if isRetry && m.transientLogIdx >= 0 && m.transientIsRetry {
		// Replace previous retry in-place.
		colored := tuikit.ColorizeLogLine(line, style, m.theme)
		colored = tuikit.LineExtraGutter(style) + colored
		m.historyLines[m.transientLogIdx] = colored
		m.lastCommittedStyle = style
		m.lastCommittedRaw = line
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
		m.syncViewportContent()
		return
	}

	// Leaving a transient slot — clear tracking.
	m.transientLogIdx = -1

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
	} else if isWarn {
		m.transientLogIdx = len(m.historyLines) - 1
		m.transientIsRetry = false
	}

	m.lastCommittedStyle = style
	m.lastCommittedRaw = line
	m.hasCommittedLine = true
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

func isToolCallLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "▸ ")
}
