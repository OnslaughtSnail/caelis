package acpext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type AdapterFactory func(*internalacp.Conn) (internalacp.Adapter, error)

type Config struct {
	Store         session.Store
	WorkspaceRoot string
	WorkspaceCWD  string
	ClientRuntime toolexec.Runtime
	AgentRegistry *appagents.Registry
	NewAdapter    AdapterFactory
}

func NewACPSubagentRunnerFactory(cfg Config) runtime.SubagentRunnerFactory {
	if cfg.Store == nil {
		return nil
	}
	return func(rt *runtime.Runtime, parent *session.Session, req runtime.RunRequest) agent.SubagentRunner {
		_ = req
		if rt == nil || parent == nil {
			return nil
		}
		return &selfACPSubagentRunner{
			runtime:       rt,
			store:         cfg.Store,
			parent:        parent,
			workspaceRoot: strings.TrimSpace(cfg.WorkspaceRoot),
			workspaceCWD:  strings.TrimSpace(cfg.WorkspaceCWD),
			clientRuntime: cfg.ClientRuntime,
			agentRegistry: cfg.AgentRegistry,
			newAdapter:    cfg.NewAdapter,
			active:        map[string]context.CancelFunc{},
		}
	}
}

func NewSelfACPSubagentRunnerFactory(cfg Config) runtime.SubagentRunnerFactory {
	return NewACPSubagentRunnerFactory(cfg)
}

type selfACPSubagentRunner struct {
	runtime       *runtime.Runtime
	store         session.Store
	parent        *session.Session
	workspaceRoot string
	workspaceCWD  string
	clientRuntime toolexec.Runtime
	agentRegistry *appagents.Registry
	newAdapter    AdapterFactory

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

type readyState struct {
	sessionID string
	meta      runtime.DelegationMetadata
}

func (r *selfACPSubagentRunner) RunSubagent(ctx context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "self"
	}
	if strings.TrimSpace(req.Task) == "" {
		return agent.SubagentRunResult{}, fmt.Errorf("acpext: child task is required")
	}
	desc, err := r.resolveAgentDescriptor(agentName)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	requestedSessionID, err := runtime.ResolveChildSessionID(r.parent.ID, req.SessionID)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	metaBase := r.delegationMetadata(ctx, "")
	runCtx, cancel := context.WithCancel(runtime.DetachDelegationContext(ctx, metaBase))
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(runtime.DetachDelegationContext(ctx, metaBase), req.Timeout)
	}
	var (
		clientMu sync.Mutex
		client   *acpclient.Client
		idMu     sync.RWMutex
		actualID string
	)
	cancelSubagent := func() {
		clientMu.Lock()
		activeClient := client
		clientMu.Unlock()
		idMu.RLock()
		sessionID := actualID
		idMu.RUnlock()
		if activeClient != nil && strings.TrimSpace(sessionID) != "" {
			_ = activeClient.Cancel(context.Background(), sessionID)
		}
		cancel()
	}

	type runOutcome struct {
		sessionID string
		meta      runtime.DelegationMetadata
		created   bool
		err       error
	}
	ready := make(chan readyState, 1)
	done := make(chan runOutcome, 1)
	go func() {
		outcome := runOutcome{}
		outcome.sessionID, outcome.meta, outcome.created, outcome.err = r.runACPSubagent(runCtx, desc, requestedSessionID, req.Task, metaBase, func(created *acpclient.Client) {
			clientMu.Lock()
			client = created
			clientMu.Unlock()
		}, func(state readyState) {
			select {
			case ready <- state:
			default:
			}
		})
		if strings.TrimSpace(outcome.sessionID) != "" {
			r.unregisterCancel(outcome.sessionID)
		}
		done <- outcome
	}()

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	var (
		childSessionID string
		meta           runtime.DelegationMetadata
	)
	select {
	case state := <-ready:
		childSessionID = strings.TrimSpace(state.sessionID)
		meta = state.meta
		idMu.Lock()
		actualID = childSessionID
		idMu.Unlock()
		r.registerCancel(childSessionID, cancelSubagent)
	case outcome := <-done:
		if outcome.err != nil {
			return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, outcome.err)
		}
		childSessionID = strings.TrimSpace(outcome.sessionID)
		meta = outcome.meta
		result, inspectErr := r.inspect(ctx, childSessionID)
		if inspectErr != nil {
			return agent.SubagentRunResult{}, inspectErr
		}
		result.Agent = agentName
		result.Timeout = req.Timeout
		if result.DelegationID == "" {
			result.DelegationID = meta.DelegationID
		}
		return result, nil
	case <-waitCtx.Done():
		return agent.SubagentRunResult{}, waitCtx.Err()
	}

	yielded := false
	if req.Yield > 0 {
		timer := time.NewTimer(req.Yield)
		defer timer.Stop()
		select {
		case outcome := <-done:
			if outcome.err != nil {
				return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, outcome.err)
			}
		case <-timer.C:
			yielded = true
		case <-waitCtx.Done():
			return agent.SubagentRunResult{}, waitCtx.Err()
		}
	} else {
		select {
		case outcome := <-done:
			if outcome.err != nil {
				return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, outcome.err)
			}
		default:
			yielded = true
		}
	}
	if yielded {
		return agent.SubagentRunResult{
			SessionID:    childSessionID,
			DelegationID: meta.DelegationID,
			Agent:        agentName,
			State:        string(runtime.RunLifecycleStatusRunning),
			Running:      true,
			Yielded:      true,
			Timeout:      req.Timeout,
		}, nil
	}
	result, err := r.inspect(ctx, childSessionID)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	result.Agent = agentName
	result.Timeout = req.Timeout
	if result.DelegationID == "" {
		result.DelegationID = meta.DelegationID
	}
	return result, nil
}

