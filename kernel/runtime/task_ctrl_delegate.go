package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type subagentTaskController struct {
	runtime      *Runtime
	appName      string
	userID       string
	sessionID    string
	delegationID string
	cancel       context.CancelFunc
	store        task.Store
	agent        string
	timeout      time.Duration
}

func (c *subagentTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	deadline := time.Time{}
	if yield > 0 {
		deadline = time.Now().Add(yield)
	}
	for {
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		default:
		}
		snapshot, err := c.inspect(ctx, record, true)
		if err != nil {
			return task.Snapshot{}, err
		}
		if !snapshot.Running {
			return snapshot, nil
		}
		if deadline.IsZero() || time.Now().After(deadline) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (c *subagentTaskController) Status(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	return c.inspect(ctx, record, false)
}

func (c *subagentTaskController) Write(context.Context, *task.Record, string, time.Duration) (task.Snapshot, error) {
	return task.Snapshot{}, fmt.Errorf("task: subagent tasks do not accept input")
}

func (c *subagentTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c.cancel != nil {
		c.cancel()
	}
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"_ui_child_session_id": c.sessionID,
			"_ui_delegation_id":    c.delegationID,
			"_ui_agent":            c.agent,
			"progress_state":       string(task.StateCancelled),
		}
		if c.timeout > 0 {
			one.Result["_ui_timeout_seconds"] = int(c.timeout / time.Second)
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *subagentTaskController) inspect(ctx context.Context, record *task.Record, advance bool) (task.Snapshot, error) {
	if c == nil || c.runtime == nil {
		return task.Snapshot{}, fmt.Errorf("task: subagent controller is unavailable")
	}
	state, err := c.runtime.RunState(ctx, RunStateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	events, err := c.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          c.appName,
		UserID:           c.userID,
		SessionID:        c.sessionID,
		IncludeLifecycle: false,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	var snapshot task.Snapshot
	var output task.Output
	var assistant string
	record.WithLock(func(one *task.Record) {
		assistant, _ = one.Result["final_result"].(string)
	})
	if final := FinalAssistantText(events); final != "" {
		assistant = final
	}
	record.WithLock(func(one *task.Record) {
		start := one.EventCursor
		if start < 0 || start > len(events) {
			start = len(events)
		}
		if advance {
			for _, ev := range events[start:] {
				output.Log += subagentEventLogLine(ev)
			}
			one.EventCursor = len(events)
		}
		if !state.HasLifecycle {
			one.State = task.StateRunning
			one.Running = true
		} else {
			one.State = runtimeTaskState(state.Status)
			one.Running = state.Status == RunLifecycleStatusRunning || state.Status == RunLifecycleStatusWaitingApproval
		}
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"_ui_child_session_id": c.sessionID,
			"_ui_delegation_id":    c.delegationID,
			"_ui_agent":            c.agent,
			"progress_state":       string(one.State),
		}
		if c.timeout > 0 {
			one.Result["_ui_timeout_seconds"] = int(c.timeout / time.Second)
		}
		if one.State == task.StateWaitingApproval {
			one.Result["approval_pending"] = true
			one.Result["_ui_approval_pending"] = true
		}
		if !one.Running && assistant != "" {
			one.Result["final_result"] = assistant
			one.Result["final_summary"] = assistant
		}
		snapshot = one.LockedSnapshot(output)
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func subagentPreviewFromEvents(events []*session.Event) string {
	lines := make([]string, 0, 8)
	inFence := false
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if reasoning := strings.TrimSpace(ev.Message.Reasoning); reasoning != "" {
			for _, line := range strings.Split(reasoning, "\n") {
				line = subagentPreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, "· "+line)
			}
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			for _, line := range strings.Split(text, "\n") {
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

func runtimeTaskState(status RunLifecycleStatus) task.State {
	switch status {
	case RunLifecycleStatusCompleted:
		return task.StateCompleted
	case RunLifecycleStatusFailed:
		return task.StateFailed
	case RunLifecycleStatusInterrupted:
		return task.StateInterrupted
	case RunLifecycleStatusWaitingApproval:
		return task.StateWaitingApproval
	default:
		return task.StateRunning
	}
}

func subagentEventLogLine(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	var b strings.Builder
	if reasoning := strings.TrimSpace(ev.Message.Reasoning); reasoning != "" {
		b.WriteString(reasoning)
		b.WriteByte('\n')
	}
	if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String()
}
