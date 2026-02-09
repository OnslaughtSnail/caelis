package agent

import (
	"context"
	"iter"

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
	History() []*session.Event
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

// InvocationContext composes all kernel contexts used by one agent run.
type InvocationContext interface {
	ModelContext
	ToolContext
	PolicyContext
}
