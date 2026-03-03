package tuiapp

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuidiff"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

const maxInputBarRows = 6
const ctrlCExitWindow = 2 * time.Second
const reservedHintRows = 1
const runningHintRotateEveryTicks = 20

var runningBreathFrames = []string{"·", "•", "●", "•"}

var runningCarouselLines = []string{
	"Tip: queue your next prompt now; it will run after this one.",
	"Tip: use @path to anchor the model on exact files.",
	"Joke: There are 10 types of people; binary readers and others.",
	"Tip: /model can switch both model and reasoning level.",
	"Joke: I would tell you a UDP joke, but you might not get it.",
	"Tip: press Esc to interrupt, Enter to queue your next message.",
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
	ShowWelcomeCard     bool
	InitialLogs         []string
	Commands            []string
	ExecuteLine         func(string) tuievents.TaskResultMsg
	CancelRunning       func() bool
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
	prompt   string
	secret   bool
	input    []rune
	cursor   int
	response chan tuievents.PromptResponse
}

type textSelectionPoint struct {
	line int
	col  int
}

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
	diffBlocks         []diffBlockState

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
	modelReasoningRef  string
	connectProvider    string
	connectModel       string
	connectBaseURL     string
	connectTimeout     string
	connectAPIKey      string

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
		selectionStart:      textSelectionPoint{line: -1, col: -1},
		selectionEnd:        textSelectionPoint{line: -1, col: -1},
		inputSelectionStart: textSelectionPoint{line: -1, col: -1},
		inputSelectionEnd:   textSelectionPoint{line: -1, col: -1},
		inputLatencyWindow:  make([]time.Duration, 0, 128),
		diag: Diagnostics{
			RedrawMode: "fullscreen",
		},
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
	if m.cfg.ShowWelcomeCard {
		m.appendWelcomeCard()
	}
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
	body := strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(m.theme.PanelTitle).Render("CAELIS " + versionLabel),
		"workspace: " + lipgloss.NewStyle().Bold(true).Foreground(m.theme.TextPrimary).Render(workspace),
		"tip: type / for command list",
	}, "\n")
	card := lipgloss.NewStyle().MarginBottom(1).Render(body)
	lines := strings.Split(card, "\n")
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
		m.textarea.SetWidth(maxInt(20, m.width-4))
		m.adjustTextareaHeight()
		m.palette.SetSize(maxInt(30, m.width-12), maxInt(8, minInt(16, m.height-10)))

		vpHeight, _ := m.computeLayout()
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
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

	case tuievents.SetHintMsg:
		m.hint = strings.TrimSpace(typed.Hint)
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
		// Commit any remaining streaming content.
		m.flushStream()
		m.finalizeAssistantBlock()
		m.finalizeReasoningBlock()
		m.running = false
		m.runningHint = ""
		m.stopRunningAnimation()
		m.attachmentCount = 0
		if typed.Err != nil {
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
// Log chunk handling — inline commit architecture
// ---------------------------------------------------------------------------

func (m *Model) handleLogChunk(chunk string) (tea.Model, tea.Cmd) {
	if chunk == "" {
		return m, nil
	}

	// Sanitize incoming text.
	chunk = tuikit.SanitizeLogText(chunk)
	normalized := strings.ReplaceAll(strings.ReplaceAll(chunk, "\r\n", "\n"), "\r", "\n")

	m.streamLine += normalized

	// Commit all complete lines (those terminated by \n).
	for {
		idx := strings.IndexByte(m.streamLine, '\n')
		if idx < 0 {
			break
		}
		line := m.streamLine[:idx]
		m.streamLine = m.streamLine[idx+1:]
		m.commitLine(line)
	}

	// Detect running hint from tool call lines.
	if strings.HasPrefix(strings.TrimSpace(m.streamLine), "▸ ") {
		parts := strings.SplitN(strings.TrimSpace(m.streamLine), " ", 3)
		if len(parts) >= 2 {
			m.runningHint = parts[1]
		}
	}

	m.syncViewportContent()
	return m, nil
}

func (m *Model) handleAssistantStream(text string, final bool) (tea.Model, tea.Cmd) {
	return m.handleStreamBlock("answer", text, final)
}

func (m *Model) finalizeAssistantBlock() {
	if m.assistantBlock == nil {
		return
	}
	m.assistantBlock = nil
}

func (m *Model) handleReasoningStream(text string, final bool) (tea.Model, tea.Cmd) {
	return m.handleStreamBlock("reasoning", text, final)
}

func normalizeStreamKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "reasoning", "thinking":
		return "reasoning"
	default:
		return "answer"
	}
}

func (m *Model) handleStreamBlock(kind string, text string, final bool) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	streamKind := normalizeStreamKind(kind)
	blockStyle := tuikit.LineStyleAssistant
	blockMarker := "* "
	render := m.renderAssistantBlockLines

	var activeBlock **assistantBlockState
	if streamKind == "reasoning" {
		blockStyle = tuikit.LineStyleReasoning
		blockMarker = "│ "
		render = m.renderReasoningBlockLines
		activeBlock = &m.reasoningBlock
	} else {
		activeBlock = &m.assistantBlock
	}
	if *activeBlock == nil {
		if m.hasCommittedLine && m.lastCommittedStyle != blockStyle &&
			!(m.lastCommittedStyle == tuikit.LineStyleAssistant && blockStyle == tuikit.LineStyleReasoning) &&
			!(m.lastCommittedStyle == tuikit.LineStyleReasoning && blockStyle == tuikit.LineStyleAssistant) {
			m.historyLines = append(m.historyLines, "")
		}
		start := len(m.historyLines)
		lines := render(text)
		m.historyLines = append(m.historyLines, lines...)
		*activeBlock = &assistantBlockState{
			start: start,
			end:   start + len(lines),
			raw:   text,
		}
		m.hasCommittedLine = true
		m.lastCommittedStyle = blockStyle
		m.lastCommittedRaw = blockMarker
		m.syncViewportContent()
		return m, nil
	}
	block := *activeBlock
	block.raw = mergeStreamChunk(block.raw, text, final)
	lines := render(block.raw)
	m.replaceHistoryRange(block.start, block.end, lines)
	block.end = block.start + len(lines)
	if final {
		*activeBlock = nil
	}
	m.lastCommittedStyle = blockStyle
	m.lastCommittedRaw = blockMarker
	m.syncViewportContent()
	return m, nil
}

func mergeStreamChunk(existing string, incoming string, final bool) string {
	if final {
		incoming = strings.TrimSpace(incoming)
		if incoming == "" {
			return existing
		}
		return incoming
	}
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	if strings.HasPrefix(incoming, existing) {
		// Cumulative stream chunk.
		return incoming
	}
	if strings.HasPrefix(existing, incoming) {
		// Replayed/duplicated old chunk.
		return existing
	}
	return existing + incoming
}

func (m *Model) finalizeReasoningBlock() {
	if m.reasoningBlock == nil {
		return
	}
	m.reasoningBlock = nil
}

func (m *Model) handleDiffBlock(msg tuievents.DiffBlockMsg) (tea.Model, tea.Cmd) {
	m.flushStream()
	m.finalizeAssistantBlock()
	m.finalizeReasoningBlock()
	if m.hasCommittedLine && !isToolCallLine(m.lastCommittedRaw) {
		m.historyLines = append(m.historyLines, "")
	}
	start := len(m.historyLines)
	lines := m.renderDiffBlockLines(msg)
	m.historyLines = append(m.historyLines, lines...)
	m.diffBlocks = append(m.diffBlocks, diffBlockState{
		start: start,
		end:   start + len(lines),
		msg:   msg,
	})
	m.hasCommittedLine = true
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.syncViewportContent()
	return m, nil
}

