package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

type delegatePreviewProjector struct {
	mu     sync.Mutex
	states map[string]*delegatePreviewState
}

type delegatePreviewState struct {
	assistant        string
	reasoning        string
	pendingToolCalls map[string]toolCallSnapshot
}

func newDelegatePreviewProjector() *delegatePreviewProjector {
	return &delegatePreviewProjector{
		states: map[string]*delegatePreviewState{},
	}
}

func (c *cliConsole) forwardSessionEventToTUI(rootSessionID string, update sessionstream.Update) {
	if c == nil || c.tuiSender == nil || c.delegatePreviewer == nil || update.Event == nil {
		return
	}
	if strings.TrimSpace(update.SessionID) == "" || strings.TrimSpace(update.SessionID) == strings.TrimSpace(rootSessionID) {
		return
	}
	for _, msg := range c.delegatePreviewer.Project(update) {
		c.tuiSender.Send(msg)
	}
}

func (p *delegatePreviewProjector) Project(update sessionstream.Update) []tuievents.TaskStreamMsg {
	if p == nil || update.Event == nil {
		return nil
	}
	meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
	if !ok || strings.TrimSpace(meta.ParentToolCall) == "" {
		return nil
	}
	key := strings.TrimSpace(meta.ChildSessionID)
	if key == "" {
		key = strings.TrimSpace(update.SessionID)
	}
	if key == "" {
		key = meta.ParentToolCall
	}
	if info, ok := runtime.LifecycleFromEvent(update.Event); ok {
		if isTerminalDelegateStatus(info.Status) {
			p.mu.Lock()
			delete(p.states, key)
			p.mu.Unlock()
			return []tuievents.TaskStreamMsg{{
				Label:  toolDisplayDelegate,
				CallID: meta.ParentToolCall,
				State:  string(info.Status),
				Final:  true,
			}}
		}
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[key]
	if state == nil {
		state = &delegatePreviewState{pendingToolCalls: map[string]toolCallSnapshot{}}
		p.states[key] = state
	}

	msgs := make([]tuievents.TaskStreamMsg, 0, 4)
	if toolMsgs := projectDelegateToolActivity(state, meta.ParentToolCall, update.Event); len(toolMsgs) > 0 {
		msgs = append(msgs, toolMsgs...)
	}
	if update.Event.Message.Role != model.RoleAssistant {
		return msgs
	}
	partial := eventIsPartial(update.Event)
	if chunk := projectDelegateReasoning(state, update.Event, partial); chunk != "" {
		msgs = append(msgs, tuievents.TaskStreamMsg{
			Label:  toolDisplayDelegate,
			CallID: meta.ParentToolCall,
			Stream: "reasoning",
			Chunk:  chunk,
		})
	}
	if chunk := projectDelegateAssistant(state, update.Event, partial); chunk != "" {
		msgs = append(msgs, tuievents.TaskStreamMsg{
			Label:  toolDisplayDelegate,
			CallID: meta.ParentToolCall,
			Stream: "assistant",
			Chunk:  chunk,
		})
	}
	return msgs
}

const toolDisplayDelegate = "DELEGATE"

func projectDelegateReasoning(state *delegatePreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	text := ev.Message.Reasoning
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if partial {
		delta := delegatePreviewDelta(state.reasoning, text)
		state.reasoning = text
		return delta
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	chunk := delegatePreviewDelta(state.reasoning, text)
	state.reasoning = text
	if strings.TrimSpace(chunk) == "" {
		return ""
	}
	return chunk + "\n"
}

func projectDelegateAssistant(state *delegatePreviewState, ev *session.Event, partial bool) string {
	if state == nil || ev == nil {
		return ""
	}
	text := ev.Message.TextContent()
	if partial {
		delta := delegatePreviewDelta(state.assistant, text)
		state.assistant = text
		return delta
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	chunk := delegatePreviewDelta(state.assistant, text)
	state.assistant = text
	if chunk == "" {
		return ""
	}
	return chunk + "\n"
}

func projectDelegateToolActivity(state *delegatePreviewState, parentCallID string, ev *session.Event) []tuievents.TaskStreamMsg {
	if state == nil || ev == nil {
		return nil
	}
	msgs := make([]tuievents.TaskStreamMsg, 0, len(ev.Message.ToolCalls)+1)
	for _, call := range ev.Message.ToolCalls {
		args := parseToolArgsForDisplay(call.Args)
		if state.pendingToolCalls == nil {
			state.pendingToolCalls = map[string]toolCallSnapshot{}
		}
		if strings.TrimSpace(call.ID) != "" {
			state.pendingToolCalls[strings.TrimSpace(call.ID)] = toolCallSnapshot{Args: args}
		}
		line := strings.TrimSpace(fmt.Sprintf("▸ %s %s", displayToolCallName(call.Name, args), summarizeToolArgs(call.Name, args)))
		if line == "" {
			continue
		}
		msgs = append(msgs, tuievents.TaskStreamMsg{
			Label:  toolDisplayDelegate,
			CallID: parentCallID,
			Stream: "assistant",
			Chunk:  line + "\n",
		})
	}
	if resp := ev.Message.ToolResponse; resp != nil {
		var callArgs map[string]any
		if state.pendingToolCalls != nil && strings.TrimSpace(resp.ID) != "" {
			if snapshot, ok := state.pendingToolCalls[strings.TrimSpace(resp.ID)]; ok {
				callArgs = snapshot.Args
				delete(state.pendingToolCalls, strings.TrimSpace(resp.ID))
			}
		}
		displayName := displayToolResponseName(resp.Name, callArgs, resp.Result)
		summary := summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
		if summary == "" && !hasToolError(resp.Result) {
			return msgs
		}
		prefix := "✓ "
		stream := "assistant"
		if hasToolError(resp.Result) {
			prefix = "! "
			stream = "stderr"
		}
		msgs = append(msgs, tuievents.TaskStreamMsg{
			Label:  toolDisplayDelegate,
			CallID: parentCallID,
			Stream: stream,
			Chunk:  formatToolResultLine(prefix, displayName, summary),
		})
	}
	return msgs
}

func delegatePreviewDelta(existing string, incoming string) string {
	switch {
	case incoming == "":
		return ""
	case existing == "":
		return incoming
	case strings.HasPrefix(incoming, existing):
		return incoming[len(existing):]
	case strings.HasPrefix(existing, incoming):
		return ""
	default:
		return incoming
	}
}

func isTerminalDelegateStatus(status runtime.RunLifecycleStatus) bool {
	switch status {
	case runtime.RunLifecycleStatusCompleted, runtime.RunLifecycleStatusFailed, runtime.RunLifecycleStatusInterrupted:
		return true
	default:
		return false
	}
}
