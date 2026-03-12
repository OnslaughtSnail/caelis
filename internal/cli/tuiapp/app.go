package tuiapp

import (
	"os"
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
	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func NewModel(cfg Config) *Model {
	theme := tuikit.ResolveThemeFromEnv()

	items := make([]list.Item, 0, len(cfg.Commands))
	for _, one := range cfg.Commands {
		name := strings.TrimSpace(one)
		if name == "" {
			continue
		}
		items = append(items, commandItem{name: name})
	}
	delegate := list.NewDefaultDelegate()
	palette := list.New(items, delegate, 20, 10)
	palette.SetShowHelp(false)
	palette.SetShowStatusBar(false)
	palette.SetFilteringEnabled(true)
	palette.Styles.Title = lipgloss.NewStyle().Foreground(theme.PanelTitle).Bold(true)
	palette.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	palette.Styles.HelpStyle = lipgloss.NewStyle().Foreground(theme.TextSecondary)

	ta := textarea.New()
	ta.Placeholder = "Type your message, @path/to/file or $skill"
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
		cfg:                 cfg,
		theme:               theme,
		keys:                defaultKeyMap(),
		textarea:            ta,
		spinner:             sp,
		palette:             palette,
		viewport:            vp,
		historyIndex:        -1,
		transientLogIdx:     -1,
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
	configureHelpStyles(&m.help, theme)

	if cfg.RefreshStatus != nil {
		m.statusModel, m.statusContext = cfg.RefreshStatus()
	}
	if strings.TrimSpace(m.statusModel) == "" {
		m.statusModel = "not configured"
	}
	m.syncTextareaChrome()
	return m
}

func (m *Model) Init() tea.Cmd {
	for _, line := range m.cfg.InitialLogs {
		if strings.TrimSpace(line) == "" {
			continue
		}
		colored := tuikit.ColorizeLogLine(line, tuikit.DetectLineStyle(line), m.theme)
		m.historyLines = append(m.historyLines, colored)
	}
	m.hasCommittedLine = len(m.historyLines) > 0
	m.syncViewportContent()
	return tea.Batch(tickStatusCmd(), m.spinner.Tick)
}

func (m *Model) appendWelcomeCard() {
	versionText := strings.TrimSpace(m.cfg.Version)
	if versionText == "" {
		versionText = "unknown"
	}
	versionLabel := versionText
	if !strings.HasPrefix(strings.ToLower(versionText), "v") {
		versionLabel = "v" + versionText
	}
	workspace := strings.TrimSpace(m.cfg.Workspace)
	if workspace == "" {
		workspace = "."
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		workspace = strings.Replace(workspace, home, "~", 1)
	}
	modelAlias := strings.TrimSpace(m.cfg.ModelAlias)
	if modelAlias == "" {
		modelAlias = "not configured (/connect)"
	}

	prefix := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Accent).Render(">_")
	title := lipgloss.NewStyle().Bold(true).Foreground(m.theme.PanelTitle).Render("CAELIS")
	version := lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Render("(" + versionLabel + ")")

	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Info).Width(10)
	valueStyle := lipgloss.NewStyle().Foreground(m.theme.TextPrimary)
	tipValueStyle := lipgloss.NewStyle().Foreground(m.theme.TextSecondary)

	titleLine := prefix + " " + title + " " + version
	modelLine := labelStyle.Render("model:") + " " + valueStyle.Render(modelAlias)
	workspaceLine := labelStyle.Render("workspace:") + " " + valueStyle.Render(workspace)
	tipLine := labelStyle.Render("tip:") + " " + tipValueStyle.Render("type / for command list")

	body := strings.Join([]string{
		titleLine,
		"",
		modelLine,
		workspaceLine,
		tipLine,
	}, "\n")

	frame := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.PanelBorder).
		Foreground(m.theme.TextPrimary).
		Width(maxInt(30, minInt(72, maxInt(30, m.viewport.Width()-6)))).
		Padding(0, 2).
		Margin(1, 0, 1, 1).
		Render(body)
	lines := strings.Split(frame, "\n")
	m.historyLines = append(m.historyLines, lines...)
	if len(lines) > 0 {
		m.hasCommittedLine = true
		m.lastCommittedStyle = tuikit.LineStyleDefault
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.syncTextareaChrome()
		m.help.SetWidth(maxInt(20, m.width/2))
		m.palette.SetSize(maxInt(30, m.width-12), maxInt(8, minInt(16, m.height-10)))

		vpHeight, _ := m.computeLayout()
		m.viewport.SetWidth(m.viewportContentWidth())
		m.viewport.SetHeight(vpHeight)
		m.syncPaletteAnimationTarget()
		if m.welcomeCardPending {
			m.appendWelcomeCard()
			m.welcomeCardPending = false
		}
		m.rerenderDiffBlocks()
		m.syncViewportContent()

		if !m.ready {
			m.ready = true
			m.viewport.GotoBottom()
		}
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
		return m.handleLogChunk(typed.Chunk)

	case tuievents.AssistantStreamMsg:
		return m.handleStreamBlock(typed.Kind, typed.Text, typed.Final)

	case tuievents.ReasoningStreamMsg:
		return m.handleStreamBlock("reasoning", typed.Text, typed.Final)

	case tuievents.DiffBlockMsg:
		return m.handleDiffBlock(typed)

	case tuievents.TaskStreamMsg:
		return m.handleToolStreamMsg(typed)

	case tuievents.SetHintMsg:
		m.hint = strings.TrimSpace(typed.Hint)
		return m, clearHintLaterCmd(m.hint, typed.ClearAfter)

	case clearHintMsg:
		if strings.TrimSpace(m.hint) == strings.TrimSpace(typed.expected) {
			m.hint = ""
		}
		return m, nil

	case ctrlCExpireMsg:
		if m.ctrlCArmSeq == typed.seq && m.lastCtrlCAt.Equal(typed.armedAt) {
			m.ctrlCArmed = false
			m.lastCtrlCAt = time.Time{}
			if strings.TrimSpace(m.hint) == "press Ctrl+C again to quit" {
				m.hint = ""
			}
		}
		return m, nil

	case paletteAnimationMsg:
		if !m.paletteAnimating {
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
		return m, animatePaletteCmd()

	case toolOutputFadeMsg:
		return m.handleToolOutputFadeMsg(typed)

	case tuievents.SetRunningMsg:
		wasRunning := m.running
		m.running = typed.Running
		if typed.Running && !wasRunning {
			m.startRunningAnimation()
		}
		if !typed.Running {
			m.stopRunningAnimation()
		}
		return m, nil

	case tuievents.SetStatusMsg:
		if strings.TrimSpace(typed.Model) != "" {
			m.statusModel = typed.Model
		}
		m.statusContext = strings.TrimSpace(typed.Context)
		return m, nil

	case tuievents.AttachmentCountMsg:
		if typed.Count <= 0 {
			m.clearInputAttachments()
			m.hint = ""
		} else {
			m.syncAttachmentSummary()
		}
		m.syncTextareaChrome()
		return m, nil

	case tuievents.ClearHistoryMsg:
		m.resetConversationView()
		return m, nil

	case tuievents.TaskResultMsg:
		if typed.Interrupted {
			m.discardActiveAssistantStream()
		} else {
			m.flushStream()
			m.finalizeAssistantBlock()
			m.finalizeReasoningBlock()
		}
		if !m.runStartedAt.IsZero() {
			m.lastRunDuration = time.Since(m.runStartedAt)
			m.hasLastRunDuration = true
			m.runStartedAt = time.Time{}
		}
		m.running = false
		m.stopRunningAnimation()
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
				colored := tuikit.ColorizeLogLine(errLine, tuikit.LineStyleError, m.theme)
				m.historyLines = append(m.historyLines, colored)
			}
		}
		if m.showTurnDivider && len(m.historyLines) > 0 &&
			strings.TrimSpace(ansi.Strip(m.historyLines[len(m.historyLines)-1])) != "" {
			m.historyLines = append(m.historyLines, m.userTurnDividerLine())
		}
		m.showTurnDivider = false
		m.syncViewportContent()
		if cmd := m.maybeStartClosingToolOutputFades(); cmd != nil {
			return m, cmd
		}
		if typed.ExitNow {
			m.quit = true
			return m, tea.Quit
		}
		if next, ok := m.dequeuePendingPrompt(); ok {
			return m.submitLineWithDisplayAndAttachments(next.execLine, next.displayLine, next.attachments)
		}
		return m, nil

	case tuievents.PromptRequestMsg:
		m.enqueuePrompt(typed)
		return m, nil

	case tuievents.TickStatusMsg:
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
	return maxInt(1, m.width-tuikit.GutterNarrative-m.viewportScrollbarWidth())
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
