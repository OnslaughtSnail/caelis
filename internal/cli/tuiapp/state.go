package tuiapp

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

const maxInputBarRows = 4
const ctrlCExitWindow = 2 * time.Second
const runningHintRotateEveryTicks = 60
const runningLightSpeed = 0.55
const runningLightBandRadius = 5.5
const runningLightLead = 4.0
const copyHintDuration = 1600 * time.Millisecond
const toolOutputPreviewLines = 4
const toolOutputHistoryLines = 2000
const subagentOutputPreviewLines = 12
const activityBlockPreviewLines = 8
const inputHorizontalInset = tuikit.InputInset
const paletteAnimationInterval = 16 * time.Millisecond
const paletteAnimationStep = 3
const systemHintDuration = 1800 * time.Millisecond
const streamSmoothingTickIntervalDefault = 16 * time.Millisecond
const streamSmoothingWarmDelayDefault = 40 * time.Millisecond
const streamSmoothingTargetLagDefault = 160 * time.Millisecond
const streamSmoothingNormalCPSDefault = 68.0
const streamSmoothingCatchupCPSDefault = 128.0
const streamSmoothingNormalMaxPerFrameDefault = 5
const streamSmoothingCatchupMaxPerFrameDefault = 12
const inlinePanelMinVisibleDuration = 600 * time.Millisecond
const inlinePanelCollapseDuration = 180 * time.Millisecond

type hintEntry struct {
	id             uint64
	text           string
	priority       tuievents.HintPriority
	clearOnMessage bool
}

var runningSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var runningCarouselLines = []string{
	"Send follow-up guidance while the current run is still active.",
	"Use #path to anchor the model on exact files.",
	"/model can switch both model and reasoning level.",
	"Press Esc to interrupt, Enter to submit another message.",
	"Review the latest tool output before sending follow-up guidance.",
}

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

type Config struct {
	Version              string
	Workspace            string
	ModelAlias           string
	ShowWelcomeCard      bool
	InitialLogs          []string
	Commands             []string
	Wizards              []WizardDef
	ExecuteLine          func(Submission) tuievents.TaskResultMsg
	CancelRunning        func() bool
	ToggleMode           func() (string, error)
	ModeLabel            func() string
	RefreshWorkspace     func() string
	RefreshStatus        func() (string, string)
	MentionComplete      func(string, int) ([]string, error)
	FileComplete         func(string, int) ([]string, error)
	SkillComplete        func(string, int) ([]string, error)
	ResumeComplete       func(string, int) ([]ResumeCandidate, error)
	SlashArgComplete     func(command string, query string, limit int) ([]SlashArgCandidate, error)
	ReadClipboardText    func() (string, error)
	WriteClipboardText   func(string) error
	PasteClipboardImage  func() ([]string, string, error)
	ClearAttachments     func() []string
	SetAttachments       func([]string) []string
	OnDiagnostics        func(Diagnostics)
	FrameBatchMainStream bool
	StreamTickInterval   time.Duration
	StreamWarmDelay      time.Duration
	StreamNormalCPS      float64
	StreamCatchupCPS     float64
	StreamTargetLag      time.Duration
	StreamThreshold      int
	StreamNormalMaxTick  int
	StreamCatchupMaxTick int
}

type ResumeCandidate struct {
	SessionID string
	Prompt    string
	Age       string
}

type SlashArgCandidate struct {
	Value   string
	Display string
	Detail  string
	NoAuth  bool
}

type commandItem struct {
	name string
}

func (i commandItem) Title() string       { return "/" + i.name }
func (i commandItem) Description() string { return "Run slash command " + i.name }
func (i commandItem) FilterValue() string { return i.name }

type streamSmoothingState struct {
	targetKind   string
	streamKind   string
	sessionKey   string
	actor        string
	pending      []string
	firstSeen    time.Time
	firstPaint   time.Time
	pendingSince time.Time
	lastTick     time.Time
	budget       float64
	upstreamDone bool
	rendered     int
}

type streamPlaybackMetrics struct {
	FirstByteLatency     time.Duration
	BacklogRunes         int
	MaxBacklogRunes      int
	LastFrameAppendRunes int
	LastFrameRenderCost  time.Duration
	LastFrameAt          time.Time
	Frames               uint64
}

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
	attachments []Attachment
}

type inputAttachment struct {
	Name   string
	Offset int
}

type activityBlockKind string

const (
	activityBlockExploration activityBlockKind = "exploration"
	activityBlockTaskMonitor activityBlockKind = "task_monitor"
)

