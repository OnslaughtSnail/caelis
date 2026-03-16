package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskmem "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
)

type resumeAsyncRunnerStub struct {
	status toolexec.SessionStatus
}

func (s *resumeAsyncRunnerStub) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}
func (s *resumeAsyncRunnerStub) StartAsync(context.Context, toolexec.CommandRequest) (string, error) {
	return s.status.ID, nil
}
func (s *resumeAsyncRunnerStub) WriteInput(string, []byte) error { return nil }
func (s *resumeAsyncRunnerStub) ReadOutput(string, int64, int64) ([]byte, []byte, int64, int64, error) {
	return nil, nil, 0, 0, nil
}
func (s *resumeAsyncRunnerStub) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	return s.status, nil
}
func (s *resumeAsyncRunnerStub) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}
func (s *resumeAsyncRunnerStub) TerminateSession(string) error { return nil }
func (s *resumeAsyncRunnerStub) ListSessions() []toolexec.SessionInfo {
	return nil
}

type resumeExecRuntime struct {
	cwd  string
	host toolexec.AsyncCommandRunner
}

func (r resumeExecRuntime) PermissionMode() toolexec.PermissionMode {
	return toolexec.PermissionModeDefault
}
func (r resumeExecRuntime) SandboxType() string                   { return "test" }
func (r resumeExecRuntime) SandboxPolicy() toolexec.SandboxPolicy { return toolexec.SandboxPolicy{} }
func (r resumeExecRuntime) FallbackToHost() bool                  { return false }
func (r resumeExecRuntime) FallbackReason() string                { return "" }
func (r resumeExecRuntime) FileSystem() toolexec.FileSystem       { return previewTestFS{cwd: r.cwd} }
func (r resumeExecRuntime) HostRunner() toolexec.CommandRunner    { return r.host }
func (r resumeExecRuntime) SandboxRunner() toolexec.CommandRunner { return nil }
func (r resumeExecRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
}

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

