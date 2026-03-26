package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func (c *cliConsole) forwardSessionEventToTUI(rootSessionID string, update sessionstream.Update) {
	if c == nil || c.tuiSender == nil || update.Event == nil {
		return
	}
	if strings.TrimSpace(update.SessionID) == "" || strings.TrimSpace(update.SessionID) == strings.TrimSpace(rootSessionID) {
		return
	}
	for _, msg := range c.projectSubagentUpdate(update) {
		c.tuiSender.Send(msg)
	}
}

type subagentProjectionTarget struct {
	RootSessionID string
	SpawnID       string
	AttachTarget  string
	CallID        string
	AnchorTool    string
	Agent         string
}

type subagentDomainUpdateKind string

const (
	subagentDomainBootstrap subagentDomainUpdateKind = "bootstrap"
	subagentDomainEvent     subagentDomainUpdateKind = "event"
	subagentDomainPlan      subagentDomainUpdateKind = "plan"
	subagentDomainTerminal  subagentDomainUpdateKind = "terminal"
	subagentDomainStatus    subagentDomainUpdateKind = "status"
	subagentDomainStream    subagentDomainUpdateKind = "stream"
	subagentDomainToolCall  subagentDomainUpdateKind = "tool_call"
)

type subagentDomainUpdate struct {
	Kind            subagentDomainUpdateKind
	Target          subagentProjectionTarget
	Event           *session.Event
	Status          string
	ClaimAnchor     bool
	Provisional     bool
	ApprovalTool    string
	ApprovalCommand string
	Entries         []tuievents.PlanEntry
	Stream          string
	Chunk           string
	ToolName        string
	ToolCallID      string
	Args            string
	Final           bool
}

func (c *cliConsole) projectSubagentUpdate(update sessionstream.Update) []any {
	if c == nil || c.spawnPreviewer == nil || update.Event == nil {
		return nil
	}
	return renderSubagentDomainUpdates(c.spawnPreviewer.ProjectDomain(update))
}

func (c *cliConsole) projectSubagentDomainUpdate(update subagentDomainUpdate) []any {
	if c == nil {
		return nil
	}
	target := update.Target
	if strings.TrimSpace(target.SpawnID) == "" {
		return nil
	}
	switch update.Kind {
	case subagentDomainBootstrap:
		msgs := []any{tuievents.SubagentStartMsg{
			SpawnID:      target.SpawnID,
			AttachTarget: target.AttachTarget,
			Agent:        target.Agent,
			CallID:       target.CallID,
			AnchorTool:   target.AnchorTool,
			ClaimAnchor:  update.ClaimAnchor,
			Provisional:  update.Provisional,
		}}
		if status := strings.TrimSpace(update.Status); status != "" {
			msgs = append(msgs, tuievents.SubagentStatusMsg{
				SpawnID:         target.SpawnID,
				State:           status,
				ApprovalTool:    update.ApprovalTool,
				ApprovalCommand: update.ApprovalCommand,
			})
		}
		return msgs
	case subagentDomainEvent:
		if update.Event == nil {
			return nil
		}
		if c.spawnPreviewer == nil {
			return nil
		}
		return renderSubagentDomainUpdates(c.spawnPreviewer.ProjectDomain(syntheticSubagentUpdate(target, update.Event)))
	default:
		return renderSubagentDomainUpdates([]subagentDomainUpdate{update})
	}
}

