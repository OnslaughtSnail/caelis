package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
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
	if err := idx.TouchEvent(workspaceA, "app", "u", "s-a", &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "hello"},
	}, now.Add(time.Second)); err != nil {
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

func TestSessionIndex_TouchEvent_CompactionDoesNotOverrideLastUserMessage(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	now := time.Now()
	if err := idx.UpsertSession(workspace, "app", "u", "s-compact", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "app", "u", "s-compact", &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "real user prompt"},
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "app", "u", "s-compact", &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "checkpoint text"},
		Meta: map[string]any{
			"kind": "compaction",
		},
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err := idx.ListWorkspaceSessions(workspace.Key, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 session, got %d", len(items))
	}
	if items[0].LastUserMessage != "real user prompt" {
		t.Fatalf("expected last_user_message to keep real prompt, got %q", items[0].LastUserMessage)
	}
}

func TestHandleResume_WithSessionID_NonTUIStaysSilent(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}, &session.Event{
		ID:      "ev-user",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleUser, Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}, &session.Event{
		ID:      "ev-assistant",
		Time:    time.Now().Add(time.Second),
		Message: model.Message{Role: model.RoleAssistant, Text: "world"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-me", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := &cliConsole{
		baseCtx:       context.Background(),
		rt:            rt,
		appName:       "app",
		userID:        "u",
		workspace:     workspace,
		sessionIndex:  idx,
		sessionID:     "default",
		out:           &out,
		ui:            newUI(&out, true, false),
		showReasoning: true,
	}
	if _, err := handleResume(c, []string{"resume-me"}); err != nil {
		t.Fatal(err)
	}
	if c.sessionID != "resume-me" {
		t.Fatalf("expected session switched to resume-me, got %q", c.sessionID)
	}
	text := out.String()
	if strings.TrimSpace(text) != "" {
		t.Fatalf("expected /resume to be silent, got %q", text)
	}
}

func TestHandleResume_WithSessionID_TUIReplaysRecentEvents(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}, &session.Event{
		ID:      "ev-user",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleUser, Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}, &session.Event{
		ID:      "ev-assistant",
		Time:    time.Now().Add(time.Second),
		Message: model.Message{Role: model.RoleAssistant, Text: "world"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-me", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:       context.Background(),
		rt:            rt,
		appName:       "app",
		userID:        "u",
		workspace:     workspace,
		sessionIndex:  idx,
		sessionID:     "default",
		out:           &out,
		ui:            newUI(&out, true, false),
		showReasoning: true,
		tuiSender:     sender,
	}
	if _, err := handleResume(c, []string{"resume-me"}); err != nil {
		t.Fatal(err)
	}
	if c.sessionID != "resume-me" {
		t.Fatalf("expected session switched to resume-me, got %q", c.sessionID)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected no direct stdout replay in TUI path, got %q", out.String())
	}
	if len(sender.msgs) == 0 {
		t.Fatal("expected TUI replay events")
	}
	if _, ok := sender.msgs[0].(tuievents.ClearHistoryMsg); !ok {
		t.Fatalf("expected first replay message to clear history, got %T", sender.msgs[0])
	}
	foundUser := false
	foundAssistant := false
	for _, raw := range sender.msgs {
		switch msg := raw.(type) {
		case tuievents.LogChunkMsg:
			if strings.Contains(msg.Chunk, "> hello") {
				foundUser = true
			}
			if strings.Contains(msg.Chunk, "session resumed:") || strings.Contains(msg.Chunk, "recent events:") {
				t.Fatalf("did not expect legacy resume headers, got %q", msg.Chunk)
			}
		case tuievents.AssistantStreamMsg:
			if msg.Final && msg.Text == "world" {
				foundAssistant = true
			}
		}
	}
	if !foundUser {
		t.Fatalf("expected replayed user message in TUI messages, got %#v", sender.msgs)
	}
	if !foundAssistant {
		t.Fatalf("expected replayed assistant message in TUI messages, got %#v", sender.msgs)
	}
}

