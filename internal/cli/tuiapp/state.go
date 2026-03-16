package tuiapp

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
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
const activityBlockPreviewLines = 8
const toolOutputFadeHold = 650 * time.Millisecond
const toolOutputFadeInterval = 110 * time.Millisecond
const inputHorizontalInset = tuikit.InputInset
const paletteAnimationInterval = 16 * time.Millisecond
const paletteAnimationStep = 3
const systemHintDuration = 1800 * time.Millisecond

type hintEntry struct {
	id             uint64
	text           string
	priority       tuievents.HintPriority
	clearOnMessage bool
}

var runningSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var runningCarouselLines = []string{
	"Send follow-up guidance while the current run is still active.",
	"Use @path to anchor the model on exact files.",
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
	Version             string
	Workspace           string
	ModelAlias          string
	ShowWelcomeCard     bool
	InitialLogs         []string
	Commands            []string
	Wizards             []WizardDef
	ExecuteLine         func(Submission) tuievents.TaskResultMsg
	CancelRunning       func() bool
	ToggleMode          func() (string, error)
	ModeLabel           func() string
	RefreshStatus       func() (string, string)
	MentionComplete     func(string, int) ([]string, error)
	SkillComplete       func(string, int) ([]string, error)
	ResumeComplete      func(string, int) ([]ResumeCandidate, error)
	SlashArgComplete    func(command string, query string, limit int) ([]SlashArgCandidate, error)
	ReadClipboardText   func() (string, error)
	WriteClipboardText  func(string) error
	PasteClipboardImage func() ([]string, string, error)
	ClearAttachments    func() []string
	SetAttachments      func([]string) []string
	OnDiagnostics       func(Diagnostics)
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
	start     int
	end       int
	active    bool
	finalized bool
	entries   []activityEntry
	summary   string
}

type toolOutputLine struct {
	text   string
	stream string
}

type toolOutputState struct {
	key              string
	tool             string
	callID           string
	state            string
	start            int
	end              int
	startedAt        time.Time
	updatedAt        time.Time
	finalizedAt      time.Time
	lastStream       string
	lines            []toolOutputLine
	stdoutPartial    string
	stderrPartial    string
	assistantPartial string
	reasoningPartial string
	delegateFence    bool
	active           bool
	closing          bool
	fadeStep         int
	fadeQueued       bool
	fadeLineCount    int
}

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

type Model struct {
	cfg       Config
	theme     tuikit.Theme
	themeAuto bool
	keys      appKeyMap
	help      help.Model

	width   int
	height  int
	focused bool

	streamLine         string
	lastCommittedStyle tuikit.LineStyle
	lastCommittedRaw   string
	hasCommittedLine   bool
	assistantBlock     *assistantBlockState
	reasoningBlock     *assistantBlockState
	lastFinalAnswer    string
	diffBlocks         []diffBlockState
	activityBlock      *foldedActivityBlockState
	toolOutputs        map[string]*toolOutputState
	planEntries        []planEntryState
	welcomeCardPending bool
	runStartedAt       time.Time
	lastRunDuration    time.Duration
	hasLastRunDuration bool
	showTurnDivider    bool
	btwOverlay         *btwOverlayState
	btwDismissed       bool

	transientLogIdx  int
	transientIsRetry bool
	transientRemove  bool

	historyLines        []string
	viewportStyledLines []string
	viewportPlainLines  []string
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

	textarea textarea.Model
	input    []rune
	cursor   int

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

	showPalette      bool
	palette          list.Model
	paletteAnimLines int
	paletteAnimating bool

	mentionQuery      string
	mentionCandidates []string
	mentionIndex      int
	mentionStart      int
	mentionEnd        int

	skillQuery      string
	skillCandidates []string
	skillIndex      int
	skillStart      int
	skillEnd        int

	history                 []string
	historyAttachments      [][]inputAttachment
	historyIndex            int
	historyDraft            string
	historyDraftAttachments []inputAttachment
	pendingQueue            *pendingPrompt

	slashCandidates []string
	slashIndex      int
	slashPrefix     string

	resumeActive     bool
	resumeQuery      string
	resumeCandidates []ResumeCandidate
	resumeIndex      int

	slashArgActive     bool
	slashArgCommand    string
	slashArgQuery      string
	slashArgCandidates []SlashArgCandidate
	slashArgIndex      int

	wizard *wizardRuntime

	activePrompt  *promptState
	pendingPrompt []tuievents.PromptRequestMsg

	attachmentCount  int
	attachmentNames  []string
	inputAttachments []inputAttachment

	pendingInputAt     time.Time
	inputLatencyWindow []time.Duration
	inputLatencyCount  uint64
	diag               Diagnostics

	ctrlCArmed  bool
	lastCtrlCAt time.Time
	ctrlCArmSeq uint64
}
