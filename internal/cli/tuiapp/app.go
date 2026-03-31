package tuiapp

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/OnslaughtSnail/caelis/internal/cli/cliputil"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func requestBackgroundColorCmd() tea.Cmd {
	return tea.RequestBackgroundColor
}

func NewModel(cfg Config) *Model {
	theme := tuikit.ResolveThemeFromOptions(cfg.NoColor, cfg.ColorProfile)
	themeAuto := tuikit.ThemeUsesAutoBackground()

	delegate := list.NewDefaultDelegate()
	palette := list.New(nil, delegate, 20, 10)
	palette.SetShowHelp(false)
	palette.SetShowStatusBar(false)
	palette.SetFilteringEnabled(true)
	palette.Styles.Title = lipgloss.NewStyle().Foreground(theme.PanelTitle).Bold(true)
	palette.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	palette.Styles.HelpStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)

	ta := textarea.New()
	ta.Placeholder = "Type your message, @agent, #path/to/file, or $skill"
	ta.Prompt = "> "
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.MaxHeight = maxInputBarRows
	ta.ShowLineNumbers = false
	ta.SetVirtualCursor(false)
	taStyles := ta.Styles()
	taStyles.Focused.CursorLine = lipgloss.NewStyle()
	taStyles.Focused.Base = lipgloss.NewStyle()
	taStyles.Focused.Prompt = theme.PromptStyle()
	taStyles.Focused.Text = theme.TextStyle()
	taStyles.Focused.Placeholder = theme.HelpHintTextStyle()
	taStyles.Blurred.CursorLine = lipgloss.NewStyle()
	taStyles.Blurred.Base = lipgloss.NewStyle()
	taStyles.Blurred.Prompt = theme.PromptStyle()
	taStyles.Blurred.Text = theme.TextStyle()
	taStyles.Blurred.Placeholder = theme.HelpHintTextStyle()
	taStyles.Cursor.Color = theme.CursorFg
	taStyles.Cursor.Shape = tea.CursorBlock
	taStyles.Cursor.Blink = true
	ta.SetStyles(taStyles)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Spinner{
		Frames: runningSpinnerFrames,
		FPS:    60 * time.Millisecond,
	}
	sp.Style = theme.SpinnerStyle()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.MouseWheelEnabled = true
	vp.KeyMap.Up.SetEnabled(false)
	vp.KeyMap.Down.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)
	vp.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"))
	vp.KeyMap.PageUp = key.NewBinding(key.WithKeys("pgup"))

	m := &Model{
		cfg:          cfg,
		theme:        theme,
		themeAuto:    themeAuto,
		noColor:      cfg.NoColor,
		noAnimation:  cfg.NoAnimation,
		colorProfile: theme.Profile,
		keys:         defaultKeyMap(cliputil.IsWSL()),
		spinner:      sp,
		viewport:     vp,
		doc:          NewDocument(),
		Composer: Composer{
			textarea:     ta,
			historyIndex: -1,
		},
		OverlayState: OverlayState{
			palette: palette,
		},
		selectionStart:      textSelectionPoint{line: -1, col: -1},
		selectionEnd:        textSelectionPoint{line: -1, col: -1},
		inputSelectionStart: textSelectionPoint{line: -1, col: -1},
		inputSelectionEnd:   textSelectionPoint{line: -1, col: -1},
		fixedSelectionArea:  fixedSelectionNone,
		fixedSelectionStart: textSelectionPoint{line: -1, col: -1},
		fixedSelectionEnd:   textSelectionPoint{line: -1, col: -1},
		inputLatencyWindow:  make([]time.Duration, 0, 128),
		diag: Diagnostics{
			RedrawMode: "fullscreen",
		},
		focused:            true,
		welcomeCardPending: cfg.ShowWelcomeCard,
	}
	m.help = help.New()
	m.applyTheme(theme)

	if cfg.RefreshStatus != nil {
		m.statusModel, m.statusContext = cfg.RefreshStatus()
	}
	if cfg.RefreshWorkspace != nil {
		if workspace := strings.TrimSpace(cfg.RefreshWorkspace()); workspace != "" {
			m.cfg.Workspace = workspace
		}
	}
	if strings.TrimSpace(m.statusModel) == "" {
		m.statusModel = "not configured"
	}
	m.setCommands(cfg.Commands)
	m.syncTextareaChrome()
	return m
}

