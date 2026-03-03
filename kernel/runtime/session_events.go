package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// SessionEventsRequest defines one session event query.
type SessionEventsRequest struct {
	AppName          string
	UserID           string
	SessionID        string
	Limit            int
	IncludeLifecycle bool
}

// SessionEvents returns recent session events ordered from old to new.
func (r *Runtime) SessionEvents(ctx context.Context, req SessionEventsRequest) ([]*session.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess := &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID}
	events, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return nil, nil
		}
		return nil, err
	}
	filtered := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if !req.IncludeLifecycle && isLifecycleEvent(ev) {
			continue
		}
		filtered = append(filtered, ev)
	}
	if req.Limit <= 0 || len(filtered) <= req.Limit {
		return filtered, nil
	}
	start := len(filtered) - req.Limit
	return append([]*session.Event(nil), filtered[start:]...), nil
}
