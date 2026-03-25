package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
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
	client    *acpclient.Client
	sessionID string
}

type externalAgentTurnMode string

const (
	externalAgentTurnNew  externalAgentTurnMode = "new"
	externalAgentTurnLoad externalAgentTurnMode = "load"
)

type externalAgentTurn struct {
	mode        externalAgentTurnMode
	desc        appagents.Descriptor
	participant externalParticipant
	routeText   string
	promptText  string
	routeKind   string
	callID      string
	runState    *activeExternalAgentRun

	mu        sync.Mutex
	assistant string
	reasoning string
	toolCalls map[string]toolCallSnapshot
	ready     atomic.Bool
}

func (r *activeExternalAgentRun) setClient(client *acpclient.Client) {
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
	registry := c.agentRegistry
	if c != nil && c.configStore != nil {
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

func (c *cliConsole) runExternalAgentSlash(desc appagents.Descriptor, prompt string) error {
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
	participants, err := c.loadSessionParticipants(context.Background())
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
	return c.startExternalAgentTurnAsync(&externalAgentTurn{
		mode:        externalAgentTurnNew,
		desc:        desc,
		participant: participant,
		routeText:   routeText,
		promptText:  prompt,
		routeKind:   "slash_create",
		callID:      newExternalAgentCallID(),
		runState:    &activeExternalAgentRun{},
		toolCalls:   map[string]toolCallSnapshot{},
	})
}

func (c *cliConsole) routeExternalParticipant(alias string, prompt string) error {
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
	participant, ok, err := c.lookupParticipantByAlias(context.Background(), alias)
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
	return c.startExternalAgentTurnAsync(&externalAgentTurn{
		mode:        externalAgentTurnLoad,
		desc:        desc,
		participant: participant,
		routeText:   "@" + participant.Alias + " " + prompt,
		promptText:  prompt,
		routeKind:   "participant_route",
		callID:      newExternalAgentCallID(),
		runState:    &activeExternalAgentRun{},
		toolCalls:   map[string]toolCallSnapshot{},
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

func (c *cliConsole) startExternalAgentTurnAsync(turn *externalAgentTurn) error {
	if c == nil || turn == nil {
		return fmt.Errorf("console is unavailable")
	}

	baseCtx := c.baseCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	runCtx, cancelCtx := context.WithCancel(baseCtx)
	cancel := func() {
		if turn.runState != nil {
			turn.runState.cancel()
		}
		cancelCtx()
	}
	c.setActiveExternalRun(cancel)

	go func() {
		defer c.clearActiveRun()
		err := c.runExternalAgentTurnOnce(runCtx, turn)
		switch {
		case err == nil:
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
	if _, err := c.ensureSessionRecord(ctx, c.sessionID); err != nil {
		return err
	}
	client, cleanup, err := c.startExternalSlashACPClient(ctx, turn.runState, turn.desc, turn)
	if err != nil {
		return err
	}
	defer cleanup()

	initCtx, initCancel := context.WithCancel(ctx)
	defer initCancel()
	if _, err := client.Initialize(initCtx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return externalAgentRunError(err, client)
	}

	switch turn.mode {
	case externalAgentTurnNew:
		sessionResp, err := client.NewSession(ctx, c.resolveExternalAgentWorkDir(turn.desc), nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return externalAgentRunError(err, client)
		}
		turn.participant.ChildSessionID = strings.TrimSpace(sessionResp.SessionID)
	case externalAgentTurnLoad:
		if _, err := client.LoadSession(ctx, strings.TrimSpace(turn.participant.ChildSessionID), c.resolveExternalAgentWorkDir(turn.desc), nil); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return externalAgentRunError(err, client)
		}
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
	if _, err := c.ensureSessionRecord(ctx, sessionID); err != nil {
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
	}
	turn.ready.Store(true)

	if _, err := client.Prompt(ctx, sessionID, turn.promptText, nil); err != nil {
		if ctx.Err() != nil {
			_ = c.updateParticipantStatus(context.Background(), sessionID, "interrupted")
			c.finalizeExternalTurnStreams(turn, true)
			return ctx.Err()
		}
		_ = c.updateParticipantStatus(context.Background(), sessionID, "failed")
		c.finalizeExternalTurnStreams(turn, true)
		return externalAgentRunError(err, client)
	}
	c.finalizeExternalTurnStreams(turn, false)
	_ = c.updateParticipantStatus(context.Background(), sessionID, "completed")
	return nil
}

func (c *cliConsole) startExternalSlashACPClient(ctx context.Context, runState *activeExternalAgentRun, desc appagents.Descriptor, turn *externalAgentTurn) (*acpclient.Client, func(), error) {
	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:   strings.TrimSpace(desc.Command),
		Args:      append([]string(nil), desc.Args...),
		Env:       copyStringMap(desc.Env),
		WorkDir:   c.resolveExternalAgentWorkDir(desc),
		Runtime:   c.execRuntime,
		Workspace: c.workspace.CWD,
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			if turn == nil || !turn.ready.Load() {
				return
			}
			c.forwardExternalAgentUpdate(turn, env)
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

func (c *cliConsole) handleExternalPermissionRequest(ctx context.Context, req acpclient.RequestPermissionRequest, agentID string, runState *activeExternalAgentRun) (acpclient.RequestPermissionResponse, error) {
	sessionID := ""
	if runState != nil {
		runState.mu.RLock()
		sessionID = runState.sessionID
		runState.mu.RUnlock()
	}
	approvalTool, approvalCommand := externalApprovalContext(req)
	if sessionID != "" {
		_ = c.updateParticipantStatus(context.Background(), sessionID, "waiting_approval")
		if c.tuiSender != nil {
			hint := strings.TrimSpace(approvalTool)
			if hint == "" {
				hint = "approval required"
			}
			if approvalCommand != "" {
				hint += ": " + truncateInline(approvalCommand, 120)
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
			_ = c.updateParticipantStatus(context.Background(), sessionID, "running")
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.ParticipantStatusMsg{
					SessionID: sessionID,
					State:     "running",
				})
			}
		}()
	}
	isToolAuthorization := externalRequestLooksLikeToolAuthorization(req)
	if isToolAuthorization && c.approver != nil {
		allowed, err := c.approver.AuthorizeTool(ctx, externalAuthorizationRequestFromACP(req))
		if err != nil {
			_ = c.updateParticipantStatus(context.Background(), sessionID, "failed")
			return acpclient.RequestPermissionResponse{}, err
		}
		return externalPermissionOutcome(req, allowed), nil
	}
	if c.approver != nil {
		allowed, err := c.approver.Approve(ctx, externalApprovalRequestFromACP(req))
		if err != nil {
			_ = c.updateParticipantStatus(context.Background(), sessionID, "failed")
			return acpclient.RequestPermissionResponse{}, err
		}
		return externalPermissionOutcome(req, allowed), nil
	}
	_ = agentID
	return externalPermissionOutcome(req, true), nil
}

func (c *cliConsole) persistExternalParticipantRoute(ctx context.Context, turn *externalAgentTurn) error {
	if turn == nil {
		return fmt.Errorf("external agent turn is unavailable")
	}
	rootEvent := routeMirrorUserEvent(turn.routeText, turn.participant, turn.routeKind)
	if err := c.appendSessionEvent(ctx, c.currentSessionRef(), rootEvent); err != nil {
		return err
	}
	childEvent := annotateChildParticipantEvent(&session.Event{
		Message: model.NewTextMessage(model.RoleUser, strings.TrimSpace(turn.promptText)),
	}, c.sessionID, turn.participant)
	return c.appendSessionEvent(ctx, c.childSessionRecord(turn.participant.ChildSessionID), childEvent)
}

func (c *cliConsole) forwardExternalAgentUpdate(turn *externalAgentTurn, env acpclient.UpdateEnvelope) {
	if c == nil || c.tuiSender == nil || env.Update == nil || turn == nil {
		return
	}
	sessionID := strings.TrimSpace(env.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(turn.participant.ChildSessionID)
	}
	if sessionID == "" {
		return
	}
	displayLabel := participantDisplayLabel(turn.participant.Alias, turn.participant.AgentID)
	switch update := env.Update.(type) {
	case acpclient.ContentChunk:
		stream, chunk := externalContentChunk(update)
		if stream == "" || chunk == "" {
			return
		}
		turn.mu.Lock()
		switch stream {
		case "assistant":
			turn.assistant += chunk
		case "reasoning":
			turn.reasoning += chunk
		}
		turn.mu.Unlock()
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetAssistant,
			ScopeID: sessionID,
			Stream:  stream,
			Actor:   displayLabel,
			Text:    chunk,
		})
	case acpclient.ToolCall:
		name, args := resumedACPToolCallShape(update.Title, update.Kind, update.RawInput)
		callID := strings.TrimSpace(update.ToolCallID)
		turn.mu.Lock()
		turn.toolCalls = rememberSubagentToolSnapshot(turn.toolCalls, callID, name, args)
		turn.mu.Unlock()
		childEvent := annotateChildParticipantEvent(&session.Event{
			Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
				ID:   callID,
				Name: name,
				Args: marshalResumeACPToolInput(args),
			}}, ""),
		}, c.sessionID, turn.participant)
		if c.appendSessionEvent(context.Background(), c.childSessionRecord(sessionID), childEvent) == nil {
			mirror := mirrorParticipantEvent(&session.Event{
				Message: childEvent.Message,
			}, c.sessionID, turn.participant, childEvent.ID)
			_ = c.appendSessionEvent(context.Background(), c.currentSessionRef(), mirror)
		}
		c.tuiSender.Send(tuievents.ParticipantToolMsg{
			SessionID: sessionID,
			CallID:    callID,
			ToolName:  name,
			Args:      formatExternalToolStart(name, args),
		})
	case acpclient.ToolCallUpdate:
		status := strings.ToLower(strings.TrimSpace(derefString(update.Status)))
		if status != "" && status != "completed" && status != "failed" {
			return
		}
		callID := strings.TrimSpace(update.ToolCallID)
		turn.mu.Lock()
		toolCalls, snapshot, ok := consumeSubagentToolSnapshot(turn.toolCalls, callID)
		turn.toolCalls = toolCalls
		turn.mu.Unlock()
		name := snapshot.Name
		args := snapshot.Args
		if strings.TrimSpace(name) == "" {
			name, args = resumedACPToolCallShape(derefString(update.Title), derefString(update.Kind), update.RawInput)
		}
		result := resumedACPResultMap(update.RawOutput)
		childEvent := annotateChildParticipantEvent(&session.Event{
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:     callID,
				Name:   name,
				Result: result,
			}),
		}, c.sessionID, turn.participant)
		if c.appendSessionEvent(context.Background(), c.childSessionRecord(sessionID), childEvent) == nil {
			mirror := mirrorParticipantEvent(&session.Event{
				Message: childEvent.Message,
			}, c.sessionID, turn.participant, childEvent.ID)
			_ = c.appendSessionEvent(context.Background(), c.currentSessionRef(), mirror)
		}
		c.tuiSender.Send(tuievents.ParticipantToolMsg{
			SessionID: sessionID,
			CallID:    callID,
			ToolName:  name,
			Output:    formatExternalToolResult(name, args, result, status, ok),
			Final:     true,
			Err:       status == "failed",
		})
	case acpclient.PlanUpdate:
		_ = update
	}
}

