package tuiapp

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, cmd
	case tea.MouseClickMsg:
		mouse := typed.Mouse()
		if handled, cmd := m.handleInputAreaMouse(mouse, mousePhasePress); handled {
			return m, cmd
		}
		if handled, cmd := m.handleFixedAreaMouse(mouse, mousePhasePress); handled {
			return m, cmd
		}
		return m, m.handleViewportMousePress(mouse)
	case tea.MouseMotionMsg:
		mouse := typed.Mouse()
		if handled, cmd := m.handleInputAreaMouse(mouse, mousePhaseMotion); handled {
			return m, cmd
		}
		if handled, cmd := m.handleFixedAreaMouse(mouse, mousePhaseMotion); handled {
			return m, cmd
		}
		return m, m.handleViewportMouseMotion(mouse)
	case tea.MouseReleaseMsg:
		mouse := typed.Mouse()
		if handled, cmd := m.handleInputAreaMouse(mouse, mousePhaseRelease); handled {
			return m, cmd
		}
		if handled, cmd := m.handleFixedAreaMouse(mouse, mousePhaseRelease); handled {
			return m, cmd
		}
		return m, m.handleViewportMouseRelease(mouse)
	default:
		return m, nil
	}
}

type mousePhase int

const (
	mousePhasePress mousePhase = iota
	mousePhaseMotion
	mousePhaseRelease
)

func (m *Model) handleViewportMousePress(mouse tea.Mouse) tea.Cmd {
	if mouse.Button != tea.MouseLeft {
		return nil
	}
	m.clearInputSelection()
	m.clearFixedSelection()
	point, ok := m.mousePointToContentPoint(mouse.X, mouse.Y, false)
	if !ok {
		return nil
	}
	m.selecting = true
	m.selectionStart = point
	m.selectionEnd = point
	m.renderViewportContent()
	return nil
}

func (m *Model) handleViewportMouseMotion(mouse tea.Mouse) tea.Cmd {
	if !m.selecting {
		return nil
	}
	point, ok := m.mousePointToContentPoint(mouse.X, mouse.Y, true)
	if !ok {
		return nil
	}
	m.selectionEnd = point
	m.renderViewportContent()
	return nil
}

func (m *Model) handleViewportMouseRelease(mouse tea.Mouse) tea.Cmd {
	if !m.selecting {
		return nil
	}
	point, ok := m.mousePointToContentPoint(mouse.X, mouse.Y, true)
	if ok {
		m.selectionEnd = point
	}
	m.selecting = false
	text := m.selectionText()
	if text == "" {
		m.clearSelection()
		m.renderViewportContent()
		return nil
	}
	m.renderViewportContent()
	return m.copySelectionToClipboard(text)
}

func (m *Model) handleInputAreaMouse(mouse tea.Mouse, phase mousePhase) (bool, tea.Cmd) {
	if m.activePrompt != nil {
		return false, nil
	}
	if mouse.Button != tea.MouseLeft && phase == mousePhasePress {
		return false, nil
	}
	lines := m.inputPlainLines()
	if len(lines) == 0 {
		return false, nil
	}
	point, ok := m.mousePointToInputPoint(mouse.X, mouse.Y, phase != mousePhasePress, lines)
	switch phase {
	case mousePhasePress:
		if !ok || mouse.Button != tea.MouseLeft {
			return false, nil
		}
		m.clearSelection()
		m.clearFixedSelection()
		m.inputSelecting = true
		m.inputSelectionStart = point
		m.inputSelectionEnd = point
		return true, nil
	case mousePhaseMotion:
		if !m.inputSelecting || !ok {
			return false, nil
		}
		m.inputSelectionEnd = point
		return true, nil
	case mousePhaseRelease:
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
		return true, m.copySelectionToClipboard(text)
	}
	return false, nil
}