func (r *selfACPSubagentRunner) CancelSubagent(sessionID string) bool {
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

func (r *selfACPSubagentRunner) runACPSubagent(ctx context.Context, desc appagents.Descriptor, requestedSessionID string, taskText string, metaBase runtime.DelegationMetadata, onClient func(*acpclient.Client), onReady func(readyState)) (string, runtime.DelegationMetadata, bool, error) {
	var (
		bridgeMu sync.RWMutex
		bridge   *acpSessionUpdateBridge
	)
	onUpdate := func(env acpclient.UpdateEnvelope) {
		bridgeMu.RLock()
		activeBridge := bridge
		bridgeMu.RUnlock()
		if activeBridge != nil {
			activeBridge.Emit(ctx, env)
		}
	}

	switch desc.Transport {
	case appagents.TransportSelf:
		if r.newAdapter == nil {
			return "", metaBase, false, fmt.Errorf("acpext: self ACP adapter is not configured")
		}
	case appagents.TransportACP:
	default:
		return "", metaBase, false, fmt.Errorf("acpext: unsupported transport %q for agent %q", desc.Transport, desc.ID)
	}

	startClient := func() (*acpclient.Client, func(), error) {
		if desc.Transport == appagents.TransportSelf {
			return r.startLoopbackClient(ctx, requestedSessionID, metaBase, onUpdate, onClient)
		}
		client, err := acpclient.Start(ctx, acpclient.Config{
			Command:             strings.TrimSpace(desc.Command),
			Args:                append([]string(nil), desc.Args...),
			Env:                 copyStringMap(desc.Env),
			WorkDir:             r.resolveAgentWorkDir(desc),
			Runtime:             r.resolveClientRuntime(),
			Workspace:           r.resolveWorkspaceRoot(),
			ClientInfo:          nil,
			OnUpdate:            onUpdate,
			OnPermissionRequest: permissionRequestHandler(ctx),
		})
		if err != nil {
			return nil, nil, err
		}
		if onClient != nil {
			onClient(client)
		}
		return client, func() { _ = client.Close() }, nil
	}

	client, cleanup, err := startClient()
	if err != nil {
		return "", metaBase, false, err
	}
	defer cleanup()

	if _, err := client.Initialize(ctx); err != nil {
		return "", metaBase, false, err
	}
	newResp, err := client.NewSession(ctx, r.resolveWorkspaceCWD(), requestedSessionID)
	if err != nil {
		return "", metaBase, false, err
	}
	actualSessionID := strings.TrimSpace(newResp.SessionID)
	if actualSessionID == "" {
		actualSessionID = strings.TrimSpace(requestedSessionID)
	}
	meta := metaBase
	meta.ChildSessionID = actualSessionID
	bridgeMu.Lock()
	bridge = newACPSessionUpdateBridge(meta, actualSessionID)
	bridgeMu.Unlock()
	if onReady != nil {
		onReady(readyState{sessionID: actualSessionID, meta: meta})
	}
	_, err = client.Prompt(ctx, actualSessionID, taskText)
	return actualSessionID, meta, true, err
}

func (r *selfACPSubagentRunner) startLoopbackClient(ctx context.Context, requestedSessionID string, meta runtime.DelegationMetadata, onUpdate func(acpclient.UpdateEnvelope), onClient func(*acpclient.Client)) (*acpclient.Client, func(), error) {
	serverReader, clientWriter := io.Pipe()
	clientReader, serverWriter := io.Pipe()
	serverConn := internalacp.NewConn(serverReader, serverWriter)
	client, err := acpclient.StartLoopback(ctx, acpclient.Config{
		Runtime:             r.resolveClientRuntime(),
		Workspace:           r.resolveWorkspaceRoot(),
		WorkDir:             r.resolveWorkspaceCWD(),
		OnUpdate:            onUpdate,
		OnPermissionRequest: permissionRequestHandler(ctx),
	}, clientReader, clientWriter)
	if err != nil {
		return nil, nil, err
	}
	if onClient != nil {
		onClient(client)
	}

	adapter, err := r.newACPAdapter(serverConn)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	server, err := internalacp.NewServer(internalacp.ServerConfig{
		Conn:            serverConn,
		ProtocolVersion: internalacp.CurrentProtocolVersion,
		Adapter:         adapter,
	})
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	serveCtx, serveCancel := context.WithCancel(runtime.AttachDelegationContext(ctx, runtime.DelegationMetadata{
		ParentSessionID: meta.ParentSessionID,
		ChildSessionID:  requestedSessionID,
		ParentToolCall:  meta.ParentToolCall,
		ParentToolName:  meta.ParentToolName,
		DelegationID:    meta.DelegationID,
	}))
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve(serveCtx)
	}()
	cleanup := func() {
		serveCancel()
		select {
		case serveErr := <-serverDone:
			if serveErr != nil && serveErr != context.Canceled {
			}
		case <-time.After(100 * time.Millisecond):
		}
		_ = client.Close()
	}
	return client, cleanup, nil
}

