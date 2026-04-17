package runtime

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func (r *Runtime) listContextWindowEvents(ctx context.Context, sess *session.Session) ([]*session.Event, error) {
	if r == nil || r.logStore == nil {
		return nil, nil
	}
	if withWindow, ok := r.logStore.(session.ContextWindowStore); ok {
		return withWindow.ListContextWindowEvents(ctx, sess)
	}
	events, err := r.logStore.ListEvents(ctx, sess)
	if err != nil {
		return nil, err
	}
	return session.ContextWindow(events), nil
}
