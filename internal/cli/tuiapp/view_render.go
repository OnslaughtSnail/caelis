package tuiapp

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// View sub-components
// ---------------------------------------------------------------------------

func (m *Model) windowTitle() string {
	title := "CAELIS"
	if alias := strings.TrimSpace(m.statusModel); alias != "" {
		title += " • " + alias
	}
	if m.running {
		title += " • running"
	}
	return title
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
		return m.overlayHintText("/resume")
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
		return m.overlayHintText(label)
	}
	// Show slash command guidance.
	if len(m.slashCandidates) > 0 {
		return m.overlayHintText("/")
	}
	return ""
}

func (m *Model) primaryDrawerHeight() int {
	drawer := m.renderPrimaryDrawer()
	if drawer == "" {
		return 0
	}
	return strings.Count(drawer, "\n") + 1
}

func (m *Model) renderPrimaryDrawer() string {
	if drawer := m.renderBTWDrawer(); drawer != "" {
		return drawer
	}
	return m.renderPlanDrawer()
}

func (m *Model) renderPlanDrawer() string {
	if len(m.planEntries) == 0 || m.width <= 0 {
		return ""
	}
	visible, _, _ := visiblePlanEntries(m.planEntries, m.planVisibleBudget())
	if len(visible) == 0 {
		return ""
	}
	contentWidth := maxInt(1, m.width-(inputHorizontalInset*2))
	lines := []string{m.theme.SeparatorStyle().Render(strings.Repeat("─", contentWidth))}
	for _, item := range visible {
		lines = append(lines, renderPlanLine(m, item))
	}
	return insetRenderedBlock(strings.Join(lines, "\n"), inputHorizontalInset)
}

func (m *Model) btwVisibleBudget() int {
	switch {
	case m.height <= 18:
		return 4
	case m.height <= 24:
		return 6
	case m.height <= 32:
		return 8
	default:
		return 10
	}
}

func (m *Model) btwContentWidth() int {
	return maxInt(1, m.width-(inputHorizontalInset*2))
}

const pendingSubmissionIcon = "↪"

func (m *Model) renderPendingSubmissionLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return m.theme.HelpHintTextStyle().Render(pendingSubmissionIcon + " " + text)
}

func (m *Model) btwContentLines() []string {
	if m == nil || m.btwOverlay == nil || m.width <= 0 {
		return nil
	}
	contentWidth := m.btwContentWidth()
	rawLines := make([]string, 0, 16)
	question := strings.TrimSpace(m.btwOverlay.Question)
	answer := strings.TrimSpace(m.btwOverlay.Answer)
	if answer == "" && m.btwOverlay.Loading {
		if pendingLine := m.renderPendingSubmissionLine(question); pendingLine != "" {
			rawLines = append(rawLines, pendingLine)
		}
		return wrapBTWContentLines(rawLines, contentWidth)
	}
	if question != "" {
		rawLines = append(rawLines, m.theme.HelpHintTextStyle().Render(question), "")
	}
	if answer != "" {
		for line := range strings.SplitSeq(answer, "\n") {
			rawLines = append(rawLines, m.theme.TextStyle().Render(strings.TrimRight(line, "\r")))
		}
	}
	if len(rawLines) == 0 {
		return nil
	}
	return wrapBTWContentLines(rawLines, contentWidth)
}