func (c *cliConsole) finalizeExternalTurnStreams(turn *externalAgentTurn, interrupted bool) {
	if c == nil || c.tuiSender == nil || turn == nil {
		return
	}
	displayLabel := participantDisplayLabel(turn.participant.Alias, turn.participant.AgentID)
	turn.mu.Lock()
	assistant := strings.TrimSpace(turn.assistant)
	reasoning := strings.TrimSpace(turn.reasoning)
	turn.mu.Unlock()
	if reasoning != "" || interrupted {
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetAssistant,
			ScopeID: turn.participant.ChildSessionID,
			Stream:  "reasoning",
			Actor:   displayLabel,
			Text:    reasoning,
			Final:   true,
		})
	}
	if assistant == "" {
		return
	}
	c.tuiSender.Send(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: turn.participant.ChildSessionID,
		Stream:  "answer",
		Actor:   displayLabel,
		Text:    assistant,
		Final:   true,
	})
	childEvent := annotateChildParticipantEvent(&session.Event{
		Message: model.NewTextMessage(model.RoleAssistant, assistant),
	}, c.sessionID, turn.participant)
	if c.appendSessionEvent(context.Background(), c.childSessionRecord(turn.participant.ChildSessionID), childEvent) == nil {
		mirror := mirrorParticipantEvent(&session.Event{
			Message: childEvent.Message,
		}, c.sessionID, turn.participant, childEvent.ID)
		_ = c.appendSessionEvent(context.Background(), c.currentSessionRef(), mirror)
	}
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
	return strings.TrimSpace(externalACPToolArgsWithName(name, args))
}

