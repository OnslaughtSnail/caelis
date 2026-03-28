package main

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
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type resumedSubagentTarget struct {
	SpawnID      string
	SessionID    string
	AttachTarget string
	CallID       string
	AnchorTool   string
	Agent        string
	ChildCWD     string
}

const (
	resumedSubagentSelfLoadTimeout = 5 * time.Second
	resumedSubagentACPLoadTimeout  = 30 * time.Second
)

func (c *cliConsole) restoreResumedSubagentPanels(ctx context.Context, rootSessionID string, events []*session.Event) {
	if c == nil || c.tuiSender == nil || len(events) == 0 {
		return
	}
	for _, target := range collectResumedSubagentTargets(events) {
		if !c.shouldReplayResumedSubagentTarget(ctx, target) {
			continue
		}
		c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
			Kind:        subagentDomainBootstrap,
			ClaimAnchor: true,
			Target: subagentProjectionTarget{
				RootSessionID: rootSessionID,
				SpawnID:       target.SpawnID,
				AttachTarget:  target.AttachTarget,
				CallID:        target.CallID,
				AnchorTool:    target.AnchorTool,
				Agent:         target.Agent,
			},
		})
		go c.restoreResumedSubagentPanelFromACP(ctx, rootSessionID, target)
	}
}

func (c *cliConsole) shouldReplayResumedSubagentTarget(ctx context.Context, target resumedSubagentTarget) bool {
	if c == nil {
		return false
	}
	if strings.TrimSpace(target.SessionID) == "" {
		return false
	}
	if c.rt == nil {
		return true
	}
	state, err := c.rt.RunState(ctx, runtime.RunStateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: target.SessionID,
	})
	if err != nil || !state.HasLifecycle {
		return true
	}
	return !isTerminalSpawnStatus(state.Status)
}

func collectResumedSubagentTargets(events []*session.Event) []resumedSubagentTarget {
	if len(events) == 0 {
		return nil
	}
	liveStates := resumedSubagentLiveStateIndex(events)
	orderedSessions := make([]string, 0)
	latestBySession := map[string]resumedSubagentTarget{}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		target, ok := resumedSubagentTargetFromToolResponse(resp, liveStates)
		if !ok {
			continue
		}
		sessionID := strings.TrimSpace(target.SessionID)
		if _, exists := latestBySession[sessionID]; !exists {
			orderedSessions = append(orderedSessions, sessionID)
		}
		latestBySession[sessionID] = target
	}
	out := make([]resumedSubagentTarget, 0, len(orderedSessions))
	for _, sessionID := range orderedSessions {
		target, ok := latestBySession[sessionID]
		if !ok {
			continue
		}
		out = append(out, target)
	}
	return out
}

func resumedSubagentTargetFromToolResponse(resp *model.ToolResponse, liveStates map[string]string) (resumedSubagentTarget, bool) {
	if resp == nil {
		return resumedSubagentTarget{}, false
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(resp.Name), tool.SpawnToolName):
		sessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id"))
		if sessionID == "" || !shouldResumeSubagentTarget(resp.Result, liveStates[sessionID]) {
			return resumedSubagentTarget{}, false
		}
		target := resumedSubagentTarget{
			SpawnID:      sessionID,
			SessionID:    sessionID,
			AttachTarget: strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id", "_ui_delegation_id", "delegation_id")),
			CallID:       strings.TrimSpace(resp.ID),
			AnchorTool:   tool.SpawnToolName,
			Agent:        strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_agent", "agent")),
			ChildCWD:     strings.TrimSpace(firstNonEmpty(resp.Result, "child_cwd")),
		}
		if target.Agent == "" {
			target.Agent = "self"
		}
		return target, true
	case strings.EqualFold(strings.TrimSpace(resp.Name), tool.TaskToolName):
		sessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id"))
		spawnID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_spawn_id"))
		callID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_parent_tool_call_id"))
		if sessionID == "" || spawnID == "" || callID == "" || !shouldResumeSubagentTarget(resp.Result, liveStates[sessionID]) {
			return resumedSubagentTarget{}, false
		}
		target := resumedSubagentTarget{
			SpawnID:      spawnID,
			SessionID:    sessionID,
			AttachTarget: strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id", "_ui_delegation_id", "delegation_id")),
			CallID:       callID,
			AnchorTool:   strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_anchor_tool")),
			Agent:        strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_agent", "agent")),
			ChildCWD:     strings.TrimSpace(firstNonEmpty(resp.Result, "child_cwd")),
		}
		if target.AnchorTool == "" {
			target.AnchorTool = runtime.SubagentContinuationAnchorTool
		}
		if target.Agent == "" {
			target.Agent = "self"
		}
		return target, true
	default:
		return resumedSubagentTarget{}, false
	}
}