func TestHandleResume_WithPatchResponse_DoesNotReplayDiffBlockMsg(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	diffFixtureDir := t.TempDir()
	diffFixturePath := filepath.Join(diffFixtureDir, "a.txt")
	if err := os.WriteFile(diffFixturePath, []byte("line1\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "resume-patch"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:   "ev-call",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					ID:   "call_patch_1",
					Name: "PATCH",
					Args: fmt.Sprintf(`{"path":%q,"old":"old","new":"new"}`, diffFixturePath),
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:   "ev-result",
		Time: time.Now().Add(time.Second),
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_patch_1",
				Name: "PATCH",
				Result: map[string]any{
					"path":      diffFixturePath,
					"created":   false,
					"replaced":  1,
					"old_count": 1,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-patch", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:       context.Background(),
		rt:            rt,
		appName:       "app",
		userID:        "u",
		workspace:     workspace,
		sessionIndex:  idx,
		sessionID:     "default",
		execRuntime:   previewTestRuntime{cwd: diffFixtureDir},
		out:           &out,
		ui:            newUI(&out, true, false),
		showReasoning: true,
		tuiSender:     sender,
	}
	if _, err := handleResume(c, []string{"resume-patch"}); err != nil {
		t.Fatal(err)
	}
	foundDiff := false
	foundSummary := false
	for _, raw := range sender.msgs {
		if _, ok := raw.(tuievents.DiffBlockMsg); ok {
			foundDiff = true
		}
		msg, ok := raw.(tuievents.LogChunkMsg)
		if ok && strings.Contains(msg.Chunk, "✓ PATCH edited a.txt") {
			foundSummary = true
		}
	}
	if foundDiff {
		t.Fatalf("did not expect DiffBlockMsg in replay events, got %#v", sender.msgs)
	}
	if !foundSummary {
		t.Fatalf("expected patch summary in replay events, got %#v", sender.msgs)
	}
}

func TestHandleResume_DefaultUsesMostRecentNonCurrent(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	now := time.Now()
	if err := idx.UpsertSession(workspace, "app", "u", "current", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "previous", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := &cliConsole{
		baseCtx:      context.Background(),
		workspace:    workspace,
		sessionIndex: idx,
		sessionID:    "current",
		out:          &out,
		ui:           newUI(&out, true, false),
	}
	if _, err := handleResume(c, nil); err != nil {
		t.Fatal(err)
	}
	if c.sessionID != "previous" {
		t.Fatalf("expected default /resume to use previous session, got %q", c.sessionID)
	}
}

func TestSessionIndex_ListWorkspaceSessionsPage_Pagination(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	base := time.Now()
	for i := 0; i < 25; i++ {
		sid := fmt.Sprintf("s-%02d", i)
		if err := idx.UpsertSession(workspace, "app", "u", sid, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	total, err := idx.CountWorkspaceSessions(workspace.Key)
	if err != nil {
		t.Fatal(err)
	}
	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
	}
	items, err := idx.ListWorkspaceSessionsPage(workspace.Key, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatalf("expected 10 items on page 2, got %d", len(items))
	}
	if items[0].SessionID != "s-14" || items[9].SessionID != "s-05" {
		t.Fatalf("unexpected page 2 range: first=%q last=%q", items[0].SessionID, items[9].SessionID)
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

func TestCompleteResumeCandidates_ShowsPromptAndAgeAndExcludesCurrent(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	now := time.Now()
	if err := idx.UpsertSession(workspace, "app", "u", "current", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "s-a", now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "s-b", now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "app", "u", "s-a", &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "请审查我未提交的更改"},
	}, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "app", "u", "s-b", &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "写一个负载均衡方案"},
	}, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	c := &cliConsole{
		workspace:    workspace,
		sessionIndex: idx,
		sessionID:    "current",
	}
	cands, err := c.completeResumeCandidates("审查", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	if cands[0].SessionID != "s-a" {
		t.Fatalf("expected session s-a, got %q", cands[0].SessionID)
	}
	if !strings.Contains(cands[0].Prompt, "审查") {
		t.Fatalf("expected prompt preview contains query, got %q", cands[0].Prompt)
	}
	if strings.TrimSpace(cands[0].Age) == "" || cands[0].Age == "-" {
		t.Fatalf("expected computed age, got %q", cands[0].Age)
	}
}
