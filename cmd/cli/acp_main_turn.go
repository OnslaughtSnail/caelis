package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

const (
	metaControllerKind = "controller_kind"
	metaControllerID   = "controller_id"
	metaEpochID        = "epoch_id"
)

type mainACPClient interface {
	Initialize(context.Context) (acpclient.InitializeResponse, error)
	NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error)
	LoadSession(context.Context, string, string, map[string]any) (acpclient.LoadSessionResponse, error)
	SetMode(context.Context, string, string) error
	SetConfigOption(context.Context, string, string, string) (acpclient.SetSessionConfigOptionResponse, error)
	PromptParts(context.Context, string, []json.RawMessage, map[string]any) (acpclient.PromptResponse, error)
	Cancel(context.Context, string) error
	StderrTail(int) string
	Close() error
}

var startMainACPClientHook = func(ctx context.Context, cfg acpclient.Config) (mainACPClient, error) {
	return acpclient.Start(ctx, cfg)
}

// persistentMainACPState holds a reusable ACP client connection that survives
// across turns. This allows continuous multi-turn conversations to use
// session/prompt directly without re-establishing the connection and calling
// session/load for every turn.
type persistentMainACPState struct {
	mu              sync.Mutex
	client          mainACPClient
	agentID         string
	remoteSessionID string
	capabilities    acpclient.AgentCapabilities
	modes           *acpclient.SessionModeState
	configOptions   []acpclient.SessionConfigOption
	availableCmds   []acpMainAvailableCommand
	modelProfiles   []acpMainModelProfile
	bootstrapState  mainACPBootstrapState
	closed          bool
	baseOnUpdate    func(acpclient.UpdateEnvelope)
	// onUpdate is the mutable per-turn callback; the client config delegates
	// to this field so it can change between turns without restarting.
	onUpdate func(acpclient.UpdateEnvelope)
}

func (s *persistentMainACPState) dispatchUpdate(env acpclient.UpdateEnvelope) {
	if s == nil {
		return
	}
	s.mu.Lock()
	baseFn := s.baseOnUpdate
	fn := s.onUpdate
	s.mu.Unlock()
	if baseFn != nil {
		baseFn(env)
	}
	if fn != nil {
		fn(env)
	}
}

func (s *persistentMainACPState) setOnUpdate(fn func(acpclient.UpdateEnvelope)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUpdate = fn
}

func (s *persistentMainACPState) isAlive(agentID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.client != nil && s.agentID == agentID
}

func (s *persistentMainACPState) getClient() mainACPClient {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return s.client
}

func (s *persistentMainACPState) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && !s.closed {
		_ = s.client.Close()
	}
	s.closed = true
	s.client = nil
	s.modes = nil
	s.configOptions = nil
	s.modelProfiles = nil
	s.bootstrapState = mainACPBootstrapNone
}

type activeMainACPClient struct {
	mu        sync.RWMutex
	client    mainACPClient
	sessionID string
}

func (r *activeMainACPClient) setClient(client mainACPClient) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.client = client
}

func (r *activeMainACPClient) setSessionID(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = strings.TrimSpace(sessionID)
}

func (r *activeMainACPClient) cancel(ctx context.Context) {
	if r == nil {
		return
	}
	ctx = cliContext(ctx)
	r.mu.RLock()
	client := r.client
	sessionID := r.sessionID
	r.mu.RUnlock()
	if client == nil {
		return
	}
	if sessionID != "" {
		_ = client.Cancel(ctx, sessionID)
		return
	}
	_ = client.Close()
}

type mainACPProjectionTracker struct {
	agentID   string
	projector *acpprojector.LiveProjector
	recorder  *mainACPTurnRecorder
	sessionID string
}

func newMainACPProjectionTracker(agentID string, epochID string) *mainACPProjectionTracker {
	return &mainACPProjectionTracker{
		agentID:   strings.TrimSpace(agentID),
		projector: acpprojector.NewLiveProjector(),
		recorder:  newMainACPTurnRecorder(agentID, epochID),
	}
}

func (t *mainACPProjectionTracker) SeedNarrative(assistant string, reasoning string) {
	if t == nil || t.projector == nil {
		return
	}
	t.projector.SeedNarrative(assistant, reasoning)
}

func (t *mainACPProjectionTracker) Project(env acpclient.UpdateEnvelope) []acpprojector.Projection {
	if t == nil || t.projector == nil {
		return nil
	}
	if sessionID := strings.TrimSpace(env.SessionID); sessionID != "" {
		t.sessionID = sessionID
	}
	raw := t.projector.Project(env)
	for _, item := range raw {
		t.recorder.Observe(item)
	}
	return raw
}