func (r *selfACPSubagentRunner) newACPAdapter(conn *internalacp.Conn) (internalacp.Adapter, error) {
	if conn == nil {
		return nil, fmt.Errorf("acpext: acp conn is required")
	}
	if r.newAdapter != nil {
		return r.newAdapter(conn)
	}
	return nil, fmt.Errorf("acpext: self ACP adapter is not configured")
}

func (r *selfACPSubagentRunner) delegationMetadata(ctx context.Context, childSessionID string) runtime.DelegationMetadata {
	callInfo, _ := toolexec.ToolCallInfoFromContext(ctx)
	return runtime.DelegationMetadata{
		ParentSessionID: r.parent.ID,
		ChildSessionID:  childSessionID,
		ParentToolCall:  strings.TrimSpace(callInfo.ID),
		ParentToolName:  strings.TrimSpace(callInfo.Name),
		DelegationID:    idutil.NewDelegationID(),
	}
}

func (r *selfACPSubagentRunner) inspect(ctx context.Context, childSessionID string) (agent.SubagentRunResult, error) {
	state, err := r.runtime.RunState(ctx, runtime.RunStateRequest{
		AppName:   r.parent.AppName,
		UserID:    r.parent.UserID,
		SessionID: childSessionID,
	})
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	events, err := r.runtime.SessionEvents(ctx, runtime.SessionEventsRequest{
		AppName:          r.parent.AppName,
		UserID:           r.parent.UserID,
		SessionID:        childSessionID,
		IncludeLifecycle: false,
	})
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	result := agent.SubagentRunResult{
		SessionID: childSessionID,
		State:     string(state.Status),
		Running:   state.Status == runtime.RunLifecycleStatusRunning || state.Status == runtime.RunLifecycleStatusWaitingApproval,
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if result.DelegationID == "" {
			if meta, ok := runtime.DelegationMetadataFromEvent(ev); ok {
				result.DelegationID = strings.TrimSpace(meta.DelegationID)
			}
		}
	}
	result.Assistant = runtime.FinalAssistantText(events)
	if result.State == "" && !state.HasLifecycle {
		result.State = string(runtime.RunLifecycleStatusRunning)
		result.Running = true
	}
	return result, nil
}

func (r *selfACPSubagentRunner) failedResult(ctx context.Context, childSessionID string, childCreated bool, meta runtime.DelegationMetadata, agentName string, timeout time.Duration, cause error) (agent.SubagentRunResult, error) {
	if childCreated && strings.TrimSpace(childSessionID) != "" {
		childSession := &session.Session{
			AppName: r.parent.AppName,
			UserID:  r.parent.UserID,
			ID:      childSessionID,
		}
		ev := runtime.LifecycleEvent(childSession, runtime.RunLifecycleStatusFailed, "self_acp", cause)
		if ev != nil {
			ev = annotateDelegationEvent(ev, meta)
			_ = r.store.AppendEvent(ctx, childSession, ev)
		}
	}
	return agent.SubagentRunResult{
		SessionID:    strings.TrimSpace(childSessionID),
		DelegationID: meta.DelegationID,
		Agent:        agentName,
		State:        string(runtime.RunLifecycleStatusFailed),
		Timeout:      timeout,
	}, cause
}

