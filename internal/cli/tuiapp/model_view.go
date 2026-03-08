package tuiapp

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) View() string {
	start := time.Now()

	if !m.ready {
		return "loading..."
	}

	// Recalculate layout in case bottom section height changed.
	vpHeight, _ := m.computeLayout()
	if m.viewport.Height != vpHeight {
		m.viewport.Height = vpHeight
		m.syncViewportContent()
	}

	var sections []string

	// 1. Viewport (scrollable history + streaming + spinner).
	sections = append(sections, m.viewport.View())

	// 2. Dedicated hint area above input (always reserved to avoid layout jitter).
	sections = append(sections, m.renderHintArea())

	// 3. Separator between viewport area and input controls.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}

	// 4. Prompt choices (if any).
	if m.activePrompt != nil {
		if promptView := m.renderPromptModal(); promptView != "" {
			sections = append(sections, promptView)
		}
	}

	// 5. Input bar.
	sections = append(sections, m.renderInputBar())

	// 5b. @mention candidates inline list (below input, above status).
	if len(m.mentionCandidates) > 0 {
		sections = append(sections, m.renderMentionList())
	}

	// 5c. $skill candidates inline list (below input, above status).
	if len(m.skillCandidates) > 0 {
		sections = append(sections, m.renderSkillList())
	}

	// 5d. /resume candidates inline list (below input, above status).
	if len(m.resumeCandidates) > 0 {
		sections = append(sections, m.renderResumeList())
	}

	// 5e. Generic slash argument candidates inline list.
	if len(m.slashArgCandidates) > 0 {
		sections = append(sections, m.renderSlashArgList())
	}

	// 5f. Slash command candidates inline list.
	if len(m.slashCandidates) > 0 {
		sections = append(sections, m.renderSlashCommandList())
	}

	// 7. Status bar (with top padding).
	sections = append(sections, "") // blank line before status
	sections = append(sections, m.renderStatusBar())
	sections = append(sections, "") // bottom margin for breathing room

	view := strings.Join(sections, "\n")

	// Overlay: command palette.
	if m.showPalette && m.width > 0 && m.height > 0 {
		lineCount := strings.Count(view, "\n") + 1
		paletteView := m.theme.ModalStyle().Render(m.palette.View())
		view = overlayBottom(view, paletteView, m.width, lineCount)
	}

	duration := time.Since(start)
	m.observeRender(duration, len(view), "fullscreen")
	return view
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// computeLayout returns (viewportHeight, bottomHeight).
func (m *Model) computeLayout() (int, int) {
	bottomHeight := m.bottomSectionHeight()
	vpHeight := maxInt(1, m.height-bottomHeight)
	return vpHeight, bottomHeight
}

// bottomSectionHeight calculates how many lines the fixed bottom area needs.
func (m *Model) bottomSectionHeight() int {
	lines := 0

	// Dedicated hint area (always reserved).
	lines += reservedHintRows

	// Separator.
	lines++

	// Input bar is always shown.
	lines += maxInt(1, m.textarea.Height())

	// Prompt choices, when present, render above the input bar.
	if m.activePrompt != nil && len(m.activePrompt.choices) > 0 {
		n := len(m.visiblePromptChoices())
		lines += minInt(8, n)
		if n > 8 {
			lines++
		}
	}

	// Mention candidates.
	if n := len(m.mentionCandidates); n > 0 {
		lines += minInt(8, n)
		if n > 8 {
			lines++ // "... and N more"
		}
	}

	// Skill candidates.
	if n := len(m.skillCandidates); n > 0 {
		lines += minInt(8, n)
		if n > 8 {
			lines++
		}
	}

	// Resume candidates.
	if n := len(m.resumeCandidates); n > 0 {
		lines += minInt(8, n)
		if n > 8 {
			lines++
		}
	}

	// Generic slash-arg candidates.
	if n := len(m.slashArgCandidates); n > 0 {
		lines += minInt(8, n)
		if n > 8 {
			lines++
		}
	}

	// Slash command candidates.
	if n := len(m.slashCandidates); n > 0 {
		lines += minInt(8, n)
		if n > 8 {
			lines++
		}
	}

	// Blank line + status bar + bottom margin.
	lines += 3

	return lines
}

// syncViewportContent rebuilds the viewport content from the history buffer
// plus any in-progress streaming content, then sets it on the
// viewport. Handles auto-scroll when the user hasn't manually scrolled up.
func (m *Model) syncViewportContent() {
	wrapWidth := maxInt(1, m.viewport.Width)
	lines := make([]string, 0, len(m.historyLines)+8)

	// 1. All committed history lines.
	for _, line := range m.historyLines {
		wrapped := hardWrapDisplayLine(line, wrapWidth)
		if wrapped == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}

	// 2. Current streaming partial line (if any).
	if m.streamLine != "" {
		streamLines := strings.Split(m.streamLine, "\n")
		prevStyle := m.lastCommittedStyle
		for _, sl := range streamLines {
			style := tuikit.DetectLineStyleWithContext(sl, prevStyle)
			colored := tuikit.ColorizeLogLine(sl, style, m.theme)
			wrapped := hardWrapDisplayLine(colored, wrapWidth)
			if wrapped == "" {
				lines = append(lines, "")
			} else {
				lines = append(lines, strings.Split(wrapped, "\n")...)
			}
			prevStyle = style
		}
	}

	m.viewportStyledLines = append(m.viewportStyledLines[:0], lines...)
	m.viewportPlainLines = m.viewportPlainLines[:0]
	for _, line := range m.viewportStyledLines {
		m.viewportPlainLines = append(m.viewportPlainLines, ansi.Strip(line))
	}
	m.renderViewportContent()
}

func (m *Model) renderViewportContent() {
	wasAtBottom := m.viewport.AtBottom()
	lines := m.viewportStyledLines
	if m.hasSelectionRange() {
		lines = m.renderSelectionLines()
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))

	// Auto-scroll: if user hasn't manually scrolled up, stay at bottom.
	if !m.userScrolledUp || wasAtBottom {
		m.viewport.GotoBottom()
		m.userScrolledUp = false
	}
}

