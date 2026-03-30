package acpext

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

type AdapterFactory func(*internalacp.Conn) (internalacp.Adapter, error)
type AgentRegistryResolver func() (*appagents.Registry, error)

type Config struct {
	Store                session.Store
	WorkspaceRoot        string
	WorkspaceCWD         string
	ClientRuntime        toolexec.Runtime
	ResolveAgentRegistry AgentRegistryResolver
	NewAdapter           AdapterFactory
}

func NewACPSubagentRunnerFactory(cfg Config) runtime.SubagentRunnerFactory {
	if cfg.Store == nil {
		return nil
	}
	snapshots := newAgentSnapshotCache(cfg.ResolveAgentRegistry)
	shared := &sharedACPSubagentState{
		tracker: newRemoteSubagentTracker(),
		active:  map[string]context.CancelFunc{},
	}
	return func(rt *runtime.Runtime, parent *session.Session, req runtime.RunRequest) agent.SubagentRunner {
		_ = req
		if rt == nil || parent == nil {
			return nil
		}
		snapshots.Warm(parent.ID)
		return &selfACPSubagentRunner{
			runtime:       rt,
			store:         cfg.Store,
			parent:        parent,
			workspaceRoot: strings.TrimSpace(cfg.WorkspaceRoot),
			workspaceCWD:  strings.TrimSpace(cfg.WorkspaceCWD),
			clientRuntime: cfg.ClientRuntime,
			snapshots:     snapshots,
			newAdapter:    cfg.NewAdapter,
			shared:        shared,
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
	snapshots     *agentSnapshotCache
	newAdapter    AdapterFactory
	shared        *sharedACPSubagentState
}

type sharedACPSubagentState struct {
	mu      sync.Mutex
	active  map[string]context.CancelFunc
	tracker *remoteSubagentTracker
}

type acpTerminalOutputClient interface {
	TerminalOutput(context.Context, string, string) (acpclient.TerminalOutputResponse, error)
	TerminalRelease(context.Context, string, string) error
}

type terminalBridgeManager struct {
	mu      sync.Mutex
	pollers map[string]context.CancelFunc
	onStart func()
	onStop  func()
}

const (
	defaultRemoteACPIdleTimeout = 3 * time.Minute
	defaultRemoteACPInitTimeout = 6 * time.Minute
)

var startACPClient = acpclient.Start

type readyState struct {
	sessionID string
	meta      runtime.DelegationMetadata
}

type agentSnapshotCache struct {
	mu      sync.Mutex
	load    AgentRegistryResolver
	entries map[string]agentSnapshotEntry
}

type agentSnapshotEntry struct {
	reg *appagents.Registry
	err error
}

func newAgentSnapshotCache(load AgentRegistryResolver) *agentSnapshotCache {
	return &agentSnapshotCache{
		load:    load,
		entries: map[string]agentSnapshotEntry{},
	}
}

func (c *agentSnapshotCache) Warm(parentSessionID string) {
	_, _ = c.snapshot(parentSessionID)
}

func (c *agentSnapshotCache) snapshot(parentSessionID string) (*appagents.Registry, error) {
	parentSessionID = strings.TrimSpace(parentSessionID)
	if parentSessionID == "" {
		return c.loadCurrent()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[parentSessionID]; ok {
		return entry.reg, entry.err
	}
	reg, err := c.loadCurrent()
	c.entries[parentSessionID] = agentSnapshotEntry{reg: reg, err: err}
	return reg, err
}

func (c *agentSnapshotCache) loadCurrent() (*appagents.Registry, error) {
	if c == nil || c.load == nil {
		return appagents.NewRegistry(), nil
	}
	reg, err := c.load()
	if err != nil {
		return nil, err
	}
	if reg == nil {
		return appagents.NewRegistry(), nil
	}
	return appagents.NewRegistry(reg.List()...), nil
}

func (r *selfACPSubagentRunner) RunSubagent(ctx context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "self"
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return agent.SubagentRunResult{}, fmt.Errorf("acpext: child prompt is required")
	}
	explicitSessionID := strings.TrimSpace(req.SessionID)
	desc, err := r.resolveAgentDescriptor(agentName)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	target, err := r.resolveSessionTarget(explicitSessionID)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	if cwd := strings.TrimSpace(req.ChildCWD); cwd != "" {
		target.childCWD = cwd
	}
	sessionMeta := r.childSessionMeta(ctx, target.requestedSessionID, desc.ID)
	metaBase := r.delegationMetadata(ctx, target.requestedSessionID)
	idleTimeout := req.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultRemoteACPIdleTimeout
	}
	baseCtx, baseCancel := context.WithCancel(runtime.DetachDelegationContext(ctx, metaBase))
	runCtx := baseCtx
	cancelRPCContext := context.WithoutCancel(runCtx)
	timeoutCancel := func() {}
	if req.Timeout > 0 {
		runCtx, timeoutCancel = context.WithTimeout(runCtx, req.Timeout)
	}
	cancel := func() {
		timeoutCancel()
		baseCancel()
	}
	var (
		idleErrMu sync.Mutex
		idleErr   error
	)
	noteIdleErr := func(err error) {
		if err == nil {
			return
		}
		idleErrMu.Lock()
		if idleErr == nil {
			idleErr = err
		}
		idleErrMu.Unlock()
		cancel()
	}
	loadIdleErr := func() error {
		idleErrMu.Lock()
		defer idleErrMu.Unlock()
		return idleErr
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
			_ = activeClient.Cancel(cancelRPCContext, sessionID)
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
		outcome.sessionID, outcome.meta, outcome.created, outcome.err = r.runACPSubagent(
			runCtx, desc, target, req.Prompt, metaBase, sessionMeta, idleTimeout, noteIdleErr,
			func(created *acpclient.Client) {
				clientMu.Lock()
				client = created
				clientMu.Unlock()
			},
			func(state readyState) {
				select {
				case ready <- state:
				default:
				}
			},
		)
		if strings.TrimSpace(outcome.sessionID) != "" {
			r.unregisterCancel(outcome.sessionID)
		}
		done <- outcome
	}()

	waitCtx := ctx
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
		if cause := loadIdleErr(); cause != nil && (errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded)) {
			outcome.err = cause
		}
		if outcome.err != nil {
			if recovered, ok := r.recoverReadyChildTimeout(context.WithoutCancel(ctx), firstNonEmpty(strings.TrimSpace(outcome.sessionID), childSessionID), outcome.meta, agentName, target.childCWD, req.Timeout, idleTimeout, outcome.err); ok {
				return recovered, nil
			}
			return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, idleTimeout, outcome.err)
		}
		childSessionID = strings.TrimSpace(outcome.sessionID)
		meta = outcome.meta
		result, inspectErr := r.InspectSubagent(ctx, childSessionID)
		if inspectErr != nil {
			return agent.SubagentRunResult{}, inspectErr
		}
		result.Agent = agentName
		result.ChildCWD = firstNonEmpty(result.ChildCWD, target.childCWD)
		result.Timeout = req.Timeout
		result.IdleTimeout = idleTimeout
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
			if cause := loadIdleErr(); cause != nil && (errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded)) {
				outcome.err = cause
			}
			if outcome.err != nil {
				if recovered, ok := r.recoverReadyChildTimeout(context.WithoutCancel(ctx), firstNonEmpty(strings.TrimSpace(outcome.sessionID), childSessionID), outcome.meta, agentName, target.childCWD, req.Timeout, idleTimeout, outcome.err); ok {
					return recovered, nil
				}
				return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, idleTimeout, outcome.err)
			}
		case <-timer.C:
			yielded = true
		case <-waitCtx.Done():
			return agent.SubagentRunResult{}, waitCtx.Err()
		}
	} else {
		select {
		case outcome := <-done:
			if cause := loadIdleErr(); cause != nil && (errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded)) {
				outcome.err = cause
			}
			if outcome.err != nil {
				if recovered, ok := r.recoverReadyChildTimeout(context.WithoutCancel(ctx), firstNonEmpty(strings.TrimSpace(outcome.sessionID), childSessionID), outcome.meta, agentName, target.childCWD, req.Timeout, idleTimeout, outcome.err); ok {
					return recovered, nil
				}
				return r.failedResult(ctx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, idleTimeout, outcome.err)
			}
		default:
			yielded = true
		}
	}
	if yielded {
		go func(detachedCtx context.Context) {
			outcome := <-done
			if cause := loadIdleErr(); cause != nil && (errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded)) {
				outcome.err = cause
			}
			if outcome.err != nil {
				_, _ = r.failedResult(detachedCtx, outcome.sessionID, outcome.created, outcome.meta, agentName, req.Timeout, idleTimeout, outcome.err)
			}
		}(context.WithoutCancel(ctx))
		return agent.SubagentRunResult{
			SessionID:    childSessionID,
			DelegationID: meta.DelegationID,
			Agent:        agentName,
			ChildCWD:     target.childCWD,
			State:        string(runtime.RunLifecycleStatusRunning),
			Running:      true,
			Yielded:      true,
			Timeout:      req.Timeout,
			IdleTimeout:  idleTimeout,
		}, nil
	}
	result, err := r.InspectSubagent(ctx, childSessionID)
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	result.Agent = agentName
	result.ChildCWD = firstNonEmpty(result.ChildCWD, target.childCWD)
	result.Timeout = req.Timeout
	result.IdleTimeout = idleTimeout
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
	if r == nil || r.shared == nil {
		return false
	}
	r.shared.mu.Lock()
	cancel, ok := r.shared.active[sessionID]
	r.shared.mu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

