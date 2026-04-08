package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type runOccupancy string

const (
	runOccupancyNone          runOccupancy = ""
	runOccupancyMainSession   runOccupancy = "main_session"
	runOccupancyExternalAgent runOccupancy = "external_agent"
)

var errExternalAgentRunBusy = errors.New("an external agent run is active; wait for it to finish or interrupt it first")

type activeExternalAgentRun struct {
	mu        sync.RWMutex
	client    externalSlashACPClient
	sessionID string
	callID    string
}

type externalAgentTurnMode string

const (
	externalAgentTurnNew  externalAgentTurnMode = "new"
	externalAgentTurnLoad externalAgentTurnMode = "load"
)

const externalACPStartupTimeout = 45 * time.Second

type externalSlashACPClient interface {
	Initialize(context.Context) (acpclient.InitializeResponse, error)
	NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error)
	LoadSession(context.Context, string, string, map[string]any) (acpclient.LoadSessionResponse, error)
	Prompt(context.Context, string, string, map[string]any) (acpclient.PromptResponse, error)
	Cancel(context.Context, string) error
	StderrTail(int) string
	Close() error
}

var startExternalSlashACPClientHook = func(c *cliConsole, ctx context.Context, runState *activeExternalAgentRun, desc appagents.Descriptor, turn *externalAgentTurn) (externalSlashACPClient, func(), error) {
	return c.startExternalSlashACPClient(ctx, runState, desc, turn)
}

type externalAgentProjector interface {
	Project(acpclient.UpdateEnvelope) []acpprojector.Projection
	Snapshot() (assistant string, reasoning string)
}

type externalAgentTurn struct {
	mode        externalAgentTurnMode
	desc        appagents.Descriptor
	participant externalParticipant
	routeText   string
	promptText  string
	routeKind   string
	callID      string
	runState    *activeExternalAgentRun
	runCancel   context.CancelFunc

	projector externalAgentProjector
	ready     atomic.Bool

	sawAssistantStream atomic.Bool
	sawReasoningStream atomic.Bool
}

func (t *externalAgentTurn) stop() {
	if t == nil {
		return
	}
	if t.runState != nil {
		t.runState.cancel()
	}
	if t.runCancel != nil {
		t.runCancel()
	}
}

func (r *activeExternalAgentRun) setClient(client externalSlashACPClient) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.client = client
}

func (r *activeExternalAgentRun) setSessionID(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = strings.TrimSpace(sessionID)
}

func (r *activeExternalAgentRun) setCallID(callID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callID = strings.TrimSpace(callID)
}

func (r *activeExternalAgentRun) cancel() {
	if r == nil {
		return
	}
	r.mu.RLock()
	client := r.client
	sessionID := r.sessionID
	r.mu.RUnlock()
	if client == nil {
		return
	}
	if sessionID != "" {
		_ = client.Cancel(context.Background(), sessionID)
		return
	}
	_ = client.Close()
}

func (c *cliConsole) currentRunKind() runOccupancy {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if c.activeRunCancel == nil && c.activeRunner == nil {
		return runOccupancyNone
	}
	return c.activeRunKind
}

func (c *cliConsole) setActiveExternalRun(cancel context.CancelFunc) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = cancel
	c.activeRunner = nil
	if cancel == nil {
		c.activeRunKind = runOccupancyNone
		return
	}
	c.activeRunKind = runOccupancyExternalAgent
}