func resumedSubagentLiveStateIndex(events []*session.Event) map[string]string {
	if len(events) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Meta != nil {
			childSessionID := strings.TrimSpace(firstNonEmpty(ev.Meta, "child_session_id"))
			if childSessionID != "" {
				if info, ok := runtime.LifecycleFromEvent(ev); ok {
					out[childSessionID] = strings.ToLower(strings.TrimSpace(string(info.Status)))
				}
			}
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
		childSessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id"))
		if childSessionID == "" {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(firstNonEmpty(resp.Result, "progress_state", "state")))
		if state == "" {
			continue
		}
		out[childSessionID] = state
	}
	return out
}

func shouldResumeSubagentTarget(result map[string]any, liveState string) bool {
	state := strings.ToLower(strings.TrimSpace(liveState))
	if state == "" {
		state = strings.ToLower(strings.TrimSpace(firstNonEmpty(result, "progress_state", "state")))
	}
	switch state {
	case "", "running", "waiting_approval":
		return true
	default:
		return false
	}
}

func (c *cliConsole) restoreResumedSubagentPanelFromACP(ctx context.Context, rootSessionID string, target resumedSubagentTarget) {
	if c == nil || c.tuiSender == nil {
		return
	}
	if strings.TrimSpace(target.SpawnID) == "" {
		return
	}
	if ctx == nil {
		return
	}
	state := &resumedACPReplayState{
		loading: true,
		calls:   map[string]toolCallSnapshot{},
	}
	client, cleanup, err := c.startResumedSubagentACPClient(ctx, target, func(env acpclient.UpdateEnvelope) {
		c.forwardResumedACPUpdate(ctx, rootSessionID, target, state, env)
	})
	if err != nil {
		c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
			Kind:   subagentDomainTerminal,
			Target: resumedSubagentProjectionTarget(rootSessionID, target),
			Status: "failed",
		})
		return
	}
	defer cleanup()

	initCtx, initCancel := context.WithTimeout(ctx, resumedSubagentLoadTimeoutForAgent(target.Agent))
	defer initCancel()
	if _, err := client.Initialize(initCtx); err != nil {
		if !errors.Is(err, context.Canceled) {
			c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
				Kind:   subagentDomainTerminal,
				Target: resumedSubagentProjectionTarget(rootSessionID, target),
				Status: "failed",
			})
		}
		return
	}
	loadCtx, loadCancel := context.WithTimeout(ctx, resumedSubagentLoadTimeoutForAgent(target.Agent))
	defer loadCancel()
	loadCWD := strings.TrimSpace(target.ChildCWD)
	if loadCWD == "" {
		loadCWD = c.workspace.CWD
	}
	if _, err := client.LoadSession(loadCtx, target.SessionID, loadCWD, nil); err != nil {
		if !errors.Is(err, context.Canceled) {
			c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
				Kind:   subagentDomainTerminal,
				Target: resumedSubagentProjectionTarget(rootSessionID, target),
				Status: "failed",
			})
		}
		return
	}
	state.markLoaded()
}

func resumedSubagentLoadTimeoutForAgent(agentName string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(agentName), "self") {
		return resumedSubagentSelfLoadTimeout
	}
	return resumedSubagentACPLoadTimeout
}

func (c *cliConsole) startResumedSubagentACPClient(ctx context.Context, target resumedSubagentTarget, onUpdate func(acpclient.UpdateEnvelope)) (*acpclient.Client, func(), error) {
	desc, err := c.resolveResumedSubagentDescriptor(target.Agent)
	if err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		return nil, nil, fmt.Errorf("cli: context is required")
	}
	switch desc.Transport {
	case appagents.TransportSelf:
		if c.newACPAdapter == nil {
			return nil, nil, fmt.Errorf("self acp adapter is unavailable")
		}
		serverReader, clientWriter := io.Pipe()
		clientReader, serverWriter := io.Pipe()
		serverConn := internalacp.NewConn(serverReader, serverWriter)
		client, err := acpclient.StartLoopback(ctx, acpclient.Config{
			Runtime:   c.execRuntime,
			Workspace: c.workspace.CWD,
			WorkDir:   c.workspace.CWD,
			OnUpdate:  onUpdate,
		}, clientReader, clientWriter)
		if err != nil {
			return nil, nil, err
		}
		adapter, err := c.newACPAdapter(serverConn)
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
		serveCtx, serveCancel := context.WithCancel(context.WithoutCancel(ctx))
		done := make(chan error, 1)
		go func() {
			done <- server.Serve(serveCtx)
		}()
		cleanup := func() {
			serveCancel()
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
			}
			_ = client.Close()
		}
		return client, cleanup, nil
	case appagents.TransportACP:
		client, err := acpclient.Start(ctx, acpclient.Config{
			Command:   strings.TrimSpace(desc.Command),
			Args:      append([]string(nil), desc.Args...),
			Env:       copyStringMap(desc.Env),
			WorkDir:   c.resolveResumedSubagentWorkDir(desc),
			Runtime:   c.execRuntime,
			Workspace: c.workspace.CWD,
			OnUpdate:  onUpdate,
		})
		if err != nil {
			return nil, nil, err
		}
		return client, func() { _ = client.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unsupported agent transport %q", desc.Transport)
	}
}

