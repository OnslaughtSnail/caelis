package main

import (
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
	if c.spawnPreviewer != nil {
		for _, msg := range c.spawnPreviewer.Project(update) {
			c.tuiSender.Send(msg)
		}
	}
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
	if p == nil || update.Event == nil {
		return nil
	}
	meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
	if !ok || strings.TrimSpace(meta.ParentToolCall) == "" {
		return nil
	}
	// Only handle SPAWN child events (identified by parent tool name).
	if meta.ParentToolName != tool.SpawnToolName {
		return nil
	}
	attachTarget := strings.TrimSpace(meta.ChildSessionID)
	if attachTarget == "" {
		attachTarget = strings.TrimSpace(meta.DelegationID)
	}

	spawnID := strings.TrimSpace(meta.ChildSessionID)
	if spawnID == "" {
		spawnID = strings.TrimSpace(update.SessionID)
	}
	if spawnID == "" {
		spawnID = meta.ParentToolCall
	}

	// Handle lifecycle transitions (done/failed/interrupted).
	if info, ok := runtime.LifecycleFromEvent(update.Event); ok {
		if !isTerminalSpawnStatus(info.Status) {
			statusMsg := tuievents.SubagentStatusMsg{
				SpawnID: spawnID,
				State:   string(info.Status),
			}
			if info.Status == runtime.RunLifecycleStatusWaitingApproval {
				statusMsg.ApprovalTool, statusMsg.ApprovalCommand = resolveApprovalContext(p, spawnID, info)
			}
			return []any{
				tuievents.SubagentStartMsg{
					SpawnID:      spawnID,
					AttachTarget: attachTarget,
					Agent:        "self",
					CallID:       meta.ParentToolCall,
				},
				statusMsg,
			}
		}
		if isTerminalSpawnStatus(info.Status) {
			p.mu.Lock()
			delete(p.states, spawnID)
			p.mu.Unlock()
			return []any{tuievents.SubagentDoneMsg{
				SpawnID: spawnID,
				State:   string(info.Status),
			}}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[spawnID]
	if state == nil {
		state = &spawnPreviewState{
			agent:     "self",
			toolCalls: map[string]toolCallSnapshot{},
		}
		p.states[spawnID] = state
	}

	msgs := make([]any, 0, 4)

	// Emit SubagentStartMsg on first event for this spawn.
	if len(state.toolCalls) == 0 && state.assistant == "" && state.reasoning == "" {
		msgs = append(msgs, tuievents.SubagentStartMsg{
			SpawnID:      spawnID,
			AttachTarget: attachTarget,
			Agent:        state.agent,
			CallID:       meta.ParentToolCall,
		})
	}

	// Project tool calls.
	if toolMsgs := projectSpawnToolActivity(state, spawnID, update.Event); len(toolMsgs) > 0 {
		msgs = append(msgs, toolMsgs...)
	}

	if update.Event.Message.Role != model.RoleAssistant {
		return msgs
	}
	partial := eventIsPartial(update.Event)

	// Project reasoning.
	if chunk := projectSpawnReasoning(state, update.Event, partial); chunk != "" {
		msgs = append(msgs, tuievents.SubagentStreamMsg{
			SpawnID: spawnID,
			Stream:  "reasoning",
			Chunk:   chunk,
		})
	}

	// Project assistant answer.
	if chunk := projectSpawnAssistant(state, update.Event, partial); chunk != "" {
		msgs = append(msgs, tuievents.SubagentStreamMsg{
			SpawnID: spawnID,
			Stream:  "assistant",
			Chunk:   chunk,
		})
	}

	return msgs
}

func isTerminalSpawnStatus(status runtime.RunLifecycleStatus) bool {
	switch status {
	case runtime.RunLifecycleStatusCompleted, runtime.RunLifecycleStatusFailed, runtime.RunLifecycleStatusInterrupted:
		return true
	default:
		return false
	}
}

func projectSpawnReasoning(state *spawnPreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	text := ev.Message.Reasoning
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if partial {
		delta := previewDelta(state.reasoning, text)
		state.reasoning = text
		return delta
	}
	chunk := previewDelta(state.reasoning, text)
	state.reasoning = text
	if strings.TrimSpace(chunk) == "" {
		return ""
	}
	return chunk + "\n"
}

func projectSpawnAssistant(state *spawnPreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	text := ev.Message.TextContent()
	if partial {
		delta := previewDelta(state.assistant, text)
		state.assistant = text
		return delta
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	chunk := previewDelta(state.assistant, text)
	state.assistant = text
	if chunk == "" {
		return ""
	}
	return chunk + "\n"
}

func previewDelta(existing string, incoming string) string {
	if strings.HasPrefix(incoming, existing) {
		return incoming[len(existing):]
	}
	return incoming
}

func projectSpawnToolActivity(state *spawnPreviewState, spawnID string, ev *session.Event) []any {
	if state == nil || ev == nil {
		return nil
	}
	msgs := make([]any, 0, len(ev.Message.ToolCalls)+1)
	for _, call := range ev.Message.ToolCalls {
		args := parseToolArgsForDisplay(call.Args)
		if state.toolCalls == nil {
			state.toolCalls = map[string]toolCallSnapshot{}
		}
		callID := strings.TrimSpace(call.ID)
		if callID != "" {
			state.toolCalls[callID] = toolCallSnapshot{Name: call.Name, Args: args}
		}
		state.lastToolCallName = call.Name
		state.lastToolCallArgs = strings.TrimSpace(summarizeToolArgs(call.Name, args))
		msgs = append(msgs, tuievents.SubagentToolCallMsg{
			SpawnID:  spawnID,
			ToolName: call.Name,
			CallID:   callID,
			Args:     strings.TrimSpace(summarizeToolArgs(call.Name, args)),
		})
	}
	if resp := ev.Message.ToolResponse; resp != nil {
		respID := strings.TrimSpace(resp.ID)
		stream := "stdout"
		if hasToolError(resp.Result) {
			stream = "stderr"
		}
		var callArgs map[string]any
		if state.toolCalls != nil && respID != "" {
			if snapshot, ok := state.toolCalls[respID]; ok {
				callArgs = snapshot.Args
				delete(state.toolCalls, respID)
			}
		}
		summary := summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
		msgs = append(msgs, tuievents.SubagentToolCallMsg{
			SpawnID:  spawnID,
			ToolName: resp.Name,
			CallID:   respID,
			Chunk:    summary,
			Stream:   stream,
			Final:    true,
		})
		// Detect PLAN tool result and emit SubagentPlanMsg.
		if isPlanToolName(resp.Name) && !hasToolError(resp.Result) {
			planMsg := subagentPlanMsgFromToolPayload(spawnID, callArgs, resp.Result)
			if len(planMsg.Entries) > 0 {
				msgs = append(msgs, planMsg)
			}
		}
	}
	return msgs
}

func subagentPlanMsgFromToolPayload(spawnID string, callArgs map[string]any, result map[string]any) tuievents.SubagentPlanMsg {
	var entries []tuievents.PlanEntry
	for _, source := range []any{callArgs["entries"], result["entries"]} {
		if err := decodePlanEntries(source, &entries); err == nil {
			break
		}
		entries = nil
	}
	msg := tuievents.SubagentPlanMsg{SpawnID: spawnID}
	for _, item := range entries {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		msg.Entries = append(msg.Entries, tuievents.PlanEntry{Content: content, Status: status})
	}
	return msg
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
