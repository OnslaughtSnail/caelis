package session

import (
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// PendingToolCall is one tool call that has not yet been matched by a response.
type PendingToolCall struct {
	EventIndex int
	ID         string
	Name       string
	Args       string
}

// ContextWindow returns the latest model-visible persisted event window.
func ContextWindow(events []*Event) []*Event {
	return ContextWindowEvents(events)
}

// ContextWindowView wraps the latest model-visible persisted event window.
func ContextWindowView(events []*Event) Events {
	return NewEvents(ContextWindow(events))
}

// AgentVisible returns events visible to agent logic from full persisted history.
func AgentVisible(events []*Event) []*Event {
	return WithoutPartial(WithoutLifecycle(ContextWindowView(events)))
}

// InvocationVisible returns events visible to the current agent invocation.
// Unlike AgentVisible, this includes overlay events that were injected only for
// the current run and must not be persisted into future history.
func InvocationVisible(events []*Event) []*Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]*Event, 0, len(events))
	for _, ev := range events {
		if !IsInvocationVisibleEvent(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// InvocationView wraps the events visible to the current agent invocation.
func InvocationView(events []*Event) Events {
	return NewEvents(InvocationVisible(events))
}

// AgentVisibleView wraps the events visible to agent logic from full persisted history.
func AgentVisibleView(events []*Event) Events {
	return NewEvents(AgentVisible(events))
}

// WithoutLifecycle returns persisted session events visible to agent logic.
func WithoutLifecycle(events Events) []*Event {
	if events == nil || events.Len() == 0 {
		return nil
	}
	out := make([]*Event, 0, events.Len())
	for ev := range events.All() {
		if ev == nil || IsLifecycle(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// WithoutPartial returns only canonical persisted history suitable for agent input.
func WithoutPartial(events []*Event) []*Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]*Event, 0, len(events))
	for _, ev := range events {
		if !IsCanonicalHistoryEvent(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// PendingToolCalls returns unmatched tool calls ordered by originating event position.
func PendingToolCalls(events Events) []PendingToolCall {
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
		if calls := ev.Message.ToolCalls(); len(calls) > 0 {
			for _, call := range calls {
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
		if resp := ev.Message.ToolResponse(); resp != nil && strings.TrimSpace(resp.ID) != "" {
			delete(pending, resp.ID)
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

// Messages projects invocation-visible events into model input messages.
func Messages(events Events, _ string, sanitizer func(map[string]any) map[string]any) []model.Message {
	if sanitizer == nil {
		sanitizer = func(result map[string]any) map[string]any { return result }
	}
	capHint := 0
	if events != nil {
		capHint = events.Len()
	}
	out := make([]model.Message, 0, capHint+1)
	if events == nil {
		return out
	}
	for ev := range events.All() {
		if !IsInvocationVisibleEvent(ev) {
			continue
		}
		msg := ev.Message
		if resp := msg.ToolResponse(); resp != nil {
			sanitized := *resp
			sanitized.Result = sanitizer(resp.Result)
			msg = model.MessageFromToolResponse(&sanitized)
		}
		out = append(out, msg)
	}
	return out
}

// IsLifecycle reports whether one event is a runtime lifecycle event.
func IsLifecycle(ev *Event) bool {
	return EventTypeOf(ev) == EventTypeLifecycle
}