func (m *Model) setCommands(commands []string) {
	if m == nil {
		return
	}
	m.cfg.Commands = append([]string(nil), commands...)
	items := make([]list.Item, 0, len(m.cfg.Commands))
	for _, one := range m.cfg.Commands {
		name := strings.TrimSpace(one)
		if name == "" {
			continue
		}
		items = append(items, commandItem{name: name})
	}
	if m.palette.Title == "" {
		m.palette.Title = "Commands"
	}
	m.palette.SetItems(items)
	m.refreshSlashCommands()
}

func (m *Model) Init() tea.Cmd {
	for _, line := range m.cfg.InitialLogs {
		if strings.TrimSpace(line) == "" {
			continue
		}
		style := tuikit.DetectLineStyle(line)
		m.doc.Append(NewTranscriptBlock(line, style))
	}
	m.hasCommittedLine = m.doc.Len() > 0
	m.syncViewportContent()
	cmds := []tea.Cmd{tickStatusCmd(), m.spinner.Tick}
	if m.themeAuto {
		cmds = append(cmds, requestBackgroundColorCmd())
	}
	return tea.Batch(cmds...)
}

func (m *Model) appendWelcomeCard() {
	m.doc.Append(NewWelcomeBlock(m.cfg.Version, m.cfg.Workspace, m.cfg.ModelAlias))
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleDefault
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.syncTextareaChrome()
		m.help.SetWidth(maxInt(20, m.fixedRowWidth()/2))
		paletteWidth := minInt(maxInt(30, m.fixedRowWidth()-4), maxInt(30, m.width-12))
		m.palette.SetSize(paletteWidth, maxInt(8, minInt(16, m.height-10)))

		vpHeight, _ := m.computeLayout()
		m.viewport.SetWidth(m.viewportContentWidth())
		m.viewport.SetHeight(vpHeight)
		m.syncPaletteAnimationTarget()
		if m.welcomeCardPending {
			m.appendWelcomeCard()
			m.welcomeCardPending = false
		}
		// In the document model, blocks re-render from their data on each
		// syncViewportContent call, so no explicit rerender needed.
		m.syncViewportContent()

		if !m.ready {
			m.ready = true
			m.viewport.GotoBottom()
		}
		return m, nil

	case tea.BackgroundColorMsg:
		if !m.themeAuto {
			return m, nil
		}
		nextTheme := tuikit.ResolveThemeWithState(typed.IsDark(), m.noColor, m.colorProfile)
		if nextTheme.Name == m.theme.Name && nextTheme.IsDark == m.theme.IsDark {
			return m, nil
		}
		m.applyTheme(nextTheme)
		return m, nil

	case tea.ColorProfileMsg:
		if m.noColor {
			return m, nil
		}
		if typed.Profile == colorprofile.Unknown || typed.Profile == m.colorProfile {
			return m, nil
		}
		m.colorProfile = typed.Profile
		nextTheme := tuikit.ResolveThemeWithState(m.theme.IsDark, m.noColor, m.colorProfile)
		m.applyTheme(nextTheme)
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(typed)

	case tea.FocusMsg:
		m.focused = true
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case tuievents.LogChunkMsg:
		m.flushAllPendingStreamSmoothing()
		m.dismissMessageHints()
		return m.handleLogChunk(typed.Chunk)

	case tuievents.AssistantStreamMsg:
		m.dismissMessageHints()
		if m.cfg.FrameBatchMainStream {
			return m.enqueueMainDelta(typed.Kind, typed.Actor, typed.Text, typed.Final)
		}
		return m.handleStreamBlock(typed.Kind, typed.Actor, typed.Text, typed.Final)

	case tuievents.RawDeltaMsg:
		m.dismissMessageHints()
		return m.handleRawDelta(typed)

	case tuievents.ReasoningStreamMsg:
		m.dismissMessageHints()
		if m.cfg.FrameBatchMainStream {
			return m.enqueueMainDelta("reasoning", typed.Actor, typed.Text, typed.Final)
		}
		return m.handleStreamBlock("reasoning", typed.Actor, typed.Text, typed.Final)

	case tuievents.DiffBlockMsg:
		m.flushAllPendingStreamSmoothing()
		m.dismissMessageHints()
		return m.handleDiffBlock(typed)

	case tuievents.TaskStreamMsg:
		m.dismissMessageHints()
		return m.handleToolStreamMsg(typed)

	case tuievents.ParticipantTurnStartMsg:
		m.flushAllPendingStreamSmoothing()
		m.dismissMessageHints()
		return m.handleParticipantTurnStart(typed)

	case tuievents.ParticipantToolMsg:
		m.dismissMessageHints()
		return m.handleParticipantToolMsg(typed)

	case tuievents.ParticipantStatusMsg:
		m.dismissMessageHints()
		return m.handleParticipantStatusMsg(typed)

	case tuievents.SubagentStartMsg:
		m.flushAllPendingStreamSmoothing()
		m.dismissMessageHints()
		return m.handleSubagentStart(typed)

	case tuievents.SubagentStatusMsg:
		m.flushAllPendingStreamSmoothing()
		return m.handleSubagentStatus(typed)

	case tuievents.SubagentStreamMsg:
		m.dismissMessageHints()
		return m.handleSubagentStream(typed)

	case tuievents.SubagentToolCallMsg:
		m.flushAllPendingStreamSmoothing()
		m.dismissMessageHints()
		return m.handleSubagentToolCall(typed)

	case tuievents.SubagentPlanMsg:
		m.flushAllPendingStreamSmoothing()
		return m.handleSubagentPlan(typed)

	case tuievents.SubagentDoneMsg:
		m.flushAllPendingStreamSmoothing()
		return m.handleSubagentDone(typed)

	case tuievents.PlanUpdateMsg:
		m.planEntries = m.planEntries[:0]
		hasIncomplete := false
		for _, item := range typed.Entries {
			content := strings.TrimSpace(item.Content)
			status := strings.TrimSpace(item.Status)
			if content == "" || status == "" {
				continue
			}
			if status != "completed" {
				hasIncomplete = true
			}
			m.planEntries = append(m.planEntries, planEntryState{
				Content: content,
				Status:  status,
			})
		}
		if !hasIncomplete {
			m.planEntries = m.planEntries[:0]
		}
		m.ensureViewportLayout()
		return m, nil

	case tuievents.SetHintMsg:
		after := typed.ClearAfter
		if after <= 0 {
			after = systemHintDuration
		}
		return m, m.showHint(typed.Hint, hintOptions{
			priority:       typed.Priority,
			clearOnMessage: typed.ClearOnMessage,
			clearAfter:     after,
		})

	case clearHintMsg:
		m.removeHintByID(typed.id)
		return m, nil

	case ctrlCExpireMsg:
		if m.ctrlCArmSeq == typed.seq && m.lastCtrlCAt.Equal(typed.armedAt) {
			m.ctrlCArmed = false
			m.lastCtrlCAt = time.Time{}
			m.removeHintsByText("press Ctrl+C again to quit")
		}
		return m, nil

	case paletteAnimationMsg:
		if !m.paletteAnimating {
			return m, nil
		}
		if m.noAnimation {
			m.paletteAnimLines = m.paletteAnimationTarget()
			m.paletteAnimating = false
			return m, nil
		}
		target := m.paletteAnimationTarget()
		switch {
		case m.paletteAnimLines < target:
			m.paletteAnimLines += paletteAnimationStep
			if m.paletteAnimLines > target {
				m.paletteAnimLines = target
			}
		case m.paletteAnimLines > target:
			m.paletteAnimLines -= paletteAnimationStep
			if m.paletteAnimLines < target {
				m.paletteAnimLines = target
			}
		}
		if m.paletteAnimLines == target {
			m.paletteAnimating = false
			return m, nil
		}
		return m, m.paletteAnimationCmd()

	case tuievents.SetRunningMsg:
		wasRunning := m.running
		m.running = typed.Running
		if typed.Running && !wasRunning {
			m.startRunningAnimation()
		}
		if !typed.Running {
			m.stopRunningAnimation()
			m.runStartedAt = time.Time{}
		}
		return m, nil

	case tuievents.SetStatusMsg:
		if workspace := strings.TrimSpace(typed.Workspace); workspace != "" {
			m.cfg.Workspace = workspace
		}
		if strings.TrimSpace(typed.Model) != "" {
			m.statusModel = typed.Model
		}
		m.statusContext = strings.TrimSpace(typed.Context)
		return m, nil

	case tuievents.SetCommandsMsg:
		m.setCommands(typed.Commands)
		return m, nil

	case tuievents.AttachmentCountMsg:
		if typed.Count <= 0 {
			m.clearInputAttachments()
			m.dismissVisibleHint()
		} else {
			m.syncAttachmentSummary()
		}
		m.syncTextareaChrome()
		m.ensureViewportLayout()
		return m, nil

	case tuievents.ClearHistoryMsg:
		m.resetConversationView()
		return m, nil

	case tuievents.UserMessageMsg:
		m.dismissMessageHints()
		m.dequeuePendingUserMessage(typed.Text)
		m.commitUserDisplayLine(typed.Text)
		m.ensureViewportLayout()
		m.syncViewportContent()
		return m, nil

	case tuievents.TaskResultMsg:
		if typed.ContinueRunning {
			if typed.Err != nil {
				m.pendingQueue = nil
				errLine := "error: " + typed.Err.Error()
				m.commitLine(errLine)
				m.ensureViewportLayout()
				m.syncViewportContent()
			}
			return m, nil
		}
		m.dismissMessageHints()
		if typed.Interrupted {
			m.discardActiveAssistantStream()
		} else {
			m.flushStream()
			m.finalizeAssistantBlock()
			m.finalizeReasoningBlock()
		}
		if typed.SuppressTurnDivider {
			m.finalizeActiveParticipantTurn(typed.Interrupted, typed.Err)
		}
		m.finalizeActivityBlock()
		if !m.runStartedAt.IsZero() {
			m.lastRunDuration = time.Since(m.runStartedAt)
			m.hasLastRunDuration = true
			m.runStartedAt = time.Time{}
		}
		m.running = false
		m.stopRunningAnimation()
		m.pendingQueue = nil
		m.planEntries = m.planEntries[:0]
		m.clearInputAttachments()
		m.syncTextareaChrome()
		m.clearInputOverlays()
		if typed.Err != nil && !typed.Interrupted {
			errText := strings.TrimSpace(typed.Err.Error())
			isPromptCancel := errText == "cli: input interrupted" ||
				errText == "cli: input eof" ||
				errText == tuievents.PromptErrInterrupt ||
				errText == tuievents.PromptErrEOF
			if !isPromptCancel {
				errLine := "error: " + typed.Err.Error()
				m.commitLine(errLine)
			}
		}
		if m.showTurnDivider && !typed.SuppressTurnDivider && m.doc.Len() > 0 {
			// Check if last block has non-empty content.
			last := m.doc.Last()
			hasContent := false
			if last != nil {
				if tb, ok := last.(*TranscriptBlock); ok {
					hasContent = strings.TrimSpace(tb.Raw) != ""
				} else {
					hasContent = true
				}
			}
			if hasContent {
				m.doc.Append(NewDividerBlock(m.userTurnDividerLine()))
			}
		}
		m.showTurnDivider = false
		m.ensureViewportLayout()
		m.syncViewportContent()
		if typed.ExitNow {
			m.quit = true
			return m, tea.Quit
		}
		return m, nil

	case tuievents.BTWOverlayMsg:
		return m.handleBTWDelta(typed.Text, typed.Final)

	case tuievents.BTWErrorMsg:
		if m.btwOverlay == nil && m.btwDismissed {
			return m, nil
		}
		m.dropPendingStreamSmoothing(streamSmoothingKey("btw", "", "answer", ""))
		m.applyBTWOverlayImmediate(typed.Text, true)
		return m, nil

	case tuievents.PromptRequestMsg:
		m.enqueuePrompt(typed)
		m.ensureViewportLayout()
		return m, nil

	case frameTickMsg:
		return m, tea.Batch(
			m.drainPendingStreamSmoothing(typed.at),
			m.advancePanelAnimations(typed.at),
			m.advanceScrollbarVisibility(typed.at),
		)

	case tuievents.TickStatusMsg:
		if m.cfg.RefreshWorkspace != nil {
			if workspace := strings.TrimSpace(m.cfg.RefreshWorkspace()); workspace != "" {
				m.cfg.Workspace = workspace
			}
		}
		if m.cfg.RefreshStatus != nil {
			modelText, contextText := m.cfg.RefreshStatus()
			if strings.TrimSpace(modelText) != "" {
				m.statusModel = modelText
			}
			m.statusContext = strings.TrimSpace(contextText)
		}
		return m, tickStatusCmd()

	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			if m.activePrompt == nil {
				m.advanceRunningAnimation()
				if m.activeActivityID != "" {
					m.syncActivityBlock()
				}
			}
			return m, cmd
		}
		return m, nil

	case tea.PasteMsg:
		now := time.Now()
		m.diag.LastInputAt = now
		m.pendingInputAt = now
		return m.handlePaste(typed)

	case tea.KeyMsg:
		now := time.Now()
		m.diag.LastInputAt = now
		m.pendingInputAt = now
		return m.handleKey(typed)
	}
	return m, nil
}

