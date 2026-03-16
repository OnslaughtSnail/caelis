package session

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

const metaEventTypeKey = "event_type"
const metaKindKey = "kind"

// EventType is the normalized machine-readable category for one session event.
// Upper layers should prefer this over ad-hoc meta inspection.
type EventType string

const (
	EventTypeUnknown                 EventType = ""
	EventTypeConversation            EventType = "conversation"
	EventTypeSystemMessage           EventType = "system_message"
	EventTypePartialAnswer           EventType = "partial_answer"
	EventTypePartialReasoning        EventType = "partial_reasoning"
	EventTypeLifecycle               EventType = "lifecycle"
	EventTypeNotice                  EventType = "notice"
	EventTypeOverlay                 EventType = "overlay"
	EventTypeOverlayPartialAnswer    EventType = "overlay_partial_answer"
	EventTypeOverlayPartialReasoning EventType = "overlay_partial_reasoning"
	EventTypeCompaction              EventType = "compaction"
	EventTypeCompactionNotice        EventType = "compaction_notice"
	EventTypeStreamResync            EventType = "stream_resync"
	EventTypeUIOnly                  EventType = "ui_only"
)

type PartialChannel string

const (
	PartialChannelAnswer    PartialChannel = "answer"
	PartialChannelReasoning PartialChannel = "reasoning"
)

func NormalizeEventType(value string) EventType {
	switch EventType(strings.TrimSpace(strings.ToLower(value))) {
	case EventTypeConversation,
		EventTypeSystemMessage,
		EventTypePartialAnswer,
		EventTypePartialReasoning,
		EventTypeLifecycle,
		EventTypeNotice,
		EventTypeOverlay,
		EventTypeOverlayPartialAnswer,
		EventTypeOverlayPartialReasoning,
		EventTypeCompaction,
		EventTypeCompactionNotice,
		EventTypeStreamResync,
		EventTypeUIOnly:
		return EventType(strings.TrimSpace(strings.ToLower(value)))
	default:
		return EventTypeUnknown
	}
}

// SetEventType stores one normalized event type on the event metadata.
func SetEventType(ev *Event, typ EventType) *Event {
	if ev == nil {
		return nil
	}
	typ = NormalizeEventType(string(typ))
	if typ == EventTypeUnknown {
		return ev
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaEventTypeKey] = string(typ)
	return ev
}

// EventTypeOf returns the explicit or inferred category for one event.
func EventTypeOf(ev *Event) EventType {
	if ev == nil {
		return EventTypeUnknown
	}
	if ev.Meta != nil {
		if raw, ok := ev.Meta[metaEventTypeKey].(string); ok {
			if normalized := NormalizeEventType(raw); normalized != EventTypeUnknown {
				return normalized
			}
		}
	}
	return inferEventType(ev)
}

// EnsureEventType annotates the event with its inferred type when not already set.
func EnsureEventType(ev *Event) *Event {
	if ev == nil {
		return nil
	}
	inferred := EventTypeOf(ev)
	if inferred == EventTypeUnknown {
		return ev
	}
	if ev.Meta != nil {
		if raw, ok := ev.Meta[metaEventTypeKey].(string); ok && NormalizeEventType(raw) != EventTypeUnknown {
			return ev
		}
	}
	return SetEventType(ev, inferred)
}

func IsPartial(ev *Event) bool {
	return EventTypeOf(ev) == EventTypePartialAnswer ||
		EventTypeOf(ev) == EventTypePartialReasoning ||
		EventTypeOf(ev) == EventTypeOverlayPartialAnswer ||
		EventTypeOf(ev) == EventTypeOverlayPartialReasoning
}

func PartialChannelOf(ev *Event) PartialChannel {
	switch explicitEventType(ev) {
	case EventTypePartialReasoning, EventTypeOverlayPartialReasoning:
		return PartialChannelReasoning
	case EventTypePartialAnswer, EventTypeOverlayPartialAnswer:
		return PartialChannelAnswer
	}
	if ev == nil || ev.Meta == nil {
		return PartialChannelAnswer
	}
	raw, ok := ev.Meta["channel"].(string)
	if !ok {
		return PartialChannelAnswer
	}
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(PartialChannelReasoning):
		return PartialChannelReasoning
	default:
		return PartialChannelAnswer
	}
}

func explicitEventType(ev *Event) EventType {
	if ev == nil || ev.Meta == nil {
		return EventTypeUnknown
	}
	raw, ok := ev.Meta[metaEventTypeKey].(string)
	if !ok {
		return EventTypeUnknown
	}
	return NormalizeEventType(raw)
}

func inferEventType(ev *Event) EventType {
	if ev == nil {
		return EventTypeUnknown
	}
	kind := ""
	if ev.Meta != nil {
		kind, _ = ev.Meta[metaKindKey].(string)
		kind = strings.TrimSpace(strings.ToLower(kind))
	}
	if kind == "stream_resync" {
		return EventTypeStreamResync
	}
	if kind == "compaction" {
		return EventTypeCompaction
	}
	if kind == "lifecycle" {
		return EventTypeLifecycle
	}
	if IsNotice(ev) {
		if kind == "compaction_notice" {
			return EventTypeCompactionNotice
		}
		return EventTypeNotice
	}
	if isPartialLegacy(ev) {
		if IsOverlay(ev) {
			if PartialChannelOf(ev) == PartialChannelReasoning {
				return EventTypeOverlayPartialReasoning
			}
			return EventTypeOverlayPartialAnswer
		}
		if PartialChannelOf(ev) == PartialChannelReasoning {
			return EventTypePartialReasoning
		}
		return EventTypePartialAnswer
	}
	if IsOverlay(ev) {
		return EventTypeOverlay
	}
	if IsUIOnly(ev) {
		return EventTypeUIOnly
	}
	if ev.Message.Role == model.RoleSystem {
		return EventTypeSystemMessage
	}
	return EventTypeConversation
}

func isPartialLegacy(ev *Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	raw, ok := ev.Meta["partial"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}
