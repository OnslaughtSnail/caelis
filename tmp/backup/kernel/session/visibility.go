package session

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

const (
	metaVisibilityKey     = "visibility"
	metaVisibilityUIOnly  = "ui_only"
	metaVisibilityOverlay = "overlay"
	metaVisibilityMirror  = "mirror"

	metaNoticeLevelKey = "notice_level"
	metaNoticeTextKey  = "notice_text"
)

const (
	NoticeLevelWarn = "warn"
	NoticeLevelNote = "note"
)

// Notice is one transient runtime notice rendered to the user but excluded
// from model context and persisted history. When Kind is non-empty, it
// identifies a structured notice whose presentation text should be generated
// by the UI layer from the event metadata rather than from the Text field.
type Notice struct {
	Level string
	Text  string
	Kind  string
	Meta  map[string]any
}

// MarkUIOnly annotates one event as UI-only so it can be shown live but
// excluded from persistence/model context reconstruction.
func MarkUIOnly(ev *Event) *Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaVisibilityKey] = metaVisibilityUIOnly
	return SetEventType(ev, EventTypeOf(ev))
}

// IsUIOnly reports whether an event is marked as UI-only.
func IsUIOnly(ev *Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	value, _ := ev.Meta[metaVisibilityKey].(string)
	return strings.TrimSpace(value) == metaVisibilityUIOnly
}

// MarkOverlay annotates one event as an ephemeral overlay event. Overlay events
// are model-visible for the current in-flight request, but excluded from
// persisted history and future conversation context.
func MarkOverlay(ev *Event) *Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaVisibilityKey] = metaVisibilityOverlay
	return SetEventType(ev, EventTypeOf(ev))
}

// IsOverlay reports whether an event is marked as an ephemeral overlay event.
func IsOverlay(ev *Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	value, _ := ev.Meta[metaVisibilityKey].(string)
	return strings.TrimSpace(value) == metaVisibilityOverlay
}

// MarkMirror annotates an event as durable transcript-only state. Mirror events
// are persisted and replayed in the UI, but excluded from model context and
// future invocation state.
func MarkMirror(ev *Event) *Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaVisibilityKey] = metaVisibilityMirror
	return SetEventType(ev, EventTypeOf(ev))
}

// IsMirror reports whether an event is transcript-only durable UI state.
func IsMirror(ev *Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	value, _ := ev.Meta[metaVisibilityKey].(string)
	return strings.TrimSpace(value) == metaVisibilityMirror
}

// MarkNotice annotates one event as a transient user-visible notice. Notices
// are inherently UI-only and do not need a model message role.
func MarkNotice(ev *Event, level string, text string) *Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	level = normalizeNoticeLevel(level)
	text = strings.TrimSpace(text)
	ev.Meta[metaNoticeLevelKey] = level
	ev.Meta[metaNoticeTextKey] = text
	return SetEventType(MarkUIOnly(ev), EventTypeOf(ev))
}

// EventNotice returns the notice carried by one event, if any.
func EventNotice(ev *Event) (Notice, bool) {
	if ev == nil {
		return Notice{}, false
	}
	if notice, ok := eventNoticeMeta(ev.Meta); ok {
		return notice, true
	}
	return MessageNotice(ev.Message)
}

// MessageNotice recognizes one system-message runtime notice.
func MessageNotice(msg model.Message) (Notice, bool) {
	if msg.Role != model.RoleSystem {
		return Notice{}, false
	}
	text := strings.TrimSpace(msg.TextContent())
	if text == "" {
		return Notice{}, false
	}
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, NoticeLevelWarn+":"):
		return Notice{Level: NoticeLevelWarn, Text: strings.TrimSpace(text[len(NoticeLevelWarn)+1:])}, true
	case strings.HasPrefix(lower, NoticeLevelNote+":"):
		return Notice{Level: NoticeLevelNote, Text: strings.TrimSpace(text[len(NoticeLevelNote)+1:])}, true
	default:
		return Notice{}, false
	}
}

// IsNotice reports whether one event carries a transient runtime notice.
func IsNotice(ev *Event) bool {
	_, ok := EventNotice(ev)
	return ok
}

// IsTransient reports whether an event is runtime-transient only. Transient
// events may be streamed to the UI during one run but must never become part of
// canonical durable history or future agent input.
//
// Event visibility tiers:
//   - Canonical durable history: persisted events projected into future context.
//     Identified by IsCanonicalHistoryEvent.
//   - Invocation-only: visible to the current agent run (includes overlays)
//     but excluded from persistence. Identified by IsInvocationVisibleEvent.
//   - Transient: streaming-only events (partials, notices, UI-only) that are
//     neither persisted nor projected. Identified by IsTransient.
func IsTransient(ev *Event) bool {
	if ev == nil {
		return true
	}
	return IsPartial(ev) || IsUIOnly(ev) || IsOverlay(ev) || IsNotice(ev)
}

// IsCanonicalHistoryEvent reports whether an event belongs to durable session
// history and may be projected back into future agent context.
func IsCanonicalHistoryEvent(ev *Event) bool {
	if ev == nil {
		return false
	}
	if IsTransient(ev) || IsLifecycle(ev) || IsMirror(ev) {
		return false
	}
	return true
}

// IsInvocationVisibleEvent reports whether an event may participate in the
// current agent invocation context. Overlay events are visible only to the
// current run, while other transient events remain excluded.
func IsInvocationVisibleEvent(ev *Event) bool {
	if ev == nil || IsLifecycle(ev) || IsPartial(ev) || IsUIOnly(ev) || IsNotice(ev) || IsMirror(ev) {
		return false
	}
	if isForeignControllerToolProtocolEvent(ev) {
		return false
	}
	return true
}

func isForeignControllerToolProtocolEvent(ev *Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	controllerKind, _ := ev.Meta["controller_kind"].(string)
	if strings.TrimSpace(strings.ToLower(controllerKind)) != "acp" {
		return false
	}
	return len(ev.Message.ToolCalls()) > 0 || ev.Message.ToolResponse() != nil
}

func eventNoticeMeta(meta map[string]any) (Notice, bool) {
	if meta == nil {
		return Notice{}, false
	}
	level, _ := meta[metaNoticeLevelKey].(string)
	text, _ := meta[metaNoticeTextKey].(string)
	kind, _ := meta["kind"].(string)
	level = normalizeNoticeLevel(level)
	text = strings.TrimSpace(text)
	if level == "" || text == "" {
		return Notice{}, false
	}
	return Notice{
		Level: level,
		Text:  text,
		Kind:  strings.TrimSpace(kind),
		Meta:  cloneMap(meta),
	}, true
}

func normalizeNoticeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case NoticeLevelWarn:
		return NoticeLevelWarn
	case NoticeLevelNote:
		return NoticeLevelNote
	default:
		return ""
	}
}