func (m *Model) replaceHistoryRange(start int, end int, replacement []string) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(m.historyLines) {
		start = len(m.historyLines)
	}
	if end > len(m.historyLines) {
		end = len(m.historyLines)
	}
	head := append([]string(nil), m.historyLines[:start]...)
	head = append(head, replacement...)
	m.historyLines = append(head, m.historyLines[end:]...)
}

func (m *Model) renderAssistantBlockLines(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	isMarkdown := looksLikeMarkdown(trimmed)
	rendered := renderAssistantMarkdown(trimmed)
	if rendered == "" {
		return []string{tuikit.ColorizeLogLine("* ", tuikit.LineStyleAssistant, m.theme)}
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) > 0 {
		lines[0] = tuikit.ColorizeLogLine("* "+lines[0], tuikit.LineStyleAssistant, m.theme)
	}
	if isMarkdown {
		return lines
	}
	for i := range lines {
		if i == 0 {
			continue
		}
		lines[i] = tuikit.ColorizeLogLine(lines[i], tuikit.LineStyleAssistant, m.theme)
	}
	return lines
}

func (m *Model) renderReasoningBlockLines(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{tuikit.ColorizeLogLine("· ", tuikit.LineStyleReasoning, m.theme)}
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if i == 0 {
			line = "· " + line
		} else {
			line = "  " + line
		}
		lines[i] = tuikit.ColorizeLogLine(line, tuikit.LineStyleReasoning, m.theme)
	}
	return lines
}

func (m *Model) renderDiffBlockLines(msg tuievents.DiffBlockMsg) []string {
	model := tuidiff.BuildModel(tuidiff.Payload{
		Tool:      msg.Tool,
		Path:      msg.Path,
		Created:   msg.Created,
		Hunk:      msg.Hunk,
		Old:       msg.Old,
		New:       msg.New,
		Preview:   msg.Preview,
		Truncated: msg.Truncated,
	})
	wrapWidth := maxInt(40, m.viewport.Width)
	return tuidiff.Render(model, wrapWidth, m.theme)
}

func (m *Model) rerenderDiffBlocks() {
	if len(m.diffBlocks) == 0 {
		return
	}
	for i := range m.diffBlocks {
		block := &m.diffBlocks[i]
		lines := m.renderDiffBlockLines(block.msg)
		oldLen := block.end - block.start
		m.replaceHistoryRange(block.start, block.end, lines)
		block.end = block.start + len(lines)
		delta := len(lines) - oldLen
		if delta == 0 {
			continue
		}
		for j := i + 1; j < len(m.diffBlocks); j++ {
			m.diffBlocks[j].start += delta
			m.diffBlocks[j].end += delta
		}
	}
}

func (m *Model) resetConversationView() {
	m.flushStream()
	m.assistantBlock = nil
	m.reasoningBlock = nil
	m.diffBlocks = m.diffBlocks[:0]
	m.historyLines = m.historyLines[:0]
	m.viewportStyledLines = m.viewportStyledLines[:0]
	m.viewportPlainLines = m.viewportPlainLines[:0]
	m.hasCommittedLine = false
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.clearSelection()
	m.clearInputSelection()
	m.userScrolledUp = false
	if m.cfg.ShowWelcomeCard {
		m.appendWelcomeCard()
	}
	m.syncViewportContent()
}

// commitLine colorizes one complete line and appends it to the history buffer.
func (m *Model) commitLine(line string) {
	if strings.TrimSpace(line) == "" && !m.hasCommittedLine {
		return // skip leading blank lines
	}

	style := tuikit.DetectLineStyleWithContext(line, m.lastCommittedStyle)

	// Insert visual gap before conversation turns.
	if m.hasCommittedLine && (tuikit.ShouldInsertGap(true, m.lastCommittedStyle, style) || shouldInsertToolGap(m.lastCommittedRaw, line)) {
		m.historyLines = append(m.historyLines, "")
	}

	colored := tuikit.ColorizeLogLine(line, style, m.theme)
	m.historyLines = append(m.historyLines, colored)

	m.lastCommittedStyle = style
	m.lastCommittedRaw = line
	m.hasCommittedLine = true
}

// flushStream commits any remaining partial line in the stream buffer.
func (m *Model) flushStream() {
	if strings.TrimSpace(m.streamLine) == "" {
		m.streamLine = ""
		return
	}
	m.commitLine(m.streamLine)
	m.streamLine = ""
}

func shouldInsertToolGap(prevLine string, currentLine string) bool {
	prev := strings.TrimSpace(prevLine)
	curr := strings.TrimSpace(currentLine)
	if prev == "" || curr == "" {
		return false
	}
	return strings.HasPrefix(prev, "▸ ") && strings.HasPrefix(curr, "▸ ")
}

func isToolCallLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "▸ ")
}

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
		m.hint = "selected text copied to clipboard"
		return m, func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		}
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
		m.hint = "selected text copied to clipboard"
		return true, func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		}
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
	switch text {
	case "/model", "/sandbox", "/connect", "/permission":
		m.openSlashArgPicker(strings.TrimPrefix(text, "/"))
		return len(m.slashArgCandidates) > 0
	case "/resume":
		m.openResumePicker()
		return len(m.resumeCandidates) > 0
	default:
		return false
	}
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