type subagentSessionTarget struct {
	requestedSessionID string
	childCWD           string
}

func (r *selfACPSubagentRunner) runACPSubagent(ctx context.Context, desc appagents.Descriptor, target subagentSessionTarget, promptText string, metaBase runtime.DelegationMetadata, sessionMeta map[string]any, idleTimeout time.Duration, onIdle func(error), onClient func(*acpclient.Client), onReady func(readyState)) (string, runtime.DelegationMetadata, bool, error) {
	var (
		bridgeMu                sync.RWMutex
		bridge                  *acpSessionUpdateBridge
		sessionIDForPermissions string
		watchdogMu              sync.RWMutex
		watchdog                *idleWatchdog
		terminalBridge          terminalBridgeManager
		client                  *acpclient.Client
		actualSessionID         string
		terminalMeta            = metaBase
	)
	cancelRPCContext := context.WithoutCancel(ctx)
	onUpdate := func(env acpclient.UpdateEnvelope) {
		watchdogMu.RLock()
		activeWatchdog := watchdog
		watchdogMu.RUnlock()
		if activeWatchdog != nil {
			activeWatchdog.Beat()
		}
		bridgeMu.RLock()
		activeBridge := bridge
		bridgeMu.RUnlock()
		if activeBridge != nil {
			activeBridge.Emit(ctx, env)
		}
		terminalBridge.observe(ctx, client, r.shared.tracker, firstNonEmpty(strings.TrimSpace(env.SessionID), actualSessionID), desc.ID, terminalMeta, env.Update)
	}
	pauseWatchdog := func() {
		watchdogMu.RLock()
		activeWatchdog := watchdog
		watchdogMu.RUnlock()
		if activeWatchdog != nil {
			activeWatchdog.PauseWithReason(idleWatchdogPauseApproval)
		}
	}
	resumeWatchdog := func() {
		watchdogMu.RLock()
		activeWatchdog := watchdog
		watchdogMu.RUnlock()
		if activeWatchdog != nil {
			activeWatchdog.ResumeWithReason(idleWatchdogPauseApproval)
		}
	}
	terminalBridge.onStart = func() {
		watchdogMu.RLock()
		activeWatchdog := watchdog
		watchdogMu.RUnlock()
		if activeWatchdog != nil {
			activeWatchdog.PauseWithReason(idleWatchdogPauseTerminalTool)
		}
	}
	terminalBridge.onStop = func() {
		watchdogMu.RLock()
		activeWatchdog := watchdog
		watchdogMu.RUnlock()
		if activeWatchdog != nil {
			activeWatchdog.ResumeWithReason(idleWatchdogPauseTerminalTool)
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
			return r.startLoopbackClient(ctx, target.requestedSessionID, metaBase, target.childCWD, sessionMeta, func() string { return strings.TrimSpace(sessionIDForPermissions) }, onUpdate, onClient, pauseWatchdog, resumeWatchdog)
		}
		client, err := startACPClient(ctx, acpclient.Config{
			Command:             strings.TrimSpace(desc.Command),
			Args:                append([]string(nil), desc.Args...),
			Env:                 copyStringMap(desc.Env),
			WorkDir:             r.resolveAgentWorkDir(desc),
			Runtime:             r.resolveClientRuntime(),
			Workspace:           r.resolveWorkspaceRoot(),
			ClientInfo:          nil,
			OnUpdate:            onUpdate,
			OnPermissionRequest: r.permissionRequestHandler(ctx, strings.TrimSpace(desc.ID), func() string { return strings.TrimSpace(sessionIDForPermissions) }, metaBase, target.childCWD, pauseWatchdog, resumeWatchdog),
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
	defer terminalBridge.stopAll()

	if _, err := client.Initialize(ctx); err != nil {
		return "", metaBase, false, err
	}
	created := false
	requestedSessionID := strings.TrimSpace(target.requestedSessionID)
	if requestedSessionID != "" {
		_, loadErr := client.LoadSession(ctx, requestedSessionID, firstNonEmpty(target.childCWD, r.resolveWorkspaceCWD()), sessionMeta)
		if loadErr == nil {
			actualSessionID = requestedSessionID
		} else {
			return "", metaBase, false, fmt.Errorf("acpext: load child session %q: %w", requestedSessionID, loadErr)
		}
	} else {
		sessionCWD := firstNonEmpty(target.childCWD, r.resolveWorkspaceCWD())
		newResp, err := client.NewSession(ctx, sessionCWD, sessionMeta)
		if err != nil {
			return "", metaBase, false, err
		}
		actualSessionID = strings.TrimSpace(newResp.SessionID)
		created = true
	}
	if actualSessionID == "" {
		return "", metaBase, false, fmt.Errorf("acpext: child session id is empty")
	}
	sessionIDForPermissions = actualSessionID
	meta := metaBase
	meta.ChildSessionID = actualSessionID
	terminalMeta = meta
	if target.childCWD == "" {
		target.childCWD = r.resolveWorkspaceCWD()
	}
	bridgeMu.Lock()
	bridge = newACPSessionUpdateBridge(meta, desc.ID, actualSessionID, target.childCWD, r.shared.tracker, nil, nil)
	bridgeMu.Unlock()
	r.shared.tracker.markRunning(desc.ID, actualSessionID, meta.DelegationID, target.childCWD)
	if onReady != nil {
		onReady(readyState{sessionID: actualSessionID, meta: meta})
	}
	if idleTimeout > 0 {
		activeWatchdog := newIdleWatchdog(idleTimeout, maxDuration(defaultRemoteACPInitTimeout, idleTimeout*2), func(idleFor time.Duration) {
			cause := fmt.Errorf("acpext: delegated child session %q idle timeout exceeded after %s without updates", actualSessionID, idleFor.Round(time.Second))
			if onIdle != nil {
				onIdle(cause)
			}
			_ = client.Cancel(cancelRPCContext, actualSessionID)
		})
		watchdogMu.Lock()
		watchdog = activeWatchdog
		watchdogMu.Unlock()
		activeWatchdog.Start()
		defer activeWatchdog.Stop()
	}
	stopDeadlineCancel := context.AfterFunc(ctx, func() {
		if ctx.Err() == nil {
			return
		}
		_ = client.Cancel(cancelRPCContext, actualSessionID)
	})
	defer stopDeadlineCancel()
	_, err = client.Prompt(ctx, actualSessionID, promptText, sessionMeta)
	bridgeMu.RLock()
	activeBridge := bridge
	bridgeMu.RUnlock()
	if activeBridge != nil {
		activeBridge.FlushAssistant(ctx)
	}
	if err == nil {
		r.emitLifecycleState(ctx, actualSessionID, meta, desc.ID, runtime.RunLifecycleStatusCompleted, nil)
		if snapshot, ok := r.shared.tracker.inspect(desc.ID, actualSessionID); ok {
			r.shared.tracker.finish(desc.ID, actualSessionID, meta.DelegationID, target.childCWD, string(runtime.RunLifecycleStatusCompleted), snapshot.Assistant, "")
		} else {
			r.shared.tracker.finish(desc.ID, actualSessionID, meta.DelegationID, target.childCWD, string(runtime.RunLifecycleStatusCompleted), "", "")
		}
	}
	return actualSessionID, meta, created, err
}

func (r *selfACPSubagentRunner) startLoopbackClient(ctx context.Context, requestedSessionID string, meta runtime.DelegationMetadata, childCWD string, sessionMeta map[string]any, sessionIDProvider func() string, onUpdate func(acpclient.UpdateEnvelope), onClient func(*acpclient.Client), onApprovalStart func(), onApprovalDone func()) (*acpclient.Client, func(), error) {
	_ = sessionMeta
	serverReader, clientWriter := io.Pipe()
	clientReader, serverWriter := io.Pipe()
	serverConn := internalacp.NewConn(serverReader, serverWriter)
	client, err := acpclient.StartLoopback(ctx, acpclient.Config{
		Runtime:             r.resolveClientRuntime(),
		Workspace:           r.resolveWorkspaceRoot(),
		WorkDir:             r.resolveWorkspaceCWD(),
		OnUpdate:            onUpdate,
		OnPermissionRequest: r.permissionRequestHandler(ctx, "self", sessionIDProvider, meta, firstNonEmpty(childCWD, r.resolveWorkspaceCWD()), onApprovalStart, onApprovalDone),
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

	serveCtx, serveCancel := newLoopbackServeContext(ctx, runtime.DelegationMetadata{
		ParentSessionID: meta.ParentSessionID,
		ChildSessionID:  requestedSessionID,
		ParentToolCall:  meta.ParentToolCall,
		ParentToolName:  meta.ParentToolName,
		DelegationID:    meta.DelegationID,
	})
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve(serveCtx)
	}()
	cleanup := func() {
		serveCancel()
		select {
		case <-serverDone:
		case <-time.After(100 * time.Millisecond):
		}
		_ = client.Close()
	}
	return client, cleanup, nil
}

func newLoopbackServeContext(ctx context.Context, meta runtime.DelegationMetadata) (context.Context, context.CancelFunc) {
	base := context.Background()
	if approver, ok := toolexec.ApproverFromContext(ctx); ok {
		base = toolexec.WithApprover(base, approver)
	}
	if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
		base = policy.WithToolAuthorizer(base, authorizer)
	}
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		base, cancel = context.WithDeadline(base, deadline)
	} else {
		base, cancel = context.WithCancel(base)
	}
	stop := context.AfterFunc(ctx, cancel)
	serveCtx := runtime.AttachDelegationContext(base, meta)
	return serveCtx, func() {
		stop()
		cancel()
	}
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
	meta := runtime.DelegationMetadata{
		ParentSessionID: r.parent.ID,
		ChildSessionID:  childSessionID,
		ParentToolCall:  strings.TrimSpace(callInfo.ID),
		ParentToolName:  strings.TrimSpace(callInfo.Name),
		DelegationID:    idutil.NewDelegationID(),
	}
	if existing, ok := r.existingChildDelegation(ctx, childSessionID); ok {
		if existing.ParentSessionID != "" {
			meta.ParentSessionID = existing.ParentSessionID
		}
		if existing.ChildSessionID != "" {
			meta.ChildSessionID = existing.ChildSessionID
		}
		if !runtime.IsSubagentContinuation(ctx) {
			if existing.ParentToolCall != "" {
				meta.ParentToolCall = existing.ParentToolCall
			}
			if existing.ParentToolName != "" {
				meta.ParentToolName = existing.ParentToolName
			}
			if existing.DelegationID != "" {
				meta.DelegationID = existing.DelegationID
			}
		}
	}
	return meta
}

func (r *selfACPSubagentRunner) existingChildDelegation(ctx context.Context, childSessionID string) (runtime.DelegationMetadata, bool) {
	if r == nil || r.runtime == nil || r.parent == nil {
		return runtime.DelegationMetadata{}, false
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return runtime.DelegationMetadata{}, false
	}
	events, err := r.runtime.SessionEvents(ctx, runtime.SessionEventsRequest{
		AppName:          r.parent.AppName,
		UserID:           r.parent.UserID,
		SessionID:        childSessionID,
		Limit:            200,
		IncludeLifecycle: true,
	})
	if err != nil {
		return runtime.DelegationMetadata{}, false
	}
	for i := len(events) - 1; i >= 0; i-- {
		meta, ok := runtime.DelegationMetadataFromEvent(events[i])
		if !ok {
			continue
		}
		if meta.ChildSessionID != "" && !strings.EqualFold(strings.TrimSpace(meta.ChildSessionID), childSessionID) {
			continue
		}
		if meta.ParentSessionID == "" {
			meta.ParentSessionID = r.parent.ID
		}
		if meta.ChildSessionID == "" {
			meta.ChildSessionID = childSessionID
		}
		return meta, true
	}
	return runtime.DelegationMetadata{}, false
}

func (r *selfACPSubagentRunner) childSessionMeta(ctx context.Context, childSessionID string, agentName string) map[string]any {
	if meta := r.sessionMeta(ctx, strings.TrimSpace(childSessionID)); len(meta) > 0 {
		return meta
	}
	meta := internalacp.CloneMeta(r.sessionMeta(ctx, r.parent.ID))
	if strings.EqualFold(strings.TrimSpace(agentName), "self") {
		return internalacp.WithDelegatedChild(meta, true)
	}
	return meta
}

func (r *selfACPSubagentRunner) sessionMeta(ctx context.Context, sessionID string) map[string]any {
	if r == nil || r.store == nil || r.parent == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	values, err := r.store.SnapshotState(ctx, &session.Session{
		AppName: r.parent.AppName,
		UserID:  r.parent.UserID,
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
	return internalacp.CloneMeta(meta)
}

func (r *selfACPSubagentRunner) currentParentSessionMode(ctx context.Context) string {
	if r == nil || r.store == nil || r.parent == nil {
		return sessionmode.DefaultMode
	}
	values, err := r.store.SnapshotState(ctx, &session.Session{
		AppName: r.parent.AppName,
		UserID:  r.parent.UserID,
		ID:      r.parent.ID,
	})
	if err != nil {
		return sessionmode.DefaultMode
	}
	return sessionmode.LoadSnapshot(values)
}

func (r *selfACPSubagentRunner) InspectSubagent(ctx context.Context, childSessionID string) (agent.SubagentRunResult, error) {
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return agent.SubagentRunResult{}, fmt.Errorf("acpext: delegated child session_id is required")
	}
	if r == nil || r.shared == nil || r.shared.tracker == nil {
		return agent.SubagentRunResult{}, fmt.Errorf("acpext: subagent tracker is unavailable")
	}
	if state, ok := r.shared.tracker.inspect("self", childSessionID); ok {
		return agent.SubagentRunResult{
			SessionID:       state.SessionID,
			DelegationID:    state.DelegationID,
			Agent:           firstNonEmpty(state.Agent, "self"),
			ChildCWD:        state.ChildCWD,
			Assistant:       state.Assistant,
			Error:           state.Error,
			State:           state.State,
			Running:         state.Running,
			ApprovalPending: state.ApprovalPending,
			ToolCallPending: state.ToolCallPending,
			LogSnapshot:     state.LogSnapshot,
			LatestOutput:    state.LatestOutput,
			ProgressSeq:     state.ProgressSeq,
			UpdatedAt:       state.UpdatedAt,
		}, nil
	}
	for _, descAgent := range r.knownAgents() {
		if state, ok := r.shared.tracker.inspect(descAgent, childSessionID); ok {
			return agent.SubagentRunResult{
				SessionID:       state.SessionID,
				DelegationID:    state.DelegationID,
				Agent:           firstNonEmpty(state.Agent, descAgent),
				ChildCWD:        state.ChildCWD,
				Assistant:       state.Assistant,
				Error:           state.Error,
				State:           state.State,
				Running:         state.Running,
				ApprovalPending: state.ApprovalPending,
				ToolCallPending: state.ToolCallPending,
				LogSnapshot:     state.LogSnapshot,
				LatestOutput:    state.LatestOutput,
				ProgressSeq:     state.ProgressSeq,
				UpdatedAt:       state.UpdatedAt,
			}, nil
		}
	}
	if result, ok, err := r.inspectPersisted(ctx, childSessionID); err != nil {
		return agent.SubagentRunResult{}, err
	} else if ok {
		return result, nil
	}
	return agent.SubagentRunResult{}, fmt.Errorf("acpext: delegated child session %q is not tracked in this process", childSessionID)
}

func (r *selfACPSubagentRunner) failedResult(ctx context.Context, childSessionID string, childCreated bool, meta runtime.DelegationMetadata, agentName string, timeout, idleTimeout time.Duration, cause error) (agent.SubagentRunResult, error) {
	status := runtime.RunLifecycleStatusFailed
	if strings.Contains(strings.ToLower(strings.TrimSpace(fmt.Sprint(cause))), "context canceled") {
		status = runtime.RunLifecycleStatusInterrupted
	}
	if strings.TrimSpace(childSessionID) != "" && r != nil && r.shared != nil && r.shared.tracker != nil {
		r.shared.tracker.finish(agentName, childSessionID, meta.DelegationID, "", string(status), "", fmt.Sprint(cause))
	}
	if childCreated || strings.TrimSpace(childSessionID) != "" {
		r.persistFailureState(ctx, childSessionID, meta, status, cause)
	}
	return agent.SubagentRunResult{
		SessionID:    strings.TrimSpace(childSessionID),
		DelegationID: meta.DelegationID,
		Agent:        agentName,
		State:        string(status),
		Timeout:      timeout,
		IdleTimeout:  idleTimeout,
	}, cause
}

func (r *selfACPSubagentRunner) recoverReadyChildTimeout(ctx context.Context, childSessionID string, meta runtime.DelegationMetadata, agentName string, childCWD string, timeout, idleTimeout time.Duration, cause error) (agent.SubagentRunResult, bool) {
	if strings.TrimSpace(childSessionID) == "" {
		return agent.SubagentRunResult{}, false
	}
	if !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return agent.SubagentRunResult{}, false
	}
	result, err := r.InspectSubagent(ctx, childSessionID)
	if err == nil {
		result.Agent = firstNonEmpty(strings.TrimSpace(result.Agent), agentName)
		result.ChildCWD = firstNonEmpty(strings.TrimSpace(result.ChildCWD), childCWD)
		result.Timeout = timeout
		result.IdleTimeout = idleTimeout
		if result.DelegationID == "" {
			result.DelegationID = meta.DelegationID
		}
		if result.Running {
			result.Yielded = true
		}
		return result, true
	}
	return agent.SubagentRunResult{
		SessionID:    strings.TrimSpace(childSessionID),
		DelegationID: meta.DelegationID,
		Agent:        agentName,
		ChildCWD:     strings.TrimSpace(childCWD),
		State:        string(runtime.RunLifecycleStatusRunning),
		Running:      true,
		Yielded:      true,
		Timeout:      timeout,
		IdleTimeout:  idleTimeout,
	}, true
}

func (r *selfACPSubagentRunner) inspectPersisted(ctx context.Context, childSessionID string) (agent.SubagentRunResult, bool, error) {
	if r == nil || r.runtime == nil || r.parent == nil {
		return agent.SubagentRunResult{}, false, nil
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return agent.SubagentRunResult{}, false, nil
	}
	state, err := r.runtime.RunState(ctx, runtime.RunStateRequest{
		AppName:   r.parent.AppName,
		UserID:    r.parent.UserID,
		SessionID: childSessionID,
	})
	if err != nil {
		return agent.SubagentRunResult{}, false, err
	}
	events, err := r.runtime.SessionEvents(ctx, runtime.SessionEventsRequest{
		AppName:          r.parent.AppName,
		UserID:           r.parent.UserID,
		SessionID:        childSessionID,
		IncludeLifecycle: true,
	})
	if err != nil {
		return agent.SubagentRunResult{}, false, err
	}
	if !state.HasLifecycle && len(events) == 0 {
		return agent.SubagentRunResult{}, false, nil
	}
	result := agent.SubagentRunResult{
		SessionID:       childSessionID,
		State:           string(state.Status),
		Running:         state.Status == runtime.RunLifecycleStatusRunning || state.Status == runtime.RunLifecycleStatusWaitingApproval,
		ApprovalPending: state.Status == runtime.RunLifecycleStatusWaitingApproval,
		UpdatedAt:       state.UpdatedAt,
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Time.After(result.UpdatedAt) {
			result.UpdatedAt = ev.Time
		}
		if info, ok := runtime.LifecycleFromEvent(ev); ok {
			if strings.TrimSpace(info.Error) != "" {
				result.Error = strings.TrimSpace(info.Error)
			}
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
	return result, true, nil
}

func (r *selfACPSubagentRunner) emitLifecycleState(ctx context.Context, childSessionID string, meta runtime.DelegationMetadata, agentName string, status runtime.RunLifecycleStatus, cause error) {
	if r == nil || r.parent == nil {
		return
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return
	}
	childSession := &session.Session{
		AppName: r.parent.AppName,
		UserID:  r.parent.UserID,
		ID:      childSessionID,
	}
	ev := runtime.LifecycleEvent(childSession, status, "self_acp", cause)
	if ev == nil {
		return
	}
	ev = annotateAgentEventMeta(annotateDelegationEvent(ev, meta), agentName)
	if r.store != nil {
		if _, err := r.store.GetOrCreate(ctx, childSession); err == nil {
			_ = r.store.AppendEvent(ctx, childSession, ev)
		}
	}
	sessionstream.Emit(ctx, childSessionID, ev)
}

func (m *terminalBridgeManager) observe(ctx context.Context, client acpTerminalOutputClient, tracker *remoteSubagentTracker, sessionID string, agentName string, meta runtime.DelegationMetadata, update acpclient.Update) {
	if m == nil || client == nil || strings.TrimSpace(sessionID) == "" || update == nil {
		return
	}
	callID, toolName, terminalID, active, ok := terminalBridgeSpec(update)
	if !ok {
		return
	}
	key := strings.TrimSpace(callID)
	if key == "" {
		key = strings.TrimSpace(terminalID)
	}
	if key == "" {
		return
	}
	if !active {
		m.stop(key)
		return
	}
	m.mu.Lock()
	if m.pollers == nil {
		m.pollers = map[string]context.CancelFunc{}
	}
	if _, exists := m.pollers[key]; exists {
		m.mu.Unlock()
		return
	}
	shouldStart := len(m.pollers) == 0
	pollCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.pollers[key] = cancel
	m.mu.Unlock()
	if shouldStart && m.onStart != nil {
		m.onStart()
	}
	go m.pollTerminalOutput(pollCtx, client, tracker, sessionID, agentName, meta, key, callID, toolName, terminalID)
}

func (m *terminalBridgeManager) pollTerminalOutput(ctx context.Context, client acpTerminalOutputClient, tracker *remoteSubagentTracker, sessionID string, agentName string, meta runtime.DelegationMetadata, key, callID, toolName, terminalID string) {
	defer m.stop(key)
	defer func() {
		_ = client.TerminalRelease(context.WithoutCancel(ctx), sessionID, terminalID)
	}()
	var cursor int
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		resp, err := client.TerminalOutput(ctx, sessionID, terminalID)
		if err == nil {
			if chunk := terminalOutputDelta(resp.Output, &cursor); chunk != "" {
				if tracker != nil {
					tracker.updateToolOutput(agentName, sessionID, chunk)
				}
				emitTerminalBridgeChunk(ctx, sessionID, agentName, meta, callID, toolName, chunk)
			}
			if resp.ExitStatus != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *terminalBridgeManager) stop(key string) {
	if m == nil || strings.TrimSpace(key) == "" {
		return
	}
	m.mu.Lock()
	cancel, ok := m.pollers[strings.TrimSpace(key)]
	if ok {
		delete(m.pollers, strings.TrimSpace(key))
	}
	shouldStop := ok && len(m.pollers) == 0
	m.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	if shouldStop && m.onStop != nil {
		m.onStop()
	}
}

func (m *terminalBridgeManager) stopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.pollers))
	shouldStop := len(m.pollers) > 0
	for key, cancel := range m.pollers {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
		delete(m.pollers, key)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if shouldStop && m.onStop != nil {
		m.onStop()
	}
}

func terminalBridgeSpec(update acpclient.Update) (callID, toolName, terminalID string, active bool, ok bool) {
	switch typed := update.(type) {
	case acpclient.ToolCallUpdate:
		terminalID = toolCallTerminalID(typed.Content)
		if terminalID == "" {
			return "", "", "", false, false
		}
		callID = strings.TrimSpace(typed.ToolCallID)
		toolName = acpToolDisplayName(strings.TrimSpace(derefString(typed.Title)), strings.TrimSpace(derefString(typed.Kind)))
		status := strings.ToLower(strings.TrimSpace(derefString(typed.Status)))
		return callID, toolName, terminalID, status == internalacp.ToolStatusInProgress || status == "", true
	default:
		return "", "", "", false, false
	}
}

func terminalOutputDelta(output string, cursor *int) string {
	if cursor == nil {
		return output
	}
	if *cursor < 0 || *cursor > len(output) {
		*cursor = 0
	}
	if len(output) <= *cursor {
		return ""
	}
	chunk := output[*cursor:]
	*cursor = len(output)
	return chunk
}

func emitTerminalBridgeChunk(ctx context.Context, sessionID, agentName string, meta runtime.DelegationMetadata, callID, toolName, chunk string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" || chunk == "" {
		return
	}
	ev := &session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   strings.TrimSpace(callID),
			Name: firstNonEmpty(strings.TrimSpace(toolName), "BASH"),
			Result: map[string]any{
				"state":  string(runtime.RunLifecycleStatusRunning),
				"stdout": chunk,
			},
		}),
	}
	sessionstream.Emit(ctx, sessionID, annotateAgentEventMeta(annotateDelegationEvent(ev, meta), agentName))
}

