package main

import (
	"context"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type indexedSessionStore struct {
	inner     session.Store
	index     *sessionIndex
	workspace workspaceContext
}

func newIndexedSessionStore(inner session.Store, index *sessionIndex, workspace workspaceContext) session.Store {
	if inner == nil || index == nil {
		return inner
	}
	return &indexedSessionStore{
		inner:     inner,
		index:     index,
		workspace: workspace,
	}
}

func (s *indexedSessionStore) GetOrCreate(ctx context.Context, req *session.Session) (*session.Session, error) {
	sess, err := s.inner.GetOrCreate(ctx, req)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		_ = s.index.UpsertSession(s.workspace, sess.AppName, sess.UserID, sess.ID, sessNow())
	}
	return sess, nil
}

func (s *indexedSessionStore) AppendEvent(ctx context.Context, req *session.Session, ev *session.Event) error {
	if err := s.inner.AppendEvent(ctx, req, ev); err != nil {
		return err
	}
	if req != nil && ev != nil {
		_ = s.index.TouchEvent(s.workspace, req.AppName, req.UserID, req.ID, ev.Message, ev.Time)
	}
	return nil
}

func (s *indexedSessionStore) ListEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	return s.inner.ListEvents(ctx, req)
}

func (s *indexedSessionStore) SnapshotState(ctx context.Context, req *session.Session) (map[string]any, error) {
	return s.inner.SnapshotState(ctx, req)
}

func (s *indexedSessionStore) ListContextWindowEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	if withWindow, ok := s.inner.(session.ContextWindowStore); ok {
		return withWindow.ListContextWindowEvents(ctx, req)
	}
	events, err := s.inner.ListEvents(ctx, req)
	if err != nil {
		return nil, err
	}
	return events, nil
}

func sessNow() time.Time {
	return time.Now()
}
