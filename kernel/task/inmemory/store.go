package inmemory

import (
	"context"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

// Store is a thread-safe in-memory task store.
type Store struct {
	mu   sync.RWMutex
	data map[string]*task.Entry
}

func New() *Store {
	return &Store{data: map[string]*task.Entry{}}
}

func (s *Store) Upsert(ctx context.Context, entry *task.Entry) error {
	_ = ctx
	if entry == nil || strings.TrimSpace(entry.TaskID) == "" {
		return task.ErrTaskNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[strings.TrimSpace(entry.TaskID)] = task.CloneEntry(entry)
	return nil
}

func (s *Store) Get(ctx context.Context, taskID string) (*task.Entry, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.data[strings.TrimSpace(taskID)]
	if !ok {
		return nil, task.ErrTaskNotFound
	}
	return task.CloneEntry(entry), nil
}

func (s *Store) ListSession(ctx context.Context, ref task.SessionRef) ([]*task.Entry, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*task.Entry, 0, len(s.data))
	for _, entry := range s.data {
		if entry == nil {
			continue
		}
		if strings.TrimSpace(entry.Session.AppName) != strings.TrimSpace(ref.AppName) {
			continue
		}
		if strings.TrimSpace(entry.Session.UserID) != strings.TrimSpace(ref.UserID) {
			continue
		}
		if strings.TrimSpace(entry.Session.SessionID) != strings.TrimSpace(ref.SessionID) {
			continue
		}
		out = append(out, task.CloneEntry(entry))
	}
	return out, nil
}
