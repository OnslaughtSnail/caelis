package tuiapp

import (
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown ||
		msg.Button == tea.MouseButtonWheelLeft || msg.Button == tea.MouseButtonWheelRight {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, cmd
	}
	if handled, cmd := m.handleInputAreaMouse(msg); handled {
		return m, cmd
	}
	if m.viewport.Height <= 0 || len(m.viewportPlainLines) == 0 {
		return m, nil
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		m.clearInputSelection()
		point, ok := m.mousePointToContentPoint(msg.X, msg.Y, false)
		if !ok {
			return m, nil
		}
		m.selecting = true
		m.selectionStart = point
		m.selectionEnd = point
		m.renderViewportContent()
		return m, nil

	case tea.MouseActionMotion:
		if !m.selecting {
			return m, nil
		}
		point, ok := m.mousePointToContentPoint(msg.X, msg.Y, true)
		if !ok {
			return m, nil
		}
		m.selectionEnd = point
		m.renderViewportContent()
		return m, nil

	case tea.MouseActionRelease:
		if !m.selecting {
			return m, nil
		}
		point, ok := m.mousePointToContentPoint(msg.X, msg.Y, true)
		if ok {
			m.selectionEnd = point
		}
		m.selecting = false
		text := m.selectionText()
		if text == "" {
			m.clearSelection()
			m.renderViewportContent()
			return m, nil
		}
		m.renderViewportContent()
		const copyHint = "selected text copied to clipboard"
		m.hint = copyHint
		clipCmd := func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		}
		return m, tea.Batch(clipCmd, clearHintLaterCmd(copyHint, copyHintDuration))
	}
	return m, nil
}