func (r *selfACPSubagentRunner) persistFailureState(ctx context.Context, childSessionID string, meta runtime.DelegationMetadata, status runtime.RunLifecycleStatus, cause error) {
	if r == nil || r.parent == nil {
		return
	}
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return
	}
	r.emitLifecycleState(ctx, childSessionID, meta, "", status, cause)
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

func approvalLifecycleMeta(parentSessionID, childSessionID, delegationID string, base runtime.DelegationMetadata) runtime.DelegationMetadata {
	meta := base
	if meta.ParentSessionID == "" {
		meta.ParentSessionID = strings.TrimSpace(parentSessionID)
	}
	if meta.ChildSessionID == "" {
		meta.ChildSessionID = strings.TrimSpace(childSessionID)
	}
	if meta.DelegationID == "" {
		meta.DelegationID = strings.TrimSpace(delegationID)
	}
	return meta
}

func (r *selfACPSubagentRunner) permissionRequestHandler(ctx context.Context, agentName string, sessionID func() string, meta runtime.DelegationMetadata, childCWD string, onApprovalStart func(), onApprovalDone func()) func(context.Context, acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
	return func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
		delegationID := strings.TrimSpace(meta.DelegationID)
		mode := r.currentParentSessionMode(reqCtx)
		decision := acpclient.ResolveApproveAllOnce(mode, agentName, req)
		if decision.Decision == acpclient.PermissionDecisionAutoAllowOnce {
			if r != nil && r.parent != nil && sessionID != nil {
				r.emitLifecycleState(ctx, sessionID(), approvalLifecycleMeta(r.parent.ID, sessionID(), delegationID, meta), agentName, runtime.RunLifecycleStatusRunning, nil)
			}
			return acpclient.PermissionSelectedOutcome(decision.OptionID), nil
		}

		if decision.Decision == acpclient.PermissionDecisionAskUser {
			reqCtx = toolexec.WithInteractiveApprovalRequired(reqCtx)
		}

		if onApprovalStart != nil {
			onApprovalStart()
		}
		if r != nil && r.shared != nil && r.shared.tracker != nil && sessionID != nil {
			r.shared.tracker.markApprovalPending(agentName, sessionID(), delegationID, childCWD)
		}
		defer func() {
			if onApprovalDone != nil {
				onApprovalDone()
			}
			if r != nil && r.shared != nil && r.shared.tracker != nil && sessionID != nil {
				r.shared.tracker.clearApproval(agentName, sessionID())
			}
		}()
		isToolAuthorization := requestLooksLikeToolAuthorization(req)
		if isToolAuthorization {
			if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
				allowed, err := authorizer.AuthorizeTool(reqCtx, authorizationRequestFromACP(req))
				if err != nil {
					return acpclient.RequestPermissionResponse{}, err
				}
				if allowed && r != nil && r.parent != nil && sessionID != nil {
					r.emitLifecycleState(ctx, sessionID(), approvalLifecycleMeta(r.parent.ID, sessionID(), delegationID, meta), agentName, runtime.RunLifecycleStatusRunning, nil)
				}
				return permissionOutcome(req, allowed), nil
			}
		}
		if approver, ok := toolexec.ApproverFromContext(ctx); ok {
			allowed, err := approver.Approve(reqCtx, approvalRequestFromACP(req))
			if err != nil {
				return acpclient.RequestPermissionResponse{}, err
			}
			if allowed && r != nil && r.parent != nil && sessionID != nil {
				r.emitLifecycleState(ctx, sessionID(), approvalLifecycleMeta(r.parent.ID, sessionID(), delegationID, meta), agentName, runtime.RunLifecycleStatusRunning, nil)
			}
			return permissionOutcome(req, allowed), nil
		}
		if !isToolAuthorization {
			if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
				allowed, err := authorizer.AuthorizeTool(reqCtx, authorizationRequestFromACP(req))
				if err != nil {
					return acpclient.RequestPermissionResponse{}, err
				}
				if allowed && r != nil && r.parent != nil && sessionID != nil {
					r.emitLifecycleState(ctx, sessionID(), approvalLifecycleMeta(r.parent.ID, sessionID(), delegationID, meta), agentName, runtime.RunLifecycleStatusRunning, nil)
				}
				return permissionOutcome(req, allowed), nil
			}
		}
		return permissionOutcome(req, true), nil
	}
}

