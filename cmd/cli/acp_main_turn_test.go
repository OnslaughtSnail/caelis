package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionsvc"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func TestMainACPProjectionTrackerPreservesACPStreamWithoutTrimming(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")
	projections := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "previous answer and current turn",
			}),
		},
	})
	if len(projections) != 1 {
		t.Fatalf("expected one projection, got %d", len(projections))
	}
	if got := projections[0].FullText; got != "previous answer and current turn" {
		t.Fatalf("expected live ACP text to pass through unchanged, got %q", got)
	}
	events := tracker.Events()
	if len(events) != 1 {
		t.Fatalf("expected one canonical event, got %d", len(events))
	}
	if got := events[0].Message.TextContent(); got != "previous answer and current turn" {
		t.Fatalf("expected canonical assistant text to match live ACP text, got %q", got)
	}
}

func TestMainACPProjectionTrackerPreservesNarrativeFormatting(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")
	projections := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "模型信息：\n由 GPT-5 mini 提供支持。",
			}),
		},
	})
	if len(projections) != 1 {
		t.Fatalf("expected one projection, got %d", len(projections))
	}
	if got := projections[0].DeltaText; got != "模型信息：\n由 GPT-5 mini 提供支持。" {
		t.Fatalf("expected formatting-preserving delta text, got %q", got)
	}
}

func TestRunPromptWithAttachmentsRejectsConcurrentACPMainTurn(t *testing.T) {
	t.Parallel()

	console := &cliConsole{
		activeRunCancel: func() {},
		activeRunKind:   runOccupancyMainSession,
	}
	err := console.runPromptWithAttachmentsContext(t.Context(), "hello", nil)
	if err == nil {
		t.Fatal("expected active main-session busy error")
	}
	if !strings.Contains(err.Error(), "main session run is active") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilterMainACPImageBlocksDropsImageOnlyWhenUnsupported(t *testing.T) {
	t.Parallel()

	text := mustMarshalMainACPContent(acpclient.TextContent{Type: "text", Text: "hello"})
	image, err := json.Marshal(acpclient.ImageContent{Type: "image", MimeType: "image/png", Data: "abcd"})
	if err != nil {
		t.Fatalf("marshal image: %v", err)
	}
	filtered := filterMainACPImageBlocks([]json.RawMessage{text, image})
	if len(filtered) != 1 {
		t.Fatalf("expected text block to remain after filtering, got %d blocks", len(filtered))
	}
}

func TestMainACPProjectionTrackerAccumulatesNarrativeChunks(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")
	first := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "我",
			}),
		},
	})
	second := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "是 caelis",
			}),
		},
	})
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected one projection per chunk, got %d and %d", len(first), len(second))
	}
	if got := first[0].DeltaText; got != "我" {
		t.Fatalf("expected first delta to be preserved, got %q", got)
	}
	if got := second[0].DeltaText; got != "是 caelis" {
		t.Fatalf("expected second delta to be preserved, got %q", got)
	}
	if got := strings.TrimSpace(second[0].FullText); got != "我是 caelis" {
		t.Fatalf("expected cumulative full text, got %q", got)
	}
	events := tracker.Events()
	if len(events) != 1 {
		t.Fatalf("expected recorder to merge adjacent assistant chunks, got %d events", len(events))
	}
	if got := strings.TrimSpace(events[0].Message.TextContent()); got != "我是 caelis" {
		t.Fatalf("expected canonical assistant text to accumulate chunks, got %q", got)
	}
}

func TestMainACPProjectionTrackerToolCallAndResult(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")

	// Tool call
	callProj := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ToolCall{
			ToolCallID: "call-1",
			Title:      "READ main.go",
			Kind:       "read",
			RawInput:   map[string]any{"path": "main.go"},
		},
	})
	if len(callProj) != 1 || callProj[0].ToolCallID != "call-1" || callProj[0].ToolName != "READ" {
		t.Fatalf("expected tool call projection, got %#v", callProj)
	}

	// Tool result
	status := "completed"
	resultProj := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ToolCallUpdate{
			ToolCallID: "call-1",
			Status:     &status,
			RawOutput:  map[string]any{"content": "package main"},
		},
	})
	if len(resultProj) != 1 || resultProj[0].ToolStatus != "completed" {
		t.Fatalf("expected tool result projection, got %#v", resultProj)
	}

	events := tracker.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 canonical events (call + result), got %d", len(events))
	}
	if !session.IsMirror(events[0]) || !session.IsMirror(events[1]) {
		t.Fatalf("expected ACP tool lifecycle to persist as mirror events, got %+v", events)
	}
	if calls := events[0].Message.ToolCalls(); len(calls) != 1 || calls[0].ID != "call-1" {
		t.Fatalf("expected tool call event, got %#v", events[0])
	}
	if resp := events[1].Message.ToolResponse(); resp == nil || resp.ID != "call-1" {
		t.Fatalf("expected tool response event, got %#v", events[1])
	}
}