func renderSubagentDomainUpdates(updates []subagentDomainUpdate) []any {
	if len(updates) == 0 {
		return nil
	}
	msgs := make([]any, 0, len(updates))
	for _, update := range updates {
		target := update.Target
		if strings.TrimSpace(target.SpawnID) == "" {
			continue
		}
		switch update.Kind {
		case subagentDomainBootstrap:
			msgs = append(msgs, tuievents.SubagentStartMsg{
				SpawnID:      target.SpawnID,
				AttachTarget: target.AttachTarget,
				Agent:        target.Agent,
				CallID:       target.CallID,
				AnchorTool:   target.AnchorTool,
				ClaimAnchor:  update.ClaimAnchor,
				Provisional:  update.Provisional,
			})
			if status := strings.TrimSpace(update.Status); status != "" {
				msgs = append(msgs, tuievents.SubagentStatusMsg{
					SpawnID:         target.SpawnID,
					State:           status,
					ApprovalTool:    update.ApprovalTool,
					ApprovalCommand: update.ApprovalCommand,
				})
			}
		case subagentDomainStatus:
			msgs = append(msgs, tuievents.SubagentStatusMsg{
				SpawnID:         target.SpawnID,
				State:           strings.TrimSpace(update.Status),
				ApprovalTool:    update.ApprovalTool,
				ApprovalCommand: update.ApprovalCommand,
			})
		case subagentDomainStream:
			msgs = append(msgs, tuievents.SubagentStreamMsg{
				SpawnID: target.SpawnID,
				Stream:  update.Stream,
				Chunk:   update.Chunk,
			})
		case subagentDomainToolCall:
			msgs = append(msgs, tuievents.SubagentToolCallMsg{
				SpawnID:  target.SpawnID,
				ToolName: update.ToolName,
				CallID:   update.ToolCallID,
				Args:     update.Args,
				Stream:   update.Stream,
				Chunk:    update.Chunk,
				Final:    update.Final,
			})
		case subagentDomainPlan:
			msgs = append(msgs, tuievents.SubagentPlanMsg{
				SpawnID: target.SpawnID,
				Entries: append([]tuievents.PlanEntry(nil), update.Entries...),
			})
		case subagentDomainTerminal:
			msgs = append(msgs, tuievents.SubagentDoneMsg{
				SpawnID: target.SpawnID,
				State:   strings.TrimSpace(update.Status),
			})
		}
	}
	return msgs
}

func (c *cliConsole) dispatchSubagentDomainUpdate(ctx context.Context, update subagentDomainUpdate) {
	if c == nil || c.tuiSender == nil {
		return
	}
	for _, msg := range c.projectSubagentDomainUpdate(update) {
		c.sendSubagentProjectionMsg(ctx, update.Target.RootSessionID, msg)
	}
}

func subagentDomainUpdateFromSpawnToolResponse(rootSessionID string, resp *model.ToolResponse) (subagentDomainUpdate, bool) {
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.SpawnToolName) {
		return subagentDomainUpdate{}, false
	}
	childSessionID := strings.TrimSpace(firstNonEmpty(
		resp.Result,
		"_ui_child_session_id",
		"child_session_id",
	))
	if hasToolError(resp.Result) && childSessionID == "" {
		return subagentDomainUpdate{}, false
	}
	spawnID := childSessionID
	provisional := false
	if spawnID == "" {
		spawnID = strings.TrimSpace(resp.ID)
		provisional = true
	}
	if spawnID == "" {
		return subagentDomainUpdate{}, false
	}
	attachTarget := strings.TrimSpace(firstNonEmpty(
		resp.Result,
		"_ui_child_session_id",
		"child_session_id",
		"_ui_delegation_id",
		"delegation_id",
	))
	if provisional && attachTarget == "" {
		attachTarget = strings.TrimSpace(resp.ID)
	}
	state := "running"
	var approvalTool, approvalCmd string
	if fmt.Sprint(resp.Result["_ui_approval_pending"]) == "true" || fmt.Sprint(resp.Result["approval_pending"]) == "true" {
		state = "waiting_approval"
		if v, ok := resp.Result["_ui_approval_tool"].(string); ok {
			approvalTool = v
		}
		if v, ok := resp.Result["_ui_approval_command"].(string); ok {
			approvalCmd = v
		}
	}
	if explicit := strings.ToLower(strings.TrimSpace(asString(resp.Result["state"]))); explicit != "" {
		state = explicit
	}
	target := subagentProjectionTarget{
		RootSessionID: strings.TrimSpace(rootSessionID),
		SpawnID:       spawnID,
		AttachTarget:  attachTarget,
		CallID:        strings.TrimSpace(resp.ID),
		AnchorTool:    tool.SpawnToolName,
		Agent:         strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_agent", "agent")),
	}
	if target.Agent == "" {
		target.Agent = "self"
	}
	return subagentDomainUpdate{
		Kind:            subagentDomainBootstrap,
		Target:          target,
		Status:          state,
		ClaimAnchor:     true,
		Provisional:     provisional,
		ApprovalTool:    approvalTool,
		ApprovalCommand: approvalCmd,
	}, true
}

