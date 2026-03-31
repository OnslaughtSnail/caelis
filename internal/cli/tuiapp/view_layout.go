package tuiapp

import (
	"strings"
	"time"

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
	// gap + workspace/model row + composer section label.
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

// renderedStyledLines returns the unwrapped styled lines from all document
// blocks. This replaces the old historyLines cache with an on-demand
// computation directly from the document model.
func (m *Model) renderedStyledLines() []string {
	ctx := BlockRenderContext{
		Width:     maxInt(1, m.viewport.Width()),
		TermWidth: m.width,
		Theme:     m.theme,
	}
	var lines []string
	for _, block := range m.doc.Blocks() {
		for _, row := range block.Render(ctx) {
			lines = append(lines, row.Styled)
		}
	}
	return lines
}

// syncViewportContent rebuilds the viewport content from the document model
// plus any in-progress streaming content, then sets it on the viewport.
// Both styled and plain text are wrapped independently from RenderedRow,
// making RenderedRow the single layout truth.
func (m *Model) syncViewportContent() {
	if m.viewportSyncDepth > 0 {
		m.viewportDirty = true
		return
	}
	wrapWidth := maxInt(1, m.viewport.Width())
	ctx := BlockRenderContext{
		Width:     wrapWidth,
		TermWidth: m.width,
		Theme:     m.theme,
	}

	// Render all blocks → collect RenderedRows (unwrapped).
	var rawRows []RenderedRow
	for _, block := range m.doc.Blocks() {
		rawRows = append(rawRows, block.Render(ctx)...)
	}

	// Build wrapped viewport lines: styled and blockIDs.
	// Plain is derived from styled at the end (rendered-text-first).
	styledLines := make([]string, 0, len(rawRows)+8)
	blockIDs := make([]string, 0, len(rawRows)+8)

	for _, row := range rawRows {
		bid := row.BlockID
		styledLine := m.adaptHistoryLineForViewport(row.Styled, wrapWidth)

		var wrappedStyled string

		if row.PreWrapped {
			// Glamour usually wraps finalized narrative correctly, but very long
			// single tokens (for example URLs) can still exceed the viewport.
			// Keep glamour's layout when it fits, otherwise apply an ANSI-aware
			// hard wrap as a safety net so the viewport never overflows.
			if graphemeWidth(ansi.Strip(styledLine)) > wrapWidth {
				wrappedStyled = hardWrapDisplayLine(styledLine, wrapWidth)
			} else {
				wrappedStyled = styledLine
			}
		} else {
			switch m.renderedRowWrapMode(bid) {
			case BlockAssistant, BlockReasoning:
				// Narrative: word-wrap and re-style.
				wrappedStyled = m.wrapNarrativeRowStyled(row, wrapWidth)
			case BlockParticipantTurn:
				// Participant turns already render wrapped rows in block.Render.
				wrappedStyled = styledLine
			default:
				wrappedStyled = hardWrapDisplayLine(styledLine, wrapWidth)
			}
		}

		if wrappedStyled == "" {
			styledLines = append(styledLines, "")
			blockIDs = append(blockIDs, bid)
			continue
		}

		sParts := strings.Split(wrappedStyled, "\n")
		styledLines = append(styledLines, sParts...)
		for range sParts {
			blockIDs = append(blockIDs, bid)
		}
	}

	// Current streaming partial line (if any).
	if m.streamLine != "" {
		streamLines := strings.Split(m.streamLine, "\n")
		prevStyle := m.lastCommittedStyle
		for _, sl := range streamLines {
			style := tuikit.DetectLineStyleWithContext(sl, prevStyle)

			var wrappedStyled string
			switch style {
			case tuikit.LineStyleAssistant, tuikit.LineStyleReasoning:
				// Word-wrap plain, then apply inline styling.
				segments := graphemeWordWrap(sl, wrapWidth)
				if len(segments) == 0 {
					wrappedStyled = ""
				} else {
					baseStyle := narrativeBodyStyle(style, m.theme)
					styledSegs := make([]string, len(segments))
					for j, seg := range segments {
						styledSegs[j] = renderInlineMarkdown(seg, baseStyle, m.theme)
					}
					wrappedStyled = strings.Join(styledSegs, "\n")
				}
			default:
				colored := tuikit.ColorizeLogLine(sl, style, m.theme)
				wrappedStyled = hardWrapDisplayLine(colored, wrapWidth)
			}

			if wrappedStyled == "" {
				styledLines = append(styledLines, "")
				blockIDs = append(blockIDs, "")
			} else {
				sParts := strings.Split(wrappedStyled, "\n")
				styledLines = append(styledLines, sParts...)
				for range sParts {
					blockIDs = append(blockIDs, "")
				}
			}
			prevStyle = style
		}
	}

	m.viewportStyledLines = append(m.viewportStyledLines[:0], styledLines...)
	m.viewportBlockIDs = append(m.viewportBlockIDs[:0], blockIDs...)

	// Rendered-text-first: derive plain from styled via ANSI strip.
	// This ensures copy text always matches what the user sees on screen.
	m.viewportPlainLines = deriveViewportPlainLines(m.viewportPlainLines[:0], styledLines)

	m.renderViewportContent()
}

func (m *Model) beginDeferredViewportSync() {
	if m == nil {
		return
	}
	m.viewportSyncDepth++
}

func (m *Model) endDeferredViewportSync() {
	if m == nil || m.viewportSyncDepth == 0 {
		return
	}
	m.viewportSyncDepth--
	if m.viewportSyncDepth == 0 && m.viewportDirty {
		m.viewportDirty = false
		m.syncViewportContent()
	}
}

func (m *Model) renderedRowWrapMode(blockID string) BlockKind {
	if blockID == "" {
		return ""
	}
	block := m.doc.Find(blockID)
	if block == nil {
		return ""
	}
	return block.Kind()
}

func (m *Model) wrapNarrativeRowStyled(row RenderedRow, width int) string {
	if width <= 0 {
		return row.Styled
	}
	plain := ansi.Strip(row.Styled)
	// If the line already fits, preserve the original styled text (which may
	// include inline markdown formatting from block.Render).
	if graphemeWidth(plain) <= width {
		return row.Styled
	}
	// Word-wrap plain text, then re-apply inline styling per segment.
	segments := graphemeWordWrap(plain, width)
	if len(segments) == 0 {
		return ""
	}
	roleStyle := tuikit.LineStyleAssistant
	if m.renderedRowWrapMode(row.BlockID) == BlockReasoning {
		roleStyle = tuikit.LineStyleReasoning
	}
	baseStyle := narrativeBodyStyle(roleStyle, m.theme)
	styled := make([]string, 0, len(segments))
	for _, segment := range segments {
		styled = append(styled, renderInlineMarkdown(segment, baseStyle, m.theme))
	}
	return strings.Join(styled, "\n")
}

func (m *Model) adaptHistoryLineForViewport(line string, wrapWidth int) string {
	plain := strings.TrimSpace(ansi.Strip(line))
	prefix := ""
	switch {
	case strings.HasPrefix(plain, "▸ SPAWN "):
		prefix = "▸ SPAWN "
	default:
		return line
	}
	taskText := strings.TrimSpace(strings.TrimPrefix(plain, prefix))
	if taskText == "" {
		return line
	}
	style := tuikit.LineStyleTool
	gutter := tuikit.LineExtraGutter(style)
	available := max(wrapWidth-displayColumns(gutter)-displayColumns(prefix), 16)
	targetWidth := minInt(available, maxInt(24, wrapWidth*2/3))
	adapted := prefix + truncateMiddleDisplay(taskText, targetWidth)
	colored := tuikit.ColorizeLogLine(adapted, style, m.theme)
	return gutter + colored
}

// deriveViewportPlainLines strips ANSI from styled lines to produce plain text.
// This is the rendered-text-first approach: what the user sees on screen
// (minus colors) is what they get when copying.
func deriveViewportPlainLines(buf []string, styledLines []string) []string {
	if cap(buf) < len(styledLines) {
		buf = make([]string, 0, len(styledLines))
	}
	for _, sl := range styledLines {
		buf = append(buf, strings.TrimRight(ansi.Strip(sl), " "))
	}
	return buf
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
	start := time.Now()
	lines := m.viewportStyledLines
	if m.hasSelectionRange() {
		lines = m.renderSelectionLines()
	}
	content := strings.Join(lines, "\n")
	if content != m.lastViewportContent {
		m.viewport.SetContent(content)
		m.lastViewportContent = content
	}

	// Auto-scroll: decide based on current state AFTER SetContent so
	// that scroll decisions use the up-to-date content length. The
	// previous approach sampled AtBottom() before SetContent, which
	// could produce the wrong decision when content/height changed.
	if !m.userScrolledUp {
		m.viewport.GotoBottom()
	}
	m.streamPlayback.LastFrameRenderCost = time.Since(start)
}

// ensureViewportLayout reconciles the viewport height with the current
// bottom-section layout. Call this from Update() after any state change
// that may affect bottomSectionHeight (textarea resize, drawer toggle,
// pending queue change, etc.). Moving this out of View() avoids
// mutating viewport state during rendering, which can cause the scroll
// offset and visible content to desynchronize for one or more frames —
// producing the "invisible but selectable text" artefact.
func (m *Model) ensureViewportLayout() {
	vpHeight, _ := m.computeLayout()
	if m.viewport.Height() != vpHeight {
		m.viewport.SetHeight(vpHeight)
		m.syncViewportContent()
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

	line := max(m.viewport.YOffset()+vy, 0)
	if line >= len(m.viewportPlainLines) {
		line = len(m.viewportPlainLines) - 1
	}

	col := max(x-m.mainColumnX()-tuikit.GutterNarrative, 0)
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
	col := max(x-m.mainColumnX()-inputHorizontalInset, 0)
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
	// Rendered-text-first selection: non-selected lines keep styled output,
	// selected lines show plain text with reverse highlight.
	return renderSelectionOnStyledLines(m.viewportStyledLines, m.viewportPlainLines, start, end)
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
	contentWidth := m.fixedRowContentWidth()
	col := x - m.mainColumnX() - tuikit.StatusInset // account for status-row horizontal padding
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