func formatExternalToolResult(name string, args map[string]any, result map[string]any, status string, _ bool) string {
	name = strings.TrimSpace(strings.ToUpper(name))
	_ = args
	summary := strings.TrimSpace(externalACPToolOutput(result))
	if summary == "" {
		if strings.EqualFold(status, "failed") {
			summary = "failed"
		} else {
			summary = "completed"
		}
	}
	if strings.EqualFold(summary, name) {
		if strings.EqualFold(status, "failed") {
			return "failed"
		}
		return "completed"
	}
	return summary
}

func externalContentChunk(update acpclient.ContentChunk) (stream string, chunk string) {
	switch strings.TrimSpace(update.SessionUpdate) {
	case acpclient.UpdateAgentThought:
		return "reasoning", decodeACPTextChunk(update.Content)
	case acpclient.UpdateAgentMessage:
		return "assistant", decodeACPTextChunk(update.Content)
	default:
		return "", ""
	}
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

func externalACPToolArgsWithName(name string, raw any) string {
	values, ok := raw.(map[string]any)
	if !ok || len(values) == 0 {
		if value := strings.TrimSpace(externalACPPrimaryValue(raw)); value != "" {
			return truncateInline(value, 120)
		}
		return ""
	}
	kind := strings.ToLower(strings.TrimSpace(externalFirstNonEmpty(asString(values["_acp_kind"]), asString(values["kind"]))))
	switch kind {
	case "search":
		if query := strings.TrimSpace(externalFirstNonEmpty(asString(values["query"]), asString(values["pattern"]), asString(values["text"]))); query != "" {
			return `for "` + truncateInline(query, 96) + `"`
		}
	case "edit":
		if path := strings.TrimSpace(externalFirstNonEmpty(asString(values["path"]), asString(values["target"]))); path != "" {
			return truncateInline(path, 120)
		}
	case "read", "delete", "move":
		if path := strings.TrimSpace(externalFirstNonEmpty(asString(values["path"]), asString(values["source"]), asString(values["target"]))); path != "" {
			return truncateInline(path, 120)
		}
	case "execute":
		if command := strings.TrimSpace(externalFirstNonEmpty(asString(values["command"]), asString(values["cmd"]))); command != "" {
			return truncateInline(command, 120)
		}
	case "fetch":
		if url := strings.TrimSpace(externalFirstNonEmpty(asString(values["url"]), asString(values["uri"]))); url != "" {
			return truncateInline(url, 120)
		}
	}
	if title := strings.TrimSpace(asString(values["_acp_title"])); title != "" {
		if summary := externalACPTitleSummary(name, kind, title); summary != "" {
			return truncateInline(summary, 120)
		}
	}
	if value := strings.TrimSpace(externalACPPrimaryValue(values)); value != "" {
		return truncateInline(value, 120)
	}
	return ""
}

func externalACPToolArgs(raw any) string {
	return externalACPToolArgsWithName("", raw)
}

func externalACPToolOutput(raw any) string {
	values, ok := raw.(map[string]any)
	if !ok || len(values) == 0 {
		if value := strings.TrimSpace(externalACPPrimaryValue(raw)); value != "" {
			return truncateInline(value, 160)
		}
		return ""
	}
	for _, key := range []string{"error", "stderr", "message", "summary", "result", "stdout"} {
		if value := strings.TrimSpace(asString(values[key])); value != "" && value != "{}" && value != "map[]" {
			return truncateInline(value, 160)
		}
	}
	if path := strings.TrimSpace(asString(values["path"])); path != "" {
		if exitCode, ok := asInt(values["exit_code"]); ok {
			return truncateInline(fmt.Sprintf("%s (exit %d)", path, exitCode), 160)
		}
		return truncateInline(path, 160)
	}
	if exitCode, ok := asInt(values["exit_code"]); ok {
		return fmt.Sprintf("exit %d", exitCode)
	}
	if value := strings.TrimSpace(externalACPPrimaryValue(values)); value != "" {
		return truncateInline(value, 160)
	}
	return ""
}

func externalACPPrimaryValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case map[string]any:
		for _, key := range []string{"command", "path", "target", "query", "pattern", "text", "prompt", "url", "error", "stderr", "stdout", "message", "summary", "result"} {
			if text := strings.TrimSpace(fmt.Sprint(value[key])); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func externalACPTitleSummary(name string, kind string, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	name = strings.TrimSpace(strings.ToUpper(name))
	kind = strings.TrimSpace(strings.ToUpper(kind))
	normalized := title
	for _, prefix := range []string{name, kind} {
		if prefix == "" {
			continue
		}
		upperTitle := strings.ToUpper(normalized)
		if strings.HasPrefix(upperTitle, prefix+" ") {
			normalized = strings.TrimSpace(normalized[len(prefix):])
			break
		}
		if strings.EqualFold(normalized, prefix) {
			return ""
		}
	}
	return strings.TrimSpace(normalized)
}

func externalPlanEntries(update acpclient.ToolCallUpdate) []tuievents.PlanEntry {
	raw, ok := update.RawOutput.(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw["entries"].([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	entries := make([]tuievents.PlanEntry, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content := strings.TrimSpace(fmt.Sprint(row["content"]))
		status := strings.TrimSpace(fmt.Sprint(row["status"]))
		if content == "" || status == "" {
			continue
		}
		entries = append(entries, tuievents.PlanEntry{Content: content, Status: status})
	}
	return entries
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
		command = externalACPToolArgs(req.ToolCall.RawInput)
	}
	return tool, command
}

func externalPermissionOutcome(req acpclient.RequestPermissionRequest, allowed bool) acpclient.RequestPermissionResponse {
	wantKind := "reject"
	fallback := "reject_once"
	if allowed {
		wantKind = "allow"
		fallback = "allow_once"
	}
	optionID := fallback
	for _, option := range req.Options {
		kind := strings.ToLower(strings.TrimSpace(option.Kind))
		if strings.Contains(kind, wantKind) && strings.TrimSpace(option.OptionID) != "" {
			optionID = strings.TrimSpace(option.OptionID)
			break
		}
	}
	return acpclient.RequestPermissionResponse{
		Outcome: mustMarshalRaw(map[string]any{
			"outcome":  "selected",
			"optionId": optionID,
		}),
	}
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

func externalAgentRunError(err error, client *acpclient.Client) error {
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

func decodeACPTextChunk(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var chunk acpclient.TextChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(chunk.Type), "text") {
		return ""
	}
	return chunk.Text
}

func mustMarshalRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
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