func wrapBTWContentLines(rawLines []string, contentWidth int) []string {
	lines := make([]string, 0, len(rawLines)*2)
	for _, line := range rawLines {
		wrapped := hardWrapDisplayLine(line, contentWidth)
		if wrapped == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}

func (m *Model) btwMaxScroll(total int) int {
	visible := m.btwVisibleBudget()
	if total <= visible {
		return 0
	}
	return total - visible
}

func (m *Model) clampBTWScroll(total int) {
	if m == nil || m.btwOverlay == nil {
		return
	}
	if m.btwOverlay.Scroll < 0 {
		m.btwOverlay.Scroll = 0
	}
	maxScroll := m.btwMaxScroll(total)
	if m.btwOverlay.Scroll > maxScroll {
		m.btwOverlay.Scroll = maxScroll
	}
}

func (m *Model) scrollBTW(delta int) {
	if m == nil || m.btwOverlay == nil || delta == 0 {
		return
	}
	total := len(m.btwContentLines())
	m.clampBTWScroll(total)
	maxScroll := m.btwMaxScroll(total)
	next := m.btwOverlay.Scroll + delta
	next = max(next, 0)
	next = min(next, maxScroll)
	m.btwOverlay.Scroll = next
}

func (m *Model) renderBTWDrawer() string {
	if m == nil || m.btwOverlay == nil || m.width <= 0 {
		return ""
	}
	lines := m.btwContentLines()
	m.clampBTWScroll(len(lines))
	start := max(m.btwOverlay.Scroll, 0)
	end := minInt(len(lines), start+m.btwVisibleBudget())
	if start > end {
		start = end
	}
	contentWidth := m.btwContentWidth()
	drawerLines := []string{m.theme.SeparatorStyle().Render(strings.Repeat("─", contentWidth))}
	drawerLines = append(drawerLines, lines[start:end]...)
	return insetRenderedBlock(strings.Join(drawerLines, "\n"), inputHorizontalInset)
}

func renderPlanLine(m *Model, item planEntryState) string {
	icon := "☐"
	iconStyle := m.theme.HelpHintTextStyle()
	textStyle := m.theme.HelpHintTextStyle()
	switch strings.TrimSpace(item.Status) {
	case "completed":
		icon = "✔"
		iconStyle = m.theme.NoteStyle()
		textStyle = m.theme.NoteStyle().Strikethrough(true)
	case "in_progress":
		iconStyle = lipgloss.NewStyle().Foreground(m.theme.Focus).Bold(true)
		textStyle = lipgloss.NewStyle().Foreground(m.theme.Focus).Bold(true)
	}
	return iconStyle.Render(icon) + " " + textStyle.Render(item.Content)
}

func (m *Model) planVisibleBudget() int {
	switch {
	case m.height <= 18:
		return 1
	case m.height <= 22:
		return 2
	case m.height <= 27:
		return 3
	case m.height <= 33:
		return 4
	case m.height <= 40:
		return 5
	default:
		return 6
	}
}

func visiblePlanEntries(entries []planEntryState, limit int) ([]planEntryState, int, int) {
	if limit <= 0 || len(entries) == 0 {
		return nil, len(entries), 0
	}
	if limit >= len(entries) {
		out := append([]planEntryState(nil), entries...)
		return out, 0, 0
	}
	anchor := 0
	found := false
	for idx, item := range entries {
		if strings.TrimSpace(item.Status) == "in_progress" {
			anchor = idx
			found = true
			break
		}
	}
	if !found {
		for idx, item := range entries {
			if strings.TrimSpace(item.Status) != "completed" {
				anchor = idx
				found = true
				break
			}
		}
	}
	if !found {
		anchor = len(entries) - 1
	}
	beforeContext := 0
	if limit >= 3 {
		beforeContext = 1
	}
	start := max(anchor-beforeContext, 0)
	maxStart := len(entries) - limit
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(entries), start+limit)
	visible := append([]planEntryState(nil), entries[start:end]...)
	return visible, len(entries) - len(visible), start
}

func (m *Model) startRunningAnimation() {
	m.runningTick = 0
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
	m.runningTip = 0
}

func (m *Model) advanceRunningAnimation() {
	if len(runningCarouselLines) > 0 {
		m.runningTick++
		if m.runningTick%runningHintRotateEveryTicks == 0 {
			m.runningTip = (m.runningTip + 1) % len(runningCarouselLines)
		}
	}
}

func (m *Model) buildRunningHintText() string {
	frame := strings.TrimSpace(ansi.Strip(m.spinner.View()))
	if frame == "" {
		frame = "⠋"
	}
	if len(runningCarouselLines) > 0 {
		text := m.renderRunningTickerText(runningCarouselLines[m.runningTip%len(runningCarouselLines)])
		prefix := m.theme.SpinnerStyle().Render(frame)
		return prefix + " " + text
	}
	return m.theme.SpinnerStyle().Render(frame)
}