func (t *mainACPProjectionTracker) Events() []*session.Event {
	if t == nil || t.recorder == nil {
		return nil
	}
	return t.recorder.Events()
}

type mainACPTurnRecorder struct {
	agentID string
	epochID string
	mu      sync.Mutex
	events  []*session.Event
}

func newMainACPTurnRecorder(agentID string, epochID string) *mainACPTurnRecorder {
	return &mainACPTurnRecorder{
		agentID: strings.TrimSpace(agentID),
		epochID: strings.TrimSpace(epochID),
	}
}

func (r *mainACPTurnRecorder) Observe(item acpprojector.Projection) {
	if r == nil || item.Event == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case item.Stream == "assistant" || item.Stream == "reasoning":
		r.appendNarrative(item)
	case item.ToolCallID != "" && item.ToolStatus == "":
		ev := session.CloneEvent(item.Event)
		ev.Time = time.Now()
		r.events = append(r.events, session.MarkMirror(annotateMainACPEvent(ev, r.agentID, r.epochID)))
	case item.ToolStatus == "completed" || item.ToolStatus == "failed":
		ev := session.CloneEvent(item.Event)
		ev.Time = time.Now()
		if ev.Meta == nil {
			ev.Meta = map[string]any{}
		}
		ev.Meta["acp_tool_status"] = strings.ToLower(strings.TrimSpace(item.ToolStatus))
		r.events = append(r.events, session.MarkMirror(annotateMainACPEvent(ev, r.agentID, r.epochID)))
	}
}

func (r *mainACPTurnRecorder) appendNarrative(item acpprojector.Projection) {
	text := item.DeltaText
	if strings.TrimSpace(text) == "" {
		return
	}
	if n := len(r.events); n > 0 {
		last := r.events[n-1]
		switch item.Stream {
		case "assistant":
			if canMergeMainACPAssistantText(last) {
				last.Message = model.NewTextMessage(model.RoleAssistant, last.Message.TextContent()+text)
				return
			}
		case "reasoning":
			if canMergeMainACPReasoningText(last) {
				last.Message = model.NewReasoningMessage(model.RoleAssistant, last.Message.ReasoningText()+text, model.ReasoningVisibilityVisible)
				return
			}
		}
	}
	if ev := mainACPNarrativeEvent(item); ev != nil {
		r.events = append(r.events, annotateMainACPEvent(ev, r.agentID, r.epochID))
	}
}

func (r *mainACPTurnRecorder) Events() []*session.Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*session.Event, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, session.CloneEvent(ev))
	}
	return out
}

func mainACPNarrativeEvent(item acpprojector.Projection) *session.Event {
	switch item.Stream {
	case "assistant":
		if strings.TrimSpace(item.DeltaText) == "" {
			return nil
		}
		return &session.Event{
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleAssistant, item.DeltaText),
		}
	case "reasoning":
		if strings.TrimSpace(item.DeltaText) == "" {
			return nil
		}
		return &session.Event{
			Time:    time.Now(),
			Message: model.NewReasoningMessage(model.RoleAssistant, item.DeltaText, model.ReasoningVisibilityVisible),
		}
	default:
		return nil
	}
}

func annotateMainACPEvent(ev *session.Event, agentID string, epochID string) *session.Event {
	if ev == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ev
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta["agent_id"] = agentID
	ev.Meta["_ui_agent"] = agentID
	ev.Meta[metaControllerKind] = coreacpmeta.ControllerKindACP
	ev.Meta[metaControllerID] = agentID
	if strings.TrimSpace(epochID) != "" {
		ev.Meta[metaEpochID] = epochID
	}
	return ev
}

func annotateControllerEpochEvent(ev *session.Event, epoch coreacpmeta.ControllerEpoch) *session.Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if kind := strings.TrimSpace(epoch.ControllerKind); kind != "" {
		ev.Meta[metaControllerKind] = kind
	}
	if controllerID := strings.TrimSpace(epoch.ControllerID); controllerID != "" {
		ev.Meta[metaControllerID] = controllerID
	}
	if epochID := strings.TrimSpace(epoch.EpochID); epochID != "" {
		ev.Meta[metaEpochID] = epochID
	}
	return ev
}

