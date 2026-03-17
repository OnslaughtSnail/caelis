package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type delegateTaskController struct {
	runtime      *Runtime
	appName      string
	userID       string
	sessionID    string
	delegationID string
	cancel       context.CancelFunc
	store        task.Store
}

func (c *delegateTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
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

func (c *delegateTaskController) Status(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	return c.inspect(ctx, record, false)
}

func (c *delegateTaskController) Write(context.Context, *task.Record, string, time.Duration) (task.Snapshot, error) {
	return task.Snapshot{}, fmt.Errorf("task: delegate tasks do not accept input")
}

func (c *delegateTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c.cancel != nil {
		c.cancel()
	}
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id": c.sessionID,
			"delegation_id":    c.delegationID,
			"state":            string(task.StateCancelled),
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *delegateTaskController) inspect(ctx context.Context, record *task.Record, advance bool) (task.Snapshot, error) {
	if c == nil || c.runtime == nil {
		return task.Snapshot{}, fmt.Errorf("task: delegate controller is unavailable")
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
		if len(one.Result) > 0 {
			if text, ok := one.Result["assistant"].(string); ok {
				assistant = text
			}
		}
	})
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			assistant = text
		}
	}
	record.WithLock(func(one *task.Record) {
		start := one.EventCursor
		if start < 0 || start > len(events) {
			start = len(events)
		}
		if advance {
			for _, ev := range events[start:] {
				output.Log += delegateEventLogLine(ev)
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
			"child_session_id": c.sessionID,
			"delegation_id":    c.delegationID,
			"assistant":        assistant,
			"summary":          assistant,
			"state":            string(one.State),
		}
		if preview := delegatePreviewFromEvents(events); preview != "" {
			one.Result["latest_output"] = preview
		}
		snapshot = one.LockedSnapshot(output)
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func delegatePreviewFromEvents(events []*session.Event) string {
	lines := make([]string, 0, 8)
	inFence := false
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if reasoning := strings.TrimSpace(ev.Message.Reasoning); reasoning != "" {
			for _, line := range strings.Split(reasoning, "\n") {
				line = delegatePreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, "· "+line)
			}
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			for _, line := range strings.Split(text, "\n") {
				line = delegatePreviewLine(line, &inFence)
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

func delegatePreviewLine(line string, inFence *bool) string {
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

func delegateEventLogLine(ev *session.Event) string {
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
