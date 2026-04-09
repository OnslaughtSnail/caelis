package acpagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	compact "github.com/OnslaughtSnail/caelis/kernel/compaction"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessioninmemory "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func TestACPAgentRunStartsFreshRemoteSessionAndPersistsControllerSession(t *testing.T) {
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-main"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}

	var seenPrompt []json.RawMessage
	restore := stubACPClientStart(func(_ context.Context, cfg acpclient.Config) (sessionClient, error) {
		return &fakeSessionClient{
			initializeResp: acpclient.InitializeResponse{
				AgentCapabilities: acpclient.AgentCapabilities{
					Prompt: acpclient.PromptCapabilities{Image: true},
				},
			},
			newSessionResp: acpclient.NewSessionResponse{SessionID: "remote-1"},
			onPrompt: func(prompt []json.RawMessage) {
				seenPrompt = append([]json.RawMessage(nil), prompt...)
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-1",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "Let me check."}),
					},
				})
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-1",
					Update: acpclient.ToolCall{
						SessionUpdate: acpclient.UpdateToolCall,
						ToolCallID:    "call-1",
						Title:         "READ README.md",
						Kind:          "read",
						RawInput:      map[string]any{"path": "README.md"},
					},
				})
				statusCompleted := "completed"
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-1",
					Update: acpclient.ToolCallUpdate{
						SessionUpdate: acpclient.UpdateToolCallState,
						ToolCallID:    "call-1",
						Status:        &statusCompleted,
						RawOutput:     map[string]any{"summary": "done"},
					},
				})
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-1",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "Let me check. Found it."}),
					},
				})
			},
		}, nil
	})
	defer restore()

	ag, err := New(Config{
		ID:            "codex",
		Command:       "codex-acp",
		WorkspaceRoot: "/workspace",
		SessionCWD:    "/workspace",
		SystemPrompt:  "Be precise.",
	})
	if err != nil {
		t.Fatal(err)
	}

	inv := testInvocationContext{
		Context: session.WithStoresContext(ctx, sess, store, store),
		sess:    sess,
		events: session.NewEvents([]*session.Event{
			{Message: model.NewTextMessage(model.RoleUser, "inspect repo")},
		}),
		state: session.NewReadonlyState(nil),
	}

	var events []*session.Event
	for ev, runErr := range ag.Run(inv) {
		if runErr != nil {
			t.Fatalf("run error: %v", runErr)
		}
		events = append(events, ev)
	}

	if len(seenPrompt) < 2 {
		t.Fatalf("expected system prelude + user prompt, got %d blocks", len(seenPrompt))
	}
	first := decodePromptBlock(t, seenPrompt[0])
	if !strings.Contains(first["text"], "Persistent operating instructions") {
		t.Fatalf("expected system prelude in first prompt block, got %#v", first)
	}

	ref, err := acpmeta.ControllerSessionFromStore(ctx, store, sess)
	if err != nil {
		t.Fatal(err)
	}
	if ref.AgentID != "codex" || ref.SessionID != "remote-1" {
		t.Fatalf("unexpected persisted controller session %+v", ref)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 canonical events, got %d", len(events))
	}
	if got := strings.TrimSpace(events[0].Message.TextContent()); got != "Let me check." {
		t.Fatalf("unexpected first assistant text %q", got)
	}
	if calls := events[1].Message.ToolCalls(); len(calls) != 1 || calls[0].Name != "READ" {
		t.Fatalf("expected second event to carry READ tool call, got %+v", events[1].Message)
	}
	if resp := events[2].Message.ToolResponse(); resp == nil || resp.Name != "READ" {
		t.Fatalf("expected tool response event, got %+v", events[2].Message)
	}
	if got := strings.TrimSpace(events[3].Message.TextContent()); got != "Found it." {
		t.Fatalf("unexpected final assistant text %q", got)
	}
}