func TestMainACPProjectionTrackerToolLifecycleExcludedFromInvocationMessages(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")
	tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "先展示工具再给总结",
			}),
		},
	})
	tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ToolCall{
			ToolCallID: "call-1",
			Title:      "READ main.go",
			Kind:       "read",
			RawInput:   map[string]any{"path": "main.go"},
		},
	})
	status := "completed"
	tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ToolCallUpdate{
			ToolCallID: "call-1",
			Status:     &status,
			RawOutput:  map[string]any{"content": "package main"},
		},
	})

	msgs := session.Messages(session.NewEvents(tracker.Events()), "", nil)
	if len(msgs) != 1 {
		t.Fatalf("expected only narrative message to remain invocation-visible, got %d", len(msgs))
	}
	if msgs[0].Role != model.RoleAssistant || msgs[0].TextContent() != "先展示工具再给总结" {
		t.Fatalf("unexpected invocation-visible messages: %+v", msgs)
	}
}

func TestMainACPProjectionTrackerSeedNarrativeSuppressesPreviousTurnReplay(t *testing.T) {
	t.Parallel()

	tracker := newMainACPProjectionTracker("copilot", "epoch-test")
	tracker.SeedNarrative("上一轮完整输出", "")

	projections := tracker.Project(acpclient.UpdateEnvelope{
		SessionID: "remote-session",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content: mustMarshalMainACPContent(acpclient.TextContent{
				Type: "text",
				Text: "上一轮完整输出\n这一轮新增输出",
			}),
		},
	})
	if len(projections) != 1 {
		t.Fatalf("expected one projection, got %d", len(projections))
	}
	if got := projections[0].DeltaText; got != "\n这一轮新增输出" {
		t.Fatalf("expected only new suffix after seeded baseline, got %q", got)
	}
	if got := projections[0].FullText; got != "上一轮完整输出\n这一轮新增输出" {
		t.Fatalf("expected cumulative full text to remain intact, got %q", got)
	}
	events := tracker.Events()
	if len(events) != 1 {
		t.Fatalf("expected one canonical delta event, got %d", len(events))
	}
	if got := events[0].Message.TextContent(); got != "\n这一轮新增输出" {
		t.Fatalf("expected canonical event to keep only new suffix, got %q", got)
	}
}

type stubMainACPClient struct {
	promptSessionID  string
	promptParts      []json.RawMessage
	newSessionCalls  int
	allowLoadSession bool
	loadSessionErr   error
	onPrompt         func(sessionID string, parts []json.RawMessage)
}

func (c *stubMainACPClient) Initialize(context.Context) (acpclient.InitializeResponse, error) {
	return acpclient.InitializeResponse{}, nil
}

func (c *stubMainACPClient) NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error) {
	c.newSessionCalls++
	return acpclient.NewSessionResponse{SessionID: "remote-main-1"}, nil
}

func (c *stubMainACPClient) LoadSession(context.Context, string, string, map[string]any) (acpclient.LoadSessionResponse, error) {
	if !c.allowLoadSession {
		return acpclient.LoadSessionResponse{}, errors.New("unexpected load session")
	}
	if c.loadSessionErr != nil {
		return acpclient.LoadSessionResponse{}, c.loadSessionErr
	}
	return acpclient.LoadSessionResponse{}, nil
}

func (c *stubMainACPClient) PromptParts(_ context.Context, sessionID string, parts []json.RawMessage, _ map[string]any) (acpclient.PromptResponse, error) {
	c.promptSessionID = strings.TrimSpace(sessionID)
	c.promptParts = append([]json.RawMessage(nil), parts...)
	if c.onPrompt != nil {
		c.onPrompt(c.promptSessionID, c.promptParts)
	}
	return acpclient.PromptResponse{}, nil
}

func (c *stubMainACPClient) Cancel(context.Context, string) error { return nil }
func (c *stubMainACPClient) StderrTail(int) string                { return "" }
func (c *stubMainACPClient) Close() error                         { return nil }

