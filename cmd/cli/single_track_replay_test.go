package main

import (
	"context"
	"strings"
	"testing"

	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionsvc"
)

// TestResumeReplayFromSessionEventsOnly verifies that resume replays main ACP
// events from the session event log, without requiring any projection log entries.
func TestResumeReplayFromSessionEventsOnly(t *testing.T) {
	t.Parallel()
	store := inmemory.New()
	target := &session.Session{AppName: "app", UserID: "u", ID: "single-track-replay"}
	if _, err := store.GetOrCreate(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	// Build a session with mixed self and ACP events — no projection log at all.
	events := []struct {
		id   string
		role model.Role
		text string
		meta map[string]any
	}{
		{"ev-1", model.RoleUser, "你好", nil},
		{"ev-2", model.RoleAssistant, "你好！有什么可以帮你的？", nil}, // self
		{"ev-3", model.RoleUser, "帮我写一个函数", nil},
		{"ev-4", model.RoleAssistant, "好的，我来写", map[string]any{"_ui_agent": "copilot"}}, // ACP
		{"ev-5", model.RoleUser, "加上测试", nil},
		{"ev-6", model.RoleAssistant, "测试也写好了", map[string]any{"_ui_agent": "copilot"}}, // ACP
	}
	for _, e := range events {
		if err := store.AppendEvent(context.Background(), target, &session.Event{
			ID:        e.id,
			SessionID: target.ID,
			Message:   model.NewTextMessage(e.role, e.text),
			Meta:      e.meta,
		}); err != nil {
			t.Fatal(err)
		}
	}

	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index:   resumeIndexStub{resolveID: target.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "current",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
		tuiSender:    sender,
	}

	if _, err := handleResume(console, []string{target.ID}); err != nil {
		t.Fatal(err)
	}

	snap := sender.Snapshot()
	var acpProjectionCount int
	var acpTurnStarts int
	var selfMessages int
	for _, raw := range snap {
		switch msg := raw.(type) {
		case tuievents.ACPMainTurnStartMsg:
			if msg.SessionID == target.ID {
				acpTurnStarts++
			}
		case tuievents.ACPProjectionMsg:
			if msg.Scope == tuievents.ACPProjectionMain {
				acpProjectionCount++
			}
		case tuievents.RawDeltaMsg:
			selfMessages++
		}
	}
	if acpTurnStarts == 0 {
		t.Fatal("expected ACP turn start messages during replay")
	}
	if acpProjectionCount < 2 {
		t.Fatalf("expected at least 2 ACP projection messages (for ev-4 and ev-6), got %d", acpProjectionCount)
	}
}

// TestReplayMainACPSessionEvent_NarrativeText verifies that assistant text is
// replayed as ACPProjectionMsg with the correct scope and content.
func TestReplayMainACPSessionEvent_NarrativeText(t *testing.T) {
	t.Parallel()
	sender := &testSender{}
	ev := &session.Event{
		ID:      "ev-1",
		Message: model.NewTextMessage(model.RoleAssistant, "这是 ACP 的输出"),
		Meta:    map[string]any{"_ui_agent": "copilot"},
	}
	replayMainACPSessionEvent(sender, ev, "test-session")

	snap := sender.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 message, got %d: %v", len(snap), snap)
	}
	msg, ok := snap[0].(tuievents.ACPProjectionMsg)
	if !ok {
		t.Fatalf("expected ACPProjectionMsg, got %T", snap[0])
	}
	if msg.Scope != tuievents.ACPProjectionMain {
		t.Fatalf("expected main scope, got %q", msg.Scope)
	}
	if msg.ScopeID != "test-session" {
		t.Fatalf("expected scope ID test-session, got %q", msg.ScopeID)
	}
	if msg.DeltaText != "这是 ACP 的输出" {
		t.Fatalf("expected narrative text, got %q", msg.DeltaText)
	}
	if msg.Stream != "assistant" {
		t.Fatalf("expected assistant stream, got %q", msg.Stream)
	}
}

