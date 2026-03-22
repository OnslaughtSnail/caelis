package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type ReconcileSessionRequest struct {
	AppName     string
	UserID      string
	SessionID   string
	ExecRuntime toolexec.Runtime
}

func (r *Runtime) ReconcileSession(ctx context.Context, req ReconcileSessionRequest) ([]*task.Entry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.taskStore == nil {
		return nil, nil
	}
	ref := task.SessionRef{
		AppName:   strings.TrimSpace(req.AppName),
		UserID:    strings.TrimSpace(req.UserID),
		SessionID: strings.TrimSpace(req.SessionID),
	}
	if ref.AppName == "" || ref.UserID == "" || ref.SessionID == "" {
		return nil, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	entries, err := r.taskStore.ListSession(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := make([]*task.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		next, err := r.reconcileTaskEntry(ctx, entry, req.ExecRuntime)
		if err != nil {
			return nil, err
		}
		out = append(out, next)
	}
	return out, nil
}

func (r *Runtime) reconcileTaskEntry(ctx context.Context, entry *task.Entry, execRuntime toolexec.Runtime) (*task.Entry, error) {
	if entry == nil {
		return nil, nil
	}
	if !entry.Running {
		return entry, nil
	}
	if live, err := r.resolveTaskRegistry(nil).Get(entry.TaskID); err == nil && live != nil {
		running := false
		live.WithLock(func(one *task.Record) {
			running = one.Running
		})
		if running {
			return entry, nil
		}
	}
	switch entry.Kind {
	case task.KindDelegate:
		return r.reconcileDelegateTask(ctx, entry)
	case task.KindBash:
		return r.reconcileBashTask(ctx, entry, execRuntime)
	default:
		return r.markTaskInterrupted(ctx, entry, "task kind is not recoverable")
	}
}

func (r *Runtime) reconcileBashTask(ctx context.Context, entry *task.Entry, execRuntime toolexec.Runtime) (*task.Entry, error) {
	sessionID := strings.TrimSpace(stringValue(entry.Spec, taskSpecExecSessionID))
	if sessionID == "" {
		sessionID = strings.TrimSpace(stringValue(entry.Result, "session_id"))
	}
	if sessionID == "" {
		return r.markTaskInterrupted(ctx, entry, "async bash session reference is missing")
	}
	if execRuntime == nil {
		return r.markTaskInterrupted(ctx, entry, "async bash runtime is unavailable for recovery")
	}
	runner, ok := asyncBashRunnerForRoute(execRuntime, stringValue(entry.Spec, taskSpecRoute))
	if !ok || runner == nil {
		return r.markTaskInterrupted(ctx, entry, "async bash runner is unavailable for recovery")
	}
	status, err := runner.GetSessionStatus(sessionID)
	if err != nil {
		if errors.Is(err, toolexec.ErrSessionNotFound) {
			return r.markTaskInterrupted(ctx, entry, "async bash session no longer exists")
		}
		return nil, err
	}
	entry.State = bashTaskState(status.State)
	entry.Running = status.State == toolexec.SessionStateRunning
	entry.UpdatedAt = time.Now()
	entry.HeartbeatAt = time.Now()
	if entry.Result == nil {
		entry.Result = map[string]any{}
	}
	entry.Result["command"] = stringValue(entry.Spec, taskSpecCommand)
	entry.Result["workdir"] = stringValue(entry.Spec, taskSpecWorkdir)
	entry.Result["tty"] = boolValue(entry.Spec, taskSpecTTY)
	entry.Result["route"] = stringValue(entry.Spec, taskSpecRoute)
	entry.Result["state"] = string(entry.State)
	entry.Result["exit_code"] = status.ExitCode
	entry.Result["session_id"] = sessionID
	entry.Result["output_meta"] = bashTaskOutputMeta(status, boolValue(entry.Spec, taskSpecTTY))
	if preview := recoveredBashPreview(runner, sessionID); preview != "" {
		entry.Result["latest_output"] = preview
	} else {
		delete(entry.Result, "latest_output")
	}
	delete(entry.Result, "interrupted")
	delete(entry.Result, "error")
	if err := r.taskStore.Upsert(ctx, task.CloneEntry(entry)); err != nil {
		return nil, err
	}
	return entry, nil
}

func (r *Runtime) reconcileDelegateTask(ctx context.Context, entry *task.Entry) (*task.Entry, error) {
	childSessionID := strings.TrimSpace(stringValue(entry.Result, "child_session_id"))
	if childSessionID == "" {
		childSessionID = strings.TrimSpace(stringValue(entry.Result, "_ui_child_session_id"))
	}
	if childSessionID == "" {
		childSessionID = strings.TrimSpace(stringValue(entry.Spec, "child_session_id"))
	}
	if childSessionID == "" {
		return r.markTaskInterrupted(ctx, entry, "child session reference is missing")
	}
	if r.hasActiveRun(entry.Session.AppName, entry.Session.UserID, childSessionID) {
		return entry, nil
	}
	state, err := r.RunState(ctx, RunStateRequest{
		AppName:   entry.Session.AppName,
		UserID:    entry.Session.UserID,
		SessionID: childSessionID,
	})
	if err != nil {
		return nil, err
	}
	if !state.HasLifecycle || state.Status == RunLifecycleStatusRunning || state.Status == RunLifecycleStatusWaitingApproval {
		sess, getErr := r.store.GetOrCreate(ctx, &session.Session{
			AppName: entry.Session.AppName,
			UserID:  entry.Session.UserID,
			ID:      childSessionID,
		})
		if getErr != nil {
			return nil, getErr
		}
		cause := fmt.Errorf("subagent execution was interrupted; relaunch is required")
		_ = r.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusInterrupted, "delegate_recovery", cause, func(*session.Event, error) bool {
			return true
		})
		state = RunState{
			HasLifecycle: true,
			Status:       RunLifecycleStatusInterrupted,
			Phase:        "delegate_recovery",
			Error:        cause.Error(),
			UpdatedAt:    time.Now(),
		}
	}
	assistant, err := latestAssistantText(ctx, r, entry.Session.AppName, entry.Session.UserID, childSessionID)
	if err != nil {
		return nil, err
	}
	entry.State = runtimeTaskState(state.Status)
	entry.Running = entry.State == task.StateRunning || entry.State == task.StateWaitingApproval
	entry.UpdatedAt = time.Now()
	if entry.Result == nil {
		entry.Result = map[string]any{}
	}
	entry.Result["_ui_child_session_id"] = childSessionID
	if delegationID := strings.TrimSpace(stringValue(entry.Spec, "delegation_id")); delegationID != "" {
		entry.Result["_ui_delegation_id"] = delegationID
	}
	entry.Result["progress_state"] = string(entry.State)
	if entry.State == task.StateWaitingApproval {
		entry.Result["approval_pending"] = true
		entry.Result["_ui_approval_pending"] = true
	}
	if !entry.Running && assistant != "" {
		entry.Result["final_result"] = assistant
		entry.Result["final_summary"] = assistant
	}
	entry.HeartbeatAt = time.Now()
	if err := r.taskStore.Upsert(ctx, task.CloneEntry(entry)); err != nil {
		return nil, err
	}
	return entry, nil
}

func (r *Runtime) markTaskInterrupted(ctx context.Context, entry *task.Entry, reason string) (*task.Entry, error) {
	if entry == nil {
		return nil, nil
	}
	entry.State = task.StateInterrupted
	entry.Running = false
	entry.UpdatedAt = time.Now()
	entry.HeartbeatAt = time.Now()
	if entry.Result == nil {
		entry.Result = map[string]any{}
	}
	if strings.TrimSpace(reason) != "" {
		entry.Result["error"] = reason
	}
	entry.Result["interrupted"] = true
	entry.Result["state"] = string(entry.State)
	if err := r.taskStore.Upsert(ctx, task.CloneEntry(entry)); err != nil {
		return nil, err
	}
	return entry, nil
}

func latestAssistantText(ctx context.Context, r *Runtime, appName, userID, sessionID string) (string, error) {
	events, err := r.SessionEvents(ctx, SessionEventsRequest{
		AppName:          appName,
		UserID:           userID,
		SessionID:        sessionID,
		IncludeLifecycle: false,
	})
	if err != nil {
		return "", err
	}
	return FinalAssistantText(events), nil
}

func stringValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func boolValue(values map[string]any, key string) bool {
	if len(values) == 0 {
		return false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return false
	}
	value, ok := raw.(bool)
	return ok && value
}

func recoveredBashPreview(runner toolexec.AsyncCommandRunner, sessionID string) string {
	if runner == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	stdout, stderr, _, _, err := runner.ReadOutput(sessionID, 0, 0)
	if err != nil {
		return ""
	}
	return bashOutputPreview(stdout, stderr)
}
