package runtime

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runreplay"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const replayBufferCapacity = runreplay.DefaultBufferCapacity
const replayFetchLimit = 256

func durableReplaySlice(events []*session.Event) []*session.Event {
	out := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if isDurableReplayEvent(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func lastCursor(events []*session.Event, fallback string) string {
	if len(events) == 0 {
		return fallback
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && strings.TrimSpace(events[i].ID) != "" {
			return events[i].ID
		}
	}
	return fallback
}

func streamResyncEvent() *session.Event {
	return session.MarkUIOnly(&session.Event{
		ID:      eventID(),
		Time:    now(),
		Message: model.Message{Role: model.RoleSystem},
		Meta: map[string]any{
			"kind": "stream_resync",
		},
	})
}

func isDurableReplayEvent(ev *session.Event) bool {
	if ev == nil {
		return false
	}
	if !shouldPersistEvent(ev) {
		return false
	}
	if isEventPartial(ev) {
		return false
	}
	return true
}

func isEventPartial(ev *session.Event) bool {
	return session.IsPartial(ev)
}
