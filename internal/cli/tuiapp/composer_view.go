package tuiapp

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type composerRender struct {
	styledLines []string
	plainLines  []string
	cursor      *tea.Cursor
}

func (r composerRender) styledText() string {
	return strings.Join(r.styledLines, "\n")
}

func (m *Model) renderRegularInputBar() string {
	return insetRenderedBlock(m.composeInputRender().styledText(), inputHorizontalInset)
}

func (m *Model) regularInputPlainLines() []string {
	lines := m.composeInputRender().plainLines
	if len(lines) == 0 {
		return []string{m.inputPromptPrefix()}
	}
	return lines
}

func (m *Model) regularInputCursor() *tea.Cursor {
	if m.activePrompt != nil {
		return nil
	}
	if m.isWizardActive() && m.wizard.hideInput() {
		return nil
	}
	render := m.composeInputRender()
	if render.cursor == nil {
		return nil
	}
	cursor := *render.cursor
	cursor.Position.X += inputHorizontalInset
	return &cursor
}

func (m *Model) composeInputRender() composerRender {
	prompt := m.inputPromptPrefix()
	promptWidth := displayColumns(prompt)
	if promptWidth <= 0 {
		prompt = "> "
		promptWidth = displayColumns(prompt)
	}
	continuation := strings.Repeat(" ", promptWidth)

	value := m.textarea.Value()
	cursorIndex := m.textareaCursorIndex()
	width := m.composerContentWidth()
	displayValue, displayCursor := composeInputDisplay(value, cursorIndex, m.inputAttachments)
	rows, cursorRow, cursorCol := wrapComposerRows("", displayValue, displayCursor, width)
	if len(rows) == 0 {
		rows = []string{""}
		cursorRow = 0
		cursorCol = 0
	}

	placeholder := ""
	if len(m.inputAttachments) == 0 && value == "" {
		placeholder = m.textarea.Placeholder
	}
	ghost := ""
	if placeholder == "" && cursorIndex == len([]rune(value)) {
		ghost = m.currentInputGhostHint()
	}

	start := composerWindowStart(cursorRow, len(rows), maxInputBarRows)
	end := minInt(len(rows), start+maxInputBarRows)

	styled := make([]string, 0, end-start)
	plain := make([]string, 0, end-start)
	for idx := start; idx < end; idx++ {
		promptPlain := continuation
		promptStyled := continuation
		if idx == 0 {
			promptPlain = prompt
			promptStyled = m.theme.PromptStyle().Render(prompt)
		}

		contentPlain := rows[idx]
		var contentStyled string
		switch {
		case placeholder != "" && idx == 0:
			contentPlain = placeholder
			contentStyled = m.theme.HelpHintTextStyle().Render(placeholder)
		case ghost != "" && idx == cursorRow:
			contentPlain += ghost
			contentStyled = m.theme.TextStyle().Render(rows[idx]) + m.theme.HelpHintTextStyle().Render(ghost)
		default:
			contentStyled = m.theme.TextStyle().Render(rows[idx])
		}

		styled = append(styled, promptStyled+contentStyled)
		plain = append(plain, promptPlain+contentPlain)
	}

	var cursor *tea.Cursor
	if m.textarea.Focused() && cursorRow >= start && cursorRow < end {
		styles := m.textarea.Styles()
		cursor = tea.NewCursor(promptWidth+cursorCol, cursorRow-start)
		cursor.Blink = styles.Cursor.Blink
		cursor.Color = styles.Cursor.Color
		cursor.Shape = styles.Cursor.Shape
	}

	return composerRender{
		styledLines: styled,
		plainLines:  plain,
		cursor:      cursor,
	}
}

func (m *Model) composerContentWidth() int {
	if m.width > 0 {
		return maxInt(20, m.width-16-(inputHorizontalInset*2))
	}
	if width := m.textarea.Width(); width > 0 {
		return width
	}
	return 20
}

func composerWindowStart(cursorRow int, totalRows int, maxRows int) int {
	if maxRows <= 0 || totalRows <= maxRows {
		return 0
	}
	start := max(cursorRow-maxRows+1, 0)
	maxStart := totalRows - maxRows
	if start > maxStart {
		start = maxStart
	}
	return start
}

