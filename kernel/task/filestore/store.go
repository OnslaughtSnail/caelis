package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

// Store persists task entries as JSON files on local disk.
type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("task filestore: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Upsert(ctx context.Context, entry *task.Entry) error {
	_ = ctx
	if entry == nil || strings.TrimSpace(entry.TaskID) == "" {
		return task.ErrTaskNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.MarshalIndent(task.CloneEntry(entry), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.entryPath(entry.TaskID), raw, 0o644)
}

func (s *Store) Get(ctx context.Context, taskID string) (*task.Entry, error) {
	_ = ctx
	raw, err := os.ReadFile(s.entryPath(taskID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, task.ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	var entry task.Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	return task.CloneEntry(&entry), nil
}

func (s *Store) ListSession(ctx context.Context, ref task.SessionRef) ([]*task.Entry, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]*task.Entry, 0, len(entries))
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		entry, err := s.Get(ctx, strings.TrimSuffix(item.Name(), ".json"))
		if err != nil {
			if errors.Is(err, task.ErrTaskNotFound) {
				continue
			}
			return nil, err
		}
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
		out = append(out, entry)
	}
	return out, nil
}

func (s *Store) entryPath(taskID string) string {
	return filepath.Join(s.root, strings.TrimSpace(taskID)+".json")
}