func (m *Model) clearSelection() {
	m.selecting = false
	m.selectionStart = textSelectionPoint{line: -1, col: -1}
	m.selectionEnd = textSelectionPoint{line: -1, col: -1}
}

func (m *Model) clearInputSelection() {
	m.inputSelecting = false
	m.inputSelectionStart = textSelectionPoint{line: -1, col: -1}
	m.inputSelectionEnd = textSelectionPoint{line: -1, col: -1}
}

func (m *Model) hasSelectionRange() bool {
	start, end, ok := normalizedSelectionRange(m.selectionStart, m.selectionEnd, len(m.viewportPlainLines))
	if !ok {
		return false
	}
	return start.line != end.line || start.col != end.col
}

func (m *Model) mousePointToContentPoint(x int, y int, clamp bool) (textSelectionPoint, bool) {
	if len(m.viewportPlainLines) == 0 || m.viewport.Height <= 0 {
		return textSelectionPoint{}, false
	}
	vy := y
	if clamp {
		if vy < 0 {
			vy = 0
		}
		if vy >= m.viewport.Height {
			vy = m.viewport.Height - 1
		}
	} else if vy < 0 || vy >= m.viewport.Height {
		return textSelectionPoint{}, false
	}

	line := m.viewport.YOffset + vy
	if line < 0 {
		line = 0
	}
	if line >= len(m.viewportPlainLines) {
		line = len(m.viewportPlainLines) - 1
	}

	col := x
	if col < 0 {
		col = 0
	}
	width := displayColumns(m.viewportPlainLines[line])
	if col > width {
		col = width
	}
	return textSelectionPoint{line: line, col: col}, true
}

