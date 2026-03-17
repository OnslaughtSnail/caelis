package tuiapp

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

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
		now := time.Now()
		panel = &toolOutputState{
			key:       key,
			tool:      strings.TrimSpace(toolName),
			callID:    strings.TrimSpace(callID),
			start:     len(m.historyLines),
			end:       len(m.historyLines),
			startedAt: now,
			updatedAt: now,
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
	panel.closing = false
	panel.fadeStep = 0
	panel.finalizedAt = time.Time{}
	return panel
}

func (m *Model) applyToolOutputState(panel *toolOutputState, state string, final bool) {
	if panel == nil {
		return
	}
	normalized := normalizeToolOutputState(state)
	if normalized != "" {
		panel.state = normalized
	}
	switch panel.state {
	case "running", "waiting_approval", "waiting_input":
		panel.active = true
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		panel.active = false
	}
	if final {
		panel.active = false
	}
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
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		switch stream {
		case "reasoning":
			panel.reasoningPartial = m.consumeDelegatePreviewChunk(panel, panel.reasoningPartial, normalized, stream)
		case "assistant":
			panel.assistantPartial = m.consumeDelegatePreviewChunk(panel, panel.assistantPartial, normalized, stream)
		case "stderr":
			panel.stderrPartial = m.consumeDelegatePreviewChunk(panel, panel.stderrPartial, normalized, stream)
		default:
			panel.stdoutPartial = m.consumeToolOutputChunk(panel, panel.stdoutPartial, normalized, stream)
		}
		panel.lastStream = stream
		panel.updatedAt = time.Now()
		return
	}
	switch stream {
	case "stderr":
		panel.stderrPartial = m.consumeToolOutputChunk(panel, panel.stderrPartial, normalized, stream)
	default:
		panel.stdoutPartial = m.consumeToolOutputChunk(panel, panel.stdoutPartial, normalized, stream)
	}
	panel.lastStream = stream
	panel.updatedAt = time.Now()
}

func (m *Model) consumeDelegatePreviewChunk(panel *toolOutputState, partial, chunk, stream string) string {
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
		if shouldSkipDelegatePreviewLine(panel, line) {
			continue
		}
		if formatted := formatDelegatePreviewText(line, stream); formatted != "" {
			m.appendDelegatePreviewLine(panel, formatted, stream)
		}
	}
	if len(panel.lines) > toolOutputPreviewLines*3 {
		panel.lines = append([]toolOutputLine(nil), panel.lines[len(panel.lines)-(toolOutputPreviewLines*3):]...)
	}
	return buf
}

func (m *Model) appendDelegatePreviewLine(panel *toolOutputState, text string, stream string) {
	if panel == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(panel.lines) > 0 {
		last := &panel.lines[len(panel.lines)-1]
		if canMergeDelegatePreviewLine(last, text, stream) {
			last.text = strings.TrimSpace(last.text) + " " + text
			return
		}
	}
	panel.lines = append(panel.lines, toolOutputLine{text: text, stream: stream})
}

func canMergeDelegatePreviewLine(last *toolOutputLine, nextText string, stream string) bool {
	if last == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(last.stream), strings.TrimSpace(stream)) {
		return false
	}
	if !isDelegateParagraphText(last.text) || !isDelegateParagraphText(nextText) {
		return false
	}
	return true
}

func isDelegateParagraphText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	switch {
	case strings.HasPrefix(text, "▸"),
		strings.HasPrefix(text, "✓"),
		strings.HasPrefix(text, "!"),
		strings.HasPrefix(text, "- "),
		strings.HasPrefix(text, "* "),
		strings.HasPrefix(text, "• "),
		strings.HasPrefix(text, "1. "):
		return false
	}
	return true
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
	if partial := strings.TrimSpace(panel.stderrPartial); partial != "" && !strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		content = append(content, toolOutputLine{text: partial, stream: "stderr"})
	}
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		if partial := formatDelegatePreviewText(panel.reasoningPartial, "reasoning"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "reasoning"})
		}
		if partial := formatDelegatePreviewText(panel.assistantPartial, "assistant"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "assistant"})
		}
		if partial := formatDelegatePreviewText(panel.stderrPartial, "stderr"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "stderr"})
		}
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
		content = prioritizeDelegatePreviewLines(content, toolOutputPreviewLines)
		return m.applyClosingToolOutputWindow(panel, content)
	}
	if len(content) > toolOutputPreviewLines {
		content = content[len(content)-toolOutputPreviewLines:]
	}
	return m.applyClosingToolOutputWindow(panel, content)
}

func (m *Model) applyClosingToolOutputWindow(panel *toolOutputState, content []toolOutputLine) []toolOutputLine {
	if panel == nil || !panel.closing || panel.fadeStep <= 0 || len(content) == 0 {
		return content
	}
	visible := len(content) - panel.fadeStep
	if visible <= 0 {
		return nil
	}
	if visible >= len(content) {
		return content
	}
	return content[len(content)-visible:]
}

