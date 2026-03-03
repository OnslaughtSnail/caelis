package tuievents

import "time"

type LogChunkMsg struct {
	Chunk string
}

type SetStatusMsg struct {
	Model   string
	Context string
}

type SetHintMsg struct {
	Hint string
}

type SetRunningMsg struct {
	Running bool
}

type TaskResultMsg struct {
	ExitNow bool
	Err     error
}

type PromptRequestMsg struct {
	Prompt   string
	Secret   bool
	Response chan PromptResponse
}

type PromptResponse struct {
	Line string
	Err  error
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

type AttachmentCountMsg struct {
	Count int
}

// ClearHistoryMsg clears viewport conversation history in TUI.
type ClearHistoryMsg struct{}

// AssistantStreamMsg carries assistant answer chunks for TUI block rendering.
// When Final is true, Text is the full finalized assistant answer.
type AssistantStreamMsg struct {
	Kind  string
	Text  string
	Final bool
}

// ReasoningStreamMsg carries assistant reasoning chunks for TUI block rendering.
// When Final is true, Text is the full finalized reasoning text.
type ReasoningStreamMsg struct {
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