func (m *Model) handleFixedAreaMouse(mouse tea.Mouse, phase mousePhase) (bool, tea.Cmd) {
	if mouse.Button != tea.MouseLeft && phase == mousePhasePress {
		return false, nil
	}
	switch phase {
	case mousePhasePress:
		region, ok := m.fixedRegionAt(mouse.Y)
		if !ok || mouse.Button != tea.MouseLeft {
			return false, nil
		}
		point, ok := m.fixedRowPoint(region, mouse.X, false)
		if !ok {
			return false, nil
		}
		m.clearSelection()
		m.clearInputSelection()
		m.fixedSelecting = true
		m.fixedSelectionArea = region.area
		m.fixedSelectionStart = point
		m.fixedSelectionEnd = point
		return true, nil
	case mousePhaseMotion:
		if !m.fixedSelecting || m.fixedSelectionArea == fixedSelectionNone {
			return false, nil
		}
		region, ok := m.fixedRegionAt(mouse.Y)
		if !ok || region.area != m.fixedSelectionArea {
			return false, nil
		}
		point, ok := m.fixedRowPoint(region, mouse.X, true)
		if !ok {
			return false, nil
		}
		m.fixedSelectionEnd = point
		return true, nil
	case mousePhaseRelease:
		if !m.fixedSelecting {
			return false, nil
		}
		if region, ok := m.fixedRegionAt(mouse.Y); ok && region.area == m.fixedSelectionArea {
			if point, ok := m.fixedRowPoint(region, mouse.X, true); ok {
				m.fixedSelectionEnd = point
			}
		}
		m.fixedSelecting = false
		text := m.fixedSelectionText()
		if text == "" {
			m.clearFixedSelection()
			return true, nil
		}
		return true, m.copySelectionToClipboard(text)
	}
	return false, nil
}

func (m *Model) copySelectionToClipboard(text string) tea.Cmd {
	const copyHint = "selected text copied to clipboard"
	if err := m.writeClipboardText(text); err != nil {
		return m.reportClipboardError("copy", err)
	}
	return m.showHint(copyHint, hintOptions{
		priority:       tuievents.HintPriorityNormal,
		clearOnMessage: true,
		clearAfter:     copyHintDuration,
	})
}