func (c *cliConsole) availableCommandNames() []string {
	names := map[string]struct{}{}
	for name := range c.commands {
		names[name] = struct{}{}
	}
	for _, item := range c.dynamicSlashAgents() {
		names[item.ID] = struct{}{}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (c *cliConsole) dynamicSlashAgents() []appagents.Descriptor {
	if c == nil {
		return nil
	}
	registry := c.agentRegistry
	if c.configStore != nil {
		if reg, err := c.configStore.AgentRegistry(); err == nil && reg != nil {
			registry = reg
		}
	}
	if registry == nil {
		return nil
	}
	out := make([]appagents.Descriptor, 0)
	for _, item := range registry.List() {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		if id == "" || item.Transport != appagents.TransportACP {
			continue
		}
		if !looksLikeSlashCommandToken(id) {
			continue
		}
		if _, reserved := c.commands[id]; reserved {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *cliConsole) dynamicSlashAgentDescriptor(id string) (appagents.Descriptor, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return appagents.Descriptor{}, false
	}
	for _, item := range c.dynamicSlashAgents() {
		if strings.EqualFold(strings.TrimSpace(item.ID), id) {
			return item, true
		}
	}
	return appagents.Descriptor{}, false
}

func (c *cliConsole) notifyCommandListChanged() {
	if c == nil || c.tuiSender == nil {
		return
	}
	c.tuiSender.Send(tuievents.SetCommandsMsg{Commands: c.availableCommandNames()})
}

func (c *cliConsole) runExternalAgentSlashContext(ctx context.Context, desc appagents.Descriptor, prompt string) error {
	ctx = cliContext(ctx)
	prompt, err := c.prepareExternalParticipantPrompt(prompt)
	if err != nil {
		return err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("usage: /%s <prompt>", strings.TrimSpace(desc.ID))
	}
	switch c.currentRunKind() {
	case runOccupancyMainSession:
		return fmt.Errorf("/%s is only available while the main session is idle", strings.TrimSpace(desc.ID))
	case runOccupancyExternalAgent:
		return errExternalAgentRunBusy
	}
	participants, err := c.loadSessionParticipants(ctx)
	if err != nil {
		return err
	}
	alias := nextExternalParticipantAlias(c.sessionID, desc.ID, participants)
	participant := externalParticipant{
		Alias:        alias,
		AgentID:      strings.TrimSpace(desc.ID),
		DisplayLabel: participantDisplayLabel(alias, desc.ID),
		Status:       "running",
	}
	participant.DisplayLabel = participantDisplayLabel(participant.Alias, participant.AgentID)
	routeText := "/" + strings.TrimSpace(desc.ID) + " " + prompt
	return c.startExternalAgentTurnAsyncContext(ctx, &externalAgentTurn{
		mode:        externalAgentTurnNew,
		desc:        desc,
		participant: participant,
		routeText:   routeText,
		promptText:  prompt,
		routeKind:   "slash_create",
		callID:      newExternalAgentCallID(),
		runState:    &activeExternalAgentRun{},
		projector:   acpprojector.NewLiveProjector(),
	})
}

func (c *cliConsole) routeExternalParticipantContext(ctx context.Context, alias string, prompt string) error {
	ctx = cliContext(ctx)
	prompt, err := c.prepareExternalParticipantPrompt(prompt)
	if err != nil {
		return err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("usage: @%s <prompt>", strings.TrimSpace(alias))
	}
	switch c.currentRunKind() {
	case runOccupancyMainSession:
		return fmt.Errorf("@%s is only available while the main session is idle", strings.TrimSpace(alias))
	case runOccupancyExternalAgent:
		return errExternalAgentRunBusy
	}
	participant, ok, err := c.lookupParticipantByAlias(ctx, alias)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown participant @%s; use /gemini, /claude, or /codex to create one", strings.TrimSpace(alias))
	}
	desc, ok := c.dynamicSlashAgentDescriptor(participant.AgentID)
	if !ok {
		return fmt.Errorf("configured agent %q is unavailable", participant.AgentID)
	}
	return c.startExternalAgentTurnAsyncContext(ctx, &externalAgentTurn{
		mode:        externalAgentTurnLoad,
		desc:        desc,
		participant: participant,
		routeText:   "@" + participant.Alias + " " + prompt,
		promptText:  prompt,
		routeKind:   "participant_route",
		callID:      newExternalAgentCallID(),
		runState:    &activeExternalAgentRun{},
		projector:   acpprojector.NewLiveProjector(),
	})
}

func (c *cliConsole) prepareExternalParticipantPrompt(prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if c == nil || c.inputRefs == nil || prompt == "" {
		return prompt, nil
	}
	result, err := c.inputRefs.RewriteInput(prompt)
	if err != nil {
		return prompt, err
	}
	return strings.TrimSpace(result.Text), nil
}

func (c *cliConsole) startExternalAgentTurnAsyncContext(ctx context.Context, turn *externalAgentTurn) error {
	if c == nil || turn == nil {
		return fmt.Errorf("console is unavailable")
	}
	runCtx, cancelCtx := context.WithCancel(cliContext(ctx))
	turn.runCancel = cancelCtx
	if turn.runState != nil {
		turn.runState.setCallID(turn.callID)
	}
	c.setActiveExternalRun(turn.stop)

	go func() {
		defer c.clearActiveRun()
		err := c.runExternalAgentTurnOnce(runCtx, turn)
		switch {
		case err == nil:
			_ = c.updateExternalParticipantProjectionStatus(runCtx, turn, "completed")
			if c.tuiSender != nil && strings.TrimSpace(turn.participant.ChildSessionID) != "" {
				c.tuiSender.Send(tuievents.ParticipantStatusMsg{
					SessionID: turn.participant.ChildSessionID,
					State:     "completed",
				})
			}
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.TaskResultMsg{SuppressTurnDivider: true})
			}
		case errors.Is(err, context.Canceled):
			_ = c.updateExternalParticipantProjectionStatus(runCtx, turn, "interrupted")
			if c.tuiSender != nil && strings.TrimSpace(turn.participant.ChildSessionID) != "" {
				c.tuiSender.Send(tuievents.ParticipantStatusMsg{
					SessionID: turn.participant.ChildSessionID,
					State:     "interrupted",
				})
			}
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.TaskResultMsg{Interrupted: true, SuppressTurnDivider: true})
			}
		default:
			_ = c.updateExternalParticipantProjectionStatus(runCtx, turn, "failed")
			if c.tuiSender != nil && strings.TrimSpace(turn.participant.ChildSessionID) != "" {
				c.tuiSender.Send(tuievents.ParticipantStatusMsg{
					SessionID: turn.participant.ChildSessionID,
					State:     "failed",
				})
			}
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.TaskResultMsg{Err: err, SuppressTurnDivider: true})
			}
		}
	}()
	return nil
}

