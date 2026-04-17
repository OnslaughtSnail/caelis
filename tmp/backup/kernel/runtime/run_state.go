package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/OnslaughtSnail/caelis/kernel/runstatus"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"strings"
)

const runtimeLifecycleStateKey = runstatus.StateKey

// RunStateRequest defines one run-state query.
type RunStateRequest struct {
	AppName   string
	UserID    string
	SessionID string
}

// RunState is the latest lifecycle status snapshot for one session.
type RunState = runstatus.State

// RunState returns the latest lifecycle state from store-backed session state,
// then falls back to persisted lifecycle events for older sessions.
func (r *Runtime) RunState(ctx context.Context, req RunStateRequest) (RunState, error) {
	if ctx == nil {
		return RunState{}, fmt.Errorf("runtime: context is required")
	}
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return RunState{}, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess := &session.Session{
		AppName: req.AppName,
		UserID:  req.UserID,
		ID:      req.SessionID,
	}
	if r.lifecycleStore != nil {
		state, err := r.lifecycleStore.Load(ctx, sess)
		if err == nil && state.HasLifecycle {
			return state, nil
		}
		if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
			return RunState{}, err
		}
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
	return runstatus.LatestState(events)
}

func runStateFromLifecycleEvent(ev *session.Event) (RunState, bool) {
	return runstatus.StateFromLifecycleEvent(ev)
}

func runStateFromSnapshot(snapshot map[string]any) (RunState, bool) {
	return runstatus.StateFromSnapshot(snapshot)
}

func runStateSnapshot(state RunState) map[string]any {
	return runstatus.StateSnapshot(state)
}