func (c *cliConsole) resolveResumedSubagentDescriptor(agentName string) (appagents.Descriptor, error) {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "self"
	}
	registry := c.agentRegistry
	if c.configStore != nil {
		if reg, err := c.configStore.AgentRegistry(); err == nil && reg != nil {
			registry = reg
		}
	}
	if registry == nil {
		registry = appagents.NewRegistry()
	}
	desc, ok := registry.Lookup(agentName)
	if !ok {
		return appagents.Descriptor{}, fmt.Errorf("unknown agent %q", agentName)
	}
	return desc, nil
}

func (c *cliConsole) resolveResumedSubagentWorkDir(desc appagents.Descriptor) string {
	workDir := strings.TrimSpace(desc.WorkDir)
	if workDir == "" {
		return c.workspace.CWD
	}
	if filepath.IsAbs(workDir) {
		return filepath.Clean(workDir)
	}
	return filepath.Join(c.workspace.CWD, workDir)
}

func (c *cliConsole) forwardResumedACPUpdate(ctx context.Context, rootSessionID string, target resumedSubagentTarget, state *resumedACPReplayState, env acpclient.UpdateEnvelope) {
	if c == nil || c.tuiSender == nil || env.Update == nil {
		return
	}
	switch update := env.Update.(type) {
	case acpclient.ContentChunk:
		if ev := state.contentEvent(update); ev != nil {
			c.forwardResumedACPEvent(ctx, rootSessionID, target, ev)
		}
	case acpclient.ToolCall:
		if ev := state.toolCallEvent(update); ev != nil {
			c.forwardResumedACPEvent(ctx, rootSessionID, target, ev)
		}
	case acpclient.ToolCallUpdate:
		if ev := state.toolCallUpdateEvent(update); ev != nil {
			c.forwardResumedACPEvent(ctx, rootSessionID, target, ev)
		}
	case acpclient.PlanUpdate:
		entries := make([]tuievents.PlanEntry, 0, len(update.Entries))
		for _, entry := range update.Entries {
			entries = append(entries, tuievents.PlanEntry{
				Content: strings.TrimSpace(entry.Content),
				Status:  strings.TrimSpace(entry.Status),
			})
		}
		c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
			Kind:    subagentDomainPlan,
			Target:  resumedSubagentProjectionTarget(rootSessionID, target),
			Entries: entries,
		})
	}
}

type resumedACPReplayState struct {
	mu        sync.Mutex
	loading   bool
	assistant string
	reasoning string
	calls     map[string]toolCallSnapshot
}

func (s *resumedACPReplayState) markLoaded() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loading = false
}

func (s *resumedACPReplayState) contentEvent(update acpclient.ContentChunk) *session.Event {
	if s == nil {
		return nil
	}
	text := decodeResumedACPTextChunk(update.Content)
	if text == "" {
		return nil
	}
	channel := strings.TrimSpace(update.SessionUpdate)
	ev := &session.Event{Message: model.Message{Role: model.RoleAssistant}}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch channel {
	case acpclient.UpdateAgentMessage:
		if s.loading {
			ev.Message = model.NewTextMessage(model.RoleAssistant, replaceSubagentReplayText(&s.assistant, text))
			return ev
		}
		ev.Message = model.NewTextMessage(model.RoleAssistant, appendSubagentReplayText(&s.assistant, text))
		ev.Meta = map[string]any{"partial": true, "channel": "answer"}
		return ev
	case acpclient.UpdateAgentThought:
		if s.loading {
			ev.Message = model.NewReasoningMessage(model.RoleAssistant, replaceSubagentReplayText(&s.reasoning, text), model.ReasoningVisibilityVisible)
			return ev
		}
		ev.Message = model.NewReasoningMessage(model.RoleAssistant, appendSubagentReplayText(&s.reasoning, text), model.ReasoningVisibilityVisible)
		ev.Meta = map[string]any{"partial": true, "channel": "reasoning"}
		return ev
	default:
		return nil
	}
}