func TestACPAgentRunReloadsPersistedControllerSessionWithoutPrelude(t *testing.T) {
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-existing"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := acpmeta.UpdateControllerSession(ctx, store, sess, func(acpmeta.ControllerSession) acpmeta.ControllerSession {
		return acpmeta.ControllerSession{AgentID: "copilot", SessionID: "remote-existing"}
	}); err != nil {
		t.Fatal(err)
	}

	var (
		loadCalled bool
		newCalled  bool
		seenPrompt []json.RawMessage
	)
	restore := stubACPClientStart(func(_ context.Context, cfg acpclient.Config) (sessionClient, error) {
		return &fakeSessionClient{
			initializeResp: acpclient.InitializeResponse{
				AgentCapabilities: acpclient.AgentCapabilities{
					Prompt: acpclient.PromptCapabilities{Image: true},
				},
			},
			onLoadSession: func(sessionID string) {
				loadCalled = sessionID == "remote-existing"
			},
			onNewSession: func() {
				newCalled = true
			},
			onPrompt: func(prompt []json.RawMessage) {
				seenPrompt = append([]json.RawMessage(nil), prompt...)
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-existing",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "done"}),
					},
				})
			},
		}, nil
	})
	defer restore()

	ag, err := New(Config{
		ID:            "copilot",
		Command:       "copilot-acp",
		WorkspaceRoot: "/workspace",
		SessionCWD:    "/workspace",
		SystemPrompt:  "Do not resend me.",
	})
	if err != nil {
		t.Fatal(err)
	}

	inv := testInvocationContext{
		Context: session.WithStoresContext(ctx, sess, store, store),
		sess:    sess,
		events: session.NewEvents([]*session.Event{
			{Message: model.NewTextMessage(model.RoleUser, "continue")},
		}),
		state: session.NewReadonlyState(acpmeta.StoreControllerSession(nil, acpmeta.ControllerSession{
			AgentID:   "copilot",
			SessionID: "remote-existing",
		})),
	}

	var events []*session.Event
	for ev, runErr := range ag.Run(inv) {
		if runErr != nil {
			t.Fatalf("run error: %v", runErr)
		}
		events = append(events, ev)
	}

	if !loadCalled {
		t.Fatal("expected persisted remote session to be loaded")
	}
	if newCalled {
		t.Fatal("did not expect a new remote session")
	}
	if len(seenPrompt) != 1 {
		t.Fatalf("expected exactly one user prompt block, got %d", len(seenPrompt))
	}
	block := decodePromptBlock(t, seenPrompt[0])
	if text := strings.TrimSpace(block["text"]); text != "continue" {
		t.Fatalf("unexpected resumed prompt block %#v", block)
	}
	if len(events) != 1 || strings.TrimSpace(events[0].Message.TextContent()) != "done" {
		t.Fatalf("unexpected canonical events %+v", events)
	}
}

func TestACPAgentRunSeedsFreshRemoteSessionWithLocalContinuationContext(t *testing.T) {
	var seenPrompt []json.RawMessage
	restore := stubACPClientStart(func(_ context.Context, cfg acpclient.Config) (sessionClient, error) {
		return &fakeSessionClient{
			initializeResp: acpclient.InitializeResponse{
				AgentCapabilities: acpclient.AgentCapabilities{
					Prompt: acpclient.PromptCapabilities{Image: true},
				},
			},
			newSessionResp: acpclient.NewSessionResponse{SessionID: "remote-seeded"},
			onPrompt: func(prompt []json.RawMessage) {
				seenPrompt = append([]json.RawMessage(nil), prompt...)
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-seeded",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "continued"}),
					},
				})
			},
		}, nil
	})
	defer restore()

	ag, err := New(Config{
		ID:            "copilot",
		Command:       "copilot-acp",
		WorkspaceRoot: "/workspace",
		SessionCWD:    "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := compact.Checkpoint{
		Objective:       "Finish auth flow",
		UserConstraints: []string{"Keep tests green"},
	}
	inv := testInvocationContext{
		Context: t.Context(),
		events: session.NewEvents([]*session.Event{
			{
				ID:      "compact-1",
				Message: model.NewTextMessage(model.RoleAssistant, compact.RenderCheckpointMarkdown(checkpoint)),
				Meta: map[string]any{
					"kind": "compaction",
					"compaction": map[string]any{
						"checkpoint": compact.CheckpointMeta(checkpoint),
					},
				},
			},
			{ID: "a-1", Message: model.NewTextMessage(model.RoleAssistant, "Opened auth middleware and confirmed the failing branch.")},
			{ID: "u-1", Message: model.NewTextMessage(model.RoleUser, "continue and patch it")},
		}),
		state: session.NewReadonlyState(nil),
	}

	for _, runErr := range ag.Run(inv) {
		if runErr != nil {
			t.Fatalf("run error: %v", runErr)
		}
	}

	if len(seenPrompt) != 2 {
		t.Fatalf("expected continuation seed + current user prompt, got %d blocks", len(seenPrompt))
	}
	seed := decodePromptBlock(t, seenPrompt[0])
	if !strings.Contains(seed["text"], "Carry forward the following local Caelis session context") {
		t.Fatalf("expected continuity seed in first prompt block, got %#v", seed)
	}
	if !strings.Contains(seed["text"], "## Active Objective") || !strings.Contains(seed["text"], "Finish auth flow") {
		t.Fatalf("expected checkpoint content in seed block, got %#v", seed)
	}
	if !strings.Contains(seed["text"], "Opened auth middleware and confirmed the failing branch.") {
		t.Fatalf("expected recent local transcript in seed block, got %#v", seed)
	}
	current := decodePromptBlock(t, seenPrompt[1])
	if text := strings.TrimSpace(current["text"]); text != "continue and patch it" {
		t.Fatalf("unexpected current prompt block %#v", current)
	}
}

