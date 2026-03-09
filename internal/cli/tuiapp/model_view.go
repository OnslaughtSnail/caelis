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

	// 1. Viewport (scrollable history + streaming + spinner) with left gutter.
	vpView := m.viewport.View()
	if tuikit.GutterNarrative > 0 {
		vpView = indentBlock(vpView, tuikit.GutterNarrative)
	}
	sections = append(sections, vpView)
	sections = append(sections, "")

	// 2. Hint row (contextual guidance).
	sections = append(sections, m.renderHintRow())
	sections = append(sections, "")

	// 3. Workspace + model status.
	sections = append(sections, m.renderStatusHeader())

	// 4. Separator above the composer input.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}

	// 5. Composer top padding before input.
	for i := 0; i < tuikit.ComposerPadTop; i++ {
		sections = append(sections, "")
	}

	// 6. Prompt choices (if any).
	if m.activePrompt != nil {
		if promptView := m.renderPromptModal(); promptView != "" {
			sections = append(sections, promptView)
		}
	}

	// 7. Input bar.
	sections = append(sections, m.renderInputBar())

	// 8. @mention candidates inline list (below input, above status).
	if len(m.mentionCandidates) > 0 {
		sections = append(sections, m.renderMentionList())
	}

	// 9. $skill candidates inline list.
	if len(m.skillCandidates) > 0 {
		sections = append(sections, m.renderSkillList())
	}

	// 10. /resume candidates inline list.
	if len(m.resumeCandidates) > 0 {
		sections = append(sections, m.renderResumeList())
	}

	// 11. Generic slash argument candidates inline list.
	if len(m.slashArgCandidates) > 0 {
		sections = append(sections, m.renderSlashArgList())
	}

	// 12. Slash command candidates inline list.
	if len(m.slashCandidates) > 0 {
		sections = append(sections, m.renderSlashCommandList())
	}

	// 13. Composer bottom padding before footer separator.
	for i := 0; i < tuikit.ComposerPadBottom; i++ {
		sections = append(sections, "")
	}

	// 14. Lower separator + secondary status bar.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}
	sections = append(sections, m.renderStatusFooter())

	// 15. Status bar bottom padding.
	for i := 0; i < tuikit.StatusBarPadBottom; i++ {
		sections = append(sections, "")
	}

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

	// Spacer + hint row + hint/header gap + workspace/model row + composer top separator.
	lines += 5

	// Composer top padding between workspace/model row and input.
	lines += tuikit.ComposerPadTop

	// Input bar (with minimum height).
	inputH := maxInt(tuikit.ComposerMinHeight, m.textarea.Height())
	lines += inputH

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

	// Composer bottom padding.
	lines += tuikit.ComposerPadBottom

	// Lower separator + status footer.
	lines += 2

	// Status bar bottom padding.
	lines += tuikit.StatusBarPadBottom

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

