package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
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
	metaParentToolName  = "parent_tool_name"
	metaDelegationID    = "delegation_id"
)

type delegationLineage struct {
	ParentSessionID string
	ChildSessionID  string
	ParentToolCall  string
	ParentToolName  string
	DelegationID    string
	TaskID          string
}

type delegationLineageContextKey struct{}

type runtimeSubagentRunner struct {
	runtime *Runtime
	parent  *session.Session
	req     RunRequest
	mu      sync.Mutex
	active  map[string]context.CancelFunc
}

func newSubagentRunner(r *Runtime, parent *session.Session, req RunRequest) agent.SubagentRunner {
	if r == nil || parent == nil {
		return nil
	}
	return &runtimeSubagentRunner{
		runtime: r,
		parent:  parent,
		req:     req,
		active:  map[string]context.CancelFunc{},
	}
}

func (r *runtimeSubagentRunner) RunSubagent(ctx context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	if strings.TrimSpace(req.Agent) == "" {
		req.Agent = "self"
	}
	childReq, lineage, err := r.prepareChildRun(ctx, req)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	base := detachSubagentContext(ctx, lineage)
	var runCtx context.Context
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(base, req.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(base)
	}
	r.registerCancel(childReq.SessionID, cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.unregisterCancel(childReq.SessionID)
		r.runDetachedSubagent(runCtx, childReq, lineage)
	}()

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	yielded := false
	if req.Yield > 0 {
		timer := time.NewTimer(req.Yield)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			yielded = true
		case <-waitCtx.Done():
			return agent.SubagentRunResult{}, waitCtx.Err()
		}
	} else {
		select {
		case <-done:
		default:
			yielded = true
		}
	}
	if yielded {
		return agent.SubagentRunResult{
			SessionID:    childReq.SessionID,
			DelegationID: lineage.DelegationID,
			Agent:        req.Agent,
			State:        string(RunLifecycleStatusRunning),
			Running:      true,
			Yielded:      true,
			Timeout:      req.Timeout,
		}, nil
	}
	result, err := r.inspectSubagent(ctx, childReq.SessionID)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	result.Agent = req.Agent
	result.Timeout = req.Timeout
	if result.Running {
		result.Yielded = true
	}
	if strings.TrimSpace(result.DelegationID) == "" {
		result.DelegationID = lineage.DelegationID
	}
	return result, nil
}

func (r *runtimeSubagentRunner) CancelSubagent(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.active[sessionID]
	r.mu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

func (r *runtimeSubagentRunner) registerCancel(sessionID string, cancel context.CancelFunc) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || cancel == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		r.active = map[string]context.CancelFunc{}
	}
	r.active[sessionID] = cancel
}

func (r *runtimeSubagentRunner) unregisterCancel(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, sessionID)
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

func ResolveChildSessionID(parentSessionID string, requested string) (string, error) {
	return resolveChildSessionID(parentSessionID, requested)
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

func DetachDelegationContext(ctx context.Context, meta DelegationMetadata) context.Context {
	return detachSubagentContext(ctx, delegationLineage{
		ParentSessionID: meta.ParentSessionID,
		ChildSessionID:  meta.ChildSessionID,
		ParentToolCall:  meta.ParentToolCall,
		ParentToolName:  meta.ParentToolName,
		DelegationID:    meta.DelegationID,
	})
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

func AttachDelegationContext(ctx context.Context, meta DelegationMetadata) context.Context {
	return attachSubagentContext(ctx, delegationLineage{
		ParentSessionID: meta.ParentSessionID,
		ChildSessionID:  meta.ChildSessionID,
		ParentToolCall:  meta.ParentToolCall,
		ParentToolName:  meta.ParentToolName,
		DelegationID:    meta.DelegationID,
	})
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
	if value := strings.TrimSpace(lineage.ParentToolName); value != "" {
		ev.Meta[metaParentToolName] = value
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
	return session.EnsureEventType(ev)
}

func (r *runtimeSubagentRunner) prepareChildRun(ctx context.Context, req agent.SubagentRunRequest) (RunRequest, delegationLineage, error) {
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
		ParentToolName:  strings.TrimSpace(callInfo.Name),
		DelegationID:    delegationID,
	}
	childReq := r.req
	childReq.SessionID = childSessionID
	childReq.Input = req.Task
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

func (r *runtimeSubagentRunner) inspectSubagent(ctx context.Context, sessionID string) (agent.SubagentRunResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return agent.SubagentRunResult{}, fmt.Errorf("runtime: delegated child session_id is required")
	}
	state, err := r.runtime.RunState(ctx, RunStateRequest{
		AppName:   r.req.AppName,
		UserID:    r.req.UserID,
		SessionID: sessionID,
	})
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	events, err := r.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          r.req.AppName,
		UserID:           r.req.UserID,
		SessionID:        sessionID,
		Limit:            200,
		IncludeLifecycle: false,
	})
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	result := agent.SubagentRunResult{
		SessionID: sessionID,
		State:     string(state.Status),
		Running:   state.Status == RunLifecycleStatusRunning || state.Status == RunLifecycleStatusWaitingApproval,
	}
	if !state.HasLifecycle && len(events) == 0 {
		return agent.SubagentRunResult{}, fmt.Errorf("runtime: delegated child session %q not found", sessionID)
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if result.DelegationID == "" {
			result.DelegationID = strings.TrimSpace(subagentStringValue(ev.Meta[metaDelegationID]))
		}
	}
	result.Assistant = FinalAssistantText(events)
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