// TestReplayMainACPSessionEvent_ToolCallAndResponse verifies that tool call
// events produce proper ACPProjectionMsg with tool metadata.
func TestReplayMainACPSessionEvent_ToolCallAndResponse(t *testing.T) {
	t.Parallel()

	// Tool call event.
	sender := &testSender{}
	pending := map[string]toolCallSnapshot{}
	callEv := &session.Event{
		ID:      "ev-tc",
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{ID: "tc-1", Name: "WRITE", Args: `{"path":"main.go"}`}}, ""),
		Meta:    map[string]any{"_ui_agent": "copilot"},
	}
	replayMainACPSessionEventWithState(sender, callEv, "test-session", pending)
	snap := sender.Snapshot()
	foundToolCall := false
	for _, raw := range snap {
		if msg, ok := raw.(tuievents.ACPProjectionMsg); ok && msg.ToolCallID == "tc-1" && msg.ToolName == "WRITE" && asString(msg.ToolArgs["path"]) == "main.go" {
			foundToolCall = true
		}
	}
	if !foundToolCall {
		t.Fatalf("expected tool call projection, got %v", snap)
	}

	// Tool response event.
	sender2 := &testSender{}
	respEv := &session.Event{
		ID:      "ev-tr",
		Message: model.MessageFromToolResponse(&model.ToolResponse{ID: "tc-1", Name: "WRITE", Result: map[string]any{"ok": true}}),
		Meta:    map[string]any{"_ui_agent": "copilot"},
	}
	replayMainACPSessionEventWithState(sender2, respEv, "test-session", pending)
	snap2 := sender2.Snapshot()
	foundToolResp := false
	for _, raw := range snap2 {
		if msg, ok := raw.(tuievents.ACPProjectionMsg); ok &&
			msg.ToolCallID == "tc-1" &&
			msg.ToolStatus == "completed" &&
			asString(msg.ToolArgs["path"]) == "main.go" &&
			asString(msg.ToolResult["ok"]) == "true" {
			foundToolResp = true
		}
	}
	if !foundToolResp {
		t.Fatalf("expected tool response projection, got %v", snap2)
	}
}

// TestReplayMainACPSessionEvent_ErrorToolResponse verifies that error tool
// results produce ToolStatus="failed".
func TestReplayMainACPSessionEvent_ErrorToolResponse(t *testing.T) {
	t.Parallel()
	sender := &testSender{}
	ev := &session.Event{
		ID:      "ev-err",
		Message: model.MessageFromToolResponse(&model.ToolResponse{ID: "tc-1", Name: "WRITE", Result: map[string]any{"stderr": "permission denied"}}),
		Meta:    map[string]any{"_ui_agent": "copilot", "acp_tool_status": "failed"},
	}
	replayMainACPSessionEvent(sender, ev, "s")
	snap := sender.Snapshot()
	for _, raw := range snap {
		if msg, ok := raw.(tuievents.ACPProjectionMsg); ok && msg.ToolStatus == "failed" {
			return // pass
		}
	}
	t.Fatalf("expected failed tool status, got %v", snap)
}

// TestReplayMainACPSessionEvent_NilSender is a safety test.
func TestReplayMainACPSessionEvent_NilSender(t *testing.T) {
	t.Parallel()
	// Should not panic.
	replayMainACPSessionEvent(nil, &session.Event{
		Message: model.NewTextMessage(model.RoleAssistant, "text"),
	}, "s")
}

// TestSingleTrackReplay_ACPProjectionLogNotRequired verifies that the
// resume flow works even when the ACP projection log is completely empty.
// This is the core regression test for single-track persistence.
func TestSingleTrackReplay_ACPProjectionLogNotRequired(t *testing.T) {
	store := inmemory.New()
	target := &session.Session{AppName: "app", UserID: "u", ID: "no-projection-log"}
	if _, err := store.GetOrCreate(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	// Write only to session JSONL, no projection log at all.
	for _, ev := range []*session.Event{
		{ID: "ev-1", SessionID: target.ID, Message: model.NewTextMessage(model.RoleUser, "hello")},
		{ID: "ev-2", SessionID: target.ID, Message: model.NewTextMessage(model.RoleAssistant, "ACP output"), Meta: map[string]any{"_ui_agent": "copilot"}},
	} {
		if err := store.AppendEvent(context.Background(), target, ev); err != nil {
			t.Fatal(err)
		}
	}

	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store: store, AppName: "app", UserID: "u",
		Index: resumeIndexStub{resolveID: target.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "current",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
		tuiSender:    sender,
	}
	if _, err := handleResume(console, []string{target.ID}); err != nil {
		t.Fatal(err)
	}

	snap := sender.Snapshot()
	var foundACPOutput bool
	for _, raw := range snap {
		if msg, ok := raw.(tuievents.ACPProjectionMsg); ok {
			if strings.Contains(msg.DeltaText+msg.FullText, "ACP output") {
				foundACPOutput = true
			}
		}
	}
	if !foundACPOutput {
		t.Fatalf("expected ACP output from single-track replay without projection log, got %v", snap)
	}
}
