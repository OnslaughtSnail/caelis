package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskruntime"
)

type subagentTaskController struct {
	runtime      *Runtime
	appName      string
	userID       string
	sessionID    string
	delegationID string
	cancel       context.CancelFunc
	runner       agent.SubagentRunner
	store        task.Store
	agent        string
	childCWD     string
	idleTimeout  time.Duration
}

func (c *subagentTaskController) delegate() *taskruntime.SubagentTaskController {
	if c == nil {
		return nil
	}
	return &taskruntime.SubagentTaskController{
		SessionID:              c.sessionID,
		DelegationID:           c.delegationID,
		CancelFunc:             c.cancel,
		Runner:                 c.runner,
		ContinueRunner:         newSubagentContinuationRunner(c.runner),
		Store:                  c.store,
		Agent:                  c.agent,
		ChildCWD:               c.childCWD,
		IdleTimeout:            c.idleTimeout,
		ContinuationAnchorTool: SubagentContinuationAnchorTool,
	}
}

func (c *subagentTaskController) apply(delegate *taskruntime.SubagentTaskController) {
	if c == nil || delegate == nil {
		return
	}
	c.sessionID = delegate.SessionID
	c.delegationID = delegate.DelegationID
	c.cancel = delegate.CancelFunc
	c.runner = delegate.Runner
	c.store = delegate.Store
	c.agent = delegate.Agent
	c.childCWD = delegate.ChildCWD
	c.idleTimeout = delegate.IdleTimeout
}

func (c *subagentTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	delegate := c.delegate()
	snapshot, err := delegate.Wait(ctx, record, yield)
	c.apply(delegate)
	return snapshot, err
}

func (c *subagentTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	delegate := c.delegate()
	snapshot, err := delegate.Write(ctx, record, input, yield)
	c.apply(delegate)
	return snapshot, err
}

func (c *subagentTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	delegate := c.delegate()
	snapshot, err := delegate.Cancel(ctx, record)
	c.apply(delegate)
	return snapshot, err
}

func runtimeTaskStateName(status string) task.State {
	return taskruntime.RuntimeTaskStateName(status)
}

type subagentContinuationRunner struct {
	base agent.SubagentRunner
}

func newSubagentContinuationRunner(base agent.SubagentRunner) agent.SubagentRunner {
	if base == nil {
		return nil
	}
	return subagentContinuationRunner{base: base}
}

func (r subagentContinuationRunner) RunSubagent(ctx context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	return r.base.RunSubagent(withSubagentContinuation(ctx), req)
}

func (r subagentContinuationRunner) InspectSubagent(ctx context.Context, sessionID string) (agent.SubagentRunResult, error) {
	return r.base.InspectSubagent(ctx, sessionID)
}

func subagentPreviewFromEvents(events []*session.Event) string {
	lines := make([]string, 0, 8)
	inFence := false
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if reasoning := strings.TrimSpace(ev.Message.ReasoningText()); reasoning != "" {
			for line := range strings.SplitSeq(reasoning, "\n") {
				line = subagentPreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, "· "+line)
			}
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			for line := range strings.SplitSeq(text, "\n") {
				line = subagentPreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}

func subagentPreviewLine(line string, inFence *bool) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		if inFence != nil {
			*inFence = !*inFence
		}
		return ""
	}
	if inFence != nil && *inFence {
		return ""
	}
	return trimmed
}