func subagentDomainUpdatesFromTaskToolResponse(rootSessionID string, resp *model.ToolResponse, callArgs map[string]any) []subagentDomainUpdate {
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.TaskToolName) {
		return nil
	}
	spawnID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id"))
	if spawnID == "" {
		return nil
	}
	state := strings.ToLower(strings.TrimSpace(firstNonEmpty(resp.Result, "progress_state", "state")))
	if state == "" {
		return nil
	}
	if fmt.Sprint(resp.Result["_ui_idle_timed_out"]) == "true" || fmt.Sprint(resp.Result["idle_timed_out"]) == "true" {
		state = "timed_out"
	}
	callID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_parent_tool_call_id"))
	panelSpawnID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_spawn_id"))
	if panelSpawnID == "" {
		panelSpawnID = spawnID
	}
	anchorTool := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_anchor_tool"))
	if anchorTool == "" {
		anchorTool = tool.SpawnToolName
	}
	target := subagentProjectionTarget{
		RootSessionID: strings.TrimSpace(rootSessionID),
		SpawnID:       panelSpawnID,
		AttachTarget:  strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_child_session_id", "child_session_id", "_ui_delegation_id", "delegation_id")),
		CallID:        callID,
		AnchorTool:    anchorTool,
		Agent:         strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_agent", "agent")),
	}
	if target.Agent == "" {
		target.Agent = "self"
	}
	updates := make([]subagentDomainUpdate, 0, 2)
	action := strings.ToLower(strings.TrimSpace(asString(callArgs["action"])))
	if panelSpawnID != spawnID || action == "write" {
		updates = append(updates, subagentDomainUpdate{
			Kind:        subagentDomainBootstrap,
			Target:      target,
			Status:      state,
			ClaimAnchor: callID != "",
		})
	}
	switch state {
	case "completed", "failed", "interrupted", "timed_out":
		updates = append(updates, subagentDomainUpdate{
			Kind:   subagentDomainTerminal,
			Target: target,
			Status: state,
		})
		return updates
	}
	update := subagentDomainUpdate{
		Kind:   subagentDomainStatus,
		Target: target,
		Status: state,
	}
	if state == "waiting_approval" {
		update.ApprovalTool = strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_approval_tool", "approval_tool"))
		update.ApprovalCommand = strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_approval_command", "approval_command"))
	}
	updates = append(updates, update)
	return updates
}

func subagentDomainUpdateFromSpawnToolCall(rootSessionID string, call model.ToolCall, args map[string]any, defaultAgent string) (subagentDomainUpdate, bool) {
	if !strings.EqualFold(strings.TrimSpace(call.Name), tool.SpawnToolName) {
		return subagentDomainUpdate{}, false
	}
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		return subagentDomainUpdate{}, false
	}
	agent := strings.TrimSpace(asString(args["agent"]))
	if agent == "" {
		agent = strings.TrimSpace(defaultAgent)
	}
	if agent == "" {
		agent = "self"
	}
	return subagentDomainUpdate{
		Kind: subagentDomainBootstrap,
		Target: subagentProjectionTarget{
			RootSessionID: strings.TrimSpace(rootSessionID),
			SpawnID:       callID,
			AttachTarget:  callID,
			CallID:        callID,
			AnchorTool:    tool.SpawnToolName,
			Agent:         agent,
		},
		Status:      "running",
		ClaimAnchor: true,
		Provisional: true,
	}, true
}

