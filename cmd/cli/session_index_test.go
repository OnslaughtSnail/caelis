package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestSessionIndex_ListByWorkspace(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspaceA := workspaceContext{CWD: "/tmp/a", Key: "a-key"}
	workspaceB := workspaceContext{CWD: "/tmp/b", Key: "b-key"}
	now := time.Now()
	if err := idx.UpsertSession(workspaceA, "app", "u", "s-a", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspaceA, "app", "u", "s-a", model.Message{Role: model.RoleUser, Text: "hello"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspaceB, "app", "u", "s-b", now); err != nil {
		t.Fatal(err)
	}

	items, err := idx.ListWorkspaceSessions(workspaceA.Key, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 session in workspace A, got %d", len(items))
	}
	if items[0].SessionID != "s-a" {
		t.Fatalf("unexpected session id %q", items[0].SessionID)
	}
	if items[0].EventCount != 1 {
		t.Fatalf("expected event_count=1, got %d", items[0].EventCount)
	}
	if items[0].LastUserMessage != "hello" {
		t.Fatalf("unexpected last user message %q", items[0].LastUserMessage)
	}
	ok, err := idx.HasWorkspaceSession(workspaceA.Key, "s-b")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("did not expect session s-b in workspace A")
	}
}

func TestIndexedSessionStore_AppendEventUpdatesIndex(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/workspace", Key: "ws-key"}
	store := newIndexedSessionStore(inmemory.New(), idx, workspace)
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-1"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "e1",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleUser, Text: "first prompt"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "e2",
		Time:    time.Now().Add(time.Second),
		Message: model.Message{Role: model.RoleAssistant, Text: "ok"},
	}); err != nil {
		t.Fatal(err)
	}
	items, err := idx.ListWorkspaceSessions(workspace.Key, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 indexed session, got %d", len(items))
	}
	if items[0].SessionID != "s-1" {
		t.Fatalf("unexpected session id %q", items[0].SessionID)
	}
	if items[0].EventCount != 2 {
		t.Fatalf("expected event_count=2, got %d", items[0].EventCount)
	}
	if items[0].LastUserMessage != "first prompt" {
		t.Fatalf("unexpected last_user_message %q", items[0].LastUserMessage)
	}
}

func TestHandleSessionsResume(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-me", time.Now()); err != nil {
		t.Fatal(err)
	}
	c := &cliConsole{
		workspace:    workspace,
		sessionIndex: idx,
		sessionID:    "default",
	}
	if _, err := handleSessions(c, []string{"resume", "resume-me"}); err != nil {
		t.Fatal(err)
	}
	if c.sessionID != "resume-me" {
		t.Fatalf("expected session switched to resume-me, got %q", c.sessionID)
	}
}

func TestSessionIndex_SyncWorkspaceFromStoreDir(t *testing.T) {
	root := t.TempDir()
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	sessionDir := filepath.Join(root, "s-123")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte("{\"ID\":\"e1\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	if err := idx.SyncWorkspaceFromStoreDir(workspace, "app", "u", root); err != nil {
		t.Fatal(err)
	}
	ok, err := idx.HasWorkspaceSession(workspace.Key, "s-123")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected synced session s-123")
	}
}
