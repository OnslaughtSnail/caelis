package eventview

import (
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// PendingToolCall is one tool call that has not yet been matched by a response.
type PendingToolCall struct {
	EventIndex int
	ID         string
	Name       string
	Args       string
}

// ContextWindow returns the latest model-visible persisted event window.
func ContextWindow(events []*session.Event) []*session.Event {
	return session.ContextWindowEvents(events)
}

// ContextWindowView wraps the latest model-visible persisted event window.
func ContextWindowView(events []*session.Event) session.Events {
	return session.NewEvents(ContextWindow(events))
}

// AgentVisible returns events visible to agent logic from full persisted history.
func AgentVisible(events []*session.Event) []*session.Event {
	return WithoutLifecycle(ContextWindowView(events))
}

// AgentVisibleView wraps the events visible to agent logic from full persisted history.
func AgentVisibleView(events []*session.Event) session.Events {
	return session.NewEvents(AgentVisible(events))
}

// WithoutLifecycle returns persisted session events visible to agent logic.
func WithoutLifecycle(events session.Events) []*session.Event {
	if events == nil || events.Len() == 0 {
		return nil
	}
	out := make([]*session.Event, 0, events.Len())
	for ev := range events.All() {
		if ev == nil || IsLifecycle(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// PendingToolCalls returns unmatched tool calls ordered by originating event position.
func PendingToolCalls(events session.Events) []PendingToolCall {
	if events == nil || events.Len() == 0 {
		return nil
	}
	pending := map[string]PendingToolCall{}
	order := make([]string, 0, 8)
	for idx := 0; idx < events.Len(); idx++ {
		ev := events.At(idx)
		if ev == nil {
			continue
		}
		if len(ev.Message.ToolCalls) > 0 {
			for _, call := range ev.Message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
					continue
				}
				if _, exists := pending[call.ID]; exists {
					continue
				}
				pending[call.ID] = PendingToolCall{
					EventIndex: idx,
					ID:         call.ID,
					Name:       call.Name,
					Args:       strings.TrimSpace(call.Args),
				}
				order = append(order, call.ID)
			}
		}
		if ev.Message.ToolResponse != nil && strings.TrimSpace(ev.Message.ToolResponse.ID) != "" {
			delete(pending, ev.Message.ToolResponse.ID)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(order, func(i, j int) bool {
		left, lok := pending[order[i]]
		right, rok := pending[order[j]]
		if !lok || !rok {
			return order[i] < order[j]
		}
		if left.EventIndex == right.EventIndex {
			return left.ID < right.ID
		}
		return left.EventIndex < right.EventIndex
	})
	out := make([]PendingToolCall, 0, len(order))
	for _, callID := range order {
		call, exists := pending[callID]
		if exists {
			out = append(out, call)
		}
	}
	return out
}

// Messages projects persisted events into model input messages.
func Messages(events session.Events, systemPrompt string, sanitizer func(map[string]any) map[string]any) []model.Message {
	if sanitizer == nil {
		sanitizer = func(result map[string]any) map[string]any { return result }
	}
	capHint := 0
	if events != nil {
		capHint = events.Len()
	}
	out := make([]model.Message, 0, capHint+1)
	if strings.TrimSpace(systemPrompt) != "" {
		out = append(out, model.Message{Role: model.RoleSystem, Text: systemPrompt})
	}
	if events == nil {
		return out
	}
	for ev := range events.All() {
		if ev == nil {
			continue
		}
		if session.IsUIOnly(ev) || session.IsNotice(ev) {
			continue
		}
		msg := ev.Message
		if msg.ToolResponse != nil {
			resp := *msg.ToolResponse
			resp.Result = sanitizer(resp.Result)
			msg.ToolResponse = &resp
		}
		out = append(out, msg)
	}
	return out
}

// IsLifecycle reports whether one event is a runtime lifecycle event.
func IsLifecycle(ev *session.Event) bool {
	return session.EventTypeOf(ev) == session.EventTypeLifecycle
}