func subagentDomainUpdatesFromSpawnToolError(rootSessionID string, resp *model.ToolResponse) []subagentDomainUpdate {
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.SpawnToolName) || !hasToolError(resp.Result) {
		return nil
	}
	errText := strings.TrimSpace(firstNonEmpty(resp.Result, "error", "msg", "message"))
	if errText == "" {
		return nil
	}
	childSessionID := strings.TrimSpace(firstNonEmpty(
		resp.Result,
		"_ui_child_session_id",
		"child_session_id",
	))
	if childSessionID == "" {
		return nil
	}
	spawnID := childSessionID
	attachTarget := strings.TrimSpace(firstNonEmpty(
		resp.Result,
		"_ui_child_session_id",
		"child_session_id",
		"_ui_delegation_id",
		"delegation_id",
	))
	target := subagentProjectionTarget{
		RootSessionID: strings.TrimSpace(rootSessionID),
		SpawnID:       spawnID,
		AttachTarget:  attachTarget,
		CallID:        strings.TrimSpace(resp.ID),
		AnchorTool:    tool.SpawnToolName,
		Agent:         strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_agent", "agent")),
	}
	if target.Agent == "" {
		target.Agent = "self"
	}
	return []subagentDomainUpdate{
		{
			Kind:        subagentDomainBootstrap,
			Target:      target,
			ClaimAnchor: true,
		},
		{
			Kind:       subagentDomainToolCall,
			Target:     target,
			ToolName:   tool.SpawnToolName,
			ToolCallID: strings.TrimSpace(resp.ID),
			Chunk:      errText,
			Stream:     "stderr",
			Final:      true,
		},
		{
			Kind:   subagentDomainTerminal,
			Target: target,
			Status: subagentTerminalStateFromError(errText),
		},
	}
}

func subagentTerminalStateFromError(errText string) string {
	errText = strings.ToLower(strings.TrimSpace(errText))
	switch {
	case strings.Contains(errText, "context deadline exceeded"), strings.Contains(errText, "deadline exceeded"), strings.Contains(errText, "timed out"), strings.Contains(errText, "timeout"):
		return "timed_out"
	case strings.Contains(errText, "context canceled"), strings.Contains(errText, "cancelled"), strings.Contains(errText, "canceled"):
		return "interrupted"
	default:
		return "failed"
	}
}

func syntheticSubagentDomainUpdate(target subagentProjectionTarget, ev *session.Event) subagentDomainUpdate {
	return subagentDomainUpdate{
		Kind:   subagentDomainEvent,
		Target: target,
		Event:  ev,
	}
}

func syntheticSubagentUpdate(target subagentProjectionTarget, ev *session.Event) sessionstream.Update {
	return sessionstream.Update{
		SessionID: strings.TrimSpace(target.SpawnID),
		Event:     annotateSyntheticSubagentEvent(ev, target),
	}
}

func annotateSyntheticSubagentEvent(ev *session.Event, target subagentProjectionTarget) *session.Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if root := strings.TrimSpace(target.RootSessionID); root != "" {
		ev.Meta["parent_session_id"] = root
	}
	if child := strings.TrimSpace(target.SpawnID); child != "" {
		ev.Meta["child_session_id"] = child
	}
	if attach := strings.TrimSpace(target.AttachTarget); attach != "" {
		ev.Meta["delegation_id"] = attach
	}
	if callID := strings.TrimSpace(target.CallID); callID != "" {
		ev.Meta["parent_tool_call_id"] = callID
	}
	if agent := strings.TrimSpace(target.Agent); agent != "" {
		ev.Meta["agent_id"] = agent
		ev.Meta["_ui_agent"] = agent
	}
	ev.Meta["parent_tool_name"] = tool.SpawnToolName
	return ev
}

// spawnPreviewProjector maps child session events from SPAWN subagents
// into SubagentXxx TUI messages for dedicated subagent panel rendering.
type spawnPreviewProjector struct {
	mu     sync.Mutex
	states map[string]*spawnPreviewState
}

type spawnPreviewState struct {
	agent            string
	assistant        string
	reasoning        string
	toolCalls        map[string]toolCallSnapshot
	lastToolCallName string
	lastToolCallArgs string
}

func newSpawnPreviewProjector() *spawnPreviewProjector {
	return &spawnPreviewProjector{
		states: map[string]*spawnPreviewState{},
	}
}

// Project converts a child session event into subagent-specific TUI messages.
// Returns nil if the event is not from a SPAWN child session.
func (p *spawnPreviewProjector) Project(update sessionstream.Update) []any {
	return renderSubagentDomainUpdates(p.ProjectDomain(update))
}

