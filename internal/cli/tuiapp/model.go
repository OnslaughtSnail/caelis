package tuiapp

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

const maxInputBarRows = 4
const ctrlCExitWindow = 2 * time.Second
const runningHintRotateEveryTicks = 40
const copyHintDuration = 1600 * time.Millisecond
const toolOutputPreviewLines = 4
const inputHorizontalInset = tuikit.InputInset

var runningBreathFrames = []string{"·", "•", "●", "•"}

var runningCarouselLines = []string{
	"Queue your next prompt now; it will run after this one.",
	"Use @path to anchor the model on exact files.",
	"/model can switch both model and reasoning level.",
	"Press Esc to interrupt, Enter to queue your next message.",
	"Review the latest tool output before sending follow-up guidance.",
}

type clearHintMsg struct {
	expected string
}

func clearHintLaterCmd(expected string, after time.Duration) tea.Cmd {
	expected = strings.TrimSpace(expected)
	if expected == "" || after <= 0 {
		return nil
	}
	return tea.Tick(after, func(time.Time) tea.Msg {
		return clearHintMsg{expected: expected}
	})
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

type Diagnostics struct {
	Frames             uint64
	IncrementalFrames  uint64
	FullRepaints       uint64
	SlowFrames         uint64
	LastFrameDuration  time.Duration
	AvgFrameDuration   time.Duration
	MaxFrameDuration   time.Duration
	RenderBytes        uint64
	PeakFrameBytes     uint64
	LastRenderAt       time.Time
	LastInputAt        time.Time
	LastInputLatency   time.Duration
	AvgInputLatency    time.Duration
	P95InputLatency    time.Duration
	LastMentionLatency time.Duration
	RedrawMode         string
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

type Config struct {
	Version             string
	Workspace           string
	ModelAlias          string
	ShowWelcomeCard     bool
	InitialLogs         []string
	Commands            []string
	Wizards             []WizardDef
	ExecuteLine         func(string) tuievents.TaskResultMsg
	CancelRunning       func() bool
	ToggleMode          func() (string, error)
	ModeLabel           func() string
	RefreshStatus       func() (string, string)
	MentionComplete     func(string, int) ([]string, error)
	SkillComplete       func(string, int) ([]string, error)
	ResumeComplete      func(string, int) ([]ResumeCandidate, error)
	SlashArgComplete    func(command string, query string, limit int) ([]SlashArgCandidate, error)
	PasteClipboardImage func() (int, string, error) // returns (attachmentCount, hint, error)
	ClearAttachments    func() int                  // clears pending attachments, returns remaining count
	OnDiagnostics       func(Diagnostics)
}

// ResumeCandidate is one selectable session candidate for `/resume`.
type ResumeCandidate struct {
	SessionID string
	Prompt    string
	Age       string
}

// SlashArgCandidate is one selectable argument option for a slash command.
type SlashArgCandidate struct {
	Value   string
	Display string
	// NoAuth indicates that the provider for this candidate does not require
	// an API key. When set, the /connect inline flow skips the api_key step.
	NoAuth bool
}

// ---------------------------------------------------------------------------
// Command palette items
// ---------------------------------------------------------------------------

type commandItem struct {
	name string
}

func (i commandItem) Title() string       { return "/" + i.name }
func (i commandItem) Description() string { return "Run slash command " + i.name }
func (i commandItem) FilterValue() string { return i.name }

// ---------------------------------------------------------------------------
// Prompt state (external approval/password prompts)
// ---------------------------------------------------------------------------

type promptState struct {
	title              string
	prompt             string
	details            []tuievents.PromptDetail
	secret             bool
	input              []rune
	cursor             int
	choices            []promptChoice
	choiceIndex        int
	scrollOffset       int
	filter             []rune
	filterable         bool
	multiSelect        bool
	allowFreeformInput bool
	selected           map[string]struct{}
	response           chan tuievents.PromptResponse
}

type promptChoice struct {
	label         string
	value         string
	detail        string
	alwaysVisible bool
}

type textSelectionPoint struct {
	line int
	col  int
}

type fixedSelectionArea string

const (
	fixedSelectionNone   fixedSelectionArea = ""
	fixedSelectionHint   fixedSelectionArea = "hint"
	fixedSelectionHeader fixedSelectionArea = "header"
	fixedSelectionFooter fixedSelectionArea = "footer"
)

type pendingPrompt struct {
	execLine    string
	displayLine string
}

type assistantBlockState struct {
	start int
	end   int
	raw   string
}

type diffBlockState struct {
	start int
	end   int
	msg   tuievents.DiffBlockMsg
}

type toolOutputLine struct {
	text   string
	stream string
}

type toolOutputState struct {
	key           string
	tool          string
	callID        string
	start         int
	end           int
	lines         []toolOutputLine
	stdoutPartial string
	stderrPartial string
	delegateFence bool
	active        bool
}

// ---------------------------------------------------------------------------
// Model — inline (non-fullscreen) Bubble Tea model
//
// Architecture:
//   - Completed log lines are committed above via tea.Println()
//   - View() only renders the bottom "control area":
//     current streaming line + hint area + input bar + status bar
//   - Terminal scrollback provides natural history browsing
// ---------------------------------------------------------------------------

type Model struct {
	cfg   Config
	theme tuikit.Theme

	width  int
	height int

	// Streaming state — the current incomplete line being received.
	streamLine         string
	lastCommittedStyle tuikit.LineStyle
	lastCommittedRaw   string
	hasCommittedLine   bool // true after at least one line has been committed
	assistantBlock     *assistantBlockState
	reasoningBlock     *assistantBlockState
	lastFinalAnswer    string
	diffBlocks         []diffBlockState
	toolOutputs        map[string]*toolOutputState
	welcomeCardPending bool
	runStartedAt       time.Time
	lastRunDuration    time.Duration
	hasLastRunDuration bool

	// Transient log tracking — retry/warn lines replace in-place like status updates.
	transientLogIdx  int  // index in historyLines of the current transient line (-1 = none)
	transientIsRetry bool // true when the transient slot holds a retry line

	// Fullscreen viewport — replaces tea.Println scrollback.
	historyLines        []string // committed lines (pre-colorized)
	viewportStyledLines []string
	viewportPlainLines  []string
	viewport            viewport.Model // scrollable history area
	userScrolledUp      bool           // true when user has scrolled up from bottom
	ready               bool           // true after first WindowSizeMsg sets dimensions

	// Mouse drag-selection (Crush-like in-app selection + copy).
	selecting      bool
	selectionStart textSelectionPoint
	selectionEnd   textSelectionPoint

	// Input-area drag-selection.
	inputSelecting      bool
	inputSelectionStart textSelectionPoint
	inputSelectionEnd   textSelectionPoint

	// Fixed status-row drag-selection (hint/header/footer).
	fixedSelecting      bool
	fixedSelectionArea  fixedSelectionArea
	fixedSelectionStart textSelectionPoint
	fixedSelectionEnd   textSelectionPoint

	// Input area
	textarea textarea.Model
	input    []rune // shadow copy for history/completion ops
	cursor   int

	// Running / spinner
	running bool
	spinner spinner.Model
	quit    bool

	// Task hint message (e.g., "▸ running: read_file")
	runningHint string
	runningTick uint64
	runningBeat int
	runningTip  int

	// Status bar
	statusModel   string
	statusContext string
	hint          string

	// Command palette (Ctrl+P overlay)
	showPalette bool
	palette     list.Model

	// @mention completion
	mentionQuery      string
	mentionCandidates []string
	mentionIndex      int
	mentionStart      int
	mentionEnd        int

	// $skill completion
	skillQuery      string
	skillCandidates []string
	skillIndex      int
	skillStart      int
	skillEnd        int

	// History navigation
	history      []string
	historyIndex int // -1 = not browsing
	historyDraft string
	pendingQueue []pendingPrompt

	// Slash command tab completion
	slashCandidates []string
	slashIndex      int
	slashPrefix     string

	// /resume completion
	resumeActive     bool
	resumeQuery      string
	resumeCandidates []ResumeCandidate
	resumeIndex      int

	// Generic slash command argument completion (e.g. /model, /sandbox, /connect).
	slashArgActive     bool
	slashArgCommand    string
	slashArgQuery      string
	slashArgCandidates []SlashArgCandidate
	slashArgIndex      int

	// Multi-step wizard state (replaces per-command fields like connectProvider, etc.).
	wizard *wizardRuntime

	// Prompt queue (external approval/password)
	activePrompt  *promptState
	pendingPrompt []tuievents.PromptRequestMsg

	// Pending clipboard image attachments.
	attachmentCount int

	// Diagnostics
	pendingFullRepaint bool
	pendingInputAt     time.Time
	inputLatencyWindow []time.Duration
	inputLatencyCount  uint64
	diag               Diagnostics

	// Ctrl+C exit confirm state.
	ctrlCArmed  bool
	lastCtrlCAt time.Time
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func NewModel(cfg Config) *Model {
	theme := tuikit.DefaultTheme()

	// Command palette
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

	// Textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message, @path/to/file or $skill"
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.MaxHeight = maxInputBarRows
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.Focus()

	// Spinner
	sp := spinner.New()
	sp.Spinner = spinner.Points
	sp.Style = theme.SpinnerStyle()

	// Viewport for fullscreen scrollable history.
	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true // scroll wheel for viewport; shift+click for text selection
	vp.KeyMap.Up.SetEnabled(false)
	vp.KeyMap.Down.SetEnabled(false)
	vp.KeyMap.HalfPageUp.SetEnabled(false)
	vp.KeyMap.HalfPageDown.SetEnabled(false)
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)
	// Restrict PageUp/PageDown to dedicated keys only (remove f/b/spacebar).
	vp.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"))
	vp.KeyMap.PageUp = key.NewBinding(key.WithKeys("pgup"))

	m := &Model{
		cfg:                 cfg,
		theme:               theme,
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
		welcomeCardPending: cfg.ShowWelcomeCard,
	}

	if cfg.RefreshStatus != nil {
		m.statusModel, m.statusContext = cfg.RefreshStatus()
	}
	if strings.TrimSpace(m.statusModel) == "" {
		m.statusModel = "not configured"
	}
	return m
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func (m *Model) Init() tea.Cmd {
	// Append initial welcome lines to the history buffer.
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

	// Title: >_ CAELIS (v0.0.8)
	prefix := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Accent).
		Render(">_")
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.PanelTitle).
		Render("CAELIS")
	version := lipgloss.NewStyle().
		Foreground(m.theme.TextSecondary).
		Render("(" + versionLabel + ")")

	// Aligned key-value rows
	labelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Info).
		Width(10)
	valueStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextPrimary)
	tipValueStyle := lipgloss.NewStyle().
		Foreground(m.theme.TextSecondary)

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
		Width(maxInt(30, minInt(72, maxInt(30, m.viewport.Width-6)))).
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