// ---------------------------------------------------------------------------
// View — fullscreen layout: viewport (top) + bottom control area
// ---------------------------------------------------------------------------

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

	// 1. Viewport (scrollable history + streaming + spinner).
	sections = append(sections, m.viewport.View())

	// 2. Dedicated hint area above input (always reserved to avoid layout jitter).
	sections = append(sections, m.renderHintArea())

	// 3. Separator between viewport area and input controls.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}

	// 4. External prompt (modal-style).
	if m.activePrompt != nil {
		sections = append(sections, m.renderPromptModal())
	}

	// 5. Input bar.
	if m.activePrompt == nil {
		sections = append(sections, m.renderInputBar())
	}

	// 5b. @mention candidates inline list (below input, above status).
	if len(m.mentionCandidates) > 0 {
		sections = append(sections, m.renderMentionList())
	}

	// 5c. $skill candidates inline list (below input, above status).
	if len(m.skillCandidates) > 0 {
		sections = append(sections, m.renderSkillList())
	}

	// 5d. /resume candidates inline list (below input, above status).
	if len(m.resumeCandidates) > 0 {
		sections = append(sections, m.renderResumeList())
	}

	// 5e. Generic slash argument candidates inline list.
	if len(m.slashArgCandidates) > 0 {
		sections = append(sections, m.renderSlashArgList())
	}

	// 5f. Slash command candidates inline list.
	if len(m.slashCandidates) > 0 {
		sections = append(sections, m.renderSlashCommandList())
	}

	// 7. Status bar (with top padding).
	sections = append(sections, "") // blank line before status
	sections = append(sections, m.renderStatusBar())
	sections = append(sections, "") // bottom margin for breathing room

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

	// Dedicated hint area (always reserved).
	lines += reservedHintRows

	// Separator.
	lines++

	// Prompt modal (3 lines) or input bar (>=1).
	if m.activePrompt != nil {
		lines += 3
	} else {
		lines += maxInt(1, m.textarea.Height())
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

	// Blank line + status bar + bottom margin.
	lines += 3

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

	col := x
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
	if m.activePrompt != nil {
		return 0, 0, false
	}
	y := m.viewport.Height
	y += reservedHintRows
	y++ // separator
	h := maxInt(1, m.textarea.Height())
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

// ---------------------------------------------------------------------------
// View sub-components
// ---------------------------------------------------------------------------

func (m *Model) buildHintLine() string {
	text := m.buildHintText()
	if text == "" {
		return ""
	}
	return m.theme.HintStyle().Render("  " + text)
}

func (m *Model) buildHintText() string {
	// Show hint message if set.
	if h := strings.TrimSpace(m.hint); h != "" {
		return h
	}
	if m.running && m.activePrompt == nil {
		return m.buildRunningHintText()
	}
	if len(m.pendingQueue) > 0 {
		n := len(m.pendingQueue)
		if n == 1 {
			return "1 message queued; it will send after current run"
		}
		return fmt.Sprintf("%d messages queued; they will send in order after current run", n)
	}
	// Show /resume guidance.
	if len(m.resumeCandidates) > 0 {
		return "/resume: ↑/↓ select │ enter: resume │ tab: fill id"
	}
	// Show generic slash-arg guidance.
	if m.slashArgActive && m.slashArgCommand != "" {
		label := "/" + m.slashArgCommand
		if strings.EqualFold(strings.TrimSpace(m.slashArgCommand), "connect") {
			label = "/connect provider"
		} else if strings.HasPrefix(m.slashArgCommand, "model-reasoning:") {
			label = "/model reasoning"
		} else if strings.HasPrefix(m.slashArgCommand, "connect-model:") {
			label = "/connect model"
		} else if strings.HasPrefix(m.slashArgCommand, "connect-baseurl:") {
			label = "/connect base_url"
		} else if strings.HasPrefix(strings.TrimSpace(m.slashArgCommand), "connect-timeout:") {
			label = "/connect timeout"
		} else if strings.HasPrefix(strings.TrimSpace(m.slashArgCommand), "connect-apikey:") {
			return "/connect api_key: type and press enter"
		}
		if strings.HasPrefix(m.slashArgCommand, "model-reasoning:") && len(m.slashArgCandidates) == 0 {
			return "/model reasoning: type option and press enter"
		}
		if strings.HasPrefix(m.slashArgCommand, "connect-model:") && len(m.slashArgCandidates) == 0 {
			return "/connect model: type model name and press enter"
		}
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
	frame := "·"
	if len(runningBreathFrames) > 0 {
		frame = runningBreathFrames[m.runningBeat%len(runningBreathFrames)]
	}
	status := "thinking..."
	if h := strings.TrimSpace(m.runningHint); h != "" {
		status = "running: " + h
	}
	parts := []string{frame + " " + status}
	if len(m.pendingQueue) > 0 {
		parts = append(parts, fmt.Sprintf("queued: %d", len(m.pendingQueue)))
	}
	if len(runningCarouselLines) > 0 {
		parts = append(parts, runningCarouselLines[m.runningTip%len(runningCarouselLines)])
	}
	return strings.Join(parts, "  |  ")
}

func (m *Model) renderHintArea() string {
	w := maxInt(1, m.width)
	text := strings.TrimSpace(m.buildHintText())
	if text == "" {
		return m.theme.HintStyle().Render(" ")
	}
	if w <= 2 {
		if displayColumns(text) > w {
			text = sliceByDisplayColumns(text, 0, w)
		}
		return m.theme.HintStyle().Render(text)
	}
	maxTextWidth := w - 2
	if displayColumns(text) > maxTextWidth {
		text = sliceByDisplayColumns(text, 0, maxTextWidth)
	}
	return m.theme.HintStyle().Render("  " + text)
}

func (m *Model) renderInputBar() string {
	if start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines())); ok &&
		(start.line != end.line || start.col != end.col) {
		lines := m.inputPlainLines()
		return strings.Join(renderSelectionOnLines(lines, start, end), "\n")
	}
	w := maxInt(20, m.width)
	prompt := m.theme.PromptStyle().Render("> ")
	inputVal := m.textarea.View()
	if strings.HasPrefix(strings.TrimSpace(m.slashArgCommand), "connect-apikey:") {
		query, _ := connectWizardQueryAtCursor(m.input, m.cursor)
		inputVal = "/connect " + strings.Repeat("*", utf8.RuneCountInString(strings.TrimSpace(query)))
	}
	inputLine := renderMultilineInput(prompt, inputVal)
	if strings.Contains(inputLine, "\n") {
		return inputLine
	}

	// Build the help hints on the right side.
	helpText := m.buildHelpHints()
	helpRendered := m.theme.HelpHintTextStyle().Render(helpText)

	// Compose: "> input                                   help hints"
	inputWidth := lipgloss.Width(inputLine)
	helpWidth := lipgloss.Width(helpRendered)

	if inputWidth+helpWidth+2 <= w {
		gap := w - inputWidth - helpWidth
		return inputLine + strings.Repeat(" ", gap) + helpRendered
	}
	return inputLine
}

func (m *Model) inputPlainLines() []string {
	prompt := "> "
	indent := strings.Repeat(" ", lipgloss.Width(prompt))
	value := m.textarea.Value()
	if value == "" {
		return []string{prompt}
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
			out = append(out, prompt+line)
			continue
		}
		out = append(out, indent+line)
	}
	return out
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

func (m *Model) buildHelpHints() string {
	if m.running {
		if len(m.pendingQueue) > 0 {
			return "enter: queue │ esc: pop queued"
		}
		return "enter: queue │ esc: interrupt"
	}
	parts := []string{"enter: send", "ctrl+v: paste image", "pgup/pgdn: scroll", "ctrl+p: commands", "ctrl+c x2: quit"}
	return strings.Join(parts, " │ ")
}

func (m *Model) renderStatusBar() string {
	w := maxInt(20, m.width)
	left := strings.TrimSpace(m.cfg.Workspace)
	modelPart := strings.TrimSpace(m.statusModel)
	contextPart := strings.TrimSpace(m.statusContext)
	var right string
	if contextPart != "" {
		right = modelPart + "   " + contextPart
	} else {
		right = modelPart
	}
	content := tuikit.ComposeFooter(w-2, left, right)
	return m.theme.StatusStyle().Width(w).Render(content)
}

func (m *Model) renderPromptModal() string {
	if m.activePrompt == nil {
		return ""
	}
	p := m.activePrompt
	value := string(p.input)
	if p.secret {
		value = strings.Repeat("*", len(p.input))
	}
	return m.theme.ModalStyle().Render(
		fmt.Sprintf("%s%s\n\nEnter: submit · Esc: cancel", p.prompt, value),
	)
}

func (m *Model) adjustTextareaHeight() {
	height := desiredInputRows(m.textarea.Value(), m.textarea.Width(), maxInputBarRows)
	if height < 1 {
		height = 1
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
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Command palette
// ---------------------------------------------------------------------------

func (m *Model) togglePalette() {
	m.showPalette = !m.showPalette
	if m.showPalette {
		m.palette.ResetSelected()
	}
}

func (m *Model) handlePaletteKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.showPalette = false
		return nil
	case "enter":
		item, ok := m.palette.SelectedItem().(commandItem)
		if ok {
			m.textarea.SetValue("/" + item.name)
			m.textarea.CursorEnd()
			m.adjustTextareaHeight()
			m.syncInputFromTextarea()
			m.refreshSlashCommands()
		}
		m.showPalette = false
		return nil
	}
	var cmd tea.Cmd
	m.palette, cmd = m.palette.Update(msg)
	return cmd
}

