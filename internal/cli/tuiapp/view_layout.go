package tuiapp

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

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

	// Spacer + optional plan + optional pending queue + hint row + hint/header
	// gap + workspace/model row + composer top separator.
	lines += m.preComposerFixedHeight()

	// Composer top padding between workspace/model row and input.
	lines += tuikit.ComposerPadTop

	// Input bar (with minimum height).
	inputH := maxInt(tuikit.ComposerMinHeight, m.textarea.Height())
	lines += inputH

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
	wrapWidth := maxInt(1, m.viewport.Width())
	lines := make([]string, 0, len(m.historyLines)+8)

	// 1. All committed history lines.
	for _, line := range m.historyLines {
		line = m.adaptHistoryLineForViewport(line, wrapWidth)
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

func (m *Model) adaptHistoryLineForViewport(line string, wrapWidth int) string {
	plain := strings.TrimSpace(ansi.Strip(line))
	if !strings.HasPrefix(plain, "▸ DELEGATE ") {
		return line
	}
	taskText := strings.TrimSpace(strings.TrimPrefix(plain, "▸ DELEGATE "))
	if taskText == "" {
		return line
	}
	style := tuikit.LineStyleTool
	gutter := tuikit.LineExtraGutter(style)
	available := wrapWidth - displayColumns(gutter) - displayColumns("▸ DELEGATE ")
	if available < 16 {
		available = 16
	}
	targetWidth := minInt(available, maxInt(24, wrapWidth*2/3))
	adapted := "▸ DELEGATE " + truncateMiddleDisplay(taskText, targetWidth)
	colored := tuikit.ColorizeLogLine(adapted, style, m.theme)
	return gutter + colored
}

func truncateMiddleDisplay(text string, width int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || width <= 0 || displayColumns(text) <= width {
		return text
	}
	ellipsis := "......"
	ellipsisWidth := displayColumns(ellipsis)
	if width <= ellipsisWidth {
		return sliceByDisplayColumns(text, 0, width)
	}
	head := (width - ellipsisWidth) * 2 / 3
	tail := (width - ellipsisWidth) - head
	if head <= 0 {
		head = 1
	}
	if tail <= 0 {
		tail = 1
	}
	total := displayColumns(text)
	prefix := sliceByDisplayColumns(text, 0, head)
	suffix := sliceByDisplayColumns(text, total-tail, total)
	return prefix + ellipsis + suffix
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
	if len(m.viewportPlainLines) == 0 || m.viewport.Height() <= 0 {
		return textSelectionPoint{}, false
	}
	vy := y
	if clamp {
		if vy < 0 {
			vy = 0
		}
		if vy >= m.viewport.Height() {
			vy = m.viewport.Height() - 1
		}
	} else if vy < 0 || vy >= m.viewport.Height() {
		return textSelectionPoint{}, false
	}

	line := m.viewport.YOffset() + vy
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
	y := m.viewport.Height()
	y += m.preComposerFixedHeight()
	// composer top padding
	y += tuikit.ComposerPadTop
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
	col := x - inputHorizontalInset
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
	y := m.viewport.Height()
	layout := fixedRowLayout{
		hintY:   y + 1 + m.primaryDrawerOffsetHeight() + m.pendingQueueSectionHeight(),
		headerY: y + 3 + m.primaryDrawerOffsetHeight() + m.pendingQueueSectionHeight(),
	}
	y += m.preComposerFixedHeight()
	y += tuikit.ComposerPadTop
	y += maxInt(tuikit.ComposerMinHeight, m.textarea.Height())
	y += tuikit.ComposerPadBottom // composer bottom padding
	y++                           // lower separator
	layout.footerY = y
	return layout
}

func (m *Model) preComposerFixedHeight() int {
	return 5 + m.primaryDrawerOffsetHeight() + m.pendingQueueSectionHeight()
}

func (m *Model) primaryDrawerOffsetHeight() int {
	height := m.primaryDrawerHeight()
	if height <= 0 {
		return 0
	}
	return height + 1
}

func (m *Model) pendingQueueSectionHeight() int {
	if m.pendingQueue == nil || m.width <= 0 {
		return 0
	}
	return 3
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

func (m *Model) renderFixedRow(area fixedSelectionArea, plain string, rendered string, style lipgloss.Style) string {
	line := plain
	if m.fixedSelectionArea == area {
		start, end, ok := normalizedSelectionRange(m.fixedSelectionStart, m.fixedSelectionEnd, 1)
		if ok && (start.line != end.line || start.col != end.col) {
			line = renderSelectionOnLines([]string{plain}, start, end)[0]
			return style.Render(line)
		}
	}
	if rendered == "" {
		rendered = line
	}
	return style.Render(rendered)
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