func (m *Model) inputAreaBounds() (startY int, height int, ok bool) {
	y := m.viewport.Height
	y += reservedHintRows
	y++ // separator
	if m.activePrompt != nil && len(m.activePrompt.choices) > 0 {
		n := len(m.visiblePromptChoices())
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	h := maxInt(1, m.textarea.Height())
	return y, h, true
}

func (m *Model) mousePointToInputPoint(x int, y int, clamp bool, lines []string) (textSelectionPoint, bool) {
	startY, height, ok := m.inputAreaBounds()
	if !ok || len(lines) == 0 {
		return textSelectionPoint{}, false
	}
	ry := y - startY
	if clamp {
		if ry < 0 {
			ry = 0
		}
		if ry >= height {
			ry = height - 1
		}
	} else if ry < 0 || ry >= height {
		return textSelectionPoint{}, false
	}
	if ry >= len(lines) {
		ry = len(lines) - 1
	}
	col := x
	if col < 0 {
		col = 0
	}
	width := displayColumns(lines[ry])
	if col > width {
		col = width
	}
	return textSelectionPoint{line: ry, col: col}, true
}

func (m *Model) selectionText() string {
	start, end, ok := normalizedSelectionRange(m.selectionStart, m.selectionEnd, len(m.viewportPlainLines))
	if !ok {
		return ""
	}
	return selectionTextFromLines(m.viewportPlainLines, start, end)
}

func (m *Model) renderSelectionLines() []string {
	start, end, ok := normalizedSelectionRange(m.selectionStart, m.selectionEnd, len(m.viewportPlainLines))
	if !ok {
		return append([]string(nil), m.viewportStyledLines...)
	}
	return renderSelectionOnLines(m.viewportPlainLines, start, end)
}

// ---------------------------------------------------------------------------
// View sub-components
// ---------------------------------------------------------------------------

func (m *Model) buildHintLine() string {
	text := m.buildHintText()
	if text == "" {
		return ""
	}
	return m.theme.HintStyle().Render("  " + text)
}

func (m *Model) buildHintText() string {
	// Show hint message if set.
	if h := strings.TrimSpace(m.hint); h != "" {
		return h
	}
	if m.activePrompt != nil {
		return m.promptHintText()
	}
	if m.running && m.activePrompt == nil {
		return m.buildRunningHintText()
	}
	if text := m.pendingQueueHintText(); text != "" {
		return text
	}
	// Show /resume guidance.
	if len(m.resumeCandidates) > 0 {
		return "/resume: ↑/↓ select │ enter: resume │ tab: fill id"
	}
	// Show generic slash-arg guidance.
	if m.slashArgActive && m.slashArgCommand != "" {
		// Wizard-driven hint.
		if m.isWizardActive() {
			return m.wizardHintText()
		}
		// Non-wizard fallback.
		label := "/" + m.slashArgCommand
		if len(m.slashArgCandidates) == 0 {
			return ""
		}
		return label + ": ↑/↓ select │ enter: apply │ tab: fill"
	}
	// Show slash command guidance.
	if len(m.slashCandidates) > 0 {
		return "/: ↑/↓ select │ enter: run │ tab: fill"
	}
	return ""
}

func (m *Model) startRunningAnimation() {
	m.runningTick = 0
	m.runningBeat = 0
	if len(runningCarouselLines) > 0 {
		seed := int(time.Now().UnixNano() % int64(len(runningCarouselLines)))
		if seed < 0 {
			seed = -seed
		}
		m.runningTip = seed
	} else {
		m.runningTip = 0
	}
}

func (m *Model) stopRunningAnimation() {
	m.runningTick = 0
	m.runningBeat = 0
	m.runningTip = 0
}

func (m *Model) advanceRunningAnimation() {
	if len(runningBreathFrames) > 0 {
		m.runningBeat = (m.runningBeat + 1) % len(runningBreathFrames)
	}
	if len(runningCarouselLines) > 0 {
		m.runningTick++
		if m.runningTick%runningHintRotateEveryTicks == 0 {
			m.runningTip = (m.runningTip + 1) % len(runningCarouselLines)
		}
	}
}

func (m *Model) buildRunningHintText() string {
	frame := "·"
	if len(runningBreathFrames) > 0 {
		frame = runningBreathFrames[m.runningBeat%len(runningBreathFrames)]
	}
	queueText := m.pendingQueueHintText()
	if len(runningCarouselLines) > 0 {
		text := frame + " " + runningCarouselLines[m.runningTip%len(runningCarouselLines)]
		if queueText != "" {
			combined := text + " │ " + queueText
			maxWidth := maxInt(1, m.width) - 2
			if displayColumns(combined) > maxWidth {
				return text + " │ " + m.pendingQueueShortText()
			}
			return combined
		}
		return text
	}
	if queueText != "" {
		return frame + " " + queueText
	}
	return frame
}

func (m *Model) pendingQueueHintText() string {
	n := len(m.pendingQueue)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return "1 pending message"
	}
	return fmt.Sprintf("%d pending messages", n)
}

func (m *Model) pendingQueueShortText() string {
	n := len(m.pendingQueue)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d pending", n)
}

func (m *Model) renderHintArea() string {
	w := maxInt(1, m.width)
	blank := m.theme.HintStyle().Width(w).Render("")
	text := strings.TrimSpace(m.buildHintText())
	if text == "" {
		return blank + "\n" + blank
	}
	if w <= 2 {
		if displayColumns(text) > w {
			text = sliceByDisplayColumns(text, 0, w)
		}
		return blank + "\n" + m.theme.HintStyle().Width(w).Render(text)
	}
	maxTextWidth := w - 2
	if displayColumns(text) > maxTextWidth {
		text = sliceByDisplayColumns(text, 0, maxTextWidth)
	}
	return blank + "\n" + m.theme.HintStyle().Width(w).Render("  "+text)
}