func (c *cliConsole) runExternalAgentTurnOnce(ctx context.Context, turn *externalAgentTurn) error {
	if turn == nil {
		return fmt.Errorf("external agent turn is unavailable")
	}
	ctx = cliContext(ctx)
	persistCtx := context.WithoutCancel(ctx)
	if _, err := c.ensureSessionRecord(ctx, c.sessionID); err != nil {
		return err
	}
	client, cleanup, err := startExternalSlashACPClientHook(c, ctx, turn.runState, turn.desc, turn)
	if err != nil {
		return err
	}
	defer cleanup()

	initCtx, initCancel := externalACPStartupContext(ctx, turn.desc.ID)
	defer initCancel()
	if _, err := client.Initialize(initCtx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return externalACPStageError(turn.desc.ID, "initialize", err, client)
	}

	switch turn.mode {
	case externalAgentTurnNew:
		stageCtx, cancel := externalACPStartupContext(ctx, turn.desc.ID)
		sessionResp, err := client.NewSession(stageCtx, c.resolveExternalAgentWorkDir(turn.desc), nil)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return externalACPStageError(turn.desc.ID, "session/new", err, client)
		}
		turn.participant.ChildSessionID = strings.TrimSpace(sessionResp.SessionID)
	case externalAgentTurnLoad:
		stageCtx, cancel := externalACPStartupContext(ctx, turn.desc.ID)
		if _, err := client.LoadSession(stageCtx, strings.TrimSpace(turn.participant.ChildSessionID), c.resolveExternalAgentWorkDir(turn.desc), nil); err != nil {
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return externalACPStageError(turn.desc.ID, "session/load", err, client)
		}
		cancel()
	default:
		return fmt.Errorf("unsupported external turn mode %q", turn.mode)
	}
	sessionID := strings.TrimSpace(turn.participant.ChildSessionID)
	if sessionID == "" {
		return fmt.Errorf("external agent session id is empty")
	}
	turn.runState.setSessionID(sessionID)
	turn.participant.DisplayLabel = participantDisplayLabel(turn.participant.Alias, turn.participant.AgentID)
	turn.participant.LastActiveAt = time.Now()
	if turn.participant.CreatedAt.IsZero() {
		turn.participant.CreatedAt = turn.participant.LastActiveAt
	}
	if err := c.registerExternalParticipant(ctx, turn.participant); err != nil {
		return err
	}
	if err := c.initializeExternalParticipantProjectionTurn(ctx, turn); err != nil {
		return err
	}
	if err := c.persistExternalParticipantRoute(ctx, turn); err != nil {
		return err
	}
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.ParticipantTurnStartMsg{
			SessionID: sessionID,
			Actor:     turn.participant.DisplayLabel,
		})
		c.tuiSender.Send(tuievents.ParticipantStatusMsg{
			SessionID: sessionID,
			State:     "prompting",
		})
	}
	_ = c.updateExternalParticipantProjectionStatus(ctx, turn, "prompting")
	turn.ready.Store(true)

	if _, err := client.Prompt(ctx, sessionID, turn.promptText, nil); err != nil {
		if ctx.Err() != nil {
			_ = c.updateParticipantStatus(persistCtx, sessionID, "interrupted")
			c.finalizeExternalTurnStreams(persistCtx, turn, true)
			return ctx.Err()
		}
		_ = c.updateParticipantStatus(persistCtx, sessionID, "failed")
		c.finalizeExternalTurnStreams(persistCtx, turn, true)
		return externalAgentRunError(err, client)
	}
	c.finalizeExternalTurnStreams(persistCtx, turn, false)
	_ = c.updateParticipantStatus(persistCtx, sessionID, "completed")
	return nil
}