func TestACPAgentRunPreservesNarrativeOrderAroundToolLifecycle(t *testing.T) {
	restore := stubACPClientStart(func(_ context.Context, cfg acpclient.Config) (sessionClient, error) {
		return &fakeSessionClient{
			initializeResp: acpclient.InitializeResponse{
				AgentCapabilities: acpclient.AgentCapabilities{
					Prompt: acpclient.PromptCapabilities{Image: true},
				},
			},
			newSessionResp: acpclient.NewSessionResponse{SessionID: "remote-ordered"},
			onPrompt: func(_ []json.RawMessage) {
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-ordered",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "First."}),
					},
				})
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-ordered",
					Update: acpclient.ToolCall{
						SessionUpdate: acpclient.UpdateToolCall,
						ToolCallID:    "call-1",
						Title:         "READ README.md",
						Kind:          "read",
						RawInput:      map[string]any{"path": "README.md"},
					},
				})
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-ordered",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "First. Still checking."}),
					},
				})
				statusCompleted := "completed"
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-ordered",
					Update: acpclient.ToolCallUpdate{
						SessionUpdate: acpclient.UpdateToolCallState,
						ToolCallID:    "call-1",
						Status:        &statusCompleted,
						RawOutput:     map[string]any{"summary": "done"},
					},
				})
				cfg.OnUpdate(acpclient.UpdateEnvelope{
					SessionID: "remote-ordered",
					Update: acpclient.ContentChunk{
						SessionUpdate: acpclient.UpdateAgentMessage,
						Content:       mustMarshalRaw(acpclient.TextContent{Type: "text", Text: "First. Still checking. Done."}),
					},
				})
			},
		}, nil
	})
	defer restore()

	ag, err := New(Config{
		ID:            "copilot",
		Command:       "copilot-acp",
		WorkspaceRoot: "/workspace",
		SessionCWD:    "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}

	inv := testInvocationContext{
		Context: t.Context(),
		events: session.NewEvents([]*session.Event{
			{Message: model.NewTextMessage(model.RoleUser, "continue")},
		}),
		state: session.NewReadonlyState(nil),
	}

	var labels []string
	for ev, runErr := range ag.Run(inv) {
		if runErr != nil {
			t.Fatalf("run error: %v", runErr)
		}
		switch {
		case strings.TrimSpace(ev.Message.TextContent()) != "":
			labels = append(labels, strings.TrimSpace(ev.Message.TextContent()))
		case len(ev.Message.ToolCalls()) > 0:
			labels = append(labels, "tool:"+ev.Message.ToolCalls()[0].Name)
		case ev.Message.ToolResponse() != nil:
			labels = append(labels, "result:"+ev.Message.ToolResponse().Name)
		}
	}

	want := []string{"First.", "tool:READ", "Still checking.", "result:READ", "Done."}
	if strings.Join(labels, " | ") != strings.Join(want, " | ") {
		t.Fatalf("unexpected canonical order: got %v want %v", labels, want)
	}
}

type fakeSessionClient struct {
	initializeResp acpclient.InitializeResponse
	newSessionResp acpclient.NewSessionResponse
	onLoadSession  func(string)
	onNewSession   func()
	onPrompt       func([]json.RawMessage)
}

func (f *fakeSessionClient) Initialize(context.Context) (acpclient.InitializeResponse, error) {
	return f.initializeResp, nil
}

func (f *fakeSessionClient) NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error) {
	if f.onNewSession != nil {
		f.onNewSession()
	}
	if strings.TrimSpace(f.newSessionResp.SessionID) == "" {
		f.newSessionResp.SessionID = "remote-new"
	}
	return f.newSessionResp, nil
}

func (f *fakeSessionClient) LoadSession(_ context.Context, sessionID string, _ string, _ map[string]any) (acpclient.LoadSessionResponse, error) {
	if f.onLoadSession != nil {
		f.onLoadSession(sessionID)
	}
	return acpclient.LoadSessionResponse{}, nil
}

func (f *fakeSessionClient) PromptParts(_ context.Context, _ string, prompt []json.RawMessage, _ map[string]any) (acpclient.PromptResponse, error) {
	if f.onPrompt != nil {
		f.onPrompt(prompt)
	}
	return acpclient.PromptResponse{StopReason: "end_turn"}, nil
}

func (f *fakeSessionClient) Close() error { return nil }

type testInvocationContext struct {
	context.Context
	sess     *session.Session
	events   session.Events
	state    session.ReadonlyState
	overlay  bool
	policies []policy.Hook
}

func (c testInvocationContext) Session() *session.Session            { return c.sess }
func (c testInvocationContext) Events() session.Events               { return c.events }
func (c testInvocationContext) ReadonlyState() session.ReadonlyState { return c.state }
func (c testInvocationContext) Overlay() bool                        { return c.overlay }
func (c testInvocationContext) Model() model.LLM                     { return nil }
func (c testInvocationContext) Tools() []tool.Tool                   { return nil }
func (c testInvocationContext) Tool(string) (tool.Tool, bool)        { return nil, false }
func (c testInvocationContext) Policies() []policy.Hook              { return c.policies }
func (c testInvocationContext) SubagentRunner() agent.SubagentRunner { return nil }

func stubACPClientStart(fn func(context.Context, acpclient.Config) (sessionClient, error)) func() {
	previous := startACPClient
	startACPClient = fn
	return func() {
		startACPClient = previous
	}
}

func decodePromptBlock(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var block map[string]string
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatalf("decode prompt block: %v", err)
	}
	return block
}

var _ agent.InvocationContext = testInvocationContext{}
var _ sessionClient = (*fakeSessionClient)(nil)