func wrapComposerRows(attachment string, value string, cursor int, width int) ([]string, int, int) {
	if width <= 0 {
		width = 1
	}
	if cursor < 0 {
		cursor = 0
	}

	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}

	rows := make([]string, 0, len(lines))
	cursorRow := 0
	cursorCol := 0
	globalIndex := 0
	cursorAssigned := false

	for lineIdx, line := range lines {
		runes := []rune(line)
		prefix := ""
		if lineIdx == 0 {
			prefix = attachment
		}

		if len(runes) == 0 {
			rows = append(rows, prefix)
			if !cursorAssigned && cursor == globalIndex {
				cursorRow = len(rows) - 1
				cursorCol = displayColumns(prefix)
				cursorAssigned = true
			}
		} else {
			for segStart := 0; segStart < len(runes); {
				rowPrefix := prefix
				rowPrefixWidth := displayColumns(rowPrefix)
				rowWidth := rowPrefixWidth
				segEnd := segStart

				for segEnd < len(runes) {
					runeWidth := maxInt(1, displayColumns(string(runes[segEnd])))
					if rowWidth > 0 && rowWidth+runeWidth > width {
						break
					}
					rowWidth += runeWidth
					segEnd++
				}
				if segEnd == segStart {
					segEnd++
				}

				rowText := rowPrefix + string(runes[segStart:segEnd])
				rows = append(rows, rowText)
				rowIndex := len(rows) - 1
				rowStart := globalIndex + segStart
				rowEnd := globalIndex + segEnd
				if !cursorAssigned && cursor >= rowStart && cursor <= rowEnd {
					cursorRow = rowIndex
					consumed := max(cursor-rowStart, 0)
					consumed = min(consumed, segEnd-segStart)
					cursorCol = displayColumns(rowPrefix + string(runes[segStart:segStart+consumed]))
					cursorAssigned = true
				}

				prefix = ""
				segStart = segEnd
			}
		}

		if !cursorAssigned && cursor == globalIndex+len(runes) {
			cursorRow = len(rows) - 1
			cursorCol = displayColumns(rows[len(rows)-1])
			cursorAssigned = true
		}

		globalIndex += len(runes)
		if lineIdx < len(lines)-1 {
			globalIndex++
		}
	}

	if len(rows) == 0 {
		rows = []string{attachment}
		cursorRow = 0
		cursorCol = displayColumns(attachment)
		cursorAssigned = true
	}
	if !cursorAssigned {
		cursorRow = len(rows) - 1
		cursorCol = displayColumns(rows[len(rows)-1])
	}

	return rows, cursorRow, cursorCol
}

func desiredComposerRows(value string, _ string, width int, maxRows int) int {
	rows, _, _ := wrapComposerRows("", value, len([]rune(value)), width)
	if len(rows) < 1 {
		return 1
	}
	if maxRows > 0 && len(rows) > maxRows {
		return maxRows
	}
	return len(rows)
}

func (m *Model) textareaCursorIndex() int {
	value := m.textarea.Value()
	if value == "" {
		return 0
	}
	lines := strings.Split(value, "\n")
	row := max(m.textarea.Line(), 0)
	if row >= len(lines) {
		row = len(lines) - 1
	}
	lineInfo := m.textarea.LineInfo()
	col := lineInfo.StartColumn + lineInfo.ColumnOffset
	lineRunes := []rune(lines[row])
	if col < 0 {
		col = 0
	}
	if col > len(lineRunes) {
		col = len(lineRunes)
	}
	index := 0
	for i := 0; i < row; i++ {
		index += len([]rune(lines[i])) + 1
	}
	return index + col
}

func (m *Model) moveTextareaCursorToIndex(target int) {
	valueRunes := []rune(m.textarea.Value())
	if target < 0 {
		target = 0
	}
	if target > len(valueRunes) {
		target = len(valueRunes)
	}
	m.textarea.CursorEnd()
	for i := len(valueRunes); i > target; i-- {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		_ = cmd
	}
}