func (c *cliConsole) startExternalSlashACPClient(ctx context.Context, runState *activeExternalAgentRun, desc appagents.Descriptor, turn *externalAgentTurn) (externalSlashACPClient, func(), error) {
	ctx = cliContext(ctx)
	updateCtx := context.WithoutCancel(ctx)
	execRuntime := c.executionRuntimeForSession()
	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:    strings.TrimSpace(desc.Command),
		Args:       append([]string(nil), desc.Args...),
		Env:        copyStringMap(desc.Env),
		WorkDir:    c.resolveExternalAgentWorkDir(desc),
		Runtime:    execRuntime,
		Workspace:  c.workspace.CWD,
		ClientInfo: acpclient.DefaultClientInfo(c.version),
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			if turn == nil || !turn.ready.Load() {
				return
			}
			c.forwardExternalAgentUpdate(updateCtx, turn, env)
		},
		OnPermissionRequest: func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
			return c.handleExternalPermissionRequest(reqCtx, req, strings.TrimSpace(desc.ID), runState)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	runState.setClient(client)
	return client, func() { _ = client.Close() }, nil
}

func externalACPStartupContext(ctx context.Context, agentID string) (context.Context, context.CancelFunc) {
	if !strings.EqualFold(strings.TrimSpace(agentID), "openclaw") {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, externalACPStartupTimeout)
}

func externalACPStageError(agentID string, stage string, err error, client externalSlashACPClient) error {
	if err == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(agentID), "openclaw") && errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%s timed out after %s while starting %s ACP session", stage, externalACPStartupTimeout.Round(time.Second), strings.TrimSpace(agentID))
	}
	return externalAgentRunError(err, client)
}