func (m *Model) handleInputAreaMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	if m.activePrompt != nil {
		return false, nil
	}
	if msg.Button != tea.MouseButtonLeft && msg.Action != tea.MouseActionMotion && msg.Action != tea.MouseActionRelease {
		return false, nil
	}
	lines := m.inputPlainLines()
	if len(lines) == 0 {
		return false, nil
	}
	point, ok := m.mousePointToInputPoint(msg.X, msg.Y, msg.Action != tea.MouseActionPress, lines)
	switch msg.Action {
	case tea.MouseActionPress:
		if !ok || msg.Button != tea.MouseButtonLeft {
			return false, nil
		}
		m.clearSelection()
		m.inputSelecting = true
		m.inputSelectionStart = point
		m.inputSelectionEnd = point
		return true, nil

	case tea.MouseActionMotion:
		if !m.inputSelecting || !ok {
			return false, nil
		}
		m.inputSelectionEnd = point
		return true, nil

	case tea.MouseActionRelease:
		if !m.inputSelecting {
			return false, nil
		}
		if ok {
			m.inputSelectionEnd = point
		}
		m.inputSelecting = false
		start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(lines))
		if !ok {
			m.clearInputSelection()
			return true, nil
		}
		text := selectionTextFromLines(lines, start, end)
		if text == "" {
			m.clearInputSelection()
			return true, nil
		}
		const copyHint = "selected text copied to clipboard"
		m.hint = copyHint
		clipCmd := func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		}
		return true, tea.Batch(clipCmd, clearHintLaterCmd(copyHint, copyHintDuration))
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// External prompt input takes priority.
	if m.activePrompt != nil {
		return m, m.handlePromptKey(msg)
	}
	// Command palette overlay.
	if m.showPalette {
		return m, m.handlePaletteKey(msg)
	}
	// @mention overlay — intercept navigation keys so they don't
	// fall through to history browsing.
	if len(m.mentionCandidates) > 0 {
		if handled, cmd := m.handleMentionKey(msg); handled {
			return m, cmd
		}
	}
	// $skill overlay — same pattern.
	if len(m.skillCandidates) > 0 {
		if handled, cmd := m.handleSkillKey(msg); handled {
			return m, cmd
		}
	}
	// /resume overlay.
	if len(m.resumeCandidates) > 0 {
		if handled, cmd := m.handleResumeKey(msg); handled {
			return m, cmd
		}
	}
	// Generic slash-arg overlay (e.g. /model, /sandbox, /connect).
	if m.slashArgActive {
		if handled, cmd := m.handleSlashArgKey(msg); handled {
			return m, cmd
		}
	}
	// Slash command overlay (e.g. /resume, /status).
	if len(m.slashCandidates) > 0 {
		if handled, cmd := m.handleSlashCommandKey(msg); handled {
			return m, cmd
		}
	}
	m.clearInputSelection()
	if msg.String() != "ctrl+c" {
		m.ctrlCArmed = false
		m.lastCtrlCAt = time.Time{}
	}

	switch msg.String() {
	case "pgup":
		m.viewport.PageUp()
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, nil
	case "pgdown":
		m.viewport.PageDown()
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, nil

	case "ctrl+c":
		if m.running {
			m.hint = "press Esc to interrupt running task"
			return m, nil
		}
		now := time.Now()
		if m.ctrlCArmed && now.Sub(m.lastCtrlCAt) <= ctrlCExitWindow {
			m.quit = true
			return m, tea.Quit
		}
		current := strings.TrimSpace(m.textarea.Value())
		if current != "" {
			m.recordHistoryEntry(current)
		}
		m.textarea.SetValue("")
		m.textarea.CursorStart()
		m.adjustTextareaHeight()
		m.input = m.input[:0]
		m.cursor = 0
		m.historyIndex = -1
		m.historyDraft = ""
		m.ctrlCArmed = true
		m.lastCtrlCAt = now
		m.hint = "press Ctrl+C again to quit"
		return m, nil

	case "ctrl+d":
		if !m.running && len(m.input) == 0 && m.textarea.Value() == "" {
			m.quit = true
			return m, tea.Quit
		}
		return m, nil

	case "ctrl+p":
		if m.running {
			return m, nil
		}
		m.togglePalette()
		return m, nil

	case "esc":
		if m.running {
			if _, ok := m.popPendingPrompt(); ok {
				m.hint = ""
				return m, nil
			}
			if m.cfg.CancelRunning != nil && m.cfg.CancelRunning() {
				m.hint = "interrupt requested"
			}
			return m, nil
		}
		m.clearMention()
		m.clearSkill()
		m.clearResume()
		m.clearSlashArg()
		m.clearSlashCompletion()
		if m.showPalette {
			m.showPalette = false
		}
		return m, nil

	case "up":
		if !m.running && len(m.history) > 0 {
			val := m.textarea.Value()
			if m.historyIndex == -1 {
				m.historyDraft = val
				m.historyIndex = len(m.history) - 1
			} else if m.historyIndex > 0 {
				m.historyIndex--
			}
			if m.historyIndex >= 0 && m.historyIndex < len(m.history) {
				m.textarea.SetValue(m.history[m.historyIndex])
				m.textarea.CursorEnd()
				m.adjustTextareaHeight()
			}
		}
		return m, nil

	case "down":
		if !m.running && m.historyIndex != -1 {
			if m.historyIndex < len(m.history)-1 {
				m.historyIndex++
				m.textarea.SetValue(m.history[m.historyIndex])
				m.textarea.CursorEnd()
				m.adjustTextareaHeight()
			} else {
				m.historyIndex = -1
				m.textarea.SetValue(m.historyDraft)
				m.textarea.CursorEnd()
				m.historyDraft = ""
				m.adjustTextareaHeight()
			}
		}
		return m, nil

	case "tab":
		val := m.textarea.Value()
		m.syncInputFromTextarea()
		if len(m.mentionCandidates) > 0 {
			m.applyMentionCompletion()
			m.syncTextareaFromInput()
		} else if len(m.skillCandidates) > 0 {
			m.applySkillCompletion()
			m.syncTextareaFromInput()
		} else if len(m.resumeCandidates) > 0 {
			m.applyResumeCompletion()
			m.syncTextareaFromInput()
		} else if len(m.slashArgCandidates) > 0 {
			m.applySlashArgCompletion()
			m.syncTextareaFromInput()
		} else if len(m.slashCandidates) > 0 {
			m.applySlashCommandCompletion()
			m.syncTextareaFromInput()
		} else if strings.HasPrefix(strings.TrimSpace(val), "/") && !strings.Contains(strings.TrimSpace(val), " ") {
			m.handleSlashTab()
			m.syncTextareaFromInput()
		}
		return m, nil

	case "enter":
		line := strings.TrimSpace(m.textarea.Value())
		if line == "" {
			return m, nil
		}
		if m.running {
			if strings.HasPrefix(line, "/") {
				m.hint = "slash commands are unavailable while running"
				return m, nil
			}
			m.enqueuePendingPrompt(line, line)
			return m, nil
		}
		if m.tryOpenSlashArgPicker(line) {
			return m, nil
		}
		return m.submitLine(line)

	case "ctrl+u":
		m.textarea.SetValue("")
		m.textarea.CursorStart()
		m.adjustTextareaHeight()
		m.input = m.input[:0]
		m.cursor = 0
		m.clearMention()
		m.clearSkill()
		m.clearResume()
		m.clearSlashArg()
		m.clearSlashCompletion()
		return m, nil

	case "ctrl+v":
		if m.running {
			return m, nil
		}
		if m.cfg.PasteClipboardImage != nil {
			count, _, err := m.cfg.PasteClipboardImage()
			if err != nil {
				errLine := "paste: " + err.Error()
				colored := tuikit.ColorizeLogLine(errLine, tuikit.LineStyleError, m.theme)
				m.historyLines = append(m.historyLines, colored)
				m.syncViewportContent()
				return m, nil
			}
			if count > 0 {
				m.attachmentCount = count
				m.hint = ""
				return m, nil
			}
		}
		// No image in clipboard — forward to textarea.
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.syncInputFromTextarea()
		return m, cmd

	default:
		// If input is empty, backspace clears pending attachments as one token.
		if !m.running && m.attachmentCount > 0 &&
			(msg.String() == "backspace" || msg.String() == "ctrl+h") &&
			strings.TrimSpace(m.textarea.Value()) == "" {
			m.attachmentCount = 0
			if m.cfg.ClearAttachments != nil {
				m.attachmentCount = m.cfg.ClearAttachments()
			}
			m.hint = ""
			return m, nil
		}
		// Forward to textarea for general text input.
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.syncInputFromTextarea()

		// Trigger @mention / $skill / /resume after text changes.
		if len(msg.Runes) > 0 || msg.String() == "backspace" || msg.String() == "delete" {
			m.refreshMention()
			m.refreshSkill()
			if m.resumeActive {
				m.updateResumeCandidates()
			}
			if m.slashArgActive {
				m.updateSlashArgCandidates()
			}
			m.refreshSlashCommands()
		}
		return m, cmd
	}
}

