package sessionstream

import (
	"context"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type contextKey struct{}

// Update is one raw live session event emitted during runtime execution.
// The event payload is preserved as-is so callers can project/filter it.
type Update struct {
	SessionID string
	Event     *session.Event
}

type Streamer interface {
	StreamSessionEvent(context.Context, Update)
}

type StreamerFunc func(context.Context, Update)

func (f StreamerFunc) StreamSessionEvent(ctx context.Context, update Update) {
	if f != nil {
		f(ctx, normalizeUpdate(update))
	}
}

func WithStreamer(ctx context.Context, streamer Streamer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if streamer == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, streamer)
}

func StreamerFromContext(ctx context.Context) (Streamer, bool) {
	if ctx == nil {
		return nil, false
	}
	streamer, ok := ctx.Value(contextKey{}).(Streamer)
	return streamer, ok
}

func Emit(ctx context.Context, sessionID string, ev *session.Event) {
	streamer, ok := StreamerFromContext(ctx)
	if !ok || streamer == nil || ev == nil {
		return
	}
	streamer.StreamSessionEvent(ctx, normalizeUpdate(Update{
		SessionID: sessionID,
		Event:     ev,
	}))
}

func normalizeUpdate(update Update) Update {
	update.SessionID = strings.TrimSpace(update.SessionID)
	if update.Event != nil {
		if update.SessionID == "" {
			update.SessionID = strings.TrimSpace(update.Event.SessionID)
		}
	}
	return update
}