func (s *resumedACPReplayState) toolCallEvent(update acpclient.ToolCall) *session.Event {
	if s == nil {
		return nil
	}
	name, args := resumedACPToolCallShape(update.Title, update.Kind, update.RawInput)
	callID := strings.TrimSpace(update.ToolCallID)
	s.mu.Lock()
	s.calls = rememberSubagentToolSnapshot(s.calls, callID, name, args)
	s.mu.Unlock()
	return &session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   callID,
			Name: name,
			Args: marshalResumeACPToolInput(args),
		}}, ""),
	}
}

func (s *resumedACPReplayState) toolCallUpdateEvent(update acpclient.ToolCallUpdate) *session.Event {
	if s == nil {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(derefResumeString(update.Status)))
	if status != internalacp.ToolStatusCompleted && status != internalacp.ToolStatusFailed {
		return nil
	}
	callID := strings.TrimSpace(update.ToolCallID)
	s.mu.Lock()
	var snap toolCallSnapshot
	var ok bool
	s.calls, snap, ok = consumeSubagentToolSnapshot(s.calls, callID)
	s.mu.Unlock()

	name := snap.Name
	if ok && strings.TrimSpace(snap.Name) != "" {
		name = snap.Name
	}
	if title := strings.TrimSpace(derefResumeString(update.Title)); title != "" || strings.TrimSpace(derefResumeString(update.Kind)) != "" || update.RawInput != nil {
		name, _ = resumedACPToolCallShape(derefResumeString(update.Title), derefResumeString(update.Kind), update.RawInput)
	}
	if name == "" {
		name = "TOOL"
	}
	return &session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:     callID,
			Name:   name,
			Result: resumedACPResultMap(update.RawOutput),
		}),
	}
}

func (c *cliConsole) forwardResumedACPEvent(ctx context.Context, rootSessionID string, target resumedSubagentTarget, ev *session.Event) {
	if ev == nil {
		return
	}
	c.dispatchSubagentDomainUpdate(ctx, syntheticSubagentDomainUpdate(resumedSubagentProjectionTarget(rootSessionID, target), ev))
}

func resumedSubagentProjectionTarget(rootSessionID string, target resumedSubagentTarget) subagentProjectionTarget {
	return subagentProjectionTarget{
		RootSessionID: rootSessionID,
		SpawnID:       target.SpawnID,
		AttachTarget:  target.AttachTarget,
		CallID:        target.CallID,
		AnchorTool:    target.AnchorTool,
		Agent:         target.Agent,
	}
}

func (c *cliConsole) sendSubagentProjectionMsg(_ context.Context, rootSessionID string, msg any) {
	if c == nil || c.tuiSender == nil {
		return
	}
	if rootSessionID != "" && strings.TrimSpace(c.sessionID) != strings.TrimSpace(rootSessionID) {
		return
	}
	if stream, ok := msg.(tuievents.SubagentStreamMsg); ok {
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetSubagent,
			ScopeID: stream.SpawnID,
			Stream:  stream.Stream,
			Text:    stream.Chunk,
		})
		return
	}
	c.tuiSender.Send(msg)
}

func decodeResumedACPTextChunk(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var chunk struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(chunk.Type), "text") {
		return ""
	}
	return chunk.Text
}

func resumedACPToolCallShape(title string, kind string, rawInput any) (string, map[string]any) {
	name := "TOOL"
	title = strings.TrimSpace(title)
	if title != "" {
		if fields := strings.Fields(title); len(fields) > 0 {
			name = strings.ToUpper(strings.TrimSpace(fields[0]))
		}
	} else if strings.TrimSpace(kind) != "" {
		name = strings.ToUpper(strings.TrimSpace(kind))
	}
	args := map[string]any{}
	if title != "" {
		args["_acp_title"] = title
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		args["_acp_kind"] = kind
	}
	switch typed := rawInput.(type) {
	case nil:
	case map[string]any:
		for key, value := range typed {
			args[key] = value
		}
	default:
		args["_acp_raw_input"] = typed
	}
	return name, args
}

func marshalResumeACPToolInput(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(data)
}

func resumedACPResultMap(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, one := range typed {
			out[key] = one
		}
		return out
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil && out != nil {
			return out
		}
	default:
		return map[string]any{"result": typed}
	}
	return map[string]any{}
}

func derefResumeString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
