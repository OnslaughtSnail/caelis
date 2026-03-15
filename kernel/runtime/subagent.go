package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/delegation"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

const (
	metaParentSessionID = "parent_session_id"
	metaChildSessionID  = "child_session_id"
	metaParentToolCall  = "parent_tool_call_id"
	metaDelegationID    = "delegation_id"
)

type delegationLineage struct {
	ParentSessionID string
	ChildSessionID  string
	ParentToolCall  string
	DelegationID    string
	TaskID          string
}

type delegationLineageContextKey struct{}

type runtimeSubagentRunner struct {
	runtime *Runtime
	parent  *session.Session
	req     RunRequest
}

func newSubagentRunner(r *Runtime, parent *session.Session, req RunRequest) delegation.Runner {
	if r == nil || parent == nil {
		return nil
	}
	return &runtimeSubagentRunner{
		runtime: r,
		parent:  parent,
		req:     req,
	}
}

func (r *runtimeSubagentRunner) RunSubagent(ctx context.Context, req delegation.RunRequest) (delegation.RunResult, error) {
	childReq, lineage, err := r.prepareChildRun(ctx, req)
	if err != nil {
		return delegation.RunResult{}, err
	}
	runner, err := r.runtime.Run(attachSubagentContext(ctx, lineage), childReq)
	if err != nil {
		return delegation.RunResult{}, err
	}
	defer runner.Close()
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			return delegation.RunResult{}, runErr
		}
		_ = ev
	}
	result, err := r.inspectSubagent(ctx, childReq.SessionID)
	if err != nil {
		return delegation.RunResult{}, err
	}
	if strings.TrimSpace(result.DelegationID) == "" {
		result.DelegationID = lineage.DelegationID
	}
	return result, nil
}

func (r *runtimeSubagentRunner) StartSubagent(ctx context.Context, req delegation.RunRequest) (delegation.RunResult, error) {
	childReq, lineage, err := r.prepareChildRun(ctx, req)
	if err != nil {
		return delegation.RunResult{}, err
	}
	go r.runDetachedSubagent(detachSubagentContext(ctx, lineage), childReq, lineage)
	return delegation.RunResult{
		SessionID:    childReq.SessionID,
		DelegationID: lineage.DelegationID,
		State:        string(RunLifecycleStatusRunning),
		Running:      true,
	}, nil
}

func (r *runtimeSubagentRunner) StatusSubagent(ctx context.Context, req delegation.StatusRequest) (delegation.RunResult, error) {
	return r.inspectSubagent(ctx, req.SessionID)
}

func (r *runtimeSubagentRunner) WaitSubagent(ctx context.Context, req delegation.WaitRequest) (delegation.RunResult, error) {
	deadline := time.Time{}
	if req.Timeout > 0 {
		deadline = time.Now().Add(req.Timeout)
	}
	for {
		result, err := r.inspectSubagent(ctx, req.SessionID)
		if err != nil {
			return delegation.RunResult{}, err
		}
		if !result.Running {
			return result, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return delegation.RunResult{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func resolveChildSessionID(parentSessionID string, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	parentSessionID = strings.TrimSpace(parentSessionID)
	if requested == "" {
		return idutil.NewSessionID(), nil
	}
	if requested == parentSessionID {
		return "", fmt.Errorf("runtime: delegated child session_id must differ from parent session")
	}
	return requested, nil
}

func withDelegationLineage(ctx context.Context, lineage delegationLineage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, delegationLineageContextKey{}, lineage)
}

func detachSubagentContext(ctx context.Context, lineage delegationLineage) context.Context {
	base := withDelegationLineage(context.Background(), lineage)
	if approver, ok := toolexec.ApproverFromContext(ctx); ok {
		base = toolexec.WithApprover(base, approver)
	}
	if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
		base = policy.WithToolAuthorizer(base, authorizer)
	}
	if streamer, ok := sessionstream.StreamerFromContext(ctx); ok {
		base = sessionstream.WithStreamer(base, streamer)
	}
	return base
}

func attachSubagentContext(ctx context.Context, lineage delegationLineage) context.Context {
	base := withDelegationLineage(context.Background(), lineage)
	if approver, ok := toolexec.ApproverFromContext(ctx); ok {
		base = toolexec.WithApprover(base, approver)
	}
	if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
		base = policy.WithToolAuthorizer(base, authorizer)
	}
	if streamer, ok := sessionstream.StreamerFromContext(ctx); ok {
		base = sessionstream.WithStreamer(base, streamer)
	}
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		base, cancel = context.WithDeadline(base, deadline)
		context.AfterFunc(ctx, cancel)
		return base
	}
	base, cancel := context.WithCancel(base)
	context.AfterFunc(ctx, cancel)
	return base
}

func delegationLineageFromContext(ctx context.Context) (delegationLineage, bool) {
	if ctx == nil {
		return delegationLineage{}, false
	}
	lineage, ok := ctx.Value(delegationLineageContextKey{}).(delegationLineage)
	return lineage, ok
}