func (m *Model) renderRunningTickerText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	totalWidth := maxInt(1, displayColumns(text))
	pathWidth := float64(totalWidth) + (runningLightLead * 2)
	head := math.Mod(float64(m.runningTick)*runningLightSpeed, pathWidth) - runningLightLead
	styles := []lipgloss.Style{
		m.theme.HelpHintTextStyle(),
		lipgloss.NewStyle().Foreground(m.theme.TextSecondary),
		lipgloss.NewStyle().Foreground(m.theme.Info),
		lipgloss.NewStyle().Foreground(m.theme.SpinnerFg),
		lipgloss.NewStyle().Foreground(m.theme.Focus),
	}

	var out strings.Builder
	column := 0
	for _, r := range runes {
		runeWidth := maxInt(1, displayColumns(string(r)))
		center := float64(column) + (float64(runeWidth) / 2)
		distance := math.Abs(center - head)
		level := 0
		intensity := 1 - (distance / runningLightBandRadius)
		switch {
		case intensity >= 0.82:
			level = 4
		case intensity >= 0.62:
			level = 3
		case intensity >= 0.42:
			level = 2
		case intensity >= 0.22:
			level = 1
		}
		out.WriteString(styles[level].Render(string(r)))
		column += runeWidth
	}
	return out.String()
}

func (m *Model) pendingQueueHintText() string {
	if m.pendingQueue == nil {
		return ""
	}
	return "1 pending message"
}

func (m *Model) renderPendingQueueDrawer() string {
	if m.pendingQueue == nil || m.width <= 0 {
		return ""
	}
	contentWidth := maxInt(1, m.width-(inputHorizontalInset*2))
	lines := []string{m.theme.SeparatorStyle().Render(strings.Repeat("─", contentWidth))}
	text := strings.TrimSpace(m.pendingQueue.displayLine)
	if text == "" {
		text = strings.TrimSpace(m.pendingQueue.execLine)
	}
	if pendingLine := m.renderPendingSubmissionLine(text); pendingLine != "" {
		lines = append(lines, pendingLine)
	}
	return insetRenderedBlock(strings.Join(lines, "\n"), inputHorizontalInset)
}

func (m *Model) dequeuePendingUserMessage(_ string) {
	if m.pendingQueue == nil {
		return
	}
	m.pendingQueue = nil
}

func (m *Model) renderInputBar() string {
	if m.activePrompt != nil {
		return insetRenderedBlock(m.renderPromptInputBar(), inputHorizontalInset)
	}
	if start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines())); ok &&
		(start.line != end.line || start.col != end.col) {
		lines := m.inputPlainLines()
		return insetRenderedBlock(strings.Join(renderSelectionOnLines(lines, start, end), "\n"), inputHorizontalInset)
	}

	prompt := m.theme.PromptStyle().Render("> ")
	if m.isWizardActive() && m.wizard.hideInput() {
		query, _ := wizardQueryAtCursor(m.wizard.def.Command, m.input, m.cursor)
		inputVal := strings.TrimSpace("> /" + m.wizard.def.Command + " " + strings.Repeat("*", utf8.RuneCountInString(strings.TrimSpace(query))))
		return insetRenderedBlock(renderMultilineInput(prompt, inputVal), inputHorizontalInset)
	}
	return m.renderRegularInputBar()
}

func (m *Model) syncTextareaChrome() {
	ta := m.textarea
	m.applyTextareaChrome(&ta)
	m.textarea = ta
}

func (m *Model) applyTextareaChrome(ta *textarea.Model) {
	if ta == nil {
		return
	}
	if m == nil {
		return
	}
	first := m.inputPromptPrefix()
	width := displayColumns(first)
	if width <= 0 {
		first = "> "
		width = displayColumns(first)
	}
	continuation := strings.Repeat(" ", width)
	ta.SetPromptFunc(width, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return first
		}
		return continuation
	})
	ta.SetWidth(m.composerContentWidth())
	displayValue, _ := composeInputDisplay(ta.Value(), len([]rune(ta.Value())), m.inputAttachments)
	height := max(desiredComposerRows(displayValue, "", ta.Width(), maxInputBarRows), tuikit.ComposerMinHeight)
	ta.SetHeight(height)
}

func (m *Model) inputPromptPrefix() string {
	return "> "
}