func (m *Model) renderToolOutputBlockLines(panel *toolOutputState, content []toolOutputLine) []string {
	if len(content) == 0 {
		return nil
	}
	lines := make([]string, 0, len(content)+1)
	panelInnerWidth := maxInt(1, m.viewport.Width()-4)
	if header := m.renderToolOutputHeaderLine(panel, panelInnerWidth); header != "" {
		lines = append(lines, header)
	}
	for _, line := range content {
		text, prefix, style := m.renderToolOutputLine(panel, line)
		availableTextWidth := maxInt(1, panelInnerWidth-displayColumns(prefix))
		wrapped := []string{text}
		if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
			wrapped = wrapToolOutputText(text, availableTextWidth)
		} else if displayColumns(text) > availableTextWidth {
			if availableTextWidth == 1 {
				wrapped = []string{"…"}
			} else {
				wrapped = []string{sliceByDisplayColumns(text, 0, availableTextWidth-1) + "…"}
			}
		}
		for _, segment := range wrapped {
			lines = append(lines, style.Width(panelInnerWidth).Render(prefix+segment))
			prefix = strings.Repeat(" ", displayColumns(prefix))
		}
	}
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.toolOutputBorderColor(panel)).
		Padding(0, 1).
		Faint(panel != nil && panel.closing).
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

func (m *Model) beginFinalizeToolOutputBlock(panel *toolOutputState) tea.Cmd {
	if panel == nil {
		return nil
	}
	content := m.currentToolOutputLines(panel)
	if len(content) == 0 {
		m.finalizeToolOutputBlock(panel)
		return nil
	}
	panel.active = false
	panel.closing = true
	panel.fadeStep = 0
	panel.fadeQueued = false
	panel.fadeLineCount = len(content)
	panel.finalizedAt = time.Now()
	m.syncAnchoredToolOutputBlock(panel)
	return nil
}

func (m *Model) finalizeToolOutputBlock(panel *toolOutputState) {
	if panel == nil {
		return
	}
	oldLen := panel.end - panel.start
	m.replaceHistoryRange(panel.start, panel.end, nil)
	delta := -oldLen
	delete(m.toolOutputs, panel.key)
	if delta != 0 {
		m.shiftAnchoredBlocks(panel.end, delta, panel.key)
	}
	m.refreshHistoryTailState()
	m.syncViewportContent()
}

func toolOutputFadeCmd(key string, step int, after time.Duration) tea.Cmd {
	key = strings.TrimSpace(key)
	if key == "" || step <= 0 || after <= 0 {
		return nil
	}
	return tea.Tick(after, func(time.Time) tea.Msg {
		return toolOutputFadeMsg{key: key, step: step}
	})
}

func (m *Model) handleToolOutputFadeMsg(msg toolOutputFadeMsg) (tea.Model, tea.Cmd) {
	if m == nil || m.toolOutputs == nil {
		return m, nil
	}
	panel, ok := m.toolOutputs[strings.TrimSpace(msg.key)]
	if !ok || panel == nil || !panel.closing {
		return m, nil
	}
	panel.fadeQueued = false
	if panel.fadeLineCount <= 0 {
		panel.fadeLineCount = len(m.currentToolOutputLines(panel))
	}
	if msg.step >= panel.fadeLineCount {
		m.finalizeToolOutputBlock(panel)
		return m, nil
	}
	panel.fadeStep = msg.step
	m.syncAnchoredToolOutputBlock(panel)
	panel.fadeQueued = true
	return m, toolOutputFadeCmd(panel.key, msg.step+1, toolOutputFadeInterval)
}

func (m *Model) renderToolOutputLine(panel *toolOutputState, line toolOutputLine) (text string, prefix string, style lipgloss.Style) {
	text = tuikit.LinkifyText(strings.TrimSpace(line.text), m.theme.LinkStyle())
	prefix = "  "
	style = lipgloss.NewStyle().Foreground(m.theme.TextPrimary)
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		return text, "  ", m.applyToolOutputFadeStyle(panel, style)
	}
	stream := strings.ToLower(strings.TrimSpace(line.stream))
	if stream == "stderr" {
		return text, "! ", m.theme.ErrorStyle()
	}
	style = m.applyToolOutputFadeStyle(panel, style)
	return text, prefix, style
}

func (m *Model) renderToolOutputHeaderLine(panel *toolOutputState, width int) string {
	if panel == nil || width <= 0 {
		return ""
	}
	right := m.theme.HelpHintTextStyle().Render(m.toolOutputMeta(panel))
	if strings.EqualFold(strings.TrimSpace(panel.tool), "DELEGATE") {
		return composeStyledFooter(width, "", right)
	}
	tool := strings.ToUpper(strings.TrimSpace(panel.tool))
	if tool == "" {
		tool = "TASK"
	}
	left := m.theme.KeyLabelStyle().Bold(true).Render(tool)
	return composeStyledFooter(width, left, right)
}

