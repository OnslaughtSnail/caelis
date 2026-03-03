package runtime

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type invocationContext struct {
	context.Context
	session  *session.Session
	history  []*session.Event
	model    model.LLM
	tools    []tool.Tool
	toolMap  map[string]tool.Tool
	policies []policy.Hook
}

func (c *invocationContext) Session() *session.Session {
	return c.session
}

func (c *invocationContext) History() []*session.Event {
	out := make([]*session.Event, 0, len(c.history))
	for _, ev := range c.history {
		if ev == nil {
			continue
		}
		cp := *ev
		out = append(out, &cp)
	}
	return out
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