func (m *Model) renderInputBar() string {
	if m.activePrompt != nil {
		return m.renderPromptInputBar()
	}
	if start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines())); ok &&
		(start.line != end.line || start.col != end.col) {
		lines := m.inputPlainLines()
		return strings.Join(renderSelectionOnLines(lines, start, end), "\n")
	}
	w := maxInt(20, m.width)
	prompt := m.theme.PromptStyle().Render("> ")
	inputVal := m.textarea.View()
	if m.isWizardActive() && m.wizard.hideInput() {
		query, _ := wizardQueryAtCursor(m.wizard.def.Command, m.input, m.cursor)
		inputVal = "/" + m.wizard.def.Command + " " + strings.Repeat("*", utf8.RuneCountInString(strings.TrimSpace(query)))
	}
	inputLine := renderMultilineInput(prompt, inputVal)
	if strings.Contains(inputLine, "\n") {
		return inputLine
	}

	// Build the help hints on the right side.
	helpText := m.buildHelpHints()
	helpRendered := m.theme.HelpHintTextStyle().Render(helpText)

	// Compose: "> input                                   help hints"
	inputWidth := lipgloss.Width(inputLine)
	helpWidth := lipgloss.Width(helpRendered)

	if inputWidth+helpWidth+2 <= w {
		gap := w - inputWidth - helpWidth
		return inputLine + strings.Repeat(" ", gap) + helpRendered
	}
	return inputLine
}

func (m *Model) inputPlainLines() []string {
	prompt := "> "
	indent := strings.Repeat(" ", lipgloss.Width(prompt))
	value := m.textarea.Value()
	if value == "" {
		return []string{prompt}
	}
	wrapWidth := maxInt(1, m.textarea.Width())
	contentLines := make([]string, 0, maxInt(1, m.textarea.Height()))
	for _, line := range strings.Split(value, "\n") {
		wrapped := ansi.HardwrapWc(line, wrapWidth, true)
		if wrapped == "" {
			contentLines = append(contentLines, "")
			continue
		}
		contentLines = append(contentLines, strings.Split(wrapped, "\n")...)
	}
	if len(contentLines) == 0 {
		contentLines = append(contentLines, "")
	}
	out := make([]string, 0, len(contentLines))
	for i, line := range contentLines {
		if i == 0 {
			out = append(out, prompt+line)
			continue
		}
		out = append(out, indent+line)
	}
	return out
}

func renderMultilineInput(prompt string, input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return prompt
	}
	indent := strings.Repeat(" ", maxInt(0, lipgloss.Width(prompt)))
	lines[0] = prompt + lines[0]
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) buildHelpHints() string {
	if m.running {
		if len(m.pendingQueue) > 0 {
			return "enter: queue │ esc: pop queued"
		}
		return "enter: queue │ esc: interrupt"
	}
	parts := []string{"enter: send", "ctrl+v: paste image", "pgup/pgdn: scroll", "ctrl+p: commands", "ctrl+c x2: quit"}
	return strings.Join(parts, " │ ")
}

func (m *Model) renderStatusBar() string {
	w := maxInt(20, m.width)
	left := strings.TrimSpace(m.cfg.Workspace)
	modelPart := strings.TrimSpace(m.statusModel)
	contextPart := strings.TrimSpace(m.statusContext)
	var right string
	if contextPart != "" {
		right = modelPart + "   " + contextPart
	} else {
		right = modelPart
	}
	content := tuikit.ComposeFooter(w-2, left, right)
	return m.theme.StatusStyle().Width(w).Render(content)
}