func tickStatusCmd() tea.Cmd {
	return tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg {
		return tuievents.TickStatusMsg{}
	})
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.textarea.SetWidth(maxInt(20, m.width-16-(inputHorizontalInset*2)))
		m.adjustTextareaHeight()
		m.palette.SetSize(maxInt(30, m.width-12), maxInt(8, minInt(16, m.height-10)))

		vpHeight, _ := m.computeLayout()
		m.viewport.Width = maxInt(1, m.width-tuikit.GutterNarrative)
		m.viewport.Height = vpHeight
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

	case tuievents.SetRunningMsg:
		wasRunning := m.running
		m.running = typed.Running
		if typed.Running && !wasRunning {
			m.startRunningAnimation()
		}
		if !typed.Running {
			m.runningHint = ""
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
		m.attachmentCount = typed.Count
		if typed.Count <= 0 {
			m.hint = ""
		}
		return m, nil

	case tuievents.ClearHistoryMsg:
		m.resetConversationView()
		return m, nil

	case tuievents.TaskResultMsg:
		if typed.Interrupted {
			m.discardActiveAssistantStream()
		} else {
			// Commit any remaining streaming content.
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
		m.runningHint = ""
		m.stopRunningAnimation()
		m.attachmentCount = 0
		m.clearInputOverlays()
		if typed.Err != nil && !typed.Interrupted {
			// Suppress prompt interrupt/EOF errors — these are user-initiated
			// cancel actions (e.g., pressing Esc during /connect prompts) and
			// should not be displayed as errors.
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
		m.syncViewportContent()
		if typed.ExitNow {
			m.quit = true
			return m, tea.Quit
		}
		if next, ok := m.dequeuePendingPrompt(); ok {
			return m.submitLineWithDisplay(next.execLine, next.displayLine)
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

	case tea.KeyMsg:
		now := time.Now()
		m.diag.LastInputAt = now
		m.pendingInputAt = now
		return m.handleKey(typed)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Input sync helpers
// ---------------------------------------------------------------------------

func (m *Model) syncInputFromTextarea() {
	m.input = []rune(m.textarea.Value())
	m.cursor = len(m.input) // approximate
	m.adjustTextareaHeight()
}

func (m *Model) syncTextareaFromInput() {
	m.textarea.SetValue(string(m.input))
	m.textarea.CursorEnd()
	m.adjustTextareaHeight()
}