func (m *Model) toolOutputStatus(panel *toolOutputState) (string, lipgloss.Style) {
	if panel == nil {
		return "", m.theme.HelpHintTextStyle()
	}
	switch panel.state {
	case "running":
		return "running", m.theme.AssistantStyle().Bold(true)
	case "waiting_approval":
		return "approval", m.theme.WarnStyle().Bold(true)
	case "waiting_input":
		return "input", m.theme.HelpHintTextStyle().Bold(true)
	case "completed":
		return "done", m.theme.HelpHintTextStyle()
	case "failed":
		return "failed", m.theme.ErrorStyle().Bold(true)
	case "interrupted":
		return "interrupted", m.theme.WarnStyle().Bold(true)
	case "cancelled", "canceled":
		return "cancelled", m.theme.WarnStyle().Bold(true)
	case "terminated":
		return "terminated", m.theme.WarnStyle().Bold(true)
	}
	switch {
	case panel.closing:
		return "", m.theme.HelpHintTextStyle()
	default:
		return "", m.theme.HelpHintTextStyle()
	}
}

func (m *Model) toolOutputMeta(panel *toolOutputState) string {
	if panel == nil {
		return ""
	}
	if age := formatToolOutputAge(time.Since(panel.startedAt)); age != "" {
		return age
	}
	return ""
}

func normalizeToolOutputState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	switch normalized {
	case "running", "waiting_approval", "waiting_input", "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		return normalized
	default:
		return ""
	}
}

func delegateToolSummary(panel *toolOutputState) string {
	if panel == nil {
		return ""
	}
	hasReasoning := false
	hasAssistant := false
	for _, line := range panel.lines {
		switch strings.ToLower(strings.TrimSpace(line.stream)) {
		case "reasoning":
			hasReasoning = true
		case "assistant":
			hasAssistant = true
		}
	}
	switch {
	case hasReasoning && hasAssistant:
		return "reasoning + answer"
	case hasAssistant:
		return "answer"
	case hasReasoning:
		return "reasoning"
	default:
		return "delegate"
	}
}

func formatToolOutputAge(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	return strconv.Itoa(minutes) + "m" + strconv.Itoa(seconds) + "s"
}

func (m *Model) applyToolOutputFadeStyle(panel *toolOutputState, style lipgloss.Style) lipgloss.Style {
	if panel == nil || !panel.closing {
		return style
	}
	style = style.Faint(true)
	if panel.fadeStep > 0 {
		style = style.Foreground(m.theme.TextSecondary)
	}
	return style
}

func (m *Model) toolOutputBorderColor(panel *toolOutputState) color.Color {
	if panel == nil || !panel.closing {
		return m.theme.PanelBorder
	}
	return m.theme.TextSecondary
}

func (m *Model) maybeStartClosingToolOutputFades() tea.Cmd {
	if m == nil || len(m.toolOutputs) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(m.toolOutputs))
	for _, panel := range m.toolOutputs {
		if panel == nil || !panel.closing || panel.fadeQueued || panel.fadeStep > 0 {
			continue
		}
		if !m.hasMeaningfulContentBelow(panel) {
			continue
		}
		panel.fadeQueued = true
		cmds = append(cmds, toolOutputFadeCmd(panel.key, 1, toolOutputFadeHold))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) hasMeaningfulContentBelow(panel *toolOutputState) bool {
	if panel == nil || panel.end >= len(m.historyLines) {
		return false
	}
	for _, line := range m.historyLines[panel.end:] {
		raw := strings.TrimSpace(ansi.Strip(line))
		if raw == "" {
			continue
		}
		if isDividerLike(raw) {
			continue
		}
		return true
	}
	return false
}

func isDividerLike(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	for _, r := range text {
		switch {
		case r == '─' || r == ' ':
		case r >= '0' && r <= '9':
		case r == '.' || r == ':' || r == 'm' || r == 's':
		default:
			return false
		}
	}
	return true
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

func formatDelegatePreviewText(text string, stream string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\t", " "))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	text = strings.ReplaceAll(text, "`", "")
	text = strings.TrimLeft(text, "#*- ")
	text = collapseDelegateInlineSpaces(text)
	if text == "" {
		return ""
	}
	if stream == "assistant" {
		if text == "answer" || text == "assistant" {
			return ""
		}
		text = strings.TrimPrefix(text, "answer ")
		text = strings.TrimPrefix(text, "assistant ")
	}
	if stream == "reasoning" {
		if text == "reasoning" {
			return ""
		}
		text = strings.TrimPrefix(text, "reasoning ")
	}
	return strings.TrimSpace(text)
}

func collapseDelegateInlineSpaces(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	spaceRun := false
	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			if !spaceRun {
				b.WriteByte(' ')
				spaceRun = true
			}
			continue
		}
		spaceRun = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func wrapToolOutputText(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	width = maxInt(1, width)
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for displayColumns(part) > width {
			cut := width
			slice := sliceByDisplayColumns(part, 0, cut)
			lastSpace := strings.LastIndex(slice, " ")
			if lastSpace > 8 {
				cut = displayColumns(slice[:lastSpace])
				slice = sliceByDisplayColumns(part, 0, cut)
			}
			out = append(out, strings.TrimSpace(slice))
			part = strings.TrimSpace(sliceByDisplayColumns(part, cut, displayColumns(part)))
		}
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}