func (m *Model) currentInputGhostHint() string {
	if m == nil || m.activePrompt != nil || m.running {
		return ""
	}
	value := m.textarea.Value()
	if value == "" || strings.Contains(value, "\n") {
		return ""
	}
	if m.cursor != len(m.input) {
		return ""
	}

	suggestion := ""
	switch {
	case len(m.slashCandidates) > 0 && m.slashIndex >= 0 && m.slashIndex < len(m.slashCandidates):
		suggestion = strings.TrimSpace(m.slashCandidates[m.slashIndex])
	case len(m.resumeCandidates) > 0 && m.resumeIndex >= 0 && m.resumeIndex < len(m.resumeCandidates):
		selected := strings.TrimSpace(m.resumeCandidates[m.resumeIndex].SessionID)
		if selected != "" {
			suggestion = "/resume " + selected
		}
	case len(m.slashArgCandidates) > 0 && m.slashArgIndex >= 0 && m.slashArgIndex < len(m.slashArgCandidates):
		selected := strings.TrimSpace(m.slashArgCandidates[m.slashArgIndex].Value)
		suggestion = m.suggestedSlashArgInput(selected)
	}
	if suggestion == "" || !strings.HasPrefix(suggestion, value) {
		return ""
	}
	return suggestion[len(value):]
}

func (m *Model) suggestedSlashArgInput(choice string) string {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return ""
	}
	command := strings.TrimSpace(m.slashArgCommand)
	switch {
	case command == "model":
		return "/model " + choice
	case command == "model use":
		return "/model use " + choice
	case strings.HasPrefix(command, "model use "):
		return "/" + command + " " + choice
	default:
		if command == "" {
			return ""
		}
		return "/" + command + " " + choice
	}
}

func (m *Model) inputPlainLines() []string {
	return m.regularInputPlainLines()
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
	return m.renderFixedRow(fixedSelectionHeader, m.headerRowText(), m.headerRowText(), style)
}

func (m *Model) renderHintRow() string {
	style := m.theme.HintRowStyle().Width(maxInt(20, m.width))
	return m.renderFixedRow(fixedSelectionHint, m.hintRowText(), m.renderHintRowStyledText(), style)
}

func (m *Model) hintRowText() string {
	w := maxInt(20, m.width)
	return composeStyledFooter(w-(tuikit.StatusInset*2), m.buildHintText(), "")
}

func (m *Model) renderHintRowStyledText() string {
	w := maxInt(20, m.width) - (tuikit.StatusInset * 2)
	if w <= 0 {
		return ""
	}
	text := m.buildHintText()
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return composeStyledFooter(w, text, "")
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
		return m.renderFixedRow(fixedSelectionFooter, m.footerRowText(), m.renderFooterRowStyledText(), style)
	}
	contentWidth := maxInt(1, maxInt(20, m.width)-(tuikit.StatusInset*2))
	left := m.renderFooterLeft()
	right := m.theme.TextStyle().Render(strings.TrimSpace(m.statusContext))
	return style.Render(composeStyledFooter(contentWidth, left, right))
}

func (m *Model) shouldRenderPalette() bool {
	return m.showPalette || m.paletteAnimLines > 0
}

