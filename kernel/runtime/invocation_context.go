package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
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
	lsp      *lspbroker.Broker
	active   map[string]struct{}
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

func (c *invocationContext) ActivateLSP(ctx context.Context, req lspbroker.ActivateRequest) (lspbroker.ActivateResult, error) {
	if c == nil {
		return lspbroker.ActivateResult{}, fmt.Errorf("runtime: invocation context is nil")
	}
	language := strings.ToLower(strings.TrimSpace(req.Language))
	if language == "" {
		return lspbroker.ActivateResult{}, fmt.Errorf("runtime: lsp language is required")
	}
	toolsetID := "lsp:" + language
	if c.active == nil {
		c.active = map[string]struct{}{}
	}
	if _, exists := c.active[toolsetID]; exists {
		return lspbroker.ActivateResult{
			Language:       language,
			ToolsetID:      toolsetID,
			Activated:      false,
			AddedTools:     nil,
			ActiveToolsets: c.ActivatedToolsets(),
		}, nil
	}
	if c.lsp == nil {
		return lspbroker.ActivateResult{}, fmt.Errorf("runtime: lsp broker is not configured")
	}

	resolved, err := c.lsp.Resolve(ctx, lspbroker.ActivateRequest{
		Language:     language,
		Capabilities: req.Capabilities,
		Workspace:    req.Workspace,
	})
	if err != nil {
		return lspbroker.ActivateResult{}, err
	}

	added := make([]string, 0, len(resolved.Tools))
	for _, one := range resolved.Tools {
		if one == nil || strings.TrimSpace(one.Name()) == "" {
			continue
		}
		if _, exists := c.toolMap[one.Name()]; exists {
			continue
		}
		c.tools = append(c.tools, one)
		c.toolMap[one.Name()] = one
		added = append(added, one.Name())
	}
	c.active[resolved.ID] = struct{}{}
	return lspbroker.ActivateResult{
		Language:       resolved.Language,
		ToolsetID:      resolved.ID,
		Activated:      true,
		AddedTools:     added,
		ActiveToolsets: c.ActivatedToolsets(),
	}, nil
}

func (c *invocationContext) ActivatedToolsets() []string {
	if c == nil || len(c.active) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.active))
	for id := range c.active {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (c *invocationContext) AvailableLSP() []string {
	if c == nil || c.lsp == nil {
		return nil
	}
	return c.lsp.AvailableLanguages()
}