type activityEntry struct {
	tool   string
	action string
	path   string
	query  string
	raw    string
	waitMS int
	result bool
}

type foldedActivityBlockState struct {
	kind      activityBlockKind
	active    bool
	finalized bool
	entries   []activityEntry
	summary   string
}

type toolOutputLine struct {
	text   string
	stream string
}

// toolOutputState is REMOVED — replaced by BashPanelBlock in Document.
// The type definition is kept temporarily for compilation during migration.

// subagentPanelState is REMOVED — replaced by SubagentPanelBlock in Document.
// The type definition is kept temporarily for compilation during migration.

type planEntryState struct {
	Content string
	Status  string
}

type btwOverlayState struct {
	Question string
	Answer   string
	Loading  bool
	Scroll   int
}

// toolAnchor is a pending, unclaimed tool-style TranscriptBlock.
type toolAnchor struct {
	blockID  string
	toolName string // normalized tool name from "▸ TOOLNAME ..." line
}

type Model struct {
	cfg       Config
	theme     tuikit.Theme
	themeAuto bool
	keys      appKeyMap
	help      help.Model

	width   int
	height  int
	focused bool

	// --- Document model (source of truth for viewport content) ---
	doc *Document

	// Active block tracking IDs (empty string means no active block).
	activeAssistantID    string
	activeAssistantActor string
	activeReasoningID    string
	activeReasoningActor string
	activeActivityID     string

	// Maps external keys to doc block IDs.
	toolOutputBlockIDs             map[string]string
	subagentBlockIDs               map[string]string
	subagentSessions               map[string]*SubagentSessionState
	subagentSessionRefs            map[string][]string
	participantTurnIDs             map[string]string
	activeParticipantTurnSessionID string

	// pendingToolAnchors tracks tool-style TranscriptBlocks ("▸ BASH ...",
	// "▸ SPAWN ...") that haven't yet been claimed by a panel. FIFO order.
	pendingToolAnchors []toolAnchor

	// callAnchorIndex maps a CallID (or SpawnID parent CallID) to the block ID
	// of its corresponding "▸ TOOL ..." line. Once a pending anchor is claimed,
	// it's stored here for stable future lookups.
	callAnchorIndex map[string]string

	// taskOriginCallID maps a background TaskID to the CallID of the original
	// BASH tool invocation that yielded it. This ensures that subsequent
	// watch/wait/write/cancel events route back to the original panel.
	taskOriginCallID map[string]string

	streamLine         string
	lastCommittedStyle tuikit.LineStyle
	lastCommittedRaw   string
	hasCommittedLine   bool
	lastFinalAnswer    string
	planEntries        []planEntryState
	welcomeCardPending bool
	runStartedAt       time.Time
	lastRunDuration    time.Duration
	hasLastRunDuration bool
	showTurnDivider    bool

	// Transient log replacement tracking — now uses block IDs.
	transientBlockID string
	transientIsRetry bool
	transientRemove  bool

	// Viewport caches — populated by syncViewportContent from Document.
	viewportStyledLines []string
	viewportPlainLines  []string
	viewportBlockIDs    []string
	viewport            viewport.Model
	userScrolledUp      bool
	ready               bool

	selecting      bool
	selectionStart textSelectionPoint
	selectionEnd   textSelectionPoint

	inputSelecting      bool
	inputSelectionStart textSelectionPoint
	inputSelectionEnd   textSelectionPoint

	fixedSelecting      bool
	fixedSelectionArea  fixedSelectionArea
	fixedSelectionStart textSelectionPoint
	fixedSelectionEnd   textSelectionPoint

	// --- Composer (independent sub-model for input management) ---
	Composer

	// --- Overlay state (unified overlay management) ---
	OverlayState

	running bool
	spinner spinner.Model
	quit    bool

	runningTick uint64
	runningTip  int

	statusModel   string
	statusContext string
	hint          string
	hintEntries   []hintEntry
	nextHintID    uint64

	pendingInputAt     time.Time
	inputLatencyWindow []time.Duration
	inputLatencyCount  uint64
	diag               Diagnostics

	ctrlCArmed  bool
	lastCtrlCAt time.Time
	ctrlCArmSeq uint64

	streamSmoothing              map[string]*streamSmoothingState
	streamSmoothingTickScheduled bool
	panelAnimationTickScheduled  bool
	streamPlayback               streamPlaybackMetrics
	lastViewportContent          string
	viewportSyncDepth            int
	viewportDirty                bool
}