// ---------------------------------------------------------------------------
// @Mention completion
// ---------------------------------------------------------------------------

func (m *Model) clearMention() {
	m.mentionQuery = ""
	m.mentionCandidates = nil
	m.mentionIndex = 0
	m.mentionStart = 0
	m.mentionEnd = 0
}

func (m *Model) refreshMention() {
	m.clearMention()
	if m.cfg.MentionComplete == nil || m.running {
		return
	}
	start, end, query, ok := mentionQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	begin := time.Now()
	candidates, err := m.cfg.MentionComplete(query, 8)
	latency := time.Since(begin)
	m.diag.LastMentionLatency = latency
	if err != nil || len(candidates) == 0 {
		return
	}
	m.mentionQuery = query
	m.mentionCandidates = append([]string(nil), candidates...)
	m.mentionStart = start
	m.mentionEnd = end
	m.mentionIndex = 0
}

func (m *Model) applyMentionCompletion() {
	if len(m.mentionCandidates) == 0 {
		m.refreshMention()
		if len(m.mentionCandidates) == 0 {
			return
		}
	}
	choice := "@" + m.mentionCandidates[m.mentionIndex]
	replaced, nextCursor := replaceRuneSpan(m.input, m.mentionStart, m.mentionEnd, choice)
	m.input = replaced
	m.cursor = nextCursor
	if m.cursor == len(m.input) {
		m.input = append(m.input, ' ')
		m.cursor++
	}
	m.clearMention()
}

func (m *Model) handleMentionKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.clearMention()
		return true, nil
	case "up":
		if m.mentionIndex > 0 {
			m.mentionIndex--
		}
		return true, nil
	case "down":
		if m.mentionIndex < len(m.mentionCandidates)-1 {
			m.mentionIndex++
		}
		return true, nil
	case "enter", "tab":
		m.applyMentionCompletion()
		m.syncTextareaFromInput()
		return true, nil
	default:
		return false, nil
	}
}

// ---------------------------------------------------------------------------
// $skill completion
// ---------------------------------------------------------------------------

func (m *Model) clearSkill() {
	m.skillQuery = ""
	m.skillCandidates = nil
	m.skillIndex = 0
	m.skillStart = 0
	m.skillEnd = 0
}

func (m *Model) refreshSkill() {
	m.clearSkill()
	if m.cfg.SkillComplete == nil || m.running {
		return
	}
	// Don't show skill popup if mention popup is active.
	if len(m.mentionCandidates) > 0 {
		return
	}
	start, end, query, ok := skillQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	candidates, err := m.cfg.SkillComplete(query, 8)
	if err != nil || len(candidates) == 0 {
		return
	}
	m.skillQuery = query
	m.skillCandidates = append([]string(nil), candidates...)
	m.skillStart = start
	m.skillEnd = end
	m.skillIndex = 0
}

func (m *Model) applySkillCompletion() {
	if len(m.skillCandidates) == 0 {
		m.refreshSkill()
		if len(m.skillCandidates) == 0 {
			return
		}
	}
	choice := "$" + m.skillCandidates[m.skillIndex]
	replaced, nextCursor := replaceRuneSpan(m.input, m.skillStart, m.skillEnd, choice)
	m.input = replaced
	m.cursor = nextCursor
	if m.cursor == len(m.input) {
		m.input = append(m.input, ' ')
		m.cursor++
	}
	m.clearSkill()
}

func (m *Model) handleSkillKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.clearSkill()
		return true, nil
	case "up":
		if m.skillIndex > 0 {
			m.skillIndex--
		}
		return true, nil
	case "down":
		if m.skillIndex < len(m.skillCandidates)-1 {
			m.skillIndex++
		}
		return true, nil
	case "enter", "tab":
		m.applySkillCompletion()
		m.syncTextareaFromInput()
		return true, nil
	default:
		return false, nil
	}
}

// renderSkillList renders the $skill candidates as an inline list.
func (m *Model) renderSkillList() string {
	if len(m.skillCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.skillCandidates))
	var lines []string
	for i := 0; i < maxItems; i++ {
		prefix := "  "
		if i == m.skillIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render("$"+m.skillCandidates[i]))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render("$"+m.skillCandidates[i]))
		}
	}
	if len(m.skillCandidates) > maxItems {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.skillCandidates)-maxItems),
		))
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// /resume completion
// ---------------------------------------------------------------------------

func (m *Model) clearResume() {
	m.resumeActive = false
	m.resumeQuery = ""
	m.resumeCandidates = nil
	m.resumeIndex = 0
}

func (m *Model) openResumePicker() {
	m.clearMention()
	m.clearSkill()
	m.clearSlashArg()
	m.clearSlashCompletion()
	m.resumeActive = true
	m.setInputText("/resume ")
	m.syncTextareaFromInput()
	m.updateResumeCandidates()
}

func (m *Model) updateResumeCandidates() {
	if !m.resumeActive || m.cfg.ResumeComplete == nil || m.running {
		m.resumeCandidates = nil
		m.resumeQuery = ""
		m.resumeIndex = 0
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.slashArgCandidates) > 0 {
		m.resumeCandidates = nil
		return
	}
	query, ok := resumeQueryAtCursor(m.input, m.cursor)
	if !ok {
		m.resumeCandidates = nil
		m.resumeQuery = ""
		m.resumeIndex = 0
		return
	}
	candidates, err := m.cfg.ResumeComplete(query, 200)
	if err != nil || len(candidates) == 0 {
		m.resumeCandidates = nil
		m.resumeQuery = query
		m.resumeIndex = 0
		return
	}
	m.resumeQuery = query
	m.resumeCandidates = append([]ResumeCandidate(nil), candidates...)
	if m.resumeIndex >= len(m.resumeCandidates) {
		m.resumeIndex = len(m.resumeCandidates) - 1
	}
	if m.resumeIndex < 0 {
		m.resumeIndex = 0
	}
}

func (m *Model) applyResumeCompletion() {
	if len(m.resumeCandidates) == 0 {
		m.updateResumeCandidates()
		if len(m.resumeCandidates) == 0 {
			return
		}
	}
	choice := strings.TrimSpace(m.resumeCandidates[m.resumeIndex].SessionID)
	if choice == "" {
		return
	}
	m.setInputText("/resume " + choice + " ")
	m.clearResume()
}

func (m *Model) handleResumeKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if _, ok := resumeQueryAtCursor(m.input, m.cursor); ok {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearResume()
		return true, nil
	case "up":
		if m.resumeIndex > 0 {
			m.resumeIndex--
		}
		return true, nil
	case "down":
		if m.resumeIndex < len(m.resumeCandidates)-1 {
			m.resumeIndex++
		}
		return true, nil
	case "tab":
		m.applyResumeCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case "enter":
		if m.running || len(m.resumeCandidates) == 0 {
			return true, nil
		}
		selected := strings.TrimSpace(m.resumeCandidates[m.resumeIndex].SessionID)
		if selected == "" {
			return true, nil
		}
		_, cmd := m.submitLine("/resume " + selected)
		return true, cmd
	default:
		return false, nil
	}
}

