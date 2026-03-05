package tuiapp

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuidiff"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	tea "github.com/charmbracelet/bubbletea"
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
		if m.hasCommittedLine && m.lastCommittedStyle != blockStyle &&
			!(m.lastCommittedStyle == tuikit.LineStyleAssistant && blockStyle == tuikit.LineStyleReasoning) &&
			!(m.lastCommittedStyle == tuikit.LineStyleReasoning && blockStyle == tuikit.LineStyleAssistant) {
			m.historyLines = append(m.historyLines, "")
		}
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
	if m.hasCommittedLine && !isToolCallLine(m.lastCommittedRaw) {
		m.historyLines = append(m.historyLines, "")
	}
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
		for j := i + 1; j < len(m.diffBlocks); j++ {
			m.diffBlocks[j].start += delta
			m.diffBlocks[j].end += delta
		}
	}
}

func (m *Model) resetConversationView() {
	m.flushStream()
	m.assistantBlock = nil
	m.reasoningBlock = nil
	m.diffBlocks = m.diffBlocks[:0]
	m.historyLines = m.historyLines[:0]
	m.viewportStyledLines = m.viewportStyledLines[:0]
	m.viewportPlainLines = m.viewportPlainLines[:0]
	m.hasCommittedLine = false
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.lastFinalAnswer = ""
	m.clearSelection()
	m.clearInputSelection()
	m.userScrolledUp = false
	if m.cfg.ShowWelcomeCard {
		m.appendWelcomeCard()
	}
	m.syncViewportContent()
}

// commitLine colorizes one complete line and appends it to the history buffer.
func (m *Model) commitLine(line string) {
	if strings.TrimSpace(line) == "" && !m.hasCommittedLine {
		return // skip leading blank lines
	}

	style := tuikit.DetectLineStyleWithContext(line, m.lastCommittedStyle)

	// Insert visual gap before conversation turns.
	if m.hasCommittedLine && (tuikit.ShouldInsertGap(true, m.lastCommittedStyle, style) || shouldInsertToolGap(m.lastCommittedRaw, line)) {
		m.historyLines = append(m.historyLines, "")
	}

	colored := tuikit.ColorizeLogLine(line, style, m.theme)
	m.historyLines = append(m.historyLines, colored)

	m.lastCommittedStyle = style
	m.lastCommittedRaw = line
	m.hasCommittedLine = true
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

func shouldInsertToolGap(prevLine string, currentLine string) bool {
	prev := strings.TrimSpace(prevLine)
	curr := strings.TrimSpace(currentLine)
	if prev == "" || curr == "" {
		return false
	}
	return strings.HasPrefix(prev, "▸ ") && strings.HasPrefix(curr, "▸ ")
}

func isToolCallLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "▸ ")
}