func TestRunPreparedACPMainSubmissionContext_HandoffExcludesCurrentUserTurn(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := store.GetOrCreate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []*session.Event{
		{
			ID:      "ev-1",
			Message: model.NewTextMessage(model.RoleUser, "先介绍一下自己"),
		},
		{
			ID:      "ev-2",
			Message: model.NewTextMessage(model.RoleAssistant, "我是 self 主控"),
		},
	} {
		if err := store.AppendEvent(t.Context(), root, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := coreacpmeta.UpdateControllerEpoch(t.Context(), store, root, func(coreacpmeta.ControllerEpoch) coreacpmeta.ControllerEpoch {
		return coreacpmeta.ControllerEpoch{
			EpochID:        "1",
			ControllerKind: coreacpmeta.ControllerKindSelf,
			ControllerID:   "self",
		}
	}); err != nil {
		t.Fatal(err)
	}

	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}

	client := &stubMainACPClient{}
	client.allowLoadSession = true
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
		gateway:      gw,
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: defaultAppConfig(),
		},
		workspace: workspaceContext{
			Key: "wk",
			CWD: "/workspace",
		},
		workspaceRoot: "/workspace",
	}

	currentUser := "演示一下工具调用能力"
	err = console.runPreparedACPMainSubmissionContext(t.Context(), preparedPromptSubmission{
		mainACP: &preparedMainACPSubmission{
			descriptor: appagents.Descriptor{ID: "copilot", Command: "copilot"},
		},
	}, runtime.Submission{
		Text: currentUser,
		Mode: runtime.SubmissionConversation,
	})
	if err != nil {
		t.Fatalf("run main ACP turn: %v", err)
	}

	promptText := flattenMainACPPromptText(t, client.promptParts)
	if got := strings.Count(promptText, currentUser); got != 1 {
		t.Fatalf("expected current user request to appear exactly once in prompt, got %d in:\n%s", got, promptText)
	}
	if !strings.Contains(promptText, "[System-generated handoff checkpoint]") {
		t.Fatalf("expected handoff checkpoint in ACP prompt, got:\n%s", promptText)
	}
	if !strings.Contains(promptText, "先介绍一下自己") {
		t.Fatalf("expected prior history to appear in handoff context, got:\n%s", promptText)
	}
}

func TestRunPreparedACPMainSubmissionContext_SeedsReusedSessionNarrative(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-reconnect"}
	if _, err := store.GetOrCreate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []*session.Event{
		{
			ID:      "ev-user-1",
			Message: model.NewTextMessage(model.RoleUser, "继续这个任务"),
		},
		{
			ID:      "ev-assistant-1",
			Message: model.NewTextMessage(model.RoleAssistant, "Earlier answer."),
			Meta:    map[string]any{"_ui_agent": "copilot"},
		},
	} {
		if err := store.AppendEvent(t.Context(), root, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := coreacpmeta.UpdateControllerEpoch(t.Context(), store, root, func(coreacpmeta.ControllerEpoch) coreacpmeta.ControllerEpoch {
		return coreacpmeta.ControllerEpoch{
			EpochID:        "1",
			ControllerKind: coreacpmeta.ControllerKindACP,
			ControllerID:   "copilot",
		}
	}); err != nil {
		t.Fatal(err)
	}
	history, err := store.ListEvents(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if assistant, reasoning := previousMainACPTurnNarrative(history, "copilot"); assistant != "Earlier answer." || reasoning != "" {
		t.Fatalf("unexpected seed narrative: assistant=%q reasoning=%q", assistant, reasoning)
	}

	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}

	client := &stubMainACPClient{}
	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
		gateway:      gw,
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: defaultAppConfig(),
		},
		workspace: workspaceContext{
			Key: "wk",
			CWD: "/workspace",
		},
		workspaceRoot: "/workspace",
	}
	persistent := &persistentMainACPState{
		agentID:         "copilot",
		client:          client,
		remoteSessionID: "remote-existing",
	}
	client.onPrompt = func(sessionID string, _ []json.RawMessage) {
		persistent.dispatchUpdate(acpclient.UpdateEnvelope{
			SessionID: sessionID,
			Update: acpclient.ContentChunk{
				SessionUpdate: acpclient.UpdateAgentMessage,
				Content: mustMarshalMainACPContent(acpclient.TextContent{
					Type: "text",
					Text: "Earlier answer. New answer.",
				}),
			},
		})
	}
	console.persistentMainACP = persistent

	err = console.runPreparedACPMainSubmissionContext(t.Context(), preparedPromptSubmission{
		mainACP: &preparedMainACPSubmission{
			descriptor: appagents.Descriptor{ID: "copilot", Command: "copilot"},
		},
	}, runtime.Submission{
		Text: "继续推进",
		Mode: runtime.SubmissionConversation,
	})
	if err != nil {
		t.Fatalf("run main ACP reused turn: %v", err)
	}
	if client.promptSessionID != "remote-existing" {
		t.Fatalf("expected reused session to keep remote-existing, got %q", client.promptSessionID)
	}

	events, err := store.ListEvents(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	lastAssistant := ""
	for _, ev := range events {
		if ev == nil || ev.Message.Role != model.RoleAssistant || strings.TrimSpace(asString(ev.Meta["_ui_agent"])) != "copilot" {
			continue
		}
		lastAssistant = ev.Message.TextContent()
	}
	if strings.TrimSpace(lastAssistant) != "New answer." {
		t.Fatalf("expected reused-session replay to persist only the new suffix, got %q", lastAssistant)
	}
}

func flattenMainACPPromptText(t *testing.T, parts []json.RawMessage) string {
	t.Helper()
	var texts []string
	for _, raw := range parts {
		var header struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatalf("unmarshal ACP prompt block: %v", err)
		}
		if strings.EqualFold(strings.TrimSpace(header.Type), "text") {
			texts = append(texts, header.Text)
		}
	}
	return strings.Join(texts, "\n")
}