func canMergeMainACPAssistantText(ev *session.Event) bool {
	if ev == nil || ev.Message.Role != model.RoleAssistant {
		return false
	}
	if len(ev.Message.ToolCalls()) > 0 || ev.Message.ToolResponse() != nil {
		return false
	}
	return ev.Message.ReasoningText() == ""
}

func canMergeMainACPReasoningText(ev *session.Event) bool {
	if ev == nil || ev.Message.Role != model.RoleAssistant {
		return false
	}
	if len(ev.Message.ToolCalls()) > 0 || ev.Message.ToolResponse() != nil {
		return false
	}
	return ev.Message.TextContent() == ""
}

func previousMainACPTurnNarrative(events []*session.Event, agentID string) (assistant string, reasoning string) {
	agentID = strings.TrimSpace(agentID)
	if len(events) == 0 || agentID == "" {
		return "", ""
	}
	lastUserIndex := -1
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if session.EventTypeOf(ev) == session.EventTypeCompaction {
			continue
		}
		if ev.Message.Role == model.RoleUser {
			lastUserIndex = i
			break
		}
	}
	start := 0
	if lastUserIndex >= 0 {
		start = lastUserIndex + 1
	}
	for i := start; i < len(events); i++ {
		ev := events[i]
		if ev == nil || session.EventTypeOf(ev) == session.EventTypeCompaction || session.IsTransient(ev) || session.IsMirror(ev) {
			continue
		}
		if !strings.EqualFold(eventACPAgentID(ev), agentID) {
			continue
		}
		if text := ev.Message.TextContent(); strings.TrimSpace(text) != "" {
			assistant += text
		}
		if text := ev.Message.ReasoningText(); strings.TrimSpace(text) != "" {
			reasoning += text
		}
	}
	return strings.TrimSpace(assistant), strings.TrimSpace(reasoning)
}

