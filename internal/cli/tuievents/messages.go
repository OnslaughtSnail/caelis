package tuievents

import "time"

type HintPriority int

const (
	HintPriorityUnspecified HintPriority = iota
	HintPriorityLow
	HintPriorityNormal
	HintPriorityHigh
	HintPriorityCritical
)

type LogChunkMsg struct {
	Chunk string
}

type SetStatusMsg struct {
	Model   string
	Context string
}

type SetCommandsMsg struct {
	Commands []string
}

type SetHintMsg struct {
	Hint           string
	ClearAfter     time.Duration
	Priority       HintPriority
	ClearOnMessage bool
}

type SetRunningMsg struct {
	Running bool
}

type TaskResultMsg struct {
	ExitNow             bool
	Err                 error
	Interrupted         bool
	ContinueRunning     bool
	SuppressTurnDivider bool
}

type PromptRequestMsg struct {
	Title              string
	Prompt             string
	Details            []PromptDetail
	Secret             bool
	Choices            []PromptChoice
	DefaultChoice      string
	SelectedChoices    []string
	Filterable         bool
	MultiSelect        bool
	AllowFreeformInput bool
	Response           chan PromptResponse
}

type PromptResponse struct {
	Line string
	Err  error
}

type PromptChoice struct {
	Label         string
	Value         string
	Detail        string
	AlwaysVisible bool
}

type PromptDetail struct {
	Label    string
	Value    string
	Emphasis bool
}

const (
	PromptErrInterrupt = "prompt_interrupted"
	PromptErrEOF       = "prompt_eof"
)

type MentionCandidatesMsg struct {
	Query      string
	Candidates []string
	Latency    time.Duration
}

type TickStatusMsg struct{}

type RawDeltaTarget string

const (
	RawDeltaTargetAssistant RawDeltaTarget = "assistant"
	RawDeltaTargetBTW       RawDeltaTarget = "btw"
	RawDeltaTargetSubagent  RawDeltaTarget = "subagent"
)

// RawDeltaMsg carries upstream raw stream payloads into the Bubble Tea event
// loop. Presentation smoothing and playback happen inside the model.
type RawDeltaMsg struct {
	Target  RawDeltaTarget
	ScopeID string
	Stream  string
	Actor   string
	Text    string
	Final   bool
}

type AttachmentCountMsg struct {
	Count int
}

// ClearHistoryMsg clears viewport conversation history in TUI.
type ClearHistoryMsg struct{}

type UserMessageMsg struct {
	Text string
}

type ParticipantTurnStartMsg struct {
	SessionID string
	Actor     string
}

type ParticipantToolMsg struct {
	SessionID string
	CallID    string
	ToolName  string
	Args      string
	Output    string
	Final     bool
	Err       bool
}

type ParticipantStatusMsg struct {
	SessionID       string
	State           string
	ApprovalTool    string
	ApprovalCommand string
}

// AssistantStreamMsg carries assistant answer chunks for TUI block rendering.
// When Final is true, Text is the full finalized assistant answer.
type AssistantStreamMsg struct {
	Kind  string
	Actor string
	Text  string
	Final bool
}

// ReasoningStreamMsg carries assistant reasoning chunks for TUI block rendering.
// When Final is true, Text is the full finalized reasoning text.
type ReasoningStreamMsg struct {
	Actor string
	Text  string
	Final bool
}

// DiffBlockMsg carries a structured PATCH diff block for rich TUI rendering.
type DiffBlockMsg struct {
	Tool      string
	Path      string
	Created   bool
	Hunk      string
	Old       string
	New       string
	Preview   string
	Truncated bool
}

type TaskStreamMsg struct {
	Label  string
	Tool   string
	TaskID string
	CallID string
	Stream string
	Chunk  string
	State  string
	Reset  bool
	Final  bool
}

type ToolStreamMsg = TaskStreamMsg

type PlanEntry struct {
	Content string
	Status  string
}

type PlanUpdateMsg struct {
	Entries []PlanEntry
}

type BTWOverlayMsg struct {
	Text  string
	Final bool
}

type BTWErrorMsg struct {
	Text string
}

// SubagentStartMsg signals a new subagent panel should be created.
type SubagentStartMsg struct {
	SpawnID      string // unique spawn instance identifier
	AttachTarget string // child session id or delegation id accepted by /attach
	Agent        string // agent id (e.g. "self")
	CallID       string // parent tool call ID
	AnchorTool   string // transcript anchor label (e.g. "SPAWN", "WRITE")
	ClaimAnchor  bool   // whether this start may claim a pending SPAWN anchor
	Provisional  bool   // whether this is a pre-bootstrap placeholder keyed by callID
}

type SubagentStatusMsg struct {
	SpawnID string
	State   string // "running", "waiting_approval", "completed", "failed", "interrupted"

	// Optional approval context (populated when State == "waiting_approval").
	ApprovalTool    string // tool requesting approval (e.g. "BASH")
	ApprovalCommand string // command or action awaiting approval
}

// SubagentStreamMsg carries assistant or reasoning chunks for a subagent panel.
type SubagentStreamMsg struct {
	SpawnID string
	Stream  string // "assistant" or "reasoning"
	Chunk   string
}

// SubagentToolCallMsg carries tool activity for a subagent panel.
type SubagentToolCallMsg struct {
	SpawnID  string
	ToolName string
	CallID   string
	Args     string
	Stream   string // "stdout", "stderr", "assistant"
	Chunk    string
	Final    bool
}

// SubagentPlanMsg carries a plan update for a subagent panel.
type SubagentPlanMsg struct {
	SpawnID string
	Entries []PlanEntry
}

// SubagentDoneMsg signals a subagent panel has completed.
type SubagentDoneMsg struct {
	SpawnID string
	State   string // "completed", "failed", "interrupted"
}
