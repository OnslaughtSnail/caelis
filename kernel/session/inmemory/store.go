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
	copyEv := *ev
	e.events = append(e.events, &copyEv)
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
		cp := *ev
		out = append(out, &cp)
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