func (c *cliConsole) runPreparedACPMainSubmissionContext(ctx context.Context, prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx = cliContext(ctx)
	ctx = toolexec.WithApprover(ctx, c.approver)
	ctx = policy.WithToolAuthorizer(ctx, c.approver)
	ctx = withMainACPProjectionState(ctx)

	gw, err := c.sessionGateway()
	if err != nil {
		return err
	}
	started, err := gw.StartSession(ctx, appgateway.StartSessionRequest{
		Channel:            c.gatewayChannel(),
		PreferredSessionID: c.sessionID,
	})
	if err != nil {
		return err
	}
	c.sessionID = started.SessionID
	rootSession := c.currentSessionRef()
	if rootSession == nil {
		return fmt.Errorf("main ACP: session is unavailable")
	}
	desc := prepared.mainACP.descriptor
	agentID := strings.TrimSpace(desc.ID)
	stateCtx := session.WithStateContext(ctx, rootSession, c.sessionStore)

	epochID, _, _, err := c.prepareMainControllerTurn(stateCtx, coreacpmeta.ControllerKindACP, agentID)
	if err != nil {
		return err
	}

	// Build any handoff state from the fully settled pre-turn history. This
	// must happen after controller transition/checkpointing, but before the
	// current user turn is appended, otherwise the current request gets
	// duplicated inside the handoff bundle.
	history, err := c.sessionStore.ListEvents(stateCtx, rootSession)
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		return err
	}

	// Ensure the persistent ACP client is alive and connected.
	client, freshClient, err := c.ensurePersistentMainACPClient(stateCtx, desc)
	if err != nil {
		return err
	}
	seedAssistant, seedReasoning := previousMainACPTurnNarrative(history, agentID)
	tracker := newMainACPProjectionTracker(agentID, epochID)
	if seedAssistant != "" || seedReasoning != "" {
		tracker.SeedNarrative(seedAssistant, seedReasoning)
	}

	updateCtx := context.WithoutCancel(stateCtx)
	// The OnUpdate is set per-turn via the tracker's forwarding.
	onUpdateFn := func(env acpclient.UpdateEnvelope) {
		c.forwardMainACPClientUpdate(updateCtx, tracker, env)
	}

	sessionMeta, err := c.mainACPSessionMeta(stateCtx)
	if err != nil {
		return err
	}

	// Determine remote session strategy:
	// - persistentMainACP has a live session → reuse directly (no LoadSession)
	// - persistentMainACP has no session → check stored session for possible
	//   reconnect (LoadSession only), else NewSession
	remoteSessionID, freshSession, reconnected, err := c.ensureMainACPRemoteSession(stateCtx, client, agentID, sessionMeta, freshClient, false)
	if err != nil {
		c.closePersistentMainACP()
		return mainACPClientError(err, client)
	}
	if freshSession {
		tracker = newMainACPProjectionTracker(agentID, epochID)
	}

	// Load remote sync state to decide handoff mode.
	syncState, err := coreacpmeta.RemoteSyncStateFromStore(stateCtx, c.sessionStore, rootSession)
	if err != nil {
		return err
	}
	handoffCoordinator := c.handoffCoordinator()
	if handoffCoordinator == nil {
		return fmt.Errorf("main ACP: handoff coordinator is unavailable")
	}

	// Build and inject handoff via Epoch Handoff Layer.
	prompt := marshalMainACPPromptParts(submission)
	transcriptTail := buildHandoffTranscriptTail(history)
	var (
		handoffNeedsSyncUpdate bool
		handoffWatermark       string
		handoffHash            string
	)
	if freshSession {
		// Full handoff for new remote sessions — use empty sync state to force full.
		bundle, bundleErr := handoffCoordinator.BuildHandoffBundle(stateCtx, rootSession, agentID, coreacpmeta.RemoteSyncState{}, transcriptTail)
		if bundleErr != nil {
			return bundleErr
		}
		if text := bundle.RenderLLMView(); text != "" {
			prompt = prependMainACPTextBlock(prompt, text)
		}
		// Update sync state waterline.
		handoffWatermark = bundle.SyncWatermarkEventID
		if len(bundle.Checkpoints) > 0 {
			handoffHash = bundle.Checkpoints[0].System.Hash
		}
		handoffNeedsSyncUpdate = true
	} else if reconnected {
		// Incremental handoff for reconnected sessions with existing sync state.
		bundle, bundleErr := handoffCoordinator.BuildHandoffBundle(stateCtx, rootSession, agentID, syncState, transcriptTail)
		if bundleErr != nil {
			return bundleErr
		}
		if text := bundle.RenderLLMView(); text != "" {
			prompt = prependMainACPTextBlock(prompt, text)
		}
		handoffWatermark = bundle.SyncWatermarkEventID
		if len(bundle.Checkpoints) > 0 {
			handoffHash = bundle.Checkpoints[0].System.Hash
		}
		handoffNeedsSyncUpdate = true
	}
	// For continuous multi-turn (not fresh, not reconnected): no handoff needed.
	// The remote ACP session already has the context from the previous turn.

	if c.persistentMainACP != nil && !c.persistentMainACP.capabilities.Prompt.Image {
		prompt = filterMainACPImageBlocks(prompt)
	}
	if len(prompt) == 0 {
		return fmt.Errorf("main ACP: prompt is empty")
	}

	userEvent := &session.Event{
		Time:    time.Now(),
		Message: model.MessageFromTextAndContentParts(model.RoleUser, submission.Text, submission.ContentParts),
	}
	if err := c.appendSessionEvent(ctx, rootSession, userEvent); err != nil {
		return err
	}
	if err := c.persistSessionModelAlias(ctx); err != nil {
		return err
	}

	c.emitMainACPTurnStart(stateCtx, remoteSessionID)

	// Set the per-turn OnUpdate callback.
	if c.persistentMainACP != nil {
		c.persistentMainACP.setOnUpdate(onUpdateFn)
	}

	runCtx, cancel := context.WithCancel(stateCtx)
	interruptCtx := context.WithoutCancel(stateCtx)
	runState := &activeMainACPClient{}
	runState.setClient(client)
	runState.setSessionID(remoteSessionID)
	c.setActiveRunCancel(func() {
		cancel()
		runState.cancel(interruptCtx)
	})
	defer func() {
		cancel()
		c.clearActiveRun()
	}()

	if _, err := client.PromptParts(runCtx, remoteSessionID, prompt, sessionMeta); err != nil {
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		// If PromptParts fails, the persistent client may be dead.
		c.closePersistentMainACP()
		return mainACPClientError(err, client)
	}
	if handoffNeedsSyncUpdate {
		if err := handoffCoordinator.UpdateSyncWaterline(stateCtx, rootSession, agentID, remoteSessionID, epochID, handoffWatermark, handoffHash); err != nil {
			return err
		}
	}

	for _, ev := range tracker.Events() {
		if err := c.appendSessionEvent(stateCtx, rootSession, ev); err != nil {
			return err
		}
	}
	return nil
}