func (m *Model) submitLine(line string) (tea.Model, tea.Cmd) {
	return m.submitLineWithDisplay(line, line)
}

func (m *Model) submitLineWithDisplay(execLine string, displayLine string) (tea.Model, tea.Cmd) {
	// Commit user input line to history buffer.
	userLine := "> " + strings.TrimSpace(displayLine)
	colored := tuikit.ColorizeLogLine(userLine, tuikit.LineStyleUser, m.theme)
	if m.hasCommittedLine {
		m.historyLines = append(m.historyLines, "") // gap before user input
	}
	m.historyLines = append(m.historyLines, colored)
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleUser
	m.lastCommittedRaw = userLine
	m.lastFinalAnswer = ""

	// Push to history.
	displayTrimmed := strings.TrimSpace(displayLine)
	m.recordHistoryEntry(displayTrimmed)
	m.historyIndex = -1
	m.historyDraft = ""

	// Clear input.
	m.textarea.SetValue("")
	m.textarea.CursorStart()
	m.adjustTextareaHeight()
	m.input = m.input[:0]
	m.cursor = 0
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashArg()
	m.clearSlashCompletion()

	m.running = true
	m.startRunningAnimation()
	m.userScrolledUp = false
	m.syncViewportContent()

	if m.cfg.ExecuteLine == nil {
		m.running = false
		return m, nil
	}
	cmds := []tea.Cmd{
		func() tea.Msg { return m.cfg.ExecuteLine(strings.TrimSpace(execLine)) },
		m.spinner.Tick,
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) enqueuePendingPrompt(execLine string, displayLine string) {
	m.pendingQueue = append(m.pendingQueue, pendingPrompt{
		execLine:    strings.TrimSpace(execLine),
		displayLine: strings.TrimSpace(displayLine),
	})
	m.textarea.SetValue("")
	m.textarea.CursorStart()
	m.adjustTextareaHeight()
	m.input = m.input[:0]
	m.cursor = 0
	m.historyIndex = -1
	m.historyDraft = ""
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashArg()
	m.clearSlashCompletion()
}

func (m *Model) dequeuePendingPrompt() (pendingPrompt, bool) {
	if len(m.pendingQueue) == 0 {
		return pendingPrompt{}, false
	}
	next := m.pendingQueue[0]
	copy(m.pendingQueue, m.pendingQueue[1:])
	m.pendingQueue = m.pendingQueue[:len(m.pendingQueue)-1]
	return next, true
}

func (m *Model) popPendingPrompt() (pendingPrompt, bool) {
	if len(m.pendingQueue) == 0 {
		return pendingPrompt{}, false
	}
	lastIdx := len(m.pendingQueue) - 1
	out := m.pendingQueue[lastIdx]
	m.pendingQueue = m.pendingQueue[:lastIdx]
	return out, true
}

func (m *Model) tryOpenSlashArgPicker(line string) bool {
	text := strings.TrimSpace(line)
	if text == "/resume" {
		m.openResumePicker()
		return len(m.resumeCandidates) > 0
	}
	if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
		cmd := strings.TrimPrefix(text, "/")
		// Check registered wizards first, then well-known simple commands.
		if m.findWizard(cmd) != nil {
			m.openSlashArgPicker(cmd)
			return m.slashArgActive
		}
		switch text {
		case "/sandbox", "/permission":
			m.openSlashArgPicker(cmd)
			return len(m.slashArgCandidates) > 0
		}
	}
	return false
}