func (m *Model) renderResumeList() string {
	if len(m.resumeCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.resumeCandidates))
	start := 0
	if m.resumeIndex >= maxItems {
		start = m.resumeIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.resumeCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.resumeCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		item := m.resumeCandidates[i]
		prompt := strings.TrimSpace(item.Prompt)
		if prompt == "" {
			prompt = "-"
		}
		age := strings.TrimSpace(item.Age)
		if age == "" {
			age = "-"
		}
		display := fmt.Sprintf("%s  %s", age, prompt)
		prefix := "  "
		if i == m.resumeIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(display))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(display))
		}
	}
	if end < len(m.resumeCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.resumeCandidates)-end),
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) clearSlashArg() {
	m.slashArgActive = false
	m.slashArgCommand = ""
	m.slashArgQuery = ""
	m.slashArgCandidates = nil
	m.slashArgIndex = 0
	m.modelReasoningRef = ""
	m.connectProvider = ""
	m.connectModel = ""
	m.connectBaseURL = ""
	m.connectTimeout = ""
	m.connectAPIKey = ""
}

func (m *Model) openSlashArgPicker(command string) {
	cmd := strings.ToLower(strings.TrimSpace(command))
	if cmd == "" {
		return
	}
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashCompletion()
	m.slashArgActive = true
	m.slashArgCommand = cmd
	m.modelReasoningRef = ""
	m.connectProvider = ""
	m.connectModel = ""
	m.connectBaseURL = ""
	m.connectTimeout = ""
	m.connectAPIKey = ""
	switch cmd {
	case "model", "sandbox", "connect", "permission":
		m.setInputText("/" + cmd + " ")
		m.syncTextareaFromInput()
	}
	m.updateSlashArgCandidates()
}

func (m *Model) updateSlashArgCandidates() {
	if !m.slashArgActive || m.cfg.SlashArgComplete == nil || m.running {
		m.slashArgCandidates = nil
		m.slashArgQuery = ""
		m.slashArgIndex = 0
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.resumeCandidates) > 0 {
		m.slashArgCandidates = nil
		return
	}
	command := m.slashArgCommand
	query := ""
	ok := false
	if strings.HasPrefix(command, "model-reasoning:") {
		query, ok = modelWizardQueryAtCursor(m.input, m.cursor)
	} else if strings.EqualFold(strings.TrimSpace(command), "connect") ||
		strings.HasPrefix(command, "connect-model:") ||
		strings.HasPrefix(command, "connect-baseurl:") ||
		strings.HasPrefix(strings.TrimSpace(command), "connect-timeout:") ||
		strings.HasPrefix(strings.TrimSpace(command), "connect-apikey:") {
		query, ok = connectWizardQueryAtCursor(m.input, m.cursor)
		if strings.HasPrefix(strings.TrimSpace(command), "connect-apikey:") {
			m.slashArgCandidates = nil
			m.slashArgQuery = query
			m.slashArgIndex = 0
			return
		}
	} else {
		var parsedCmd string
		parsedCmd, query, ok = slashArgQueryAtCursor(m.input, m.cursor)
		if ok && parsedCmd != command {
			ok = false
		}
	}
	if !ok {
		m.slashArgCandidates = nil
		m.slashArgQuery = ""
		m.slashArgIndex = 0
		return
	}
	candidates, err := m.cfg.SlashArgComplete(command, query, 200)
	if err != nil || len(candidates) == 0 {
		m.slashArgCandidates = nil
		m.slashArgQuery = query
		m.slashArgIndex = 0
		return
	}
	filtered := make([]SlashArgCandidate, 0, len(candidates))
	for _, one := range candidates {
		value := strings.TrimSpace(one.Value)
		if value == "" {
			continue
		}
		display := strings.TrimSpace(one.Display)
		if display == "" {
			display = value
		}
		filtered = append(filtered, SlashArgCandidate{Value: value, Display: display})
	}
	if len(filtered) == 0 {
		m.slashArgCandidates = nil
		m.slashArgQuery = query
		m.slashArgIndex = 0
		return
	}
	m.slashArgQuery = query
	m.slashArgCandidates = filtered
	if m.slashArgIndex >= len(m.slashArgCandidates) {
		m.slashArgIndex = len(m.slashArgCandidates) - 1
	}
	if m.slashArgIndex < 0 {
		m.slashArgIndex = 0
	}
}

func (m *Model) applySlashArgCompletion() {
	if len(m.slashArgCandidates) == 0 || strings.TrimSpace(m.slashArgCommand) == "" {
		m.updateSlashArgCandidates()
		if len(m.slashArgCandidates) == 0 || strings.TrimSpace(m.slashArgCommand) == "" {
			return
		}
	}
	choice := strings.TrimSpace(m.slashArgCandidates[m.slashArgIndex].Value)
	if choice == "" {
		return
	}
	command := strings.TrimSpace(m.slashArgCommand)
	switch {
	case strings.HasPrefix(command, "model-reasoning:"):
		m.setInputText("/model " + choice)
	case strings.EqualFold(command, "connect"):
		m.setInputText("/connect " + choice)
	case strings.HasPrefix(command, "connect-model:"):
		m.setInputText("/connect " + choice)
	case strings.HasPrefix(command, "connect-baseurl:"):
		m.setInputText("/connect " + choice)
	case strings.HasPrefix(command, "connect-timeout:"):
		m.setInputText("/connect " + choice)
	default:
		m.setInputText("/" + m.slashArgCommand + " " + choice + " ")
	}
	if strings.HasPrefix(command, "model-reasoning:") ||
		strings.EqualFold(command, "connect") ||
		strings.HasPrefix(command, "connect-model:") ||
		strings.HasPrefix(command, "connect-baseurl:") ||
		strings.HasPrefix(command, "connect-timeout:") ||
		strings.HasPrefix(command, "connect-apikey:") {
		m.syncTextareaFromInput()
		m.updateSlashArgCandidates()
		return
	}
	m.clearSlashArg()
}

