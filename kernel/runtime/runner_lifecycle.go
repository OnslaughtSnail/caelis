package runtime

import (
	"errors"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

func (h *runHandle) appendOutputLifecycle(status RunLifecycleStatus, phase string, cause error) bool {
	return h.runtime.appendAndYieldLifecycle(h.ctx, h.sess, status, phase, cause, func(ev *session.Event, err error) bool {
		return h.appendOutput(ev, err, false)
	})
}

func (h *runHandle) emitRunError(err error) bool {
	if err == nil {
		return true
	}
	if !h.appendVisibleRunErrorNotice(err) {
		return false
	}
	status := lifecycleStatusForError(err)
	if !h.appendOutputLifecycle(status, "run", err) {
		return false
	}
	return h.appendOutput(nil, err, false)
}

func (h *runHandle) emitTerminalError(err error) {
	if err == nil {
		return
	}
	_ = h.emitRunError(err)
}

func (h *runHandle) appendVisibleRunErrorNotice(err error) bool {
	text := strings.TrimSpace(visibleRunErrorNotice(err))
	if text == "" {
		return true
	}
	ev := session.MarkNotice(&session.Event{
		ID:   eventID(),
		Time: time.Now(),
	}, session.NoticeLevelWarn, text)
	return h.appendOutput(ev, nil, false)
}

func visibleRunErrorNotice(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errLoopDetected):
		return "runtime detected repeated identical model output and stopped the turn to prevent an infinite tool loop."
	default:
		return ""
	}
}

func (h *runHandle) appendOutput(ev *session.Event, err error, persist bool) bool {
	if ev != nil {
		prepareEvent(h.ctx, h.sess, ev)
		if persist {
			if appendErr := h.runtime.store.AppendEvent(h.ctx, h.sess, ev); appendErr != nil {
				h.replay.append(nil, appendErr, false)
				return false
			}
		}
		sessionstream.Emit(h.ctx, ev.SessionID, ev)
	}
	durable := ev != nil && isDurableReplayEvent(ev)
	h.replay.append(ev, err, durable)
	select {
	case h.eventNotifyCh <- struct{}{}:
	default:
	}
	return err == nil
}
