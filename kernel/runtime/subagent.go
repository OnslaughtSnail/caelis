package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	coremeta "github.com/OnslaughtSnail/caelis/internal/acpmeta"
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
	result, err := r.InspectSubagent(ctx, childReq.SessionID)
	if err != nil {
		if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "not found") {
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
	if existing, ok := r.existingChildLineage(ctx, childSessionID); ok {
		if existing.ParentSessionID != "" {
			lineage.ParentSessionID = existing.ParentSessionID
		}
		if !isSubagentContinuation(ctx) {
			if existing.ParentToolCall != "" {
				lineage.ParentToolCall = existing.ParentToolCall
			}
			if existing.ParentToolName != "" {
				lineage.ParentToolName = existing.ParentToolName
			}
			if existing.DelegationID != "" {
				lineage.DelegationID = existing.DelegationID
			}
		}
	}
	if err := r.seedChildSessionMeta(ctx, childSessionID, req.Agent); err != nil {
		return RunRequest{}, delegationLineage{}, err
	}
	childReq := r.req
	childReq.SessionID = childSessionID
	childReq.Input = req.Prompt
	childReq.ContentParts = model.ContentPartsFromParts(req.Parts)
	return childReq, lineage, nil
}

func (r *runtimeSubagentRunner) existingChildLineage(ctx context.Context, childSessionID string) (delegationLineage, bool) {
	if r == nil || r.runtime == nil {
		return delegationLineage{}, false
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return delegationLineage{}, false
	}
	events, err := r.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          r.req.AppName,
		UserID:           r.req.UserID,
		SessionID:        childSessionID,
		Limit:            200,
		IncludeLifecycle: true,
	})
	if err != nil {
		return delegationLineage{}, false
	}
	for i := len(events) - 1; i >= 0; i-- {
		meta, ok := DelegationMetadataFromEvent(events[i])
		if !ok {
			continue
		}
		if meta.ChildSessionID != "" && meta.ChildSessionID != childSessionID {
			continue
		}
		return delegationLineage{
			ParentSessionID: meta.ParentSessionID,
			ChildSessionID:  firstNonEmptyString(meta.ChildSessionID, childSessionID),
			ParentToolCall:  meta.ParentToolCall,
			ParentToolName:  meta.ParentToolName,
			DelegationID:    meta.DelegationID,
		}, true
	}
	return delegationLineage{}, false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (r *runtimeSubagentRunner) seedChildSessionMeta(ctx context.Context, childSessionID string, agentName string) error {
	if r == nil || r.runtime == nil || r.runtime.store == nil {
		return nil
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return nil
	}
	meta := r.childSessionMeta(ctx, childSessionID, agentName)
	if len(meta) == 0 {
		return nil
	}
	child := &session.Session{
		AppName: r.req.AppName,
		UserID:  r.req.UserID,
		ID:      childSessionID,
	}
	if _, err := r.runtime.store.GetOrCreate(ctx, child); err != nil {
		return err
	}
	return mergeChildSessionMeta(ctx, r.runtime.store, child, meta)
}

func (r *runtimeSubagentRunner) childSessionMeta(ctx context.Context, childSessionID string, agentName string) map[string]any {
	if meta := r.sessionMeta(ctx, strings.TrimSpace(childSessionID)); len(meta) > 0 {
		return meta
	}
	parentMeta := r.sessionMeta(ctx, r.parent.ID)
	depth := coremeta.SelfSpawnDepthFromMeta(parentMeta)
	if strings.EqualFold(strings.TrimSpace(agentName), "self") {
		depth++
	}
	return coremeta.WithSelfSpawnDepth(parentMeta, depth)
}

func (r *runtimeSubagentRunner) sessionMeta(ctx context.Context, sessionID string) map[string]any {
	if r == nil || r.runtime == nil || r.runtime.store == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	values, err := r.runtime.store.SnapshotState(ctx, &session.Session{
		AppName: r.req.AppName,
		UserID:  r.req.UserID,
		ID:      sessionID,
	})
	if err != nil {
		return nil
	}
	acpState, _ := values["acp"].(map[string]any)
	if len(acpState) == 0 {
		return nil
	}
	meta, _ := acpState["meta"].(map[string]any)
	return coremeta.CloneMeta(meta)
}

func mergeChildSessionMeta(ctx context.Context, store session.Store, sess *session.Session, meta map[string]any) error {
	if store == nil || sess == nil || len(meta) == 0 {
		return nil
	}
	merge := func(values map[string]any) map[string]any {
		if values == nil {
			values = map[string]any{}
		}
		acpState, _ := values["acp"].(map[string]any)
		if acpState == nil {
			acpState = map[string]any{}
		} else {
			acpState = cloneMap(acpState)
		}
		acpState["meta"] = coremeta.CloneMeta(meta)
		values["acp"] = acpState
		return values
	}
	if updater, ok := store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sess, func(values map[string]any) (map[string]any, error) {
			return merge(values), nil
		})
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	return store.ReplaceState(ctx, sess, merge(values))
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
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

func (r *runtimeSubagentRunner) InspectSubagent(ctx context.Context, sessionID string) (agent.SubagentRunResult, error) {
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
		UpdatedAt: state.UpdatedAt,
	}
	if !state.HasLifecycle && len(events) == 0 {
		return agent.SubagentRunResult{}, fmt.Errorf("runtime: delegated child session %q not found", sessionID)
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Time.After(result.UpdatedAt) {
			result.UpdatedAt = ev.Time
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
