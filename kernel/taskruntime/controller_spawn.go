package taskruntime

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

const subagentMissingStateGrace = 5 * time.Second

type SubagentTaskController struct {
	SessionID              string
	DelegationID           string
	CancelFunc             context.CancelFunc
	Runner                 agent.SubagentRunner
	ContinueRunner         agent.SubagentRunner
	Store                  task.Store
	Agent                  string
	ChildCWD               string
	IdleTimeout            time.Duration
	ContinuationAnchorTool string
}

func (c *SubagentTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
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

func (c *SubagentTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.Runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: subagent runner is unavailable")
	}
	if ctx == nil {
		return task.Snapshot{}, fmt.Errorf("task: context is required")
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
		state := cmp.Or(strings.TrimSpace(string(current.State)), "running")
		return task.Snapshot{}, fmt.Errorf("task: TASK write can continue a spawn subagent only after it reaches completed; current state is %s, use TASK wait while it is still running", state)
	}
	callInfo, _ := toolexec.ToolCallInfoFromContext(ctx)
	runner := c.Runner
	if c.ContinueRunner != nil {
		runner = c.ContinueRunner
	}
	runResult, err := runner.RunSubagent(ctx, agent.SubagentRunRequest{
		Agent:       c.Agent,
		Prompt:      input,
		SessionID:   c.SessionID,
		ChildCWD:    c.ChildCWD,
		Yield:       yield,
		IdleTimeout: c.IdleTimeout,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	if sessionID := strings.TrimSpace(runResult.SessionID); sessionID != "" {
		c.SessionID = sessionID
	}
	if delegationID := strings.TrimSpace(runResult.DelegationID); delegationID != "" {
		c.DelegationID = delegationID
	}
	if agentName := strings.TrimSpace(runResult.Agent); agentName != "" {
		c.Agent = agentName
	}
	if childCWD := strings.TrimSpace(runResult.ChildCWD); childCWD != "" {
		c.ChildCWD = childCWD
	}
	if runResult.IdleTimeout > 0 {
		c.IdleTimeout = runResult.IdleTimeout
	}
	c.CancelFunc = cancelSubagentFunc(c.Runner, c.SessionID)
	now := time.Now()
	record.WithLock(func(one *task.Record) {
		if one.Spec == nil {
			one.Spec = map[string]any{}
		}
		one.Spec[SpecPrompt] = input
		one.Spec[SpecChildSession] = c.SessionID
		one.Spec[SpecDelegationID] = c.DelegationID
		one.Spec[SpecAgent] = c.Agent
		one.Spec[SpecChildCWD] = c.ChildCWD
		if callID := strings.TrimSpace(callInfo.ID); callID != "" {
			one.Spec[SpecParentToolCall] = callID
			one.Spec[SpecUISpawnID] = callID
			one.Spec[SpecUIAnchorTool] = cmp.Or(strings.TrimSpace(c.ContinuationAnchorTool), "TASK WRITE")
		}
		if toolName := strings.TrimSpace(callInfo.Name); toolName != "" {
			one.Spec[SpecParentToolName] = toolName
		}
		if c.IdleTimeout > 0 {
			one.Spec[SpecIdleTimeout] = int(c.IdleTimeout / time.Second)
		}
		progressAt := LatestSubagentProgressTime(runResult.UpdatedAt, now)
		if progressAt.After(one.HeartbeatAt) {
			one.HeartbeatAt = progressAt
		}
	})
	return c.inspect(ctx, record, true)
}

func (c *SubagentTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c.CancelFunc != nil {
		c.CancelFunc()
	}
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id": c.SessionID,
			"delegation_id":    c.DelegationID,
			"agent":            c.Agent,
			"child_cwd":        c.ChildCWD,
			"progress_state":   string(task.StateCancelled),
		}
		if callID := strings.TrimSpace(StringValue(one.Spec, SpecParentToolCall)); callID != "" {
			one.Result["_ui_parent_tool_call_id"] = callID
		}
		if toolName := strings.TrimSpace(StringValue(one.Spec, SpecParentToolName)); toolName != "" {
			one.Result["_ui_parent_tool_name"] = toolName
		}
		if spawnID := strings.TrimSpace(StringValue(one.Spec, SpecUISpawnID)); spawnID != "" {
			one.Result["_ui_spawn_id"] = spawnID
		}
		if anchorTool := strings.TrimSpace(StringValue(one.Spec, SpecUIAnchorTool)); anchorTool != "" {
			one.Result["_ui_anchor_tool"] = anchorTool
		}
		if c.IdleTimeout > 0 {
			one.Result["_ui_idle_timeout_seconds"] = int(c.IdleTimeout / time.Second)
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.Store, record)
	return snapshot, nil
}

func (c *SubagentTaskController) inspect(ctx context.Context, record *task.Record, advance bool) (task.Snapshot, error) {
	if c == nil || c.Runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: subagent controller is unavailable")
	}
	now := time.Now()
	runResult, err := c.Runner.InspectSubagent(ctx, c.SessionID)
	if err != nil {
		if !isMissingSubagentStateErr(err) {
			return task.Snapshot{}, err
		}
		lastSeen := subagentLastSeenAt(record)
		if lastSeen.IsZero() || now.Sub(lastSeen) <= subagentMissingStateGrace {
			runResult = agent.SubagentRunResult{
				SessionID:    c.SessionID,
				DelegationID: c.DelegationID,
				Agent:        c.Agent,
				ChildCWD:     c.ChildCWD,
				State:        string(task.StateRunning),
				Running:      true,
				UpdatedAt:    lastSeen,
			}
		} else {
			runResult = agent.SubagentRunResult{
				SessionID:    c.SessionID,
				DelegationID: c.DelegationID,
				Agent:        c.Agent,
				ChildCWD:     c.ChildCWD,
				State:        string(task.StateInterrupted),
				Running:      false,
				UpdatedAt:    now,
			}
		}
	}
	var snapshot task.Snapshot
	var output task.Output
	var assistant string
	previewSource := cmp.Or(strings.TrimSpace(runResult.LatestOutput), runResult.LogSnapshot)
	preview := task.FormatLatestOutput(previewSource)
	record.WithLock(func(one *task.Record) {
		assistant, _ = one.Result["final_result"].(string)
	})
	if final := strings.TrimSpace(runResult.Assistant); final != "" {
		assistant = final
	}
	errorReason := SubagentErrorReason(runResult)
	record.WithLock(func(one *task.Record) {
		if preview == "" {
			preview = task.FormatLatestOutput(fmt.Sprint(one.Result["latest_output"]))
		}
		progressAt := LatestSubagentProgressTime(runResult.UpdatedAt, one.HeartbeatAt, one.CreatedAt)
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
		one.State = RuntimeTaskStateName(runResult.State)
		one.Running = runResult.Running
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id":     c.SessionID,
			"delegation_id":        c.DelegationID,
			"agent":                c.Agent,
			"child_cwd":            c.ChildCWD,
			"progress_state":       string(one.State),
			"progress_seq":         progressSeq,
			"progress_age_seconds": ProgressAgeSeconds(progressAt, now),
		}
		if text := strings.TrimSpace(runResult.Error); text != "" {
			one.Result["error"] = text
		}
		if errorReason != "" {
			one.Result["error_reason"] = errorReason
			one.Result["_ui_error_reason"] = errorReason
		}
		if callID := strings.TrimSpace(StringValue(one.Spec, SpecParentToolCall)); callID != "" {
			one.Result["_ui_parent_tool_call_id"] = callID
		}
		if toolName := strings.TrimSpace(StringValue(one.Spec, SpecParentToolName)); toolName != "" {
			one.Result["_ui_parent_tool_name"] = toolName
		}
		if spawnID := strings.TrimSpace(StringValue(one.Spec, SpecUISpawnID)); spawnID != "" {
			one.Result["_ui_spawn_id"] = spawnID
		}
		if anchorTool := strings.TrimSpace(StringValue(one.Spec, SpecUIAnchorTool)); anchorTool != "" {
			one.Result["_ui_anchor_tool"] = anchorTool
		}
		if c.IdleTimeout > 0 {
			one.Result["_ui_idle_timeout_seconds"] = int(c.IdleTimeout / time.Second)
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
	_ = persistControllerRecord(ctx, c.Store, record)
	return snapshot, nil
}

func SubagentErrorReason(runResult agent.SubagentRunResult) string {
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

func ProgressAgeSeconds(last time.Time, now time.Time) int {
	if last.IsZero() || now.Before(last) {
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
		out = LatestSubagentProgressTime(one.HeartbeatAt, one.CreatedAt)
	})
	return out
}

func LatestSubagentProgressTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func RuntimeTaskStateName(status string) task.State {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(task.StateCompleted):
		return task.StateCompleted
	case string(task.StateFailed):
		return task.StateFailed
	case string(task.StateInterrupted):
		return task.StateInterrupted
	case string(task.StateWaitingApproval):
		return task.StateWaitingApproval
	case string(task.StateCancelled):
		return task.StateCancelled
	case string(task.StateWaitingInput):
		return task.StateWaitingInput
	default:
		return task.StateRunning
	}
}