func (m *Model) applyTheme(theme tuikit.Theme) {
	if m == nil {
		return
	}
	m.theme = theme
	clearGlamourCache()
	configureHelpStyles(&m.help, theme)
	m.applyPaletteTheme(theme)
	m.applyTextareaStyles(theme)
	m.spinner.Style = theme.SpinnerStyle()
	m.rethemeHistory()
	m.syncTextareaChrome()
	m.syncViewportContent()
}

func (m *Model) applyPaletteTheme(theme tuikit.Theme) {
	styles := m.palette.Styles
	styles.Title = lipgloss.NewStyle().Foreground(theme.PanelTitle).Bold(true)
	styles.PaginationStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	styles.HelpStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	m.palette.Styles = styles
}

func (m *Model) applyTextareaStyles(theme tuikit.Theme) {
	styles := m.textarea.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Focused.Prompt = theme.PromptStyle()
	styles.Focused.Text = theme.TextStyle()
	styles.Focused.Placeholder = theme.HelpHintTextStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	styles.Blurred.Base = lipgloss.NewStyle()
	styles.Blurred.Prompt = theme.PromptStyle()
	styles.Blurred.Text = theme.TextStyle()
	styles.Blurred.Placeholder = theme.HelpHintTextStyle()
	styles.Cursor.Color = theme.CursorFg
	styles.Cursor.Shape = tea.CursorBlock
	styles.Cursor.Blink = true
	m.textarea.SetStyles(styles)
}