func annotateDelegationMeta(ctx context.Context, ev *session.Event, sessionID string) {
	if ev == nil {
		return
	}
	lineage, ok := delegationLineageFromContext(ctx)
	if !ok {
		return
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if value := strings.TrimSpace(lineage.ParentSessionID); value != "" {
		ev.Meta[metaParentSessionID] = value
	}
	childSessionID := strings.TrimSpace(lineage.ChildSessionID)
	if childSessionID == "" {
		childSessionID = strings.TrimSpace(sessionID)
	}
	if childSessionID != "" {
		ev.Meta[metaChildSessionID] = childSessionID
	}
	if value := strings.TrimSpace(lineage.ParentToolCall); value != "" {
		ev.Meta[metaParentToolCall] = value
	}
	if value := strings.TrimSpace(lineage.DelegationID); value != "" {
		ev.Meta[metaDelegationID] = value
	}
}

func prepareEvent(ctx context.Context, sess *session.Session, ev *session.Event) *session.Event {
	if ev == nil {
		return nil
	}
	if ev.ID == "" {
		ev.ID = eventID()
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if sess != nil {
		ev.SessionID = sess.ID
	}
	annotateDelegationMeta(ctx, ev, ev.SessionID)
	return ev
}

func (r *runtimeSubagentRunner) prepareChildRun(ctx context.Context, req delegation.RunRequest) (RunRequest, delegationLineage, error) {
	if r == nil || r.runtime == nil || r.parent == nil {
		return RunRequest{}, delegationLineage{}, fmt.Errorf("runtime: subagent runner is unavailable")
	}
	childSessionID, err := resolveChildSessionID(r.parent.ID, req.SessionID)
	if err != nil {
		return RunRequest{}, delegationLineage{}, err
	}
	delegationID := idutil.NewDelegationID()
	callInfo, _ := toolexec.ToolCallInfoFromContext(ctx)
	lineage := delegationLineage{
		ParentSessionID: r.parent.ID,
		ChildSessionID:  childSessionID,
		ParentToolCall:  strings.TrimSpace(callInfo.ID),
		DelegationID:    delegationID,
	}
	childReq := r.req
	childReq.SessionID = childSessionID
	childReq.Input = req.Input
	childReq.ContentParts = append([]model.ContentPart(nil), req.ContentParts...)
	return childReq, lineage, nil
}

func (r *runtimeSubagentRunner) runDetachedSubagent(ctx context.Context, childReq RunRequest, lineage delegationLineage) {
	defer func() {
		if p := recover(); p != nil {
			panicErr := fmt.Errorf("subagent panic: %v", p)
			r.persistDetachedSubagentFailure(ctx, childReq, panicErr)
		}
	}()
	runner, err := r.runtime.Run(attachSubagentContext(ctx, lineage), childReq)
	if err != nil {
		r.persistDetachedSubagentFailure(ctx, childReq, err)
		return
	}
	defer runner.Close()
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			panicPrefix := "runtime: agent panic: "
			if strings.HasPrefix(strings.TrimSpace(runErr.Error()), panicPrefix) {
				r.persistDetachedSubagentFailure(ctx, childReq, fmt.Errorf("subagent panic: %s", strings.TrimSpace(strings.TrimPrefix(runErr.Error(), panicPrefix))))
			}
			return
		}
		_ = ev
	}
}

func (r *runtimeSubagentRunner) persistDetachedSubagentFailure(ctx context.Context, childReq RunRequest, cause error) {
	if r == nil || r.runtime == nil || r.runtime.store == nil {
		return
	}
	sess, err := r.runtime.store.GetOrCreate(ctx, &session.Session{
		AppName: childReq.AppName,
		UserID:  childReq.UserID,
		ID:      childReq.SessionID,
	})
	if err != nil {
		return
	}
	_ = r.runtime.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusFailed, "delegate_panic", cause, func(*session.Event, error) bool {
		return true
	})
}

func (r *runtimeSubagentRunner) inspectSubagent(ctx context.Context, sessionID string) (delegation.RunResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return delegation.RunResult{}, fmt.Errorf("runtime: delegated child session_id is required")
	}
	state, err := r.runtime.RunState(ctx, RunStateRequest{
		AppName:   r.req.AppName,
		UserID:    r.req.UserID,
		SessionID: sessionID,
	})
	if err != nil {
		return delegation.RunResult{}, err
	}
	events, err := r.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          r.req.AppName,
		UserID:           r.req.UserID,
		SessionID:        sessionID,
		Limit:            200,
		IncludeLifecycle: false,
	})
	if err != nil {
		return delegation.RunResult{}, err
	}
	result := delegation.RunResult{
		SessionID: sessionID,
		State:     string(state.Status),
		Running:   state.Status == RunLifecycleStatusRunning || state.Status == RunLifecycleStatusWaitingApproval,
	}
	if !state.HasLifecycle && len(events) == 0 {
		return delegation.RunResult{}, fmt.Errorf("runtime: delegated child session %q not found", sessionID)
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if result.DelegationID == "" {
			result.DelegationID = strings.TrimSpace(subagentStringValue(ev.Meta[metaDelegationID]))
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			result.Assistant = text
		}
	}
	if result.State == "" && !state.HasLifecycle {
		result.State = string(RunLifecycleStatusRunning)
		result.Running = true
	}
	return result, nil
}

func subagentStringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