func (m *Model) fullPaletteLineCount() int {
	if m.width <= 0 || m.height <= 0 {
		return 0
	}
	text := ansi.Strip(m.theme.ModalStyle().Render(m.palette.View()))
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func (m *Model) renderPaletteOverlay() string {
	full := m.theme.ModalStyle().Render(m.palette.View())
	if full == "" {
		return ""
	}
	lines := strings.Split(full, "\n")
	visible := m.paletteAnimLines
	if visible <= 0 {
		return ""
	}
	if visible >= len(lines) {
		return full
	}
	return strings.Join(lines[len(lines)-visible:], "\n")
}

func (m *Model) renderViewportScrollbar(vpView string) string {
	if m.viewportScrollbarWidth() == 0 || vpView == "" {
		return vpView
	}
	total := m.viewport.TotalLineCount()
	visible := maxInt(1, m.viewport.Height())
	if total <= visible {
		return vpView
	}
	lines := strings.Split(vpView, "\n")
	if len(lines) == 0 {
		return vpView
	}
	thumbHeight := maxInt(1, visible*visible/maxInt(visible, total))
	maxStart := maxInt(0, visible-thumbHeight)
	thumbStart := 0
	if total > visible && maxStart > 0 {
		thumbStart = (m.viewport.YOffset() * maxStart) / maxInt(1, total-visible)
	}
	for i := range lines {
		glyph := m.theme.ScrollbarTrackStyle().Render("│")
		if i >= thumbStart && i < thumbStart+thumbHeight {
			glyph = m.theme.ScrollbarThumbStyle().Render("█")
		}
		if pad := m.viewport.Width() - lipgloss.Width(lines[i]); pad > 0 {
			lines[i] += strings.Repeat(" ", pad)
		}
		lines[i] += glyph
	}
	return strings.Join(lines, "\n")
}

func (m *Model) footerRowText() string {
	w := maxInt(20, m.width)
	left := m.footerLeftText()
	right := strings.TrimSpace(m.statusContext)
	return tuikit.ComposeFooter(w-(tuikit.StatusInset*2), left, right)
}

func (m *Model) footerLeftText() string {
	mode := strings.TrimSpace(m.modeLabel())
	helpText := m.footerHelpText()
	switch {
	case mode == "":
		return helpText
	case helpText == "":
		return mode
	default:
		return mode + "  " + helpText
	}
}

func (m *Model) renderFooterLeft() string {
	mode := strings.TrimSpace(m.modeLabel())
	if mode == "" {
		return m.renderHelp(m.currentFooterHelp())
	}
	modeStyle := m.theme.TextStyle().Bold(true)
	if mode == "full_access" {
		modeStyle = m.theme.WarnStyle().Bold(true)
	}
	modeText := modeStyle.Render(mode)
	helpText := m.renderHelp(m.currentFooterHelp())
	if helpText == "" {
		return modeText
	}
	return modeText + "  " + helpText
}

func (m *Model) renderFooterRowStyledText() string {
	w := maxInt(20, m.width)
	left := m.renderFooterLeft()
	right := m.theme.TextStyle().Render(strings.TrimSpace(m.statusContext))
	return composeStyledFooter(w-(tuikit.StatusInset*2), left, right)
}

func (m *Model) footerHelpText() string {
	return ansi.Strip(m.renderHelp(m.currentFooterHelp()))
}

func (m *Model) modeLabel() string {
	if m.cfg.ModeLabel == nil {
		return ""
	}
	return strings.TrimSpace(m.cfg.ModeLabel())
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
	gap := max(width-leftWidth-rightWidth, 1)
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
	bodyLines := make([]string, 0, 24)
	if title := strings.TrimSpace(p.title); title != "" {
		bodyLines = append(bodyLines, m.theme.TitleStyle().Render(title))
	}
	if len(p.details) > 0 {
		if len(bodyLines) > 0 {
			bodyLines = append(bodyLines, "")
		}
		bodyLines = append(bodyLines, m.renderPromptDetailLines(p.details)...)
	}
	visible := m.visiblePromptChoices()
	if len(visible) == 0 {
		if len(bodyLines) > 0 {
			bodyLines = append(bodyLines, "")
		}
		bodyLines = append(bodyLines, m.theme.HelpHintTextStyle().Render("no matching choices"))
		return m.renderPromptModalBox(bodyLines)
	}
	const maxVisiblePromptChoices = 8
	m.syncPromptChoiceWindow()
	start := max(m.activePrompt.scrollOffset, 0)
	start = min(start, len(visible))
	end := minInt(len(visible), start+maxVisiblePromptChoices)
	window := visible[start:end]
	maxItems := len(window)
	lines := make([]string, 0, maxItems+1)
	for i := range maxItems {
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
				line += "  " + m.theme.HelpHintTextStyle().Render(choice.detail)
			}
			lines = append(lines, line)
			continue
		}
		line := "  " + m.theme.TextStyle().Render(marker+choice.label)
		if choice.detail != "" {
			line += "  " + m.theme.HelpHintTextStyle().Render(choice.detail)
		}
		lines = append(lines, line)
	}
	if len(visible) > end {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("… and %d more", len(visible)-end),
		))
	}
	if len(bodyLines) > 0 {
		bodyLines = append(bodyLines, "")
	}
	bodyLines = append(bodyLines, lines...)
	return m.renderPromptModalBox(bodyLines)
}

