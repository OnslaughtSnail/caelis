package runtime

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type invocationContext struct {
	context.Context
	session  *session.Session
	events   session.Events
	state    session.ReadonlyState
	model    model.LLM
	tools    []tool.Tool
	toolMap  map[string]tool.Tool
	policies []policy.Hook
	runner   agent.SubagentRunner
	tasks    *runtimeTaskManager
	overlay  bool
}

func (c *invocationContext) Session() *session.Session {
	return c.session
}

func (c *invocationContext) Events() session.Events {
	return c.events
}

func (c *invocationContext) ReadonlyState() session.ReadonlyState {
	return c.state
}

func (c *invocationContext) Overlay() bool {
	return c != nil && c.overlay
}

func (c *invocationContext) Model() model.LLM {
	return c.model
}

func (c *invocationContext) Tools() []tool.Tool {
	out := make([]tool.Tool, 0, len(c.tools))
	out = append(out, c.tools...)
	return out
}

func (c *invocationContext) Tool(name string) (tool.Tool, bool) {
	t, ok := c.toolMap[name]
	return t, ok
}

func (c *invocationContext) Policies() []policy.Hook {
	out := make([]policy.Hook, 0, len(c.policies))
	out = append(out, c.policies...)
	return out
}

func (c *invocationContext) SubagentRunner() agent.SubagentRunner {
	return c.runner
}
