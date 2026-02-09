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