func (c *cliConsole) handleExternalPermissionRequest(ctx context.Context, req acpclient.RequestPermissionRequest, agentID string, runState *activeExternalAgentRun) (acpclient.RequestPermissionResponse, error) {
	ctx = cliContext(ctx)
	statusCtx := context.WithoutCancel(ctx)
	sessionID := ""
	callID := ""
	if runState != nil {
		runState.mu.RLock()
		sessionID = runState.sessionID
		callID = runState.callID
		runState.mu.RUnlock()
	}
	decision := acpclient.ResolveApproveAllOnce(c.sessionMode, agentID, req)
	if resp, ok := decision.AutoResponse(); ok {
		return resp, nil
	}
	requireInteractive := decision.RequiresInteractiveApproval()
	approvalTool, approvalCommand := externalApprovalContext(req)
	if requireInteractive && sessionID != "" {
		_ = c.updateParticipantStatus(statusCtx, sessionID, "waiting_approval")
		if callID != "" {
			_ = c.acpProjectionStore().AppendParticipantStatusByIDs(statusCtx, callID, sessionID, "waiting_approval")
		}
		if c.tuiSender != nil {
			hint := strings.TrimSpace(approvalTool)
			if hint == "" {
				hint = "approval required"
			}
			if approvalCommand != "" {
				hint += ": " + approvalInlinePreview(approvalCommand, approvalPreviewMaxLineCols)
			}
			c.tuiSender.Send(tuievents.SetHintMsg{
				Hint:           hint,
				ClearAfter:     transientHintDuration,
				Priority:       tuievents.HintPriorityHigh,
				ClearOnMessage: true,
			})
			c.tuiSender.Send(tuievents.ParticipantStatusMsg{
				SessionID:       sessionID,
				State:           "waiting_approval",
				ApprovalTool:    approvalTool,
				ApprovalCommand: approvalCommand,
			})
		}
		defer func() {
			_ = c.updateParticipantStatus(statusCtx, sessionID, "running")
			if callID != "" {
				_ = c.acpProjectionStore().AppendParticipantStatusByIDs(statusCtx, callID, sessionID, "running")
			}
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.ParticipantStatusMsg{
					SessionID: sessionID,
					State:     "running",
				})
			}
		}()
	}
	if requireInteractive {
		ctx = toolexec.WithInteractiveApprovalRequired(ctx)
	}
	if c.approver != nil {
		selectedID, err := c.readExternalPermissionChoice(ctx, req)
		if err != nil {
			_ = c.updateParticipantStatus(statusCtx, sessionID, "failed")
			return acpclient.RequestPermissionResponse{}, err
		}
		return acpclient.PermissionSelectedOutcome(selectedID), nil
	}
	return externalPermissionOutcome(req, true), nil
}

