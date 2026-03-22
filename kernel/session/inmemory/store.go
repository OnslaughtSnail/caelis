package inmemory

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type key struct {
	app, user, id string
}

type entry struct {
	session *session.Session
	events  []*session.Event
	state   map[string]any
}

// Store is a thread-safe in-memory session store.
type Store struct {
	mu   sync.RWMutex
	data map[key]*entry
}

func New() *Store {
	return &Store{data: make(map[key]*entry)}
}

func makeKey(s *session.Session) (key, error) {
	if s == nil || s.AppName == "" || s.UserID == "" || s.ID == "" {
		return key{}, fmt.Errorf("session: app_name, user_id and session_id are required")
	}
	return key{app: s.AppName, user: s.UserID, id: s.ID}, nil
}

func (s *Store) GetOrCreate(ctx context.Context, req *session.Session) (*session.Session, error) {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.data[k]; ok {
		cp := *e.session
		return &cp, nil
	}
	cp := *req
	s.data[k] = &entry{session: &cp, state: map[string]any{}}
	out := cp
	return &out, nil
}

func (s *Store) SessionExists(ctx context.Context, req *session.Session) (bool, error) {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[k]
	return ok, nil
}

func (s *Store) AppendEvent(ctx context.Context, req *session.Session, ev *session.Event) error {
	_ = ctx
	if ev == nil {
		return fmt.Errorf("session: event is nil")
	}
	k, err := makeKey(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[k]
	if !ok {
		return session.ErrSessionNotFound
	}
	e.events = append(e.events, session.CloneEvent(ev))
	return nil
}

func (s *Store) ListEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[k]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	out := make([]*session.Event, 0, len(e.events))
	for _, ev := range e.events {
		out = append(out, session.CloneEvent(ev))
	}
	return out, nil
}

func (s *Store) ListEventsAfter(ctx context.Context, req *session.Session, afterCursor string, limit int) ([]*session.Event, string, error) {
	events, err := s.ListEvents(ctx, req)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if afterCursor != "" {
		start = len(events)
		for i, ev := range events {
			if ev != nil && ev.ID == afterCursor {
				start = i + 1
				break
			}
		}
	}
	if start > len(events) {
		start = len(events)
	}
	remaining := events[start:]
	if limit > 0 && len(remaining) > limit {
		remaining = remaining[:limit]
	}
	nextCursor := afterCursor
	if n := len(remaining); n > 0 && remaining[n-1] != nil {
		nextCursor = remaining[n-1].ID
	}
	return remaining, nextCursor, nil
}

func (s *Store) ListContextWindowEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[k]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	window := session.ContextWindowEvents(e.events)
	out := make([]*session.Event, 0, len(window))
	for _, ev := range window {
		out = append(out, session.CloneEvent(ev))
	}
	return out, nil
}

func (s *Store) SnapshotState(ctx context.Context, req *session.Session) (map[string]any, error) {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[k]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	out := map[string]any{}
	maps.Copy(out, e.state)
	return out, nil
}

func (s *Store) ReplaceState(ctx context.Context, req *session.Session, values map[string]any) error {
	_ = ctx
	k, err := makeKey(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[k]
	if !ok {
		return session.ErrSessionNotFound
	}
	next := map[string]any{}
	maps.Copy(next, values)
	e.state = next
	return nil
}

func (s *Store) UpdateState(ctx context.Context, req *session.Session, update func(map[string]any) (map[string]any, error)) error {
	_ = ctx
	if update == nil {
		return nil
	}
	k, err := makeKey(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[k]
	if !ok {
		return session.ErrSessionNotFound
	}
	current := map[string]any{}
	maps.Copy(current, e.state)
	next, err := update(current)
	if err != nil {
		return err
	}
	if next == nil {
		next = map[string]any{}
	}
	snapshot := map[string]any{}
	maps.Copy(snapshot, next)
	e.state = snapshot
	return nil
}