func (c *cliConsole) forwardMainACPClientUpdate(ctx context.Context, tracker *mainACPProjectionTracker, env acpclient.UpdateEnvelope) {
	if c == nil || tracker == nil || env.Update == nil {
		return
	}
	if c.updateMainACPStateFromUpdate(env) {
		c.syncTUIStatus()
	}
	for _, item := range tracker.Project(env) {
		c.emitMainACPProjectionMsg(ctx, projectionToACPMsg(item, tuievents.ACPProjectionMain, c.sessionID, ""))
	}
}

func (c *cliConsole) mainACPSessionMeta(ctx context.Context) (map[string]any, error) {
	if c == nil || c.sessionStore == nil || c.currentSessionRef() == nil {
		return map[string]any{}, nil
	}
	values, err := c.sessionStore.SnapshotState(ctx, c.currentSessionRef())
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		return nil, err
	}
	return coreacpmeta.SessionMetaFromState(values), nil
}

func (c *cliConsole) ensureMainACPRemoteSession(ctx context.Context, client mainACPClient, agentID string, sessionMeta map[string]any, freshClient bool, promptless bool) (string, bool, bool, error) {
	// If persistent state already has a remote session for this agent,
	// reuse it directly — no LoadSession needed for continuous multi-turn.
	if c.persistentMainACP != nil && !freshClient {
		existing, _, _, _ := c.persistentMainACP.snapshotSessionState()
		if existing != "" {
			if promptless {
				return existing, false, false, nil
			}
			switch c.persistentMainACP.consumeBootstrapState(existing) {
			case mainACPBootstrapFresh:
				return existing, true, false, nil
			case mainACPBootstrapReconnected:
				return existing, false, true, nil
			default:
				return existing, false, false, nil
			}
		}
	}

	// Check stored session for reconnect after app restart / fresh client start.
	storedSessionID, hasStoredSession, err := c.currentSessionACPRemoteSession(ctx, agentID)
	if err != nil {
		return "", false, false, err
	}
	sessionCWD := firstNonEmptyString(c.workspace.CWD, c.workspaceRoot)

	// Attempt LoadSession only when we have a stored session for the same agent
	// AND we just started a fresh client (reconnect scenario).
	if freshClient && hasStoredSession {
		if resp, err := client.LoadSession(ctx, storedSessionID, sessionCWD, sessionMeta); err == nil {
			remoteSessionID := strings.TrimSpace(storedSessionID)
			if c.persistentMainACP != nil {
				bootstrap := mainACPBootstrapNone
				if promptless {
					bootstrap = mainACPBootstrapReconnected
				}
				c.persistentMainACP.storeSessionState(remoteSessionID, resp.Modes, resp.ConfigOptions, bootstrap)
			}
			return remoteSessionID, false, !promptless, nil
		}
		// LoadSession failed — fall through to NewSession.
	}

	// Create a new remote session.
	resp, err := client.NewSession(ctx, sessionCWD, sessionMeta)
	if err != nil {
		return "", false, false, err
	}
	sessionID := strings.TrimSpace(resp.SessionID)
	if sessionID == "" {
		return "", false, false, fmt.Errorf("main ACP: remote session id is empty")
	}
	if err := c.persistMainACPRemoteSession(ctx, agentID, sessionID); err != nil {
		return "", false, false, err
	}
	if c.persistentMainACP != nil {
		bootstrap := mainACPBootstrapNone
		if promptless {
			bootstrap = mainACPBootstrapFresh
		}
		c.persistentMainACP.storeSessionState(sessionID, resp.Modes, resp.ConfigOptions, bootstrap)
	}
	return sessionID, !promptless, false, nil
}