// ProjectDomain converts a child session event into normalized subagent domain
// updates. Returns nil if the event is not from a SPAWN child session.
func (p *spawnPreviewProjector) ProjectDomain(update sessionstream.Update) []subagentDomainUpdate {
	if p == nil || update.Event == nil {
		return nil
	}
	meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
	if !ok || strings.TrimSpace(meta.ParentToolCall) == "" {
		return nil
	}
	// Only handle SPAWN child events and TASK-write continuations.
	if !strings.EqualFold(meta.ParentToolName, tool.SpawnToolName) && !strings.EqualFold(meta.ParentToolName, tool.TaskToolName) {
		return nil
	}
	attachTarget := strings.TrimSpace(meta.ChildSessionID)
	if attachTarget == "" {
		attachTarget = strings.TrimSpace(meta.DelegationID)
	}

	spawnID := strings.TrimSpace(meta.ChildSessionID)
	anchorTool := tool.SpawnToolName
	if strings.EqualFold(meta.ParentToolName, tool.TaskToolName) && strings.TrimSpace(meta.ParentToolCall) != "" {
		spawnID = strings.TrimSpace(meta.ParentToolCall)
		anchorTool = runtime.SubagentContinuationAnchorTool
	} else {
		if spawnID == "" {
			spawnID = strings.TrimSpace(update.SessionID)
		}
		if spawnID == "" {
			spawnID = meta.ParentToolCall
		}
	}

	p.mu.Lock()
	state := p.states[spawnID]
	if state == nil {
		state = &spawnPreviewState{
			toolCalls: map[string]toolCallSnapshot{},
		}
		p.states[spawnID] = state
	}
	if agent := spawnPreviewEventAgent(update.Event); agent != "" {
		state.agent = agent
	}
	if state.agent == "" {
		state.agent = "self"
	}
	agentName := state.agent
	p.mu.Unlock()

	// Handle lifecycle transitions (done/failed/interrupted).
	if info, ok := runtime.LifecycleFromEvent(update.Event); ok {
		if !isTerminalSpawnStatus(info.Status) {
			status := subagentDomainUpdate{
				Kind: subagentDomainBootstrap,
				Target: subagentProjectionTarget{
					SpawnID:      spawnID,
					AttachTarget: attachTarget,
					Agent:        agentName,
					CallID:       meta.ParentToolCall,
					AnchorTool:   anchorTool,
				},
				Status:      string(info.Status),
				ClaimAnchor: false,
			}
			if info.Status == runtime.RunLifecycleStatusWaitingApproval {
				status.ApprovalTool, status.ApprovalCommand = resolveApprovalContext(p, spawnID, info)
			}
			return []subagentDomainUpdate{status}
		}
		if isTerminalSpawnStatus(info.Status) {
			p.mu.Lock()
			delete(p.states, spawnID)
			p.mu.Unlock()
			return []subagentDomainUpdate{{
				Kind: subagentDomainTerminal,
				Target: subagentProjectionTarget{
					SpawnID:      spawnID,
					AttachTarget: attachTarget,
					Agent:        agentName,
					CallID:       meta.ParentToolCall,
					AnchorTool:   anchorTool,
				},
				Status: string(info.Status),
			}}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	state = p.states[spawnID]

	updates := make([]subagentDomainUpdate, 0, 4)
	target := subagentProjectionTarget{
		SpawnID:      spawnID,
		AttachTarget: attachTarget,
		Agent:        state.agent,
		CallID:       meta.ParentToolCall,
		AnchorTool:   anchorTool,
	}

	// Emit SubagentStartMsg on first event for this spawn.
	if len(state.toolCalls) == 0 && state.assistant == "" && state.reasoning == "" {
		updates = append(updates, subagentDomainUpdate{
			Kind:        subagentDomainBootstrap,
			Target:      target,
			ClaimAnchor: false,
		})
	}

	// Project tool calls.
	if toolUpdates := projectSpawnToolActivity(state, target, update.Event); len(toolUpdates) > 0 {
		updates = append(updates, toolUpdates...)
	}

	if update.Event.Message.Role != model.RoleAssistant {
		return updates
	}
	partial := eventIsPartial(update.Event)

	// Project reasoning.
	if chunk := projectSpawnReasoning(state, update.Event, partial); chunk != "" {
		updates = append(updates, subagentDomainUpdate{
			Kind:   subagentDomainStream,
			Target: target,
			Stream: "reasoning",
			Chunk:  chunk,
		})
	}

	// Project assistant answer.
	if chunk := projectSpawnAssistant(state, update.Event, partial); chunk != "" {
		updates = append(updates, subagentDomainUpdate{
			Kind:   subagentDomainStream,
			Target: target,
			Stream: "assistant",
			Chunk:  chunk,
		})
	}

	return updates
}

func spawnPreviewEventAgent(ev *session.Event) string {
	if ev == nil || len(ev.Meta) == 0 {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(ev.Meta, "_ui_agent", "agent_id", "agent"))
}

func isTerminalSpawnStatus(status runtime.RunLifecycleStatus) bool {
	switch status {
	case runtime.RunLifecycleStatusCompleted, runtime.RunLifecycleStatusFailed, runtime.RunLifecycleStatusInterrupted:
		return true
	default:
		return false
	}
}

func projectSubagentSnapshotText(slot *string, text string, partial bool) string {
	if slot == nil || text == "" {
		return ""
	}
	if partial {
		delta := previewDelta(*slot, text)
		*slot = text
		return delta
	}
	chunk := previewDelta(*slot, text)
	*slot = text
	if chunk == "" {
		return ""
	}
	return chunk + "\n"
}

func replaceSubagentReplayText(slot *string, text string) string {
	if slot == nil {
		return ""
	}
	*slot = text
	return *slot
}

func appendSubagentReplayText(slot *string, text string) string {
	if slot == nil {
		return ""
	}
	*slot += text
	return *slot
}

func rememberSubagentToolSnapshot(toolCalls map[string]toolCallSnapshot, callID, name string, args map[string]any) map[string]toolCallSnapshot {
	if strings.TrimSpace(callID) == "" {
		return toolCalls
	}
	if toolCalls == nil {
		toolCalls = map[string]toolCallSnapshot{}
	}
	toolCalls[callID] = toolCallSnapshot{Name: name, Args: args}
	return toolCalls
}

func consumeSubagentToolSnapshot(toolCalls map[string]toolCallSnapshot, callID string) (map[string]toolCallSnapshot, toolCallSnapshot, bool) {
	if toolCalls == nil || strings.TrimSpace(callID) == "" {
		return toolCalls, toolCallSnapshot{}, false
	}
	snapshot, ok := toolCalls[callID]
	if ok {
		delete(toolCalls, callID)
	}
	return toolCalls, snapshot, ok
}

func projectSpawnReasoning(state *spawnPreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	return projectSubagentSnapshotText(&state.reasoning, ev.Message.ReasoningText(), partial)
}

func projectSpawnAssistant(state *spawnPreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	return projectSubagentSnapshotText(&state.assistant, ev.Message.TextContent(), partial)
}

func previewDelta(existing string, incoming string) string {
	if strings.HasPrefix(incoming, existing) {
		return incoming[len(existing):]
	}
	if strings.HasPrefix(existing, incoming) {
		return ""
	}
	return incoming
}

func projectSpawnToolActivity(state *spawnPreviewState, target subagentProjectionTarget, ev *session.Event) []subagentDomainUpdate {
	if state == nil || ev == nil {
		return nil
	}
	calls := ev.Message.ToolCalls()
	updates := make([]subagentDomainUpdate, 0, len(calls)+1)
	for _, call := range calls {
		args := parseToolArgsForDisplay(call.Args)
		callID := strings.TrimSpace(call.ID)
		state.toolCalls = rememberSubagentToolSnapshot(state.toolCalls, callID, call.Name, args)
		state.lastToolCallName = call.Name
		state.lastToolCallArgs = strings.TrimSpace(summarizeToolArgs(call.Name, args))
		updates = append(updates, subagentDomainUpdate{
			Kind:       subagentDomainToolCall,
			Target:     target,
			ToolName:   call.Name,
			ToolCallID: callID,
			Args:       strings.TrimSpace(summarizeToolArgs(call.Name, args)),
		})
	}
	if resp := ev.Message.ToolResponse(); resp != nil {
		respID := strings.TrimSpace(resp.ID)
		stream := "stdout"
		if hasToolError(resp.Result) {
			stream = "stderr"
		}
		var callArgs map[string]any
		if toolCalls, snapshot, ok := consumeSubagentToolSnapshot(state.toolCalls, respID); ok {
			state.toolCalls = toolCalls
			callArgs = snapshot.Args
		}
		summary := summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
		updates = append(updates, subagentDomainUpdate{
			Kind:       subagentDomainToolCall,
			Target:     target,
			ToolName:   resp.Name,
			ToolCallID: respID,
			Chunk:      summary,
			Stream:     stream,
			Final:      true,
		})
		// Detect PLAN tool result and emit SubagentPlanMsg.
		if isPlanToolName(resp.Name) && !hasToolError(resp.Result) {
			entries := subagentPlanEntriesFromToolPayload(callArgs, resp.Result)
			if len(entries) > 0 {
				updates = append(updates, subagentDomainUpdate{
					Kind:    subagentDomainPlan,
					Target:  target,
					Entries: entries,
				})
			}
		}
	}
	return updates
}

func subagentPlanEntriesFromToolPayload(callArgs map[string]any, result map[string]any) []tuievents.PlanEntry {
	var entries []tuievents.PlanEntry
	for _, source := range []any{callArgs["entries"], result["entries"]} {
		if err := decodePlanEntries(source, &entries); err == nil {
			break
		}
		entries = nil
	}
	out := make([]tuievents.PlanEntry, 0, len(entries))
	for _, item := range entries {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		out = append(out, tuievents.PlanEntry{Content: content, Status: status})
	}
	return out
}

// resolveApprovalContext determines the tool name and command for an approval
// event using three sources in priority order:
//  1. Parse the lifecycle error string for explicit tool name (from kernel policy)
//  2. If exactly one pending tool call in the spawn state, use that
//  3. Fall back to the most recently tracked tool call
//
// All state reads happen under the projector lock to avoid races.
func resolveApprovalContext(p *spawnPreviewProjector, spawnID string, info runtime.LifecycleInfo) (tool, command string) {
	// Source 1: Parse tool name from error string.
	// Policy-generated errors include: tool "BASH" requires authorization: ...
	if parsed := parseApprovalToolFromError(info.Error); parsed != "" {
		tool = parsed
	}

	// Snapshot state fields under lock.
	p.mu.Lock()
	s := p.states[spawnID]
	if s == nil {
		p.mu.Unlock()
		return tool, command
	}
	pendingCount := len(s.toolCalls)
	var singleSnap toolCallSnapshot
	if pendingCount == 1 {
		for _, snap := range s.toolCalls {
			singleSnap = snap
		}
	}
	fallbackName := s.lastToolCallName
	fallbackArgs := s.lastToolCallArgs
	p.mu.Unlock()

	// Source 2: If exactly one pending (unresolved) tool call, that's the
	// approval target — use its stored name and args directly.
	if pendingCount == 1 {
		if tool == "" {
			tool = singleSnap.Name
		}
		command = strings.TrimSpace(summarizeToolArgs(tool, singleSnap.Args))
		return tool, command
	}

	// Source 3: Fall back to last tracked tool call.
	if tool == "" {
		tool = fallbackName
	}
	if command == "" {
		command = fallbackArgs
	}
	return tool, command
}

// parseApprovalToolFromError extracts a tool name from a kernel approval error
// string. Policy-layer errors use the format: tool "TOOLNAME" requires ...
func parseApprovalToolFromError(errStr string) string {
	// Look for policy-style pattern: tool "TOOLNAME"
	const prefix = `tool "`
	idx := strings.Index(errStr, prefix)
	if idx < 0 {
		return ""
	}
	rest := errStr[idx+len(prefix):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return ""
	}
	return rest[:end]
}