func (c *cliConsole) readExternalPermissionChoice(ctx context.Context, req acpclient.RequestPermissionRequest) (string, error) {
	if c == nil || c.approver == nil || c.approver.prompter == nil {
		return "", &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	var selected string
	err := c.approver.queue.Do(ctx, func(context.Context) error {
		line, err := c.promptExternalPermissionChoice(req)
		if err != nil {
			if errors.Is(err, errInputInterrupt) || errors.Is(err, errInputEOF) {
				return &toolexec.ApprovalAbortedError{Reason: "approval canceled by user"}
			}
			return err
		}
		optionID, ok := resolveExternalPermissionSelection(req.Options, line)
		if !ok {
			return &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
		}
		selected = optionID
		return nil
	})
	return selected, err
}

func (c *cliConsole) promptExternalPermissionChoice(req acpclient.RequestPermissionRequest) (string, error) {
	promptReq := externalPermissionPromptRequest(req)
	if chooser, ok := c.approver.prompter.(structuredPromptReader); ok {
		return chooser.RequestStructuredPrompt(promptReq)
	}
	if chooser, ok := c.approver.prompter.(choicePromptReader); ok {
		return chooser.RequestChoicePrompt(promptReq.Title, promptReq.Choices, promptReq.DefaultChoice, false)
	}
	c.approver.renderExternalPermissionRequest(req)
	return c.approver.prompter.ReadLine("Choose option: ")
}

func (c *cliConsole) persistExternalParticipantRoute(ctx context.Context, turn *externalAgentTurn) error {
	if turn == nil {
		return fmt.Errorf("external agent turn is unavailable")
	}
	rootEvent := routeMirrorUserEvent(turn.routeText, turn.participant, turn.routeKind)
	if rootEvent.Meta == nil {
		rootEvent.Meta = map[string]any{}
	}
	rootEvent.Meta[metaRouteCallID] = strings.TrimSpace(turn.callID)
	return c.appendSessionEvent(ctx, c.currentSessionRef(), rootEvent)
}

func (c *cliConsole) forwardExternalAgentUpdate(ctx context.Context, turn *externalAgentTurn, env acpclient.UpdateEnvelope) {
	if c == nil || env.Update == nil || turn == nil {
		return
	}
	ctx = cliContext(ctx)
	sessionID := strings.TrimSpace(env.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(turn.participant.ChildSessionID)
	}
	if sessionID == "" {
		return
	}
	displayLabel := participantDisplayLabel(turn.participant.Alias, turn.participant.AgentID)
	for _, item := range turn.projector.Project(env) {
		switch strings.ToLower(strings.TrimSpace(item.Stream)) {
		case "assistant", "answer":
			if strings.TrimSpace(item.DeltaText) != "" || strings.TrimSpace(item.FullText) != "" {
				turn.sawAssistantStream.Store(true)
			}
		case "reasoning":
			if strings.TrimSpace(item.DeltaText) != "" || strings.TrimSpace(item.FullText) != "" {
				turn.sawReasoningStream.Store(true)
			}
		}
		if strings.TrimSpace(item.SessionID) != "" {
			sessionID = strings.TrimSpace(item.SessionID)
		}
		if c.tuiSender != nil {
			c.tuiSender.Send(tuievents.ACPProjectionMsg{
				Scope:         tuievents.ACPProjectionParticipant,
				ScopeID:       sessionID,
				Actor:         displayLabel,
				Stream:        item.Stream,
				DeltaText:     tuikit.SanitizeLogText(item.DeltaText),
				FullText:      tuikit.SanitizeLogText(item.FullText),
				ToolCallID:    item.ToolCallID,
				ToolName:      item.ToolName,
				ToolArgs:      item.ToolArgs,
				ToolResult:    item.ToolResult,
				ToolStatus:    item.ToolStatus,
				PlanEntries:   acpPlanEntriesToTUI(item.PlanEntries),
				HasPlanUpdate: item.PlanEntries != nil,
			})
		}
		_ = c.appendExternalParticipantProjection(ctx, turn, item)
	}
}

func shouldPersistExternalProjectionEvent(item acpprojector.Projection) bool {
	if item.Event == nil {
		return false
	}
	if partial, _ := item.Event.Meta["partial"].(bool); partial {
		return false
	}
	return true
}

func (c *cliConsole) finalizeExternalTurnStreams(ctx context.Context, turn *externalAgentTurn, interrupted bool) {
	if c == nil || turn == nil {
		return
	}
	ctx = cliContext(ctx)
	displayLabel := participantDisplayLabel(turn.participant.Alias, turn.participant.AgentID)
	assistant, reasoning := turn.projector.Snapshot()
	if reasoning != "" && (!turn.sawReasoningStream.Load() || interrupted) {
		if c.tuiSender != nil {
			c.tuiSender.Send(tuievents.RawDeltaMsg{
				Target:  tuievents.RawDeltaTargetAssistant,
				ScopeID: turn.participant.ChildSessionID,
				Stream:  "reasoning",
				Actor:   displayLabel,
				Text:    reasoning,
				Final:   true,
			})
		}
		_ = c.acpProjectionStore().AppendParticipantStreamSnapshot(ctx, turn, "reasoning", reasoning)
	}
	if assistant == "" {
		return
	}
	if turn.sawAssistantStream.Load() {
		return
	}
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetAssistant,
			ScopeID: turn.participant.ChildSessionID,
			Stream:  "answer",
			Actor:   displayLabel,
			Text:    assistant,
			Final:   true,
		})
	}
	_ = c.acpProjectionStore().AppendParticipantStreamSnapshot(ctx, turn, "assistant", assistant)
}

