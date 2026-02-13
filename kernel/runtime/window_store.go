package runtime

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func (r *Runtime) listContextWindowEvents(ctx context.Context, sess *session.Session) ([]*session.Event, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	if withWindow, ok := r.store.(session.ContextWindowStore); ok {
		return withWindow.ListContextWindowEvents(ctx, sess)
	}
	events, err := r.store.ListEvents(ctx, sess)
	if err != nil {
		return nil, err
	}
	return contextWindowEvents(events), nil
}
