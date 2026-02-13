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

// RunState returns latest lifecycle state from persisted session events.
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
	events, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return RunState{}, nil
		}
		return RunState{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		info, ok := LifecycleFromEvent(ev)
		if !ok {
			continue
		}
		return RunState{
			HasLifecycle: true,
			Status:       info.Status,
			Phase:        info.Phase,
			Error:        info.Error,
			ErrorCode:    info.ErrorCode,
			EventID:      ev.ID,
			UpdatedAt:    ev.Time,
		}, nil
	}
	return RunState{}, nil
}