func (m *Model) rethemeHistory() {
	if m == nil {
		return
	}
	// In the document model, TranscriptBlocks store raw text + style,
	// and Render() re-colorizes with the current theme. So we only need
	// to rebuild activity block cached rows (which depend on theme colors).
	if m.activeActivityID != "" {
		m.syncActivityBlock()
	}
	m.refreshHistoryTailState()
}

func (m *Model) syncInputFromTextarea() {
	m.input = []rune(m.textarea.Value())
	m.cursor = m.textareaCursorIndex()
	m.adjustTextareaHeight()
}

func (m *Model) syncTextareaFromInput() {
	before := m.textarea.Value()
	after := string(m.input)
	m.textarea.SetValue(after)
	m.inputAttachments = adjustAttachmentOffsetsForTextEdit(m.inputAttachments, before, after)
	m.syncAttachmentSummary()
	m.moveTextareaCursorToIndex(m.cursor)
	m.adjustTextareaHeight()
}

func (m *Model) viewportScrollbarWidth() int {
	if m.width < 48 {
		return 0
	}
	return 1
}

func (m *Model) viewportContentWidth() int {
	return m.readableContentWidth()
}

func (m *Model) readableContentWidth() int {
	maxWidth := maxInt(1, tuikit.ReadableContentMaxWidth)
	available := maxInt(1, m.width-tuikit.GutterNarrative-m.viewportScrollbarWidth())
	return minInt(maxWidth, available)
}

func (m *Model) mainColumnWidth() int {
	return maxInt(1, m.readableContentWidth()+tuikit.GutterNarrative+m.viewportScrollbarWidth())
}

func (m *Model) mainColumnX() int {
	if m.width <= 0 {
		return 0
	}
	if pad := (m.width - m.mainColumnWidth()) / 2; pad > 0 {
		return pad
	}
	return 0
}

func (m *Model) placeInMainColumn(block string) string {
	if block == "" {
		return ""
	}
	return indentBlock(block, m.mainColumnX())
}

func (m *Model) fixedRowWidth() int {
	return maxInt(20, m.mainColumnWidth())
}

func (m *Model) fixedRowContentWidth() int {
	return maxInt(1, m.fixedRowWidth()-(tuikit.StatusInset*2))
}

func (m *Model) paletteAnimationTarget() int {
	if !m.showPalette {
		return 0
	}
	return m.fullPaletteLineCount()
}

func (m *Model) syncPaletteAnimationTarget() {
	target := m.paletteAnimationTarget()
	if m.paletteAnimating {
		return
	}
	m.paletteAnimLines = target
}
