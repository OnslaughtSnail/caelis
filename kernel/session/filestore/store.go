package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// Store persists session events to jsonl files on local disk.
type Store struct {
	root   string
	layout Layout
	mu     sync.Mutex
}

// Layout controls how session files are organized under root.
type Layout string

const (
	// LayoutNamespaced stores events by app/user/session.
	LayoutNamespaced Layout = "namespaced"
	// LayoutSessionOnly stores events by session id only.
	LayoutSessionOnly Layout = "session_only"
)

// Options configures filestore behavior.
type Options struct {
	Layout Layout
}

func New(root string) (*Store, error) {
	return NewWithOptions(root, Options{})
}

func NewWithOptions(root string, options Options) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("filestore: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	layout := options.Layout
	if layout == "" {
		layout = LayoutNamespaced
	}
	if layout != LayoutNamespaced && layout != LayoutSessionOnly {
		return nil, fmt.Errorf("filestore: unsupported layout %q", layout)
	}
	return &Store{root: root, layout: layout}, nil
}

func (s *Store) GetOrCreate(ctx context.Context, req *session.Session) (*session.Session, error) {
	_ = ctx
	dir, err := s.sessionDir(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	metaPath := filepath.Join(dir, "meta.json")
	if _, err := os.Stat(metaPath); errors.Is(err, os.ErrNotExist) {
		raw, _ := json.MarshalIndent(req, "", "  ")
		if writeErr := os.WriteFile(metaPath, raw, 0o644); writeErr != nil {
			return nil, writeErr
		}
	}
	cp := *req
	return &cp, nil
}

func (s *Store) SessionExists(ctx context.Context, req *session.Session) (bool, error) {
	_ = ctx
	dir, err := s.sessionDir(req)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(dir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) AppendEvent(ctx context.Context, req *session.Session, ev *session.Event) error {
	_ = ctx
	if ev == nil {
		return fmt.Errorf("filestore: event is nil")
	}
	dir, err := s.sessionDir(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *Store) ListEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	_ = ctx
	dir, err := s.sessionDir(req)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := []*session.Event{}
	dec := json.NewDecoder(f)
	for {
		ev := &session.Event{}
		if err := dec.Decode(ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("filestore: decode events: %w", err)
		}
		out = append(out, ev)
	}
	return out, nil
}

func (s *Store) ListContextWindowEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	_ = ctx
	dir, err := s.sessionDir(req)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	events := []*session.Event{}
	dec := json.NewDecoder(f)
	for {
		ev := &session.Event{}
		if err := dec.Decode(ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("filestore: decode events: %w", err)
		}
		events = append(events, ev)
	}
	return session.ContextWindowEvents(events), nil
}

func (s *Store) SnapshotState(ctx context.Context, req *session.Session) (map[string]any, error) {
	_ = ctx
	dir, err := s.sessionDir(req)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ReplaceState(ctx context.Context, req *session.Session, values map[string]any) error {
	return s.UpdateState(ctx, req, func(map[string]any) (map[string]any, error) {
		next := map[string]any{}
		for key, value := range values {
			next[key] = value
		}
		return next, nil
	})
}

func (s *Store) UpdateState(ctx context.Context, req *session.Session, update func(map[string]any) (map[string]any, error)) error {
	if update == nil {
		return nil
	}
	dir, err := s.sessionDir(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	statePath := filepath.Join(dir, "state.json")
	lockPath := filepath.Join(dir, ".state.lock")
	return withStateFileLock(ctx, lockPath, func() error {
		current := map[string]any{}
		raw, err := os.ReadFile(statePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
		}
		next, err := update(current)
		if err != nil {
			return err
		}
		if next == nil {
			next = map[string]any{}
		}
		encoded, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
		return writeFileAtomically(statePath, encoded, 0o644)
	})
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func withStateFileLock(ctx context.Context, lockPath string, fn func() error) error {
	const (
		lockWait  = 10 * time.Millisecond
		staleAge  = 30 * time.Second
		lockPerm  = 0o700
	)
	for {
		err := os.Mkdir(lockPath, lockPerm)
		if err == nil {
			defer os.Remove(lockPath)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(info.ModTime()) > staleAge {
			_ = os.RemoveAll(lockPath)
			continue
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(lockWait):
			}
			continue
		}
		time.Sleep(lockWait)
	}
}

func (s *Store) sessionDir(req *session.Session) (string, error) {
	if err := validateSession(req); err != nil {
		return "", err
	}
	if s.layout == LayoutSessionOnly {
		return filepath.Join(s.root, req.ID), nil
	}
	return filepath.Join(s.root, req.AppName, req.UserID, req.ID), nil
}

func validateSession(req *session.Session) error {
	if req == nil {
		return fmt.Errorf("filestore: invalid session")
	}
	if err := validateSessionPathComponent("app_name", req.AppName); err != nil {
		return err
	}
	if err := validateSessionPathComponent("user_id", req.UserID); err != nil {
		return err
	}
	if err := validateSessionPathComponent("session_id", req.ID); err != nil {
		return err
	}
	return nil
}

func validateSessionPathComponent(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("filestore: invalid %s", name)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("filestore: invalid %s", name)
	}
	if strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("filestore: invalid %s", name)
	}
	if filepath.Clean(value) != value {
		return fmt.Errorf("filestore: invalid %s", name)
	}
	return nil
}