func maxDuration(values ...time.Duration) time.Duration {
	var out time.Duration
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}

func permissionOutcome(req acpclient.RequestPermissionRequest, allowed bool) acpclient.RequestPermissionResponse {
	return acpclient.PermissionSelectedOutcome(acpclient.SelectPermissionOptionID(req.Options, allowed))
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

func requestLooksLikeToolAuthorization(req acpclient.RequestPermissionRequest) bool {
	return strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path")) != "" ||
		strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "target")) != ""
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
	if sessionID == "" || cancel == nil || r == nil || r.shared == nil {
		return
	}
	r.shared.mu.Lock()
	defer r.shared.mu.Unlock()
	r.shared.active[sessionID] = cancel
}

func (r *selfACPSubagentRunner) unregisterCancel(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || r == nil || r.shared == nil {
		return
	}
	r.shared.mu.Lock()
	defer r.shared.mu.Unlock()
	delete(r.shared.active, sessionID)
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
	registry, err := r.snapshots.snapshot(r.parent.ID)
	if err != nil {
		return appagents.Descriptor{}, err
	}
	if registry == nil {
		registry = appagents.NewRegistry()
	}
	desc, ok := registry.Lookup(agentName)
	if !ok {
		return appagents.Descriptor{}, fmt.Errorf("acpext: unknown agent %q", agentName)
	}
	return desc, nil
}

func (r *selfACPSubagentRunner) resolveSessionTarget(explicitSessionID string) (subagentSessionTarget, error) {
	explicitSessionID = strings.TrimSpace(explicitSessionID)
	if explicitSessionID != "" {
		requested, err := runtime.ResolveChildSessionID(r.parent.ID, explicitSessionID)
		if err != nil {
			return subagentSessionTarget{}, err
		}
		return subagentSessionTarget{
			requestedSessionID: requested,
			childCWD:           r.resolveWorkspaceCWD(),
		}, nil
	}
	return subagentSessionTarget{
		childCWD: r.resolveWorkspaceCWD(),
	}, nil
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

func (r *selfACPSubagentRunner) knownAgents() []string {
	if r == nil || r.snapshots == nil || r.parent == nil {
		return []string{"self"}
	}
	registry, err := r.snapshots.snapshot(r.parent.ID)
	if err != nil || registry == nil {
		return []string{"self"}
	}
	items := registry.List()
	out := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return []string{"self"}
	}
	return out
}