func (m *Model) renderPromptModal() string {
	if m.activePrompt == nil {
		return ""
	}
	p := m.activePrompt
	if len(p.choices) == 0 {
		return ""
	}
	visible := m.visiblePromptChoices()
	if len(visible) == 0 {
		return m.theme.HelpHintTextStyle().Render("  no matching choices")
	}
	const maxVisiblePromptChoices = 8
	m.syncPromptChoiceWindow()
	start := m.activePrompt.scrollOffset
	if start < 0 {
		start = 0
	}
	if start > len(visible) {
		start = len(visible)
	}
	end := minInt(len(visible), start+maxVisiblePromptChoices)
	window := visible[start:end]
	maxItems := len(window)
	lines := make([]string, 0, maxItems+1)
	for i := 0; i < maxItems; i++ {
		choice := window[i]
		actualIndex := start + i
		marker := ""
		if p.multiSelect {
			if _, ok := p.selected[choice.value]; ok {
				marker = "[x] "
			} else {
				marker = "[ ] "
			}
		}
		if actualIndex == p.choiceIndex {
			line := m.theme.PromptStyle().Render("▸ ") + m.theme.CommandActiveStyle().Render(marker+choice.label)
			if choice.detail != "" {
				line += " " + m.theme.HelpHintTextStyle().Render(choice.detail)
			}
			lines = append(lines, line)
			continue
		}
		line := "  " + m.theme.HelpHintTextStyle().Render(marker+choice.label)
		if choice.detail != "" {
			line += " " + m.theme.HelpHintTextStyle().Render(choice.detail)
		}
		lines = append(lines, line)
	}
	if len(visible) > end {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(visible)-end),
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderPromptInputBar() string {
	prompt := m.theme.PromptStyle().Render("> ")
	value, cursor := m.promptInputValue()
	return renderMultilineInput(prompt, insertPromptCursor(value, cursor, m.theme.PromptStyle().Render("█")))
}

func (m *Model) promptInputValue() (string, int) {
	if m.activePrompt == nil {
		return "", 0
	}
	if m.activePrompt.filterable {
		return string(m.activePrompt.filter), m.activePrompt.cursor
	}
	value := string(m.activePrompt.input)
	if m.activePrompt.secret {
		value = strings.Repeat("*", len(m.activePrompt.input))
	}
	return value, m.activePrompt.cursor
}

func insertPromptCursor(value string, cursor int, cursorGlyph string) string {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	head := string(runes[:cursor])
	tail := string(runes[cursor:])
	return head + cursorGlyph + tail
}

func (m *Model) promptHintText() string {
	if m.activePrompt == nil {
		return ""
	}
	text := strings.TrimSpace(m.activePrompt.prompt)
	text = strings.TrimSuffix(text, ":")
	text = strings.TrimSpace(text)
	if len(m.activePrompt.choices) > 0 {
		footer := "Press Enter to confirm or Esc to cancel"
		if m.activePrompt.filterable {
			if m.activePrompt.multiSelect {
				return text + "; type to filter, Space to toggle. " + footer
			}
			return text + "; type to filter. " + footer
		}
		if m.activePrompt.multiSelect {
			return text + "; Space to toggle. " + footer
		}
		if text == "" {
			return footer
		}
		return text + " " + footer
	}
	if text == "" {
		return "Enter a value"
	}
	return "Enter " + text
}

func (m *Model) adjustTextareaHeight() {
	height := desiredInputRows(m.textarea.Value(), m.textarea.Width(), maxInputBarRows)
	if height < 1 {
		height = 1
	}
	if m.textarea.Height() != height {
		m.textarea.SetHeight(height)
	}
}

func desiredInputRows(value string, width int, maxRows int) int {
	if width <= 0 {
		width = 1
	}
	if maxRows <= 0 {
		maxRows = maxInputBarRows
	}
	if strings.TrimSpace(value) == "" {
		return 1
	}
	rows := 0
	for _, line := range strings.Split(value, "\n") {
		wrapped := ansi.HardwrapWc(line, width, true)
		if wrapped == "" {
			rows++
			continue
		}
		rows += strings.Count(wrapped, "\n") + 1
	}
	if rows < 1 {
		rows = 1
	}
	if rows > maxRows {
		rows = maxRows
	}
	return rows
}

func hardWrapDisplayLine(line string, width int) string {
	if width <= 0 || line == "" {
		return line
	}
	return ansi.HardwrapWc(line, width, true)
}

// renderMentionList renders the @mention candidates as an inline list.
func (m *Model) renderMentionList() string {
	if len(m.mentionCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.mentionCandidates))
	var lines []string
	for i := 0; i < maxItems; i++ {
		prefix := "  "
		if i == m.mentionIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render("@"+m.mentionCandidates[i]))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render("@"+m.mentionCandidates[i]))
		}
	}
	if len(m.mentionCandidates) > maxItems {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.mentionCandidates)-maxItems),
		))
	}
	return strings.Join(lines, "\n")
}