func (m *Model) renderPromptDetailLines(details []tuievents.PromptDetail) []string {
	if len(details) == 0 {
		return nil
	}
	lines := make([]string, 0, len(details)*2)
	for _, detail := range details {
		label := strings.TrimSpace(detail.Label)
		value := strings.TrimSpace(detail.Value)
		if label == "" || value == "" {
			continue
		}
		valueStyle := m.theme.TextStyle()
		if detail.Emphasis {
			valueStyle = valueStyle.Bold(true)
		}
		valueLines := strings.Split(value, "\n")
		first := strings.TrimRight(valueLines[0], "\r")
		if strings.TrimSpace(first) == "" {
			continue
		}
		lines = append(lines, m.theme.KeyLabelStyle().Render(strings.ToUpper(label)+":")+" "+valueStyle.Render(first))
		for _, line := range valueLines[1:] {
			line = strings.TrimRight(line, "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, "  "+valueStyle.Render(line))
		}
	}
	return lines
}

func (m *Model) renderPromptModalBox(lines []string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		filtered = append(filtered, line)
	}
	body := strings.Join(filtered, "\n")
	inset := tuikit.GutterNarrative
	width := minInt(maxInt(44, m.width-(inset*2)), 96)
	if width <= 0 {
		width = 72
	}
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Focus).
		Padding(0, 1).
		Width(width)
	return insetRenderedBlock(box.Render(body), inset)
}

func (m *Model) renderCompletionOverlay(title string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	bodyLines := make([]string, 0, len(lines)+2)
	if title = strings.TrimSpace(title); title != "" {
		bodyLines = append(bodyLines, m.theme.TitleStyle().Render(title), "")
	}
	bodyLines = append(bodyLines, lines...)
	return m.renderPromptModalBox(bodyLines)
}

func (m *Model) renderInputOverlay() string {
	switch {
	case len(m.mentionCandidates) > 0:
		return m.renderMentionList()
	case len(m.skillCandidates) > 0:
		return m.renderSkillList()
	case len(m.resumeCandidates) > 0:
		return m.renderResumeList()
	case len(m.slashArgCandidates) > 0:
		return m.renderSlashArgList()
	case len(m.slashCandidates) > 0:
		return m.renderSlashCommandList()
	default:
		return ""
	}
}

func (m *Model) renderPromptInputBar() string {
	prompt := m.theme.PromptStyle().Render("> ")
	value, cursor := m.promptInputValue()
	return renderMultilineInput(prompt, insertPromptCursor(value, cursor, m.promptCursorGlyph()))
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

func (m *Model) promptCursorGlyph() string {
	return m.theme.PromptStyle().Render("█")
}

func (m *Model) promptHintText() string {
	if m.activePrompt == nil {
		return ""
	}
	text := strings.TrimSpace(m.activePrompt.prompt)
	if text == "" {
		text = strings.TrimSpace(m.activePrompt.title)
	}
	text = strings.TrimSuffix(text, ":")
	text = strings.TrimSpace(text)
	if len(m.activePrompt.choices) > 0 {
		footer := "↑/↓ move  enter confirm  esc cancel"
		if m.activePrompt.filterable {
			if m.activePrompt.multiSelect {
				return "type filter  space toggle  " + footer
			}
			return "type filter  " + footer
		}
		if m.activePrompt.multiSelect {
			return "space toggle  " + footer
		}
		return footer
	}
	if text == "" {
		return "Enter a value"
	}
	return "Enter " + text
}

func (m *Model) adjustTextareaHeight() {
	displayValue, _ := composeInputDisplay(m.textarea.Value(), len([]rune(m.textarea.Value())), m.inputAttachments)
	height := max(desiredComposerRows(displayValue, "", m.textarea.Width(), maxInputBarRows), tuikit.ComposerMinHeight)
	if m.textarea.Height() != height {
		m.textarea.SetHeight(height)
		// Textarea height change affects bottomSectionHeight; reconcile
		// the viewport so View() doesn't need to mutate state.
		m.ensureViewportLayout()
	}
}

func hardWrapDisplayLine(line string, width int) string {
	if width <= 0 || line == "" {
		return line
	}
	return ansi.Hardwrap(line, width, true)
}

// renderMentionList renders the @mention candidates as an overlay list.
func (m *Model) renderMentionList() string {
	if len(m.mentionCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.mentionCandidates))
	var lines []string
	for i := range maxItems {
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
	title := "Agents"
	if m.mentionPrefix == "#" {
		title = "Files"
	}
	return m.renderCompletionOverlay(title, lines)
}