func (m *Model) handleSlashArgKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.slashArgActive {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearSlashArg()
		return true, nil
	case "up":
		if m.slashArgIndex > 0 {
			m.slashArgIndex--
		}
		return true, nil
	case "down":
		if m.slashArgIndex < len(m.slashArgCandidates)-1 {
			m.slashArgIndex++
		}
		return true, nil
	case "tab":
		m.applySlashArgCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case "enter":
		if m.running || strings.TrimSpace(m.slashArgCommand) == "" {
			return true, nil
		}
		command := strings.TrimSpace(m.slashArgCommand)
		selected := ""
		if len(m.slashArgCandidates) > 0 && m.slashArgIndex >= 0 && m.slashArgIndex < len(m.slashArgCandidates) {
			selected = strings.TrimSpace(m.slashArgCandidates[m.slashArgIndex].Value)
		}
		if strings.EqualFold(command, "model") {
			if selected == "" {
				selected = strings.TrimSpace(m.slashArgQuery)
			}
			modelRef := strings.ToLower(strings.TrimSpace(selected))
			if modelRef == "" {
				return true, nil
			}
			m.modelReasoningRef = modelRef
			m.slashArgCommand = buildModelReasoningStepCommand(modelRef)
			m.setInputText("/model ")
			m.syncTextareaFromInput()
			m.slashArgIndex = 0
			m.updateSlashArgCandidates()
			return true, nil
		}
		if strings.HasPrefix(command, "model-reasoning:") {
			reasoning := strings.TrimSpace(selected)
			if reasoning == "" {
				reasoning = strings.TrimSpace(m.slashArgQuery)
			}
			modelRef, ok := parseModelReasoningStepCommand(command)
			if !ok {
				modelRef = strings.ToLower(strings.TrimSpace(m.modelReasoningRef))
			}
			if modelRef == "" || reasoning == "" {
				return true, nil
			}
			_, cmd := m.submitLine("/model " + modelRef + " " + reasoning)
			return true, cmd
		}
		if strings.EqualFold(command, "connect") {
			if selected == "" {
				selected = strings.TrimSpace(m.slashArgQuery)
			}
			provider := strings.ToLower(strings.TrimSpace(selected))
			if provider == "" {
				return true, nil
			}
			m.connectProvider = provider
			m.connectModel = ""
			m.connectBaseURL = ""
			m.connectTimeout = ""
			m.connectAPIKey = ""
			m.slashArgCommand = "connect-baseurl:" + provider
			m.setInputText("/connect ")
			m.syncTextareaFromInput()
			m.slashArgIndex = 0
			m.updateSlashArgCandidates()
			return true, nil
		}
		if strings.HasPrefix(command, "connect-baseurl:") {
			if selected == "" {
				selected = strings.TrimSpace(m.slashArgQuery)
			}
			provider := strings.TrimSpace(strings.TrimPrefix(command, "connect-baseurl:"))
			baseURL := strings.TrimSpace(selected)
			if provider == "" || baseURL == "" {
				return true, nil
			}
			m.connectProvider = provider
			m.connectBaseURL = baseURL
			m.connectTimeout = ""
			m.connectAPIKey = ""
			m.connectModel = ""
			m.slashArgCommand = "connect-timeout:" + provider
			m.setInputText("/connect ")
			m.syncTextareaFromInput()
			m.slashArgIndex = 0
			m.updateSlashArgCandidates()
			return true, nil
		}
		if strings.HasPrefix(command, "connect-timeout:") {
			if selected == "" {
				selected = strings.TrimSpace(m.slashArgQuery)
			}
			provider := strings.TrimSpace(strings.TrimPrefix(command, "connect-timeout:"))
			timeout := strings.TrimSpace(selected)
			if provider == "" || timeout == "" {
				return true, nil
			}
			if _, err := strconv.Atoi(timeout); err != nil {
				return true, nil
			}
			m.connectProvider = provider
			m.connectTimeout = timeout
			m.connectAPIKey = ""
			m.connectModel = ""
			m.slashArgCommand = "connect-apikey:" + provider
			m.setInputText("/connect ")
			m.syncTextareaFromInput()
			m.slashArgIndex = 0
			m.updateSlashArgCandidates()
			return true, nil
		}
		if strings.HasPrefix(command, "connect-apikey:") {
			apiKey := strings.TrimSpace(m.slashArgQuery)
			if apiKey == "" {
				return true, nil
			}
			provider := strings.TrimSpace(m.connectProvider)
			baseURL := strings.TrimSpace(m.connectBaseURL)
			timeout := strings.TrimSpace(m.connectTimeout)
			if provider == "" || baseURL == "" || timeout == "" {
				return true, nil
			}
			m.connectAPIKey = apiKey
			m.slashArgCommand = buildConnectModelStepCommand(provider, baseURL, timeout, apiKey)
			m.setInputText("/connect ")
			m.syncTextareaFromInput()
			m.slashArgIndex = 0
			m.updateSlashArgCandidates()
			return true, nil
		}
		if strings.HasPrefix(command, "connect-model:") {
			model := strings.TrimSpace(selected)
			if model == "" {
				model = strings.TrimSpace(m.slashArgQuery)
			}
			provider := strings.TrimSpace(m.connectProvider)
			baseURL := strings.TrimSpace(m.connectBaseURL)
			timeout := strings.TrimSpace(m.connectTimeout)
			apiKey := strings.TrimSpace(m.connectAPIKey)
			if provider == "" || model == "" || baseURL == "" || timeout == "" || apiKey == "" {
				return true, nil
			}
			execLine := "/connect " + provider + " " + model + " " + baseURL + " " + timeout + " " + apiKey
			_, cmd := m.submitLineWithDisplay(execLine, "/connect")
			return true, cmd
		}
		if selected == "" {
			return true, nil
		}
		_, cmd := m.submitLine("/" + command + " " + selected)
		return true, cmd
	default:
		return false, nil
	}
}

func (m *Model) renderSlashArgList() string {
	if len(m.slashArgCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.slashArgCandidates))
	start := 0
	if m.slashArgIndex >= maxItems {
		start = m.slashArgIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.slashArgCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.slashArgCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		display := strings.TrimSpace(m.slashArgCandidates[i].Display)
		if display == "" {
			display = strings.TrimSpace(m.slashArgCandidates[i].Value)
		}
		prefix := "  "
		if i == m.slashArgIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(display))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(display))
		}
	}
	if end < len(m.slashArgCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.slashArgCandidates)-end),
		))
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Slash command completion
// ---------------------------------------------------------------------------

func (m *Model) refreshSlashCommands() {
	m.clearSlashCompletion()
	if m.running {
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.resumeCandidates) > 0 || len(m.slashArgCandidates) > 0 {
		return
	}
	query, ok := slashCommandQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	candidates := make([]string, 0, len(m.cfg.Commands))
	for _, cmd := range m.cfg.Commands {
		full := "/" + strings.TrimSpace(cmd)
		if full == "/" {
			continue
		}
		if query == "" || strings.HasPrefix(strings.ToLower(full), "/"+strings.ToLower(query)) {
			candidates = append(candidates, full)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return
	}
	m.slashCandidates = candidates
	m.slashIndex = 0
	m.slashPrefix = "/" + query
}

func (m *Model) applySlashCommandCompletion() {
	if len(m.slashCandidates) == 0 {
		m.refreshSlashCommands()
		if len(m.slashCandidates) == 0 {
			return
		}
	}
	m.setInputText(strings.TrimSpace(m.slashCandidates[m.slashIndex]))
	m.clearSlashCompletion()
}

func (m *Model) handleSlashTab() {
	// Keep compatibility for tab on slash command prefix.
	if len(m.slashCandidates) == 0 {
		m.refreshSlashCommands()
	}
	m.applySlashCommandCompletion()
}

func (m *Model) handleSlashCommandKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if _, ok := slashCommandQueryAtCursor(m.input, m.cursor); ok {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearSlashCompletion()
		return true, nil
	case "up":
		if m.slashIndex > 0 {
			m.slashIndex--
		}
		return true, nil
	case "down":
		if m.slashIndex < len(m.slashCandidates)-1 {
			m.slashIndex++
		}
		return true, nil
	case "tab":
		m.applySlashCommandCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case "enter":
		if m.running || len(m.slashCandidates) == 0 {
			return true, nil
		}
		selected := strings.TrimSpace(m.slashCandidates[m.slashIndex])
		if selected == "" {
			return true, nil
		}
		if selected == "/model" || selected == "/sandbox" || selected == "/connect" || selected == "/permission" || selected == "/resume" {
			m.setInputText(selected)
			m.syncTextareaFromInput()
			m.clearSlashCompletion()
			m.tryOpenSlashArgPicker(selected)
			return true, nil
		}
		_, cmd := m.submitLine(selected)
		return true, cmd
	default:
		return false, nil
	}
}

