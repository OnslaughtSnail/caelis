package runstatus

import (
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const StateKey = "runtime.lifecycle"

type State struct {
	HasLifecycle bool
	Status       Status
	Phase        string
	Error        string
	ErrorCode    toolexec.ErrorCode
	EventID      string
	UpdatedAt    time.Time
}

func LatestState(events []*session.Event) (State, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if state, ok := StateFromLifecycleEvent(events[i]); ok {
			return state, true
		}
	}
	return State{}, false
}

func StateFromLifecycleEvent(ev *session.Event) (State, bool) {
	info, ok := FromEvent(ev)
	if !ok {
		return State{}, false
	}
	return State{
		HasLifecycle: true,
		Status:       info.Status,
		Phase:        info.Phase,
		Error:        info.Error,
		ErrorCode:    info.ErrorCode,
		EventID:      ev.ID,
		UpdatedAt:    ev.Time,
	}, true
}

func StateFromSnapshot(snapshot map[string]any) (State, bool) {
	if len(snapshot) == 0 {
		return State{}, false
	}
	raw, ok := snapshot[StateKey]
	if !ok {
		return State{}, false
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return State{}, false
	}
	status := Status(sprintOrEmpty(payload["status"]))
	if status == "" {
		return State{}, false
	}
	state := State{
		HasLifecycle: true,
		Status:       status,
		Phase:        sprintOrEmpty(payload["phase"]),
		Error:        sprintOrEmpty(payload["error"]),
		ErrorCode:    toolexec.ErrorCode(sprintOrEmpty(payload["error_code"])),
		EventID:      sprintOrEmpty(payload["event_id"]),
	}
	if rawUpdatedAt := sprintOrEmpty(payload["updated_at"]); rawUpdatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, rawUpdatedAt); err == nil {
			state.UpdatedAt = parsed
		}
	}
	return state, true
}

func StateSnapshot(state State) map[string]any {
	if !state.HasLifecycle {
		return map[string]any{}
	}
	payload := map[string]any{
		"status": string(state.Status),
		"phase":  state.Phase,
	}
	if strings.TrimSpace(state.Error) != "" {
		payload["error"] = state.Error
	}
	if strings.TrimSpace(string(state.ErrorCode)) != "" {
		payload["error_code"] = string(state.ErrorCode)
	}
	if strings.TrimSpace(state.EventID) != "" {
		payload["event_id"] = state.EventID
	}
	if !state.UpdatedAt.IsZero() {
		payload["updated_at"] = state.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		StateKey: payload,
	}
}

func sprintOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