func (c *cliConsole) resolveExternalAgentWorkDir(desc appagents.Descriptor) string {
	workDir := strings.TrimSpace(desc.WorkDir)
	if workDir == "" {
		return c.workspace.CWD
	}
	if filepath.IsAbs(workDir) {
		return workDir
	}
	return filepath.Join(c.workspace.CWD, workDir)
}

func newExternalAgentCallID() string {
	return "call-external-" + strings.TrimPrefix(idutil.NewTaskID(), "t-")
}

func (c *cliConsole) appendSessionEvent(ctx context.Context, sess *session.Session, ev *session.Event) error {
	if c == nil || c.sessionStore == nil || sess == nil || ev == nil {
		return fmt.Errorf("session append prerequisites missing")
	}
	if _, err := c.ensureSessionRecord(ctx, sess.ID); err != nil {
		return err
	}
	if strings.TrimSpace(ev.ID) == "" {
		ev.ID = idutil.NewTaskID()
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	ev.SessionID = sess.ID
	session.EnsureEventType(ev)
	return c.sessionStore.AppendEvent(ctx, sess, ev)
}

func formatExternalToolStart(name string, args map[string]any) string {
	return acpprojector.FormatToolStart(name, args)
}

func formatExternalToolResult(name string, args map[string]any, result map[string]any, status string, _ bool) string {
	return acpprojector.FormatToolResult(name, args, result, status)
}

func externalACPToolDisplayName(title string, kind string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		fields := strings.Fields(title)
		if len(fields) > 0 {
			return strings.ToUpper(fields[0])
		}
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		return strings.ToUpper(kind)
	}
	return "TOOL"
}

func mergeExternalNarrativeChunk(existing string, incoming string) string {
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	if incoming == existing {
		return existing
	}

	const stableReplayThreshold = 12
	if len([]rune(existing)) >= stableReplayThreshold && strings.HasPrefix(incoming, existing) {
		return incoming
	}
	if len([]rune(incoming)) >= stableReplayThreshold && strings.HasPrefix(existing, incoming) {
		return existing
	}
	if suffix := overlappingExternalNarrativeSuffix(existing, incoming, 6); suffix != incoming {
		return existing + suffix
	}
	return existing + incoming
}

func overlappingExternalNarrativeSuffix(existing string, incoming string, minOverlap int) string {
	existingRunes := []rune(existing)
	incomingRunes := []rune(incoming)
	limit := minInt(len(existingRunes), len(incomingRunes))
	for overlap := limit; overlap >= minOverlap; overlap-- {
		if string(existingRunes[len(existingRunes)-overlap:]) == string(incomingRunes[:overlap]) {
			return string(incomingRunes[overlap:])
		}
	}
	return incoming
}

func externalApprovalRequestFromACP(req acpclient.RequestPermissionRequest) toolexec.ApprovalRequest {
	command := strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "command"))
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "permission"
	}
	return toolexec.ApprovalRequest{
		ToolName: title,
		Action:   strings.TrimSpace(derefString(req.ToolCall.Kind)),
		Command:  command,
		Reason:   strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "path")),
	}
}

func externalAuthorizationRequestFromACP(req acpclient.RequestPermissionRequest) policy.ToolAuthorizationRequest {
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "tool"
	}
	path := strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "path"))
	target := strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "target"))
	return policy.ToolAuthorizationRequest{
		ToolName: title,
		Path:     path,
		Target:   target,
		ScopeKey: externalFirstNonEmpty(path, target, strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "command"))),
	}
}

func externalRequestLooksLikeToolAuthorization(req acpclient.RequestPermissionRequest) bool {
	return strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "path")) != "" ||
		strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "target")) != ""
}

func externalApprovalContext(req acpclient.RequestPermissionRequest) (tool string, command string) {
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	kind := strings.TrimSpace(derefString(req.ToolCall.Kind))
	tool = externalACPToolDisplayName(title, kind)
	command = truncateInline(strings.TrimSpace(externalRawStringField(req.ToolCall.RawInput, "command")), 120)
	if command == "" {
		command = acpprojector.FormatToolArgsValue("", req.ToolCall.RawInput)
	}
	return tool, command
}

