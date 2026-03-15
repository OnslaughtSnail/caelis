package session

import (
	"context"
	"errors"
	"iter"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

var ErrSessionNotFound = errors.New("session: not found")

// Session identifies a conversation thread.
type Session struct {
	AppName string
	UserID  string
	ID      string
}

// Event is the persisted unit of runtime history used to rebuild invocation context and state.
type Event struct {
	ID        string
	SessionID string
	Time      time.Time
	Message   model.Message
	Meta      map[string]any
}

// Store provides session and event persistence.
type Store interface {
	GetOrCreate(context.Context, *Session) (*Session, error)
	AppendEvent(context.Context, *Session, *Event) error
	ListEvents(context.Context, *Session) ([]*Event, error)
	SnapshotState(context.Context, *Session) (map[string]any, error)
	ReplaceState(context.Context, *Session, map[string]any) error
}

// CursorStore optionally exposes cursor-based event replay for efficient
// durable stream recovery.
type CursorStore interface {
	ListEventsAfter(context.Context, *Session, string, int) ([]*Event, string, error)
}

// StateUpdateStore optionally exposes an atomic read-modify-write state update.
// Implementations should ensure the updater observes a consistent snapshot and
// that unrelated keys are preserved across concurrent updates.
type StateUpdateStore interface {
	UpdateState(context.Context, *Session, func(map[string]any) (map[string]any, error)) error
}

// ExistenceStore optionally exposes session existence checks without creating
// missing sessions as a side effect.
type ExistenceStore interface {
	SessionExists(context.Context, *Session) (bool, error)
}

// ContextWindowStore optionally provides a reduced event window optimized for
// model context construction (typically latest compaction checkpoint and newer
// events).
type ContextWindowStore interface {
	ListContextWindowEvents(context.Context, *Session) ([]*Event, error)
}

// Iterator returns a sequence over events.
func Iterator(events []*Event) iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for _, ev := range events {
			if !yield(ev) {
				return
			}
		}
	}
}