func (m *Model) renderSlashCommandList() string {
	if len(m.slashCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.slashCandidates))
	start := 0
	if m.slashIndex >= maxItems {
		start = m.slashIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.slashCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.slashCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.slashIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(m.slashCandidates[i]))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(m.slashCandidates[i]))
		}
	}
	if end < len(m.slashCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.slashCandidates)-end),
		))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) clearSlashCompletion() {
	m.slashCandidates = nil
	m.slashIndex = 0
	m.slashPrefix = ""
}

func (m *Model) setInputText(text string) {
	m.input = []rune(text)
	m.cursor = len(m.input)
}

func (m *Model) recordHistoryEntry(value string) {
	entry := strings.TrimSpace(value)
	if entry == "" {
		return
	}
	// Slash commands are control inputs and should not pollute user message history.
	if strings.HasPrefix(entry, "/") {
		return
	}
	if len(m.history) == 0 || m.history[len(m.history)-1] != entry {
		m.history = append(m.history, entry)
	}
}

// ---------------------------------------------------------------------------
// External prompt handling
// ---------------------------------------------------------------------------

func (m *Model) enqueuePrompt(req tuievents.PromptRequestMsg) {
	if req.Response == nil {
		return
	}
	if m.activePrompt == nil {
		m.activePrompt = &promptState{
			prompt:   req.Prompt,
			secret:   req.Secret,
			response: req.Response,
		}
		return
	}
	m.pendingPrompt = append(m.pendingPrompt, req)
}

func (m *Model) finishPrompt(line string, err error) {
	if m.activePrompt == nil {
		return
	}
	resp := m.activePrompt.response
	if resp != nil {
		resp <- tuievents.PromptResponse{Line: line, Err: err}
	}
	if len(m.pendingPrompt) == 0 {
		m.activePrompt = nil
		return
	}
	next := m.pendingPrompt[0]
	m.pendingPrompt = m.pendingPrompt[1:]
	m.activePrompt = &promptState{
		prompt:   next.Prompt,
		secret:   next.Secret,
		response: next.Response,
	}
}

func (m *Model) handlePromptKey(msg tea.KeyMsg) tea.Cmd {
	if m.activePrompt == nil {
		return nil
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		m.finishPrompt("", errors.New(tuievents.PromptErrInterrupt))
		return nil
	case "ctrl+d":
		if len(m.activePrompt.input) == 0 {
			m.finishPrompt("", errors.New(tuievents.PromptErrEOF))
			return nil
		}
		if m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor], m.activePrompt.input[m.activePrompt.cursor+1:]...)
		}
		return nil
	case "enter":
		m.finishPrompt(strings.TrimSpace(string(m.activePrompt.input)), nil)
		return nil
	case "left":
		if m.activePrompt.cursor > 0 {
			m.activePrompt.cursor--
		}
		return nil
	case "right":
		if m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.cursor++
		}
		return nil
	case "home", "ctrl+a":
		m.activePrompt.cursor = 0
		return nil
	case "end", "ctrl+e":
		m.activePrompt.cursor = len(m.activePrompt.input)
		return nil
	case "backspace":
		if m.activePrompt.cursor > 0 {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor-1], m.activePrompt.input[m.activePrompt.cursor:]...)
			m.activePrompt.cursor--
		}
		return nil
	case "delete":
		if m.activePrompt.cursor >= 0 && m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor], m.activePrompt.input[m.activePrompt.cursor+1:]...)
		}
		return nil
	case "ctrl+u":
		m.activePrompt.input = m.activePrompt.input[:0]
		m.activePrompt.cursor = 0
		return nil
	}
	if len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			head := append([]rune(nil), m.activePrompt.input[:m.activePrompt.cursor]...)
			head = append(head, r)
			m.activePrompt.input = append(head, m.activePrompt.input[m.activePrompt.cursor:]...)
			m.activePrompt.cursor++
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

func (m *Model) observeRender(duration time.Duration, bytes int, redrawMode string) {
	m.diag.Frames++
	m.diag.LastFrameDuration = duration
	if strings.TrimSpace(redrawMode) == "" {
		redrawMode = "incremental"
	}
	m.diag.RedrawMode = redrawMode
	if redrawMode == "fullscreen" || redrawMode == "full" {
		m.diag.FullRepaints++
	} else {
		m.diag.IncrementalFrames++
	}
	if duration >= 40*time.Millisecond {
		m.diag.SlowFrames++
	}
	if duration > m.diag.MaxFrameDuration {
		m.diag.MaxFrameDuration = duration
	}
	if m.diag.Frames == 1 {
		m.diag.AvgFrameDuration = duration
	} else {
		total := time.Duration(int64(m.diag.AvgFrameDuration)*(int64(m.diag.Frames)-1) + int64(duration))
		m.diag.AvgFrameDuration = total / time.Duration(m.diag.Frames)
	}
	if bytes > 0 {
		m.diag.RenderBytes += uint64(bytes)
		if uint64(bytes) > m.diag.PeakFrameBytes {
			m.diag.PeakFrameBytes = uint64(bytes)
		}
	}
	m.observeInputLatency()
	m.diag.LastRenderAt = time.Now()
	if m.cfg.OnDiagnostics != nil {
		m.cfg.OnDiagnostics(m.diag)
	}
}

func (m *Model) requestFullRepaint() {
	m.pendingFullRepaint = true
}

func (m *Model) observeInputLatency() {
	if m.pendingInputAt.IsZero() {
		return
	}
	latency := time.Since(m.pendingInputAt)
	m.pendingInputAt = time.Time{}
	m.diag.LastInputLatency = latency
	m.inputLatencyCount++
	if m.diag.AvgInputLatency == 0 || m.inputLatencyCount <= 1 {
		m.diag.AvgInputLatency = latency
	} else {
		total := time.Duration(int64(m.diag.AvgInputLatency)*(int64(m.inputLatencyCount)-1) + int64(latency))
		m.diag.AvgInputLatency = total / time.Duration(m.inputLatencyCount)
	}
	const window = 128
	if len(m.inputLatencyWindow) >= window {
		copy(m.inputLatencyWindow, m.inputLatencyWindow[1:])
		m.inputLatencyWindow = m.inputLatencyWindow[:window-1]
	}
	m.inputLatencyWindow = append(m.inputLatencyWindow, latency)
	m.diag.P95InputLatency = percentileDuration(m.inputLatencyWindow, 0.95)
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

func mentionQueryAtCursor(input []rune, cursor int) (int, int, string, bool) {
	if len(input) == 0 {
		return 0, 0, "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	start := cursor
	for start > 0 && isMentionQueryRune(input[start-1]) {
		start--
	}
	if start == 0 || input[start-1] != '@' {
		return 0, 0, "", false
	}
	at := start - 1
	if at > 0 {
		prev := input[at-1]
		if prev != ' ' && prev != '\t' && prev != '(' && prev != '[' && prev != '{' && prev != ',' && prev != ';' && prev != ':' && prev != '"' && prev != '\'' {
			return 0, 0, "", false
		}
	}
	end := cursor
	for end < len(input) && isMentionQueryRune(input[end]) {
		end++
	}
	return at, end, string(input[start:end]), true
}

func resumeQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	text := strings.TrimSpace(string(input[:cursor]))
	if text == "" {
		return "", false
	}
	if text == "/resume" {
		return "", true
	}
	if !strings.HasPrefix(text, "/resume ") {
		return "", false
	}
	query := strings.TrimSpace(strings.TrimPrefix(text, "/resume "))
	return query, true
}

func slashArgQueryAtCursor(input []rune, cursor int) (string, string, bool) {
	if len(input) == 0 {
		return "", "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	raw := string(input[:cursor])
	text := strings.TrimSpace(raw)
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", "", false
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), "/")
	switch command {
	case "model", "sandbox", "connect", "permission":
	default:
		return "", "", false
	}
	if len(fields) == 1 {
		// Require a trailing space after command before opening the arg picker.
		if len(raw) == 0 {
			return "", "", false
		}
		last := raw[len(raw)-1]
		if last != ' ' && last != '\t' {
			return "", "", false
		}
		return command, "", true
	}
	if len(fields) == 2 {
		return command, strings.TrimSpace(fields[1]), true
	}
	return "", "", false
}

func connectWizardQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	raw := string(input[:cursor])
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/connect") {
		return "", false
	}
	if strings.EqualFold(trimmed, "/connect") {
		return "", true
	}
	if !strings.HasPrefix(raw, "/connect ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, "/connect ")), true
}

func modelWizardQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	raw := string(input[:cursor])
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/model") {
		return "", false
	}
	if strings.EqualFold(trimmed, "/model") {
		return "", true
	}
	if !strings.HasPrefix(raw, "/model ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, "/model ")), true
}

func buildModelReasoningStepCommand(alias string) string {
	return "model-reasoning:" + url.QueryEscape(strings.ToLower(strings.TrimSpace(alias)))
}

func parseModelReasoningStepCommand(command string) (string, bool) {
	payload := strings.TrimSpace(strings.TrimPrefix(command, "model-reasoning:"))
	if payload == "" {
		return "", false
	}
	decoded, err := url.QueryUnescape(payload)
	if err != nil {
		return "", false
	}
	alias := strings.ToLower(strings.TrimSpace(decoded))
	if alias == "" {
		return "", false
	}
	return alias, true
}

func buildConnectModelStepCommand(provider, baseURL, timeout, apiKey string) string {
	return "connect-model:" + strings.TrimSpace(provider) +
		"|" + url.QueryEscape(strings.TrimSpace(baseURL)) +
		"|" + strings.TrimSpace(timeout) +
		"|" + url.QueryEscape(strings.TrimSpace(apiKey))
}

func slashCommandQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	text := strings.TrimSpace(string(input[:cursor]))
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", false
	}
	if strings.Contains(text, " ") {
		return "", false
	}
	query := strings.TrimPrefix(text, "/")
	return query, true
}

func isMentionQueryRune(r rune) bool {
	if r == '_' || r == '-' || r == '.' || r == '/' || r == '\\' {
		return true
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// skillQueryAtCursor detects a $skill token at cursor position.
// Returns the span [start, end) and the query text after '$'.
func skillQueryAtCursor(input []rune, cursor int) (int, int, string, bool) {
	if len(input) == 0 {
		return 0, 0, "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	start := cursor
	for start > 0 && isSkillQueryRune(input[start-1]) {
		start--
	}
	if start == 0 || input[start-1] != '$' {
		return 0, 0, "", false
	}
	dollar := start - 1
	if dollar > 0 {
		prev := input[dollar-1]
		if prev != ' ' && prev != '\t' && prev != '(' && prev != '[' && prev != '{' && prev != ',' && prev != ';' && prev != ':' && prev != '"' && prev != '\'' {
			return 0, 0, "", false
		}
	}
	end := cursor
	for end < len(input) && isSkillQueryRune(input[end]) {
		end++
	}
	return dollar, end, string(input[start:end]), true
}

func isSkillQueryRune(r rune) bool {
	if r == '_' || r == '-' {
		return true
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func replaceRuneSpan(input []rune, start int, end int, replacement string) ([]rune, int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(input) {
		end = len(input)
	}
	out := append([]rune(nil), input[:start]...)
	repl := []rune(replacement)
	out = append(out, repl...)
	out = append(out, input[end:]...)
	return out, start + len(repl)
}

// overlayBottom places an overlay box near the bottom of the base text.
func overlayBottom(base string, overlay string, width int, baseLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	if len(baseLines) == 0 {
		return overlay
	}
	startRow := maxInt(0, len(baseLines)-len(overlayLines)-2)
	for i, line := range overlayLines {
		row := startRow + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		baseLines[row] = padCenter(line, width)
	}
	return strings.Join(baseLines, "\n")
}

func padCenter(text string, width int) string {
	if width <= 0 {
		return text
	}
	textWidth := utf8.RuneCountInString(text)
	if textWidth >= width {
		return text
	}
	left := (width - textWidth) / 2
	right := width - textWidth - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		percentile = 0
	}
	if percentile >= 1 {
		percentile = 1
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	index := int(float64(len(sorted)-1) * percentile)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func normalizedSelectionRange(start textSelectionPoint, end textSelectionPoint, lineCount int) (textSelectionPoint, textSelectionPoint, bool) {
	if lineCount <= 0 || start.line < 0 || end.line < 0 {
		return textSelectionPoint{}, textSelectionPoint{}, false
	}
	if start.line >= lineCount {
		start.line = lineCount - 1
	}
	if end.line >= lineCount {
		end.line = lineCount - 1
	}
	if start.line > end.line || (start.line == end.line && start.col > end.col) {
		start, end = end, start
	}
	if start.col < 0 {
		start.col = 0
	}
	if end.col < 0 {
		end.col = 0
	}
	return start, end, true
}

func selectionTextFromLines(lines []string, start textSelectionPoint, end textSelectionPoint) string {
	if len(lines) == 0 {
		return ""
	}
	if start.line == end.line && start.col == end.col {
		return ""
	}
	var out []string
	for i := start.line; i <= end.line && i < len(lines); i++ {
		line := lines[i]
		width := displayColumns(line)
		from := 0
		to := width
		if i == start.line {
			from = start.col
		}
		if i == end.line {
			to = end.col
		}
		if from < 0 {
			from = 0
		}
		if to > width {
			to = width
		}
		if to < from {
			to = from
		}
		out = append(out, sliceByDisplayColumns(line, from, to))
	}
	return strings.Join(out, "\n")
}

func renderSelectionOnLines(lines []string, start textSelectionPoint, end textSelectionPoint) []string {
	if len(lines) == 0 {
		return nil
	}
	highlight := lipgloss.NewStyle().Reverse(true)
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if i < start.line || i > end.line {
			out = append(out, lines[i])
			continue
		}
		line := lines[i]
		width := displayColumns(line)
		from := 0
		to := width
		if i == start.line {
			from = start.col
		}
		if i == end.line {
			to = end.col
		}
		if from < 0 {
			from = 0
		}
		if to > width {
			to = width
		}
		if to < from {
			to = from
		}
		prefix := sliceByDisplayColumns(line, 0, from)
		middle := sliceByDisplayColumns(line, from, to)
		suffix := sliceByDisplayColumns(line, to, width)
		if middle == "" {
			out = append(out, line)
			continue
		}
		out = append(out, prefix+highlight.Render(middle)+suffix)
	}
	return out
}

func displayColumns(s string) int {
	return runewidth.StringWidth(s)
}

func sliceByDisplayColumns(s string, start int, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if s == "" || start == end {
		return ""
	}
	var b strings.Builder
	col := 0
	prevIncluded := false
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if w == 0 {
			if prevIncluded {
				b.WriteRune(r)
			}
			continue
		}
		if col >= end {
			break
		}
		include := col >= start && col < end
		if include {
			b.WriteRune(r)
		}
		prevIncluded = include
		col += w
	}
	return b.String()
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
