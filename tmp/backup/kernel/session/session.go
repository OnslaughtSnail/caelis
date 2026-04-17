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

// Event is the unit of runtime history. Only canonical history events belong in
// durable session storage and future agent context; transient events such as
// partials, overlays, notices, and UI-only signals must remain outside durable
// history.
type Event struct {
	ID        string
	SessionID string
	Time      time.Time
	Message   model.Message
	Meta      map[string]any
}

// LogStore provides durable canonical session history persistence.
type LogStore interface {
	GetOrCreate(context.Context, *Session) (*Session, error)
	AppendEvent(context.Context, *Session, *Event) error
	ListEvents(context.Context, *Session) ([]*Event, error)
}

// StateStore provides durable session-scoped state persistence.
type StateStore interface {
	SnapshotState(context.Context, *Session) (map[string]any, error)
	ReplaceState(context.Context, *Session, map[string]any) error
}

// Store is the compatibility aggregate used by existing callers. New kernel
// code should depend on narrower ports such as LogStore and StateStore.
type Store interface {
	LogStore
	StateStore
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

// SessionStateStore is a typed repository for one logical session namespace.
// It can be layered over a generic StateStore or backed by a dedicated adapter.
type SessionStateStore[T any] interface {
	Load(context.Context, *Session) (T, error)
	Save(context.Context, *Session, T) error
}

// StateNamespaceCodec translates one typed state namespace to and from a
// generic session state snapshot.
type StateNamespaceCodec[T any] interface {
	LoadState(map[string]any) (T, error)
	StoreState(map[string]any, T) (map[string]any, error)
}

// MapSessionStateStore adapts a generic map-backed state store to a typed
// session namespace repository.
type MapSessionStateStore[T any] struct {
	store   StateStore
	updater StateUpdateStore
	codec   StateNamespaceCodec[T]
}

func NewMapSessionStateStore[T any](store StateStore, codec StateNamespaceCodec[T]) (*MapSessionStateStore[T], error) {
	if store == nil {
		return nil, errors.New("session: state store is nil")
	}
	if codec == nil {
		return nil, errors.New("session: state codec is nil")
	}
	typed := &MapSessionStateStore[T]{
		store: store,
		codec: codec,
	}
	if updater, ok := store.(StateUpdateStore); ok {
		typed.updater = updater
	}
	return typed, nil
}

func (s *MapSessionStateStore[T]) Load(ctx context.Context, sess *Session) (T, error) {
	var zero T
	if s == nil || s.store == nil || s.codec == nil {
		return zero, errors.New("session: typed state store is not configured")
	}
	values, err := s.store.SnapshotState(ctx, sess)
	if err != nil {
		return zero, err
	}
	return s.codec.LoadState(values)
}

func (s *MapSessionStateStore[T]) Save(ctx context.Context, sess *Session, value T) error {
	if s == nil || s.store == nil || s.codec == nil {
		return errors.New("session: typed state store is not configured")
	}
	if s.updater != nil {
		return s.updater.UpdateState(ctx, sess, func(existing map[string]any) (map[string]any, error) {
			return s.codec.StoreState(existing, value)
		})
	}
	existing, err := s.store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	next, err := s.codec.StoreState(existing, value)
	if err != nil {
		return err
	}
	return s.store.ReplaceState(ctx, sess, next)
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
