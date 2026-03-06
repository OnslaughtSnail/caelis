package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const runtimeLifecycleStateKey = "runtime.lifecycle"

// RunStateRequest defines one run-state query.
type RunStateRequest struct {
	AppName   string
	UserID    string
	SessionID string
}

// RunState is the latest lifecycle status snapshot for one session.
type RunState struct {
	HasLifecycle bool
	Status       RunLifecycleStatus
	Phase        string
	Error        string
	ErrorCode    toolexec.ErrorCode
	EventID      string
	UpdatedAt    time.Time
}

// RunState returns the latest lifecycle state from store-backed session state,
// then falls back to persisted lifecycle events for older sessions.
func (r *Runtime) RunState(ctx context.Context, req RunStateRequest) (RunState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return RunState{}, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess := &session.Session{
		AppName: req.AppName,
		UserID:  req.UserID,
		ID:      req.SessionID,
	}
	snapshot, err := r.store.SnapshotState(ctx, sess)
	if err != nil {
		if !errors.Is(err, session.ErrSessionNotFound) {
			return RunState{}, err
		}
	} else if state, ok := runStateFromSnapshot(snapshot); ok {
		return state, nil
	}
	events, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return RunState{}, nil
		}
		return RunState{}, err
	}
	if state, ok := latestRunStateFromEvents(events); ok {
		return state, nil
	}
	return RunState{}, nil
}

func latestRunStateFromEvents(events []*session.Event) (RunState, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if state, ok := runStateFromLifecycleEvent(events[i]); ok {
			return state, true
		}
	}
	return RunState{}, false
}

func runStateFromLifecycleEvent(ev *session.Event) (RunState, bool) {
	info, ok := LifecycleFromEvent(ev)
	if !ok {
		return RunState{}, false
	}
	return RunState{
		HasLifecycle: true,
		Status:       info.Status,
		Phase:        info.Phase,
		Error:        info.Error,
		ErrorCode:    info.ErrorCode,
		EventID:      ev.ID,
		UpdatedAt:    ev.Time,
	}, true
}

func runStateFromSnapshot(snapshot map[string]any) (RunState, bool) {
	if len(snapshot) == 0 {
		return RunState{}, false
	}
	raw, ok := snapshot[runtimeLifecycleStateKey]
	if !ok {
		return RunState{}, false
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return RunState{}, false
	}
	status := RunLifecycleStatus(sprintOrEmpty(payload["status"]))
	if status == "" {
		return RunState{}, false
	}
	state := RunState{
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

func runStateSnapshot(state RunState) map[string]any {
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
		runtimeLifecycleStateKey: payload,
	}
}

// sprintOrEmpty converts a value to string, returning "" for nil values
// instead of the literal "<nil>" that fmt.Sprint produces.
func sprintOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