func TestSessionIndex_SyncWorkspaceFromStoreDir_BackfillsLastUserMessage(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})

	storeDir := t.TempDir()
	sessionID := "s-hydrate"
	sessionDir := filepath.Join(storeDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{
			ID:   "e1",
			Time: time.Date(2026, 3, 12, 4, 10, 0, 0, time.UTC),
			Message: model.Message{
				Role: model.RoleUser,
				Text: "first request",
			},
		},
		{
			ID:   "e2",
			Time: time.Date(2026, 3, 12, 4, 11, 0, 0, time.UTC),
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "done",
			},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace := workspaceContext{CWD: "/tmp/workspace", Key: "ws-key"}
	if err := idx.SyncWorkspaceFromStoreDir(workspace, "app", "u", storeDir); err != nil {
		t.Fatal(err)
	}
	items, err := idx.ListWorkspaceSessions(workspace.Key, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 session, got %d", len(items))
	}
	if items[0].LastUserMessage != "first request" {
		t.Fatalf("unexpected last user message %q", items[0].LastUserMessage)
	}
	if items[0].LastEventAt.IsZero() {
		t.Fatal("expected last_event_at to be populated")
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

func TestSessionIndex_TouchEvent_StripsHiddenSessionModePrefix(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-hidden"}
	if err := idx.TouchEvent(workspace, "app", "u", "s-hidden", &session.Event{
		Message: model.Message{
			Role: model.RoleUser,
			Text: `<caelis-session-mode mode="plan" hidden="true">
This turn is running in PLAN mode.
</caelis-session-mode>

show the pending files`,
		},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := idx.MostRecentWorkspaceSession(workspace.Key, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected indexed session")
	}
	if rec.LastUserMessage != "show the pending files" {
		t.Fatalf("expected stripped user message, got %q", rec.LastUserMessage)
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

func TestHandleResume_PassesExecRuntimeForBashRecovery(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	store := inmemory.New()
	tasks := taskmem.New()
	rt, err := runtime.New(runtime.Config{Store: store, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "resume-bash"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-bash", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Upsert(context.Background(), &task.Entry{
		TaskID:         "t-bash",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "app", UserID: "u", SessionID: "resume-bash"},
		Title:          "sleep 30",
		State:          task.StateRunning,
		Running:        true,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Spec: map[string]any{
			"command":         "sleep 30",
			"workdir":         workspace.CWD,
			"route":           string(toolexec.ExecutionRouteHost),
			"exec_session_id": "proc-1",
		},
		Result: map[string]any{
			"session_id": "proc-1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	c := &cliConsole{
		baseCtx:      context.Background(),
		rt:           rt,
		appName:      "app",
		userID:       "u",
		workspace:    workspace,
		sessionIndex: idx,
		sessionID:    "default",
		execRuntime: resumeExecRuntime{
			cwd: workspace.CWD,
			host: &resumeAsyncRunnerStub{
				status: toolexec.SessionStatus{
					ID:        "proc-1",
					Command:   "sleep 30",
					Dir:       workspace.CWD,
					State:     toolexec.SessionStateRunning,
					StartTime: time.Now(),
				},
			},
		},
		out: &out,
		ui:  newUI(&out, true, false),
	}
	if _, err := handleResume(c, []string{"resume-bash"}); err != nil {
		t.Fatal(err)
	}
	got, err := tasks.Get(context.Background(), "t-bash")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != task.StateRunning || !got.Running {
		t.Fatalf("expected running bash task to be reattached, got state=%q running=%v result=%#v", got.State, got.Running, got.Result)
	}
}

func TestHandleResume_WithSessionPrefix_ResolvesUniqueMatch(t *testing.T) {
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
	fullSessionID := "s-1234567890ab"
	if _, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: fullSessionID}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", fullSessionID, time.Now().Add(time.Second)); err != nil {
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
	if _, err := handleResume(c, []string{"s-12345678"}); err != nil {
		t.Fatal(err)
	}
	if c.sessionID != fullSessionID {
		t.Fatalf("expected session switched to %q, got %q", fullSessionID, c.sessionID)
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
	foundClear := false
	foundTaskResult := false
	for _, raw := range sender.msgs {
		if _, ok := raw.(tuievents.ClearHistoryMsg); ok {
			foundClear = true
		}
		if _, ok := raw.(tuievents.TaskResultMsg); ok {
			foundTaskResult = true
		}
	}
	if !foundClear {
		t.Fatalf("expected replay stream to clear history, got %#v", sender.msgs)
	}
	if !foundTaskResult {
		t.Fatalf("expected replay stream to end with task result signal, got %#v", sender.msgs)
	}
	foundUser := false
	foundAssistant := false
	for _, raw := range sender.msgs {
		switch msg := raw.(type) {
		case tuievents.UserMessageMsg:
			if msg.Text == "hello" {
				foundUser = true
			}
		case tuievents.LogChunkMsg:
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

func TestHandleResume_TUIReplaysInterleavedUserAttachmentsInStoredOrder(t *testing.T) {
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
	if _, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-order"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "resume-order"}, &session.Event{
		ID:   "ev-user-order",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleUser,
			Text: "Hi豆包这两个是什么APP?",
			ContentParts: []model.ContentPart{
				{Type: model.ContentPartImage, FileName: "first.png", Data: "a"},
				{Type: model.ContentPartText, Text: "Hi豆包"},
				{Type: model.ContentPartImage, FileName: "second.png", Data: "b"},
				{Type: model.ContentPartText, Text: "这两个是什么APP?"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-order", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:       context.Background(),
		rt:            rt,
		appName:       "app",
		userID:        "u",
		workspace:     workspace,
		sessionIndex:  idx,
		sessionID:     "default",
		out:           &bytes.Buffer{},
		ui:            newUI(&bytes.Buffer{}, true, false),
		showReasoning: true,
		tuiSender:     sender,
	}
	if _, err := handleResume(c, []string{"resume-order"}); err != nil {
		t.Fatal(err)
	}

	want := "> [image: first.png] Hi豆包 [image: second.png] 这两个是什么APP?"
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.UserMessageMsg)
		if ok && "> "+msg.Text == want {
			return
		}
	}
	t.Fatalf("expected replayed user message %q, got %#v", want, sender.msgs)
}

func TestHandleResume_WithPatchResponse_ReplaysCompactSummaryWithoutDiffBlock(t *testing.T) {
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
					"path":          diffFixturePath,
					"created":       false,
					"replaced":      1,
					"old_count":     1,
					"added_lines":   1,
					"removed_lines": 1,
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
		if ok && strings.Contains(msg.Chunk, "✓ PATCH +1 -1") {
			foundSummary = true
		}
	}
	if foundDiff {
		t.Fatalf("did not expect DiffBlockMsg in replay events, got %#v", sender.msgs)
	}
	if !foundSummary {
		t.Fatalf("expected compact patch summary in replay events, got %#v", sender.msgs)
	}
}

func TestHandleResume_WithPatchInsert_ReplaysTrueDiffStats(t *testing.T) {
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
	if err := os.WriteFile(diffFixturePath, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "resume-patch-insert"}
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
					ID:   "call_patch_insert",
					Name: "PATCH",
					Args: fmt.Sprintf(`{"path":%q,"old":"a\nb","new":"a\nx\nb"}`, diffFixturePath),
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
				ID:   "call_patch_insert",
				Name: "PATCH",
				Result: map[string]any{
					"path":          diffFixturePath,
					"created":       false,
					"replaced":      1,
					"old_count":     1,
					"added_lines":   1,
					"removed_lines": 0,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-patch-insert", time.Now().Add(time.Second)); err != nil {
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
	if _, err := handleResume(c, []string{"resume-patch-insert"}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.LogChunkMsg)
		if ok && strings.Contains(msg.Chunk, "✓ PATCH +1 -0") {
			return
		}
	}
	t.Fatalf("expected compact patch summary +1 -0, got %#v", sender.msgs)
}

func TestHandleResume_WithLegacyWriteResult_UsesLineCountFallback(t *testing.T) {
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
	if err := os.WriteFile(diffFixturePath, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "resume-write-legacy"}
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
					ID:   "call_write_legacy",
					Name: "WRITE",
					Args: fmt.Sprintf(`{"path":%q,"content":"new-one\nnew-two\n"}`, diffFixturePath),
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
				ID:   "call_write_legacy",
				Name: "WRITE",
				Result: map[string]any{
					"path":       diffFixturePath,
					"created":    false,
					"line_count": 2,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "app", "u", "resume-write-legacy", time.Now().Add(time.Second)); err != nil {
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
	if _, err := handleResume(c, []string{"resume-write-legacy"}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.LogChunkMsg)
		if ok && strings.Contains(msg.Chunk, "✓ WRITE +2 -0") {
			return
		}
	}
	t.Fatalf("expected legacy WRITE summary +2 -0, got %#v", sender.msgs)
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

func TestSessionIndex_ResolveWorkspaceSessionID_AmbiguousPrefix(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{CWD: "/tmp/ws", Key: "ws-key"}
	now := time.Now()
	for _, sessionID := range []string{"s-1234567890ab", "s-1234567890cd"} {
		if err := idx.UpsertSession(workspace, "app", "u", sessionID, now); err != nil {
			t.Fatal(err)
		}
	}
	_, ok, err := idx.ResolveWorkspaceSessionID(workspace.Key, "s-12345678")
	if err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if ok {
		t.Fatal("did not expect ambiguous prefix to resolve")
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
