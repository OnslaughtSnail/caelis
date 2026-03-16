package session

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

const (
	metaVisibilityKey     = "visibility"
	metaVisibilityUIOnly  = "ui_only"
	metaVisibilityOverlay = "overlay"

	metaNoticeLevelKey = "notice_level"
	metaNoticeTextKey  = "notice_text"
)

const (
	NoticeLevelWarn = "warn"
	NoticeLevelNote = "note"
)

// Notice is one transient runtime notice rendered to the user but excluded
// from model context and persisted history.
type Notice struct {
	Level string
	Text  string
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

// EventNotice returns the notice carried by one event, if any. It also
// recognizes legacy RoleSystem warn:/note: messages for backward compatibility.
func EventNotice(ev *Event) (Notice, bool) {
	if ev == nil {
		return Notice{}, false
	}
	if notice, ok := eventNoticeMeta(ev.Meta); ok {
		return notice, true
	}
	return MessageNotice(ev.Message)
}

// MessageNotice recognizes legacy RoleSystem warn:/note: runtime notices.
func MessageNotice(msg model.Message) (Notice, bool) {
	if msg.Role != model.RoleSystem {
		return Notice{}, false
	}
	text := strings.TrimSpace(msg.Text)
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

func eventNoticeMeta(meta map[string]any) (Notice, bool) {
	if meta == nil {
		return Notice{}, false
	}
	level, _ := meta[metaNoticeLevelKey].(string)
	text, _ := meta[metaNoticeTextKey].(string)
	level = normalizeNoticeLevel(level)
	text = strings.TrimSpace(text)
	if level == "" || text == "" {
		return Notice{}, false
	}
	return Notice{Level: level, Text: text}, true
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