func (m *Model) clearFixedSelection() {
	m.fixedSelecting = false
	m.fixedSelectionArea = fixedSelectionNone
	m.fixedSelectionStart = textSelectionPoint{line: -1, col: -1}
	m.fixedSelectionEnd = textSelectionPoint{line: -1, col: -1}
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

	col := x - tuikit.GutterNarrative
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
	// spacer + hint + hint/header gap + workspace/model + separator = 5 lines above padding
	y += 5
	// composer top padding
	y += tuikit.ComposerPadTop
	if m.activePrompt != nil && len(m.activePrompt.choices) > 0 {
		n := len(m.visiblePromptChoices())
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	h := maxInt(tuikit.ComposerMinHeight, m.textarea.Height())
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

type fixedTextRegion struct {
	area fixedSelectionArea
	y    int
	text string
}

func (m *Model) fixedTextRegions() []fixedTextRegion {
	layout := m.fixedRowLayout()
	return []fixedTextRegion{
		{area: fixedSelectionHint, y: layout.hintY, text: m.hintRowText()},
		{area: fixedSelectionHeader, y: layout.headerY, text: m.headerRowText()},
		{area: fixedSelectionFooter, y: layout.footerY, text: m.footerRowText()},
	}
}

type fixedRowLayout struct {
	hintY   int
	headerY int
	footerY int
}

func (m *Model) fixedRowLayout() fixedRowLayout {
	y := m.viewport.Height
	layout := fixedRowLayout{
		hintY:   y + 1,
		headerY: y + 3,
	}
	y += 5 // spacer + hint + hint/header gap + workspace/model + separator
	y += tuikit.ComposerPadTop
	if m.activePrompt != nil && len(m.activePrompt.choices) > 0 {
		n := len(m.visiblePromptChoices())
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	y += maxInt(tuikit.ComposerMinHeight, m.textarea.Height())
	if n := len(m.mentionCandidates); n > 0 {
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	if n := len(m.skillCandidates); n > 0 {
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	if n := len(m.resumeCandidates); n > 0 {
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	if n := len(m.slashArgCandidates); n > 0 {
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	if n := len(m.slashCandidates); n > 0 {
		y += minInt(8, n)
		if n > 8 {
			y++
		}
	}
	y += tuikit.ComposerPadBottom // composer bottom padding
	y++                           // lower separator
	layout.footerY = y
	return layout
}

func (m *Model) fixedRegionAt(y int) (fixedTextRegion, bool) {
	for _, region := range m.fixedTextRegions() {
		if region.y == y {
			return region, true
		}
	}
	return fixedTextRegion{}, false
}

func (m *Model) fixedRowPoint(region fixedTextRegion, x int, clamp bool) (textSelectionPoint, bool) {
	contentWidth := maxInt(1, maxInt(20, m.width)-(tuikit.StatusInset*2))
	col := x - tuikit.StatusInset // account for status-row horizontal padding
	if clamp {
		if col < 0 {
			col = 0
		}
		if col > contentWidth {
			col = contentWidth
		}
	} else if col < 0 || col > contentWidth {
		return textSelectionPoint{}, false
	}
	lineWidth := displayColumns(region.text)
	if col > lineWidth {
		col = lineWidth
	}
	return textSelectionPoint{line: 0, col: col}, true
}

func (m *Model) fixedSelectionText() string {
	if m.fixedSelectionArea == fixedSelectionNone {
		return ""
	}
	start, end, ok := normalizedSelectionRange(m.fixedSelectionStart, m.fixedSelectionEnd, 1)
	if !ok {
		return ""
	}
	for _, region := range m.fixedTextRegions() {
		if region.area == m.fixedSelectionArea {
			return selectionTextFromLines([]string{region.text}, start, end)
		}
	}
	return ""
}

func (m *Model) renderFixedRow(area fixedSelectionArea, text string, style lipgloss.Style) string {
	line := text
	if m.fixedSelectionArea == area {
		start, end, ok := normalizedSelectionRange(m.fixedSelectionStart, m.fixedSelectionEnd, 1)
		if ok && (start.line != end.line || start.col != end.col) {
			line = renderSelectionOnLines([]string{text}, start, end)[0]
		}
	}
	return style.Render(line)
}

// ---------------------------------------------------------------------------
// View sub-components
// ---------------------------------------------------------------------------

func (m *Model) buildHintLine() string {
	return m.renderHintRow()
}

// indentBlock adds a fixed left margin to every line of a multi-line block.
func indentBlock(block string, indent int) string {
	if indent <= 0 || block == "" {
		return block
	}
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(block, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
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
	frame := "•"
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

func (m *Model) renderInputBar() string {
	if m.activePrompt != nil {
		return insetRenderedBlock(m.renderPromptInputBar(), inputHorizontalInset)
	}
	if start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines())); ok &&
		(start.line != end.line || start.col != end.col) {
		lines := m.inputPlainLines()
		return strings.Join(renderSelectionOnLines(lines, start, end), "\n")
	}
	prompt := m.theme.PromptStyle().Render("> ")
	inputVal := m.textarea.View()
	if m.isWizardActive() && m.wizard.hideInput() {
		query, _ := wizardQueryAtCursor(m.wizard.def.Command, m.input, m.cursor)
		inputVal = "/" + m.wizard.def.Command + " " + strings.Repeat("*", utf8.RuneCountInString(strings.TrimSpace(query)))
	}
	inputLine := renderMultilineInput(prompt, inputVal)
	return insetRenderedBlock(inputLine, inputHorizontalInset)
}

func (m *Model) inputPlainLines() []string {
	prompt := "> "
	indent := strings.Repeat(" ", lipgloss.Width(prompt))
	inset := strings.Repeat(" ", inputHorizontalInset)
	value := m.textarea.Value()
	if value == "" {
		return []string{inset + prompt}
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
			out = append(out, inset+prompt+line)
			continue
		}
		out = append(out, inset+indent+line)
	}
	return out
}

func insetRenderedBlock(text string, inset int) string {
	if inset <= 0 || text == "" {
		return text
	}
	pad := strings.Repeat(" ", inset)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
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

func (m *Model) renderStatusHeader() string {
	style := m.theme.StatusStyle().Width(maxInt(20, m.width))
	return m.renderFixedRow(fixedSelectionHeader, m.headerRowText(), style)
}

func (m *Model) renderHintRow() string {
	style := m.theme.HintRowStyle().Width(maxInt(20, m.width))
	return m.renderFixedRow(fixedSelectionHint, m.hintRowText(), style)
}

func (m *Model) hintRowText() string {
	w := maxInt(20, m.width)
	return tuikit.ComposeFooter(w-(tuikit.StatusInset*2), m.buildHintText(), "")
}

func (m *Model) headerRowText() string {
	w := maxInt(20, m.width)
	left := strings.TrimSpace(m.cfg.Workspace)
	right := strings.TrimSpace(m.statusModel)
	return tuikit.ComposeFooter(w-(tuikit.StatusInset*2), left, right)
}

func (m *Model) renderStatusFooter() string {
	style := m.theme.StatusStyle().Width(maxInt(20, m.width))
	if m.fixedSelectionArea == fixedSelectionFooter {
		return m.renderFixedRow(fixedSelectionFooter, m.footerRowText(), style)
	}
	contentWidth := maxInt(1, maxInt(20, m.width)-(tuikit.StatusInset*2))
	left := m.renderFooterLeft()
	right := m.theme.TextStyle().Render(strings.TrimSpace(m.statusContext))
	return style.Render(composeStyledFooter(contentWidth, left, right))
}

func (m *Model) footerRowText() string {
	w := maxInt(20, m.width)
	left := m.footerLeftText()
	right := strings.TrimSpace(m.statusContext)
	return tuikit.ComposeFooter(w-(tuikit.StatusInset*2), left, right)
}

func (m *Model) footerLeftText() string {
	if m.cfg.ModeLabel == nil {
		return ""
	}
	mode := strings.TrimSpace(m.cfg.ModeLabel())
	if mode == "" {
		return ""
	}
	return mode + "  shift+tab switch mode"
}

func (m *Model) renderFooterLeft() string {
	if m.cfg.ModeLabel == nil {
		return ""
	}
	mode := strings.TrimSpace(m.cfg.ModeLabel())
	if mode == "" {
		return ""
	}
	modeText := m.theme.TextStyle().Bold(true).Render(mode)
	hintText := m.theme.HelpHintTextStyle().Render("shift+tab switch mode")
	return modeText + "  " + hintText
}

func composeStyledFooter(width int, left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if width <= 0 {
		return ""
	}
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if left == "" && right == "" {
		return strings.Repeat(" ", width)
	}
	if left == "" {
		if rightWidth >= width {
			return right
		}
		return strings.Repeat(" ", width-rightWidth) + right
	}
	if right == "" {
		if leftWidth >= width {
			return left
		}
		return left + strings.Repeat(" ", width-leftWidth)
	}
	gap := width - leftWidth - rightWidth
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
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
	if height < tuikit.ComposerMinHeight {
		height = tuikit.ComposerMinHeight
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
	return insetRenderedBlock(strings.Join(lines, "\n"), inputHorizontalInset)
}
