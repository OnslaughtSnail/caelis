package main

import (
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
	assistant string
	reasoning string
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
	if update.Event.Message.Role != model.RoleAssistant {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.states[key]
	if state == nil {
		state = &delegatePreviewState{}
		p.states[key] = state
	}

	msgs := make([]tuievents.TaskStreamMsg, 0, 2)
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
	text := strings.TrimSpace(ev.Message.Reasoning)
	if text == "" {
		return ""
	}
	if partial {
		delta := delegatePreviewDelta(state.reasoning, text)
		state.reasoning = text
		return delta
	}
	chunk := delegatePreviewDelta(state.reasoning, text)
	state.reasoning = text
	if chunk == "" {
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