// ensurePersistentMainACPClient returns the persistent ACP client, starting a
// new one if the agent changed or the previous client died.
func (c *cliConsole) ensurePersistentMainACPClient(ctx context.Context, desc appagents.Descriptor) (mainACPClient, bool, error) {
	agentID := strings.TrimSpace(desc.ID)
	if c.persistentMainACP != nil && c.persistentMainACP.isAlive(agentID) {
		return c.persistentMainACP.getClient(), false, nil
	}
	// Close stale client if agent changed.
	c.closePersistentMainACP()

	state := &persistentMainACPState{
		agentID: agentID,
		baseOnUpdate: func(env acpclient.UpdateEnvelope) {
			if c.updateMainACPStateFromUpdate(env) {
				c.syncTUIStatus()
				if _, ok := env.Update.(acpclient.AvailableCommandsUpdate); ok {
					c.notifyCommandListChanged()
				}
			}
		},
	}
	client, err := startMainACPClientHook(ctx, acpclient.Config{
		Command:    strings.TrimSpace(desc.Command),
		Args:       append([]string(nil), desc.Args...),
		Env:        copyStringMap(desc.Env),
		WorkDir:    c.resolveExternalAgentWorkDir(desc),
		Runtime:    c.executionRuntimeForSession(),
		Workspace:  firstNonEmptyString(c.workspaceRoot, c.workspace.CWD),
		ClientInfo: acpclient.DefaultClientInfo(c.version),
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			state.dispatchUpdate(env)
		},
		OnPermissionRequest: func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
			return c.handleExternalPermissionRequest(reqCtx, req, agentID, nil)
		},
	})
	if err != nil {
		return nil, false, mainACPClientError(err, nil)
	}
	initResp, err := client.Initialize(ctx)
	if err != nil {
		_ = client.Close()
		return nil, false, mainACPClientError(err, client)
	}
	state.client = client
	state.capabilities = initResp.AgentCapabilities
	c.persistentMainACP = state
	return client, true, nil
}

// closePersistentMainACP shuts down the persistent ACP client if one exists.
func (c *cliConsole) closePersistentMainACP() {
	if c == nil || c.persistentMainACP == nil {
		return
	}
	c.persistentMainACP.close()
	c.persistentMainACP = nil
}

// advanceControllerEpoch bumps the epoch when the controller kind or ID changes.
func (c *cliConsole) advanceControllerEpoch(ctx context.Context, kind string, controllerID string) (string, error) {
	rootSession := c.currentSessionRef()
	if rootSession == nil {
		return "1", nil
	}
	current, err := coreacpmeta.ControllerEpochFromStore(ctx, c.sessionStore, rootSession)
	if err != nil {
		return "1", err
	}
	// If same controller kind and ID, reuse current epoch.
	if current.ControllerKind == kind && current.ControllerID == controllerID && current.EpochID != "" {
		return current.EpochID, nil
	}
	next := coreacpmeta.ControllerEpoch{
		EpochID:        coreacpmeta.NextEpochID(current.EpochID),
		ControllerKind: kind,
		ControllerID:   controllerID,
	}
	if err := coreacpmeta.UpdateControllerEpoch(ctx, c.sessionStore, rootSession, func(_ coreacpmeta.ControllerEpoch) coreacpmeta.ControllerEpoch {
		return next
	}); err != nil {
		return next.EpochID, err
	}
	return next.EpochID, nil
}

func marshalMainACPPromptParts(submission runtime.Submission) []json.RawMessage {
	if len(submission.ContentParts) > 0 {
		return marshalMainACPContentParts(submission.ContentParts)
	}
	text := strings.TrimSpace(submission.Text)
	if text == "" {
		return nil
	}
	return []json.RawMessage{mustMarshalMainACPContent(acpclient.TextContent{
		Type: "text",
		Text: text,
	})}
}

func marshalMainACPContentParts(parts []model.ContentPart) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case model.ContentPartText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			out = append(out, mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: part.Text,
			}))
		case model.ContentPartImage:
			if strings.TrimSpace(part.Data) == "" && strings.TrimSpace(part.MimeType) == "" && strings.TrimSpace(part.FileName) == "" {
				continue
			}
			out = append(out, mustMarshalMainACPContent(acpclient.ImageContent{
				Type:     "image",
				MimeType: strings.TrimSpace(part.MimeType),
				Data:     part.Data,
				Name:     strings.TrimSpace(part.FileName),
			}))
		}
	}
	return out
}

func mustMarshalMainACPContent(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func prependMainACPTextBlock(prompt []json.RawMessage, text string) []json.RawMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return append([]json.RawMessage(nil), prompt...)
	}
	out := []json.RawMessage{mustMarshalMainACPContent(acpclient.TextContent{
		Type: "text",
		Text: text,
	})}
	return append(out, prompt...)
}

func filterMainACPImageBlocks(prompt []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(prompt))
	for _, block := range prompt {
		if len(block) == 0 {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(block, &header); err != nil {
			out = append(out, block)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(header.Type), "image") {
			continue
		}
		out = append(out, block)
	}
	return out
}

func mainACPClientError(err error, client mainACPClient) error {
	if err == nil {
		return nil
	}
	stderr := ""
	if client != nil {
		stderr = strings.TrimSpace(client.StderrTail(4096))
	}
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%s\n%s", truncateInline(err.Error(), 160), tailLines(stderr, 6))
}
