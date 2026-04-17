package agent

import (
	"context"
	"iter"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// Agent is the runtime execution unit.
type Agent interface {
	Name() string
	Run(InvocationContext) iter.Seq2[*session.Event, error]
}

// ReadonlyContext exposes immutable invocation state derived from persisted events.
type ReadonlyContext interface {
	context.Context
	Session() *session.Session
	Events() session.Events
	ReadonlyState() session.ReadonlyState
	Overlay() bool
}

// ModelContext exposes model planning capabilities.
type ModelContext interface {
	ReadonlyContext
	Model() model.LLM
	Tools() []tool.Tool
}

// ToolContext exposes tool execution capabilities.
type ToolContext interface {
	ReadonlyContext
	Tool(string) (tool.Tool, bool)
}

// PolicyContext exposes policy hooks used by runtime stages.
type PolicyContext interface {
	ReadonlyContext
	Policies() []policy.Hook
}

// DelegationContext exposes child-run orchestration capabilities.
type DelegationContext interface {
	ReadonlyContext
	SubagentRunner() SubagentRunner
}

// InvocationContext composes all kernel contexts used by one agent run.
type InvocationContext interface {
	ModelContext
	ToolContext
	PolicyContext
	DelegationContext
}

// SubagentRunRequest describes one delegated child run.
type SubagentRunRequest struct {
	Agent       string
	Prompt      string
	SessionID   string
	ChildCWD    string
	Parts       []model.Part
	Yield       time.Duration
	Timeout     time.Duration
	IdleTimeout time.Duration
}

// SubagentRunResult captures the final delegated child run summary.
type SubagentRunResult struct {
	SessionID       string
	DelegationID    string
	Agent           string
	Session         string
	ChildCWD        string
	Assistant       string
	Error           string
	State           string
	Running         bool
	ApprovalPending bool
	ToolCallPending bool
	LogSnapshot     string
	LatestOutput    string
	ProgressSeq     int
	UpdatedAt       time.Time
	Yielded         bool
	Timeout         time.Duration
	IdleTimeout     time.Duration
}

// SubagentRunner starts delegated child runs from the current invocation.
type SubagentRunner interface {
	RunSubagent(context.Context, SubagentRunRequest) (SubagentRunResult, error)
	InspectSubagent(context.Context, string) (SubagentRunResult, error)
}