func (m *Model) reportClipboardError(action string, err error) tea.Cmd {
	errLine := strings.TrimSpace(action + ": " + err.Error())
	if errLine == "" {
		errLine = "clipboard operation failed"
	}
	colored := tuikit.ColorizeLogLine(errLine, tuikit.LineStyleError, m.theme)
	m.historyLines = append(m.historyLines, colored)
	m.syncViewportContent()
	return m.showHint(errLine, hintOptions{
		priority:       tuievents.HintPriorityHigh,
		clearOnMessage: true,
		clearAfter:     copyHintDuration,
	})
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
	if !key.Matches(msg, m.keys.Quit) {
		m.ctrlCArmed = false
		m.lastCtrlCAt = time.Time{}
	}
	if key.Matches(msg, m.keys.Mode) && !m.running && m.cfg.ToggleMode != nil {
		hint, err := m.cfg.ToggleMode()
		if err != nil {
			return m, m.showHint(err.Error(), hintOptions{
				priority:       tuievents.HintPriorityHigh,
				clearOnMessage: true,
				clearAfter:     copyHintDuration,
			})
		}
		if strings.TrimSpace(hint) == "" {
			hint = "mode updated"
		}
		if m.cfg.RefreshStatus != nil {
			m.statusModel, m.statusContext = m.cfg.RefreshStatus()
		}
		return m, m.showHint(hint, hintOptions{
			priority:       tuievents.HintPriorityNormal,
			clearOnMessage: true,
			clearAfter:     copyHintDuration,
		})
	}

	switch {
	case key.Matches(msg, m.keys.PageUp):
		m.viewport.PageUp()
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.viewport.PageDown()
		m.userScrolledUp = !m.viewport.AtBottom()
		return m, nil

	case key.Matches(msg, m.keys.Quit):
		if m.running {
			return m, m.showHint("press Esc to interrupt running task", hintOptions{
				priority:       tuievents.HintPriorityHigh,
				clearOnMessage: true,
				clearAfter:     copyHintDuration,
			})
		}
		now := time.Now()
		if m.ctrlCArmed && now.Sub(m.lastCtrlCAt) <= ctrlCExitWindow {
			m.quit = true
			return m, tea.Quit
		}
		current := strings.TrimSpace(m.textarea.Value())
		if current != "" || len(m.inputAttachments) > 0 {
			m.recordHistoryEntry(current, m.inputAttachments)
		}
		m.textarea.SetValue("")
		m.textarea.CursorStart()
		m.adjustTextareaHeight()
		m.input = m.input[:0]
		m.cursor = 0
		m.clearInputAttachments()
		if m.cfg.ClearAttachments != nil {
			m.cfg.ClearAttachments()
		}
		m.historyIndex = -1
		m.historyDraft = ""
		m.historyDraftAttachments = nil
		m.ctrlCArmed = true
		m.ctrlCArmSeq++
		m.lastCtrlCAt = now
		return m, tea.Batch(
			expireCtrlCCmd(now, m.ctrlCArmSeq),
			m.showHint("press Ctrl+C again to quit", hintOptions{
				priority:       tuievents.HintPriorityCritical,
				clearOnMessage: false,
				clearAfter:     ctrlCExitWindow,
			}),
		)

	case msg.String() == "ctrl+d":
		if !m.running && len(m.input) == 0 && m.textarea.Value() == "" {
			m.quit = true
			return m, tea.Quit
		}
		return m, nil

	case msg.String() == "ctrl+p":
		if m.running {
			return m, nil
		}
		m.togglePalette()
		return m, animatePaletteCmd()

	case key.Matches(msg, m.keys.Back):
		if m.running {
			m.clearInputOverlays()
			if _, ok := m.popPendingPrompt(); ok {
				m.dismissVisibleHint()
				return m, nil
			}
			if m.cfg.CancelRunning != nil && m.cfg.CancelRunning() {
				return m, m.showHint("interrupt requested", hintOptions{
					priority:       tuievents.HintPriorityCritical,
					clearOnMessage: true,
					clearAfter:     systemHintDuration,
				})
			}
			return m, nil
		}
		m.clearInputOverlays()
		return m, nil

	case key.Matches(msg, m.keys.HistoryPrev):
		if m.shouldUseTextareaVerticalNavigation(-1) {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.syncInputFromTextarea()
			return m, cmd
		}
		if !m.running && len(m.history) > 0 {
			val := m.textarea.Value()
			if m.historyIndex == -1 {
				m.historyDraft = val
				m.historyDraftAttachments = cloneInputAttachments(m.inputAttachments)
				m.historyIndex = len(m.history) - 1
			} else if m.historyIndex > 0 {
				m.historyIndex--
			}
			if m.historyIndex >= 0 && m.historyIndex < len(m.history) {
				m.restoreHistoryEntry(m.history[m.historyIndex], m.historyAttachments[m.historyIndex])
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.HistoryNext):
		if m.shouldUseTextareaVerticalNavigation(1) {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.syncInputFromTextarea()
			return m, cmd
		}
		if !m.running && m.historyIndex != -1 {
			if m.historyIndex < len(m.history)-1 {
				m.historyIndex++
				m.restoreHistoryEntry(m.history[m.historyIndex], m.historyAttachments[m.historyIndex])
			} else {
				m.historyIndex = -1
				m.restoreHistoryEntry(m.historyDraft, m.historyDraftAttachments)
				m.historyDraft = ""
				m.historyDraftAttachments = nil
				m.adjustTextareaHeight()
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Complete):
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
			m.applySlashCommandCompletion()
			m.syncTextareaFromInput()
		}
		return m, nil

	case key.Matches(msg, m.keys.Send):
		line, attachments := submissionInput(m.textarea.Value(), m.inputAttachments)
		if line == "" && len(attachments) == 0 {
			return m, nil
		}
		if m.running {
			if strings.HasPrefix(line, "/") {
				return m, m.showHint("slash commands are unavailable while running", hintOptions{
					priority:       tuievents.HintPriorityHigh,
					clearOnMessage: true,
					clearAfter:     copyHintDuration,
				})
			}
			m.enqueuePendingPrompt(line, m.displayLineWithInputAttachments(line, attachments), attachments)
			return m, nil
		}
		m.setInputAttachments(attachments)
		if (line == "/connect" || strings.HasPrefix(line, "/connect ")) && m.findWizard("connect") == nil {
			return m.submitLine("/connect")
		}
		if m.tryOpenSlashArgPicker(line) {
			return m, nil
		}
		return m.submitLine(line)

	case key.Matches(msg, m.keys.Clear):
		m.textarea.SetValue("")
		m.textarea.CursorStart()
		m.adjustTextareaHeight()
		m.input = m.input[:0]
		m.cursor = 0
		m.clearInputAttachments()
		if m.cfg.ClearAttachments != nil {
			m.cfg.ClearAttachments()
		}
		m.clearInputOverlays()
		return m, nil

	case key.Matches(msg, m.keys.ImagePaste):
		if m.running {
			return m, nil
		}
		oldAttachmentCount := len(m.inputAttachments)
		if m.cfg.PasteClipboardImage != nil {
			names, _, err := m.cfg.PasteClipboardImage()
			if err != nil {
				errLine := "paste: " + err.Error()
				colored := tuikit.ColorizeLogLine(errLine, tuikit.LineStyleError, m.theme)
				m.historyLines = append(m.historyLines, colored)
				m.syncViewportContent()
				return m, nil
			}
			if len(names) > 0 {
				added := names
				if oldAttachmentCount < len(names) {
					added = names[oldAttachmentCount:]
				}
				m.insertAttachmentsAtCursor(added)
				m.dismissVisibleHint()
				m.syncTextareaChrome()
				return m, nil
			}
		}
		pasted, err := m.pasteClipboardText()
		if err != nil {
			return m, m.reportClipboardError("paste", err)
		}
		if pasted {
			return m, nil
		}
		return m, nil

	case key.Matches(msg, m.keys.TextPaste):
		pasted, err := m.pasteClipboardText()
		if err != nil {
			return m, m.reportClipboardError("paste", err)
		}
		if pasted {
			return m, nil
		}
		return m, nil

	default:
		// Backspace should remove an attachment token when the visual cursor is
		// sitting right after that token, before it edits surrounding text.
		if !m.running && m.attachmentCount > 0 &&
			(msg.String() == "backspace" || msg.String() == "ctrl+h") &&
			m.removeAttachmentAtCursor() {
			m.dismissVisibleHint()
			return m, nil
		}
		// Forward to textarea for general text input.
		before := m.textarea.Value()
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.inputAttachments = adjustAttachmentOffsetsForTextEdit(m.inputAttachments, before, m.textarea.Value())
		m.syncAttachmentSummary()
		m.syncInputFromTextarea()

		// Trigger @mention / $skill / /resume after text changes.
		if msg.Key().Text != "" || msg.String() == "backspace" || msg.String() == "delete" {
			m.refreshMention()
			m.refreshSkill()
			if m.isWizardActive() {
				if m.resumeActive {
					m.updateResumeCandidates()
				}
				if m.slashArgActive {
					m.updateSlashArgCandidates()
				}
			} else {
				m.syncSlashInputOverlays()
			}
			m.refreshSlashCommands()
		}
		return m, cmd
	}
}

func (m *Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.activePrompt != nil {
		return m, m.handlePromptPaste(msg)
	}
	before := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.inputAttachments = adjustAttachmentOffsetsForTextEdit(m.inputAttachments, before, m.textarea.Value())
	m.syncAttachmentSummary()
	m.syncInputFromTextarea()
	m.refreshMention()
	m.refreshSkill()
	if m.isWizardActive() {
		if m.resumeActive {
			m.updateResumeCandidates()
		}
		if m.slashArgActive {
			m.updateSlashArgCandidates()
		}
	} else {
		m.syncSlashInputOverlays()
	}
	m.refreshSlashCommands()
	return m, cmd
}

func (m *Model) submitLine(line string) (tea.Model, tea.Cmd) {
	return m.submitLineWithDisplayAndAttachments(line, m.displayLineWithAttachments(line), inputAttachmentsToSubmission(m.inputAttachments))
}

func (m *Model) submitLineWithDisplay(execLine string, displayLine string) (tea.Model, tea.Cmd) {
	return m.submitLineWithDisplayAndAttachments(execLine, displayLine, inputAttachmentsToSubmission(m.inputAttachments))
}

func (m *Model) submitLineWithDisplayAndAttachments(execLine string, displayLine string, attachments []Attachment) (tea.Model, tea.Cmd) {
	attachments = cloneAttachments(attachments)
	// Commit user input line to history buffer.
	userLine := "> " + strings.TrimSpace(displayLine)
	colored := tuikit.ColorizeLogLine(userLine, tuikit.LineStyleUser, m.theme)
	m.historyLines = append(m.historyLines, colored)
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleUser
	m.lastCommittedRaw = userLine
	m.lastFinalAnswer = ""

	// Push to history.
	m.recordHistoryEntry(strings.TrimSpace(execLine), attachmentsToInputAttachments(attachments))
	m.historyIndex = -1
	m.historyDraft = ""
	m.historyDraftAttachments = nil
	submission := Submission{
		Text:        strings.TrimSpace(execLine),
		Attachments: attachments,
	}

	// Clear input.
	m.textarea.SetValue("")
	m.textarea.CursorStart()
	m.adjustTextareaHeight()
	m.input = m.input[:0]
	m.cursor = 0
	m.clearInputAttachments()
	m.clearInputOverlays()

	m.running = true
	m.runStartedAt = time.Now()
	m.hasLastRunDuration = false
	m.showTurnDivider = !strings.HasPrefix(strings.TrimSpace(execLine), "/")
	m.startRunningAnimation()
	m.userScrolledUp = false
	m.syncViewportContent()

	if m.cfg.ExecuteLine == nil {
		m.running = false
		return m, nil
	}
	cmds := []tea.Cmd{
		func() tea.Msg {
			return m.cfg.ExecuteLine(submission)
		},
		m.spinner.Tick,
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) displayLineWithAttachments(line string) string {
	return m.displayLineWithInputAttachments(line, m.inputAttachments)
}

func (m *Model) displayLineWithInputAttachments(line string, attachments []inputAttachment) string {
	return composeDisplayWithToken(line, attachments, func(name string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			return ""
		}
		return "[image: " + name + "] "
	})
}

func (m *Model) shouldUseTextareaVerticalNavigation(direction int) bool {
	if m.running {
		return false
	}
	if strings.TrimSpace(m.textarea.Value()) == "" {
		return false
	}
	lineInfo := m.textarea.LineInfo()
	if m.textarea.LineCount() <= 1 && lineInfo.Height <= 1 {
		return false
	}
	switch {
	case direction < 0:
		return m.textarea.Line() > 0 || lineInfo.RowOffset > 0
	case direction > 0:
		return m.textarea.Line() < m.textarea.LineCount()-1 || lineInfo.RowOffset+1 < lineInfo.Height
	default:
		return false
	}
}

func (m *Model) userTurnDividerLine() string {
	label := ""
	if m.hasLastRunDuration {
		label = formatTurnDuration(m.lastRunDuration)
	}
	contentWidth := maxInt(12, m.viewport.Width())
	return m.theme.HelpHintTextStyle().Render(centeredDivider(contentWidth, label))
}

func formatTurnDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func centeredDivider(width int, label string) string {
	if width <= 0 {
		return ""
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return strings.Repeat("─", width)
	}
	label = " " + label + " "
	labelWidth := displayColumns(label)
	if labelWidth >= width {
		return label
	}
	remaining := width - labelWidth
	left := remaining / 2
	right := remaining - left
	if left < 2 {
		left = 2
	}
	if right < 2 {
		right = 2
	}
	return strings.Repeat("─", left) + label + strings.Repeat("─", right)
}

func (m *Model) enqueuePendingPrompt(execLine string, displayLine string, attachments []inputAttachment) {
	m.pendingQueue = append(m.pendingQueue, pendingPrompt{
		execLine:    strings.TrimSpace(execLine),
		displayLine: strings.TrimSpace(displayLine),
		attachments: inputAttachmentsToSubmission(attachments),
	})
	m.textarea.SetValue("")
	m.textarea.CursorStart()
	m.adjustTextareaHeight()
	m.input = m.input[:0]
	m.cursor = 0
	m.clearInputAttachments()
	m.historyIndex = -1
	m.historyDraft = ""
	m.historyDraftAttachments = nil
	m.clearInputOverlays()
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
		case "/model", "/sandbox":
			m.openSlashArgPicker(cmd)
			return len(m.slashArgCandidates) > 0
		}
	}
	return false
}
