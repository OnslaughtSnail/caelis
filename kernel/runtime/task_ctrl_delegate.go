package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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
	runner       agent.SubagentRunner
	store        task.Store
	agent        string
	childCWD     string
	idleTimeout  time.Duration
}

const subagentMissingStateGrace = 5 * time.Second

func (c *subagentTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	deadline := time.Time{}
	if yield > 0 {
		deadline = time.Now().Add(yield)
	}
	var aggregated task.Output
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
		if snapshot.Output.Log != "" {
			aggregated.Log += snapshot.Output.Log
			snapshot.Output.Log = aggregated.Log
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

func (c *subagentTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: subagent runner is unavailable")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return task.Snapshot{}, fmt.Errorf("task: input is required")
	}
	current, err := c.inspect(ctx, record, false)
	if err != nil {
		return task.Snapshot{}, err
	}
	if current.Running || current.State != task.StateCompleted {
		state := strings.TrimSpace(string(current.State))
		if state == "" {
			state = "running"
		}
		return task.Snapshot{}, fmt.Errorf("task: TASK write can continue a spawn subagent only after it reaches completed; current state is %s, use TASK wait while it is still running", state)
	}
	callInfo, _ := toolexec.ToolCallInfoFromContext(ctx)
	runResult, err := c.runner.RunSubagent(withSubagentContinuation(ctx), agent.SubagentRunRequest{
		Agent:       c.agent,
		Prompt:      input,
		SessionID:   c.sessionID,
		ChildCWD:    c.childCWD,
		Yield:       yield,
		IdleTimeout: c.idleTimeout,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	if sessionID := strings.TrimSpace(runResult.SessionID); sessionID != "" {
		c.sessionID = sessionID
	}
	if delegationID := strings.TrimSpace(runResult.DelegationID); delegationID != "" {
		c.delegationID = delegationID
	}
	if agentName := strings.TrimSpace(runResult.Agent); agentName != "" {
		c.agent = agentName
	}
	if childCWD := strings.TrimSpace(runResult.ChildCWD); childCWD != "" {
		c.childCWD = childCWD
	}
	if runResult.IdleTimeout > 0 {
		c.idleTimeout = runResult.IdleTimeout
	}
	c.cancel = cancelSubagentFunc(c.runner, c.sessionID)
	now := time.Now()
	record.WithLock(func(one *task.Record) {
		if one.Spec == nil {
			one.Spec = map[string]any{}
		}
		one.Spec[taskSpecPrompt] = input
		one.Spec[taskSpecChildSession] = c.sessionID
		one.Spec[taskSpecDelegationID] = c.delegationID
		one.Spec[taskSpecAgent] = c.agent
		one.Spec[taskSpecChildCWD] = c.childCWD
		if callID := strings.TrimSpace(callInfo.ID); callID != "" {
			one.Spec[taskSpecParentToolCall] = callID
			one.Spec[taskSpecUISpawnID] = callID
			one.Spec[taskSpecUIAnchorTool] = SubagentContinuationAnchorTool
		}
		if toolName := strings.TrimSpace(callInfo.Name); toolName != "" {
			one.Spec[taskSpecParentToolName] = toolName
		}
		if c.idleTimeout > 0 {
			one.Spec[taskSpecIdleTimeout] = int(c.idleTimeout / time.Second)
		}
		progressAt := latestSubagentProgressTime(runResult.UpdatedAt, now)
		if progressAt.After(one.HeartbeatAt) {
			one.HeartbeatAt = progressAt
		}
	})
	return c.inspect(ctx, record, true)
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
			"child_session_id": c.sessionID,
			"delegation_id":    c.delegationID,
			"agent":            c.agent,
			"child_cwd":        c.childCWD,
			"progress_state":   string(task.StateCancelled),
		}
		if callID := strings.TrimSpace(stringValue(one.Spec, taskSpecParentToolCall)); callID != "" {
			one.Result["_ui_parent_tool_call_id"] = callID
		}
		if toolName := strings.TrimSpace(stringValue(one.Spec, taskSpecParentToolName)); toolName != "" {
			one.Result["_ui_parent_tool_name"] = toolName
		}
		if spawnID := strings.TrimSpace(stringValue(one.Spec, taskSpecUISpawnID)); spawnID != "" {
			one.Result["_ui_spawn_id"] = spawnID
		}
		if anchorTool := strings.TrimSpace(stringValue(one.Spec, taskSpecUIAnchorTool)); anchorTool != "" {
			one.Result["_ui_anchor_tool"] = anchorTool
		}
		if c.idleTimeout > 0 {
			one.Result["_ui_idle_timeout_seconds"] = int(c.idleTimeout / time.Second)
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *subagentTaskController) inspect(ctx context.Context, record *task.Record, advance bool) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: subagent controller is unavailable")
	}
	now := time.Now()
	runResult, err := c.runner.InspectSubagent(ctx, c.sessionID)
	if err != nil {
		if !isMissingSubagentStateErr(err) {
			return task.Snapshot{}, err
		}
		lastSeen := subagentLastSeenAt(record)
		if lastSeen.IsZero() || now.Sub(lastSeen) <= subagentMissingStateGrace {
			runResult = agent.SubagentRunResult{
				SessionID:    c.sessionID,
				DelegationID: c.delegationID,
				Agent:        c.agent,
				ChildCWD:     c.childCWD,
				State:        string(task.StateRunning),
				Running:      true,
				UpdatedAt:    lastSeen,
			}
		} else {
			runResult = agent.SubagentRunResult{
				SessionID:    c.sessionID,
				DelegationID: c.delegationID,
				Agent:        c.agent,
				ChildCWD:     c.childCWD,
				State:        string(task.StateInterrupted),
				Running:      false,
				UpdatedAt:    now,
			}
		}
	}
	var snapshot task.Snapshot
	var output task.Output
	var assistant string
	previewSource := strings.TrimSpace(runResult.LatestOutput)
	if previewSource == "" {
		previewSource = runResult.LogSnapshot
	}
	preview := task.FormatLatestOutput(previewSource)
	record.WithLock(func(one *task.Record) {
		assistant, _ = one.Result["final_result"].(string)
	})
	if final := strings.TrimSpace(runResult.Assistant); final != "" {
		assistant = final
	}
	errorReason := subagentErrorReason(runResult)
	record.WithLock(func(one *task.Record) {
		if preview == "" {
			preview = task.FormatLatestOutput(fmt.Sprint(one.Result["latest_output"]))
		}
		progressAt := latestSubagentProgressTime(runResult.UpdatedAt, one.HeartbeatAt, one.CreatedAt)
		if progressAt.After(one.HeartbeatAt) {
			one.HeartbeatAt = progressAt
		}
		logSnapshot := runResult.LogSnapshot
		progressSeq := runResult.ProgressSeq
		if progressSeq <= 0 {
			progressSeq = len(logSnapshot)
		}
		start := one.EventCursor
		if start < 0 || start > len(logSnapshot) {
			start = len(logSnapshot)
		}
		if advance {
			output.Log = logSnapshot[start:]
			one.EventCursor = len(logSnapshot)
		}
		one.State = runtimeTaskStateName(runResult.State)
		one.Running = runResult.Running
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id":     c.sessionID,
			"delegation_id":        c.delegationID,
			"agent":                c.agent,
			"child_cwd":            c.childCWD,
			"progress_state":       string(one.State),
			"progress_seq":         progressSeq,
			"progress_age_seconds": progressAgeSeconds(progressAt, now),
		}
		if text := strings.TrimSpace(runResult.Error); text != "" {
			one.Result["error"] = text
		}
		if errorReason != "" {
			one.Result["error_reason"] = errorReason
			one.Result["_ui_error_reason"] = errorReason
		}
		if callID := strings.TrimSpace(stringValue(one.Spec, taskSpecParentToolCall)); callID != "" {
			one.Result["_ui_parent_tool_call_id"] = callID
		}
		if toolName := strings.TrimSpace(stringValue(one.Spec, taskSpecParentToolName)); toolName != "" {
			one.Result["_ui_parent_tool_name"] = toolName
		}
		if spawnID := strings.TrimSpace(stringValue(one.Spec, taskSpecUISpawnID)); spawnID != "" {
			one.Result["_ui_spawn_id"] = spawnID
		}
		if anchorTool := strings.TrimSpace(stringValue(one.Spec, taskSpecUIAnchorTool)); anchorTool != "" {
			one.Result["_ui_anchor_tool"] = anchorTool
		}
		if c.idleTimeout > 0 {
			one.Result["_ui_idle_timeout_seconds"] = int(c.idleTimeout / time.Second)
		}
		if preview != "" {
			one.Result["latest_output"] = preview
		}
		if runResult.ApprovalPending || one.State == task.StateWaitingApproval {
			one.Result["approval_pending"] = true
			one.Result["_ui_approval_pending"] = true
		}
		if one.State == task.StateInterrupted {
			one.Result["interrupted"] = true
			delete(one.Result, "error")
		}
		if errorReason == "runner_idle_timeout" {
			one.Result["idle_timed_out"] = true
			one.Result["_ui_idle_timed_out"] = true
		}
		if !one.Running && assistant != "" {
			one.Result["final_result"] = assistant
			one.Result["final_summary"] = assistant
		}
		if errorReason != "runner_idle_timeout" {
			delete(one.Result, "idle_timed_out")
			delete(one.Result, "_ui_idle_timed_out")
		}
		if errorReason == "" {
			delete(one.Result, "error_reason")
			delete(one.Result, "_ui_error_reason")
		}
		snapshot = one.LockedSnapshot(output)
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func subagentErrorReason(runResult agent.SubagentRunResult) string {
	errText := strings.ToLower(strings.TrimSpace(runResult.Error))
	switch {
	case errText == "":
		return ""
	case strings.Contains(errText, "idle timeout exceeded"):
		return "runner_idle_timeout"
	case strings.Contains(errText, "context deadline exceeded"), strings.Contains(errText, "deadline exceeded"), strings.Contains(errText, "timed out"), strings.Contains(errText, "timeout"):
		return "child_timeout"
	case strings.Contains(errText, "context canceled"), strings.Contains(errText, "cancelled"), strings.Contains(errText, "canceled"):
		return "child_cancelled"
	default:
		return "child_failed"
	}
}

func progressAgeSeconds(last time.Time, now time.Time) int {
	if last.IsZero() {
		return 0
	}
	if now.Before(last) {
		return 0
	}
	return int(now.Sub(last).Round(time.Second) / time.Second)
}

func isMissingSubagentStateErr(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(errText, "not found") || strings.Contains(errText, "not tracked")
}

func subagentLastSeenAt(record *task.Record) time.Time {
	if record == nil {
		return time.Time{}
	}
	var out time.Time
	record.WithLock(func(one *task.Record) {
		// UpdatedAt tracks local TASK bookkeeping as well as real child progress.
		// Using it here lets repeated TASK wait polls artificially extend the
		// idle window for a stuck subagent.
		out = latestSubagentProgressTime(one.HeartbeatAt, one.CreatedAt)
	})
	return out
}

func latestSubagentProgressTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
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

func runtimeTaskStateName(status string) task.State {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(RunLifecycleStatusCompleted):
		return task.StateCompleted
	case string(RunLifecycleStatusFailed):
		return task.StateFailed
	case string(RunLifecycleStatusInterrupted):
		return task.StateInterrupted
	case string(RunLifecycleStatusWaitingApproval):
		return task.StateWaitingApproval
	case string(task.StateCancelled):
		return task.StateCancelled
	case string(task.StateWaitingInput):
		return task.StateWaitingInput
	default:
		return task.StateRunning
	}
}