func annotateDelegationEvent(ev *session.Event, meta runtime.DelegationMetadata) *session.Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if meta.ParentSessionID != "" {
		ev.Meta["parent_session_id"] = meta.ParentSessionID
	}
	if meta.ChildSessionID != "" {
		ev.Meta["child_session_id"] = meta.ChildSessionID
	}
	if meta.ParentToolCall != "" {
		ev.Meta["parent_tool_call_id"] = meta.ParentToolCall
	}
	if meta.ParentToolName != "" {
		ev.Meta["parent_tool_name"] = meta.ParentToolName
	}
	if meta.DelegationID != "" {
		ev.Meta["delegation_id"] = meta.DelegationID
	}
	return ev
}

func permissionRequestHandler(ctx context.Context) func(context.Context, acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
	return func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
		if approver, ok := toolexec.ApproverFromContext(ctx); ok {
			allowed, err := approver.Approve(reqCtx, approvalRequestFromACP(req))
			if err != nil {
				return acpclient.RequestPermissionResponse{}, err
			}
			return permissionOutcome(allowed), nil
		}
		if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
			allowed, err := authorizer.AuthorizeTool(reqCtx, authorizationRequestFromACP(req))
			if err != nil {
				return acpclient.RequestPermissionResponse{}, err
			}
			return permissionOutcome(allowed), nil
		}
		return permissionOutcome(true), nil
	}
}

func permissionOutcome(allowed bool) acpclient.RequestPermissionResponse {
	optionID := "reject_once"
	if allowed {
		optionID = "allow_once"
	}
	return acpclient.RequestPermissionResponse{
		Outcome: mustMarshalRaw(map[string]any{
			"outcome":  "selected",
			"optionId": optionID,
		}),
	}
}

func approvalRequestFromACP(req acpclient.RequestPermissionRequest) toolexec.ApprovalRequest {
	command := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "command"))
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "permission"
	}
	return toolexec.ApprovalRequest{
		ToolName: title,
		Action:   strings.TrimSpace(derefString(req.ToolCall.Kind)),
		Command:  command,
		Reason:   strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path")),
	}
}

func authorizationRequestFromACP(req acpclient.RequestPermissionRequest) policy.ToolAuthorizationRequest {
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "tool"
	}
	path := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path"))
	target := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "target"))
	return policy.ToolAuthorizationRequest{
		ToolName: title,
		Path:     path,
		Target:   target,
		ScopeKey: firstNonEmpty(path, target, strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "command"))),
	}
}

func rawStringField(raw any, key string) string {
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case map[string]any:
		return strings.TrimSpace(fmt.Sprint(value[key]))
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(value, &decoded); err == nil {
			return strings.TrimSpace(fmt.Sprint(decoded[key]))
		}
	}
	return ""
}

func mustMarshalRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage([]byte("null"))
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *selfACPSubagentRunner) registerCancel(sessionID string, cancel context.CancelFunc) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || cancel == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[sessionID] = cancel
}

func (r *selfACPSubagentRunner) unregisterCancel(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, sessionID)
}

func (r *selfACPSubagentRunner) resolveWorkspaceRoot() string {
	if root := strings.TrimSpace(r.workspaceRoot); root != "" {
		return root
	}
	if cwd := r.resolveWorkspaceCWD(); cwd != "" {
		return cwd
	}
	return "."
}

func (r *selfACPSubagentRunner) resolveWorkspaceCWD() string {
	if cwd := strings.TrimSpace(r.workspaceCWD); cwd != "" {
		return filepath.Clean(cwd)
	}
	if runtime := r.resolveClientRuntime(); runtime != nil && runtime.FileSystem() != nil {
		if cwd, err := runtime.FileSystem().Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
			return filepath.Clean(cwd)
		}
	}
	if root := strings.TrimSpace(r.workspaceRoot); root != "" {
		return filepath.Clean(root)
	}
	return "."
}

func (r *selfACPSubagentRunner) resolveClientRuntime() toolexec.Runtime {
	if r == nil {
		return nil
	}
	return r.clientRuntime
}

func (r *selfACPSubagentRunner) resolveAgentDescriptor(agentName string) (appagents.Descriptor, error) {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "self"
	}
	if r.agentRegistry == nil {
		if strings.EqualFold(agentName, "self") {
			return appagents.SelfDescriptor(), nil
		}
		return appagents.Descriptor{}, fmt.Errorf("acpext: unknown agent %q", agentName)
	}
	desc, ok := r.agentRegistry.Lookup(agentName)
	if !ok {
		return appagents.Descriptor{}, fmt.Errorf("acpext: unknown agent %q", agentName)
	}
	return desc, nil
}

func (r *selfACPSubagentRunner) resolveAgentWorkDir(desc appagents.Descriptor) string {
	workDir := strings.TrimSpace(desc.WorkDir)
	if workDir == "" {
		return r.resolveWorkspaceCWD()
	}
	if filepath.IsAbs(workDir) {
		return filepath.Clean(workDir)
	}
	return filepath.Join(r.resolveWorkspaceCWD(), workDir)
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