func externalPermissionOutcome(req acpclient.RequestPermissionRequest, allowed bool) acpclient.RequestPermissionResponse {
	return acpclient.PermissionSelectedOutcome(acpclient.SelectPermissionOptionID(req.Options, allowed))
}

func externalPermissionPromptRequest(req acpclient.RequestPermissionRequest) tuievents.PromptRequestMsg {
	title := commandApprovalTitle()
	details := make([]tuievents.PromptDetail, 0, 2)
	if externalRequestLooksLikeToolAuthorization(req) {
		title = toolAuthorizationTitle(externalAuthorizationRequestFromACP(req))
		if label, value := toolAuthorizationSummary(externalAuthorizationRequestFromACP(req)); label != "" && value != "" {
			details = append(details, tuievents.PromptDetail{Label: label, Value: value, Emphasis: true})
		}
	} else {
		if label, value := commandApprovalSummary(externalApprovalRequestFromACP(req)); label != "" && value != "" {
			details = append(details, tuievents.PromptDetail{Label: label, Value: value, Emphasis: true})
		}
	}
	if reason := approvalReasonText(externalRawStringField(req.ToolCall.RawInput, "reason")); reason != "" {
		details = append(details, tuievents.PromptDetail{Label: "Reason", Value: reason})
	}
	choices := externalPermissionPromptChoices(req.Options)
	return tuievents.PromptRequestMsg{
		Title:         title,
		Prompt:        title,
		Details:       details,
		Choices:       choices,
		DefaultChoice: defaultExternalPermissionOptionID(req.Options),
	}
}

func externalPermissionPromptChoices(options []acpclient.PermissionOption) []tuievents.PromptChoice {
	choices := make([]tuievents.PromptChoice, 0, len(options))
	for _, option := range options {
		optionID := strings.TrimSpace(option.OptionID)
		if optionID == "" {
			continue
		}
		label := strings.TrimSpace(option.Name)
		if label == "" {
			label = optionID
		}
		choices = append(choices, tuievents.PromptChoice{
			Label: label,
			Value: optionID,
		})
	}
	return choices
}

func defaultExternalPermissionOptionID(options []acpclient.PermissionOption) string {
	for _, candidate := range []string{
		acpclient.SelectPermissionOptionID(options, true),
		acpclient.SelectPermissionOptionID(options, false),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, option := range options {
			if strings.EqualFold(strings.TrimSpace(option.OptionID), candidate) {
				return strings.TrimSpace(option.OptionID)
			}
		}
	}
	for _, option := range options {
		if optionID := strings.TrimSpace(option.OptionID); optionID != "" {
			return optionID
		}
	}
	return ""
}

func resolveExternalPermissionSelection(options []acpclient.PermissionOption, input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultExternalPermissionOptionID(options)
	}
	if input == "" {
		return "", false
	}
	for _, option := range options {
		optionID := strings.TrimSpace(option.OptionID)
		if optionID == "" {
			continue
		}
		if strings.EqualFold(input, optionID) || strings.EqualFold(input, strings.TrimSpace(option.Name)) {
			return optionID, true
		}
	}
	return "", false
}

func externalRawStringField(raw any, key string) string {
	if raw == nil {
		return ""
	}
	value, ok := raw.(map[string]any)
	if !ok || value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value[key]))
	if text == "<nil>" {
		return ""
	}
	return text
}

func (a *terminalApprover) renderExternalPermissionRequest(req acpclient.RequestPermissionRequest) {
	if a == nil || a.ui == nil {
		return
	}
	promptReq := externalPermissionPromptRequest(req)
	a.ui.ApprovalTitle(promptReq.Title)
	for _, detail := range promptReq.Details {
		label := strings.TrimSpace(detail.Label)
		value := strings.TrimSpace(detail.Value)
		if label == "" || value == "" {
			continue
		}
		a.ui.ApprovalMeta(label, value)
	}
}

func externalAgentRunError(err error, client externalSlashACPClient) error {
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

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func externalFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
