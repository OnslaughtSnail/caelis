package chat

import (
	"context"
	"encoding/json"
	"iter"
	"testing"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

func TestChatAgentUsesSessionMessages(t *testing.T) {
	t.Parallel()

	model := &recordingModel{}
	agent, err := New("chat", model, "Be terse.")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := sdkruntime.NewContext(sdkruntime.ContextSpec{
		Context: context.Background(),
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{
				AppName:      "caelis",
				UserID:       "user-1",
				SessionID:    "sess-1",
				WorkspaceKey: "ws-1",
			},
		},
		Events: []*sdksession.Event{
			{
				Type:    sdksession.EventTypeUser,
				Message: ptrMessage(sdkmodel.NewTextMessage(sdkmodel.RoleUser, "hello")),
				Text:    "hello",
			},
		},
	})

	var final *sdksession.Event
	for event, runErr := range agent.Run(ctx) {
		if runErr != nil {
			t.Fatalf("Run() error = %v", runErr)
		}
		final = event
	}

	if got := len(model.last.Messages); got != 1 {
		t.Fatalf("len(Messages) = %d, want 1", got)
	}
	if got := model.last.Messages[0].TextContent(); got != "hello" {
		t.Fatalf("user text = %q, want %q", got, "hello")
	}
	if got := len(model.last.Instructions); got != 1 {
		t.Fatalf("len(Instructions) = %d, want 1", got)
	}
	if final == nil || final.Text != "world" {
		t.Fatalf("final event = %+v, want assistant world", final)
	}
}

func TestFactoryMetadataSystemPromptOverridesFactoryDefault(t *testing.T) {
	t.Parallel()

	model := &recordingModel{}
	agent, err := (Factory{SystemPrompt: "factory-default"}).NewAgent(context.Background(), sdkruntime.AgentSpec{
		Name:  "chat",
		Model: model,
		Metadata: map[string]any{
			"system_prompt": "assembly-override",
		},
	})
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	ctx := sdkruntime.NewContext(sdkruntime.ContextSpec{
		Context: context.Background(),
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{SessionID: "sess-override"},
		},
		Events: []*sdksession.Event{{
			Type:    sdksession.EventTypeUser,
			Message: ptrMessage(sdkmodel.NewTextMessage(sdkmodel.RoleUser, "hello")),
			Text:    "hello",
		}},
	})

	for _, runErr := range agent.Run(ctx) {
		if runErr != nil {
			t.Fatalf("Run() error = %v", runErr)
		}
	}
	if got, want := len(model.last.Instructions), 1; got != want {
		t.Fatalf("len(Instructions) = %d, want %d", got, want)
	}
	if model.last.Instructions[0].Kind != sdkmodel.PartKindText || model.last.Instructions[0].Text == nil {
		t.Fatalf("instruction[0] = %+v, want text part", model.last.Instructions[0])
	}
	if got := model.last.Instructions[0].Text.Text; got != "assembly-override" {
		t.Fatalf("instruction text = %q, want %q", got, "assembly-override")
	}
}

func TestChatAgentRunsMinimalToolLoop(t *testing.T) {
	t.Parallel()

	model := &toolLoopModel{}
	tool := sdktool.NamedTool{
		Def: sdktool.Definition{
			Name:        "ECHO",
			Description: "echo input",
			InputSchema: map[string]any{"type": "object"},
		},
		Invoke: func(_ context.Context, call sdktool.Call) (sdktool.Result, error) {
			var payload map[string]any
			_ = json.Unmarshal(call.Input, &payload)
			return sdktool.Result{
				ID:   call.ID,
				Name: call.Name,
				Content: []sdkmodel.Part{
					sdkmodel.NewJSONPart([]byte(`{"value":"pong"}`)),
				},
			}, nil
		},
	}
	agent, err := NewWithTools("chat", model, []sdktool.Tool{tool}, "Use tools when needed.")
	if err != nil {
		t.Fatalf("NewWithTools() error = %v", err)
	}

	ctx := sdkruntime.NewContext(sdkruntime.ContextSpec{
		Context: context.Background(),
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{SessionID: "sess-1"},
		},
		Events: []*sdksession.Event{{
			Type:    sdksession.EventTypeUser,
			Message: ptrMessage(sdkmodel.NewTextMessage(sdkmodel.RoleUser, "say pong")),
			Text:    "say pong",
		}},
	})

	var events []*sdksession.Event
	for event, runErr := range agent.Run(ctx) {
		if runErr != nil {
			t.Fatalf("Run() error = %v", runErr)
		}
		events = append(events, event)
	}

	if got, want := len(model.requests), 2; got != want {
		t.Fatalf("len(model.requests) = %d, want %d", got, want)
	}
	if got, want := len(model.requests[0].Tools), 1; got != want {
		t.Fatalf("len(first request tools) = %d, want %d", got, want)
	}
	if got, want := len(events), 3; got != want {
		t.Fatalf("len(events) = %d, want %d", got, want)
	}
	if events[0].Type != sdksession.EventTypeToolCall {
		t.Fatalf("events[0].Type = %q, want tool_call", events[0].Type)
	}
	if events[0].Protocol == nil || events[0].Protocol.ToolCall == nil || events[0].Protocol.UpdateType != string(sdksession.ProtocolUpdateTypeToolCall) {
		t.Fatalf("events[0].Protocol = %+v, want tool_call protocol payload", events[0].Protocol)
	}
	if events[1].Type != sdksession.EventTypeToolResult {
		t.Fatalf("events[1].Type = %q, want tool_result", events[1].Type)
	}
	if events[1].Protocol == nil || events[1].Protocol.ToolCall == nil || events[1].Protocol.UpdateType != string(sdksession.ProtocolUpdateTypeToolUpdate) {
		t.Fatalf("events[1].Protocol = %+v, want tool_call_update protocol payload", events[1].Protocol)
	}
	if events[2].Type != sdksession.EventTypeAssistant || events[2].Text != "pong" {
		t.Fatalf("events[2] = %+v, want final assistant pong", events[2])
	}
	if events[2].Protocol == nil || events[2].Protocol.UpdateType != string(sdksession.ProtocolUpdateTypeAgentMessage) {
		t.Fatalf("events[2].Protocol = %+v, want agent_message protocol payload", events[2].Protocol)
	}
}

type recordingModel struct {
	last sdkmodel.Request
}

func (m *recordingModel) Name() string { return "stub" }

func (m *recordingModel) Generate(_ context.Context, req *sdkmodel.Request) iter.Seq2[*sdkmodel.StreamEvent, error] {
	if req != nil {
		m.last = *req
		m.last.Messages = sdkmodel.CloneMessages(req.Messages)
		m.last.Instructions = sdkmodel.CloneParts(req.Instructions)
	}
	return func(yield func(*sdkmodel.StreamEvent, error) bool) {
		yield(&sdkmodel.StreamEvent{
			Type: sdkmodel.StreamEventTurnDone,
			Response: &sdkmodel.Response{
				Message:      sdkmodel.NewTextMessage(sdkmodel.RoleAssistant, "world"),
				TurnComplete: true,
				StepComplete: true,
				Status:       sdkmodel.ResponseStatusCompleted,
			},
		}, nil)
	}
}

func ptrMessage(message sdkmodel.Message) *sdkmodel.Message {
	return &message
}

type toolLoopModel struct {
	requests []sdkmodel.Request
}

func (m *toolLoopModel) Name() string { return "tool-loop" }

func (m *toolLoopModel) Generate(_ context.Context, req *sdkmodel.Request) iter.Seq2[*sdkmodel.StreamEvent, error] {
	if req != nil {
		cp := *req
		cp.Messages = sdkmodel.CloneMessages(req.Messages)
		cp.Instructions = sdkmodel.CloneParts(req.Instructions)
		cp.Tools = append([]sdkmodel.ToolSpec(nil), req.Tools...)
		m.requests = append(m.requests, cp)
	}
	index := len(m.requests)
	return func(yield func(*sdkmodel.StreamEvent, error) bool) {
		if index == 1 {
			yield(&sdkmodel.StreamEvent{
				Type: sdkmodel.StreamEventTurnDone,
				Response: &sdkmodel.Response{
					Message: sdkmodel.MessageFromToolCalls(sdkmodel.RoleAssistant, []sdkmodel.ToolCall{{
						ID:   "call-1",
						Name: "ECHO",
						Args: `{"value":"pong"}`,
					}}, ""),
					TurnComplete: true,
					StepComplete: true,
					Status:       sdkmodel.ResponseStatusCompleted,
					FinishReason: sdkmodel.FinishReasonToolCalls,
				},
			}, nil)
			return
		}
		yield(&sdkmodel.StreamEvent{
			Type: sdkmodel.StreamEventTurnDone,
			Response: &sdkmodel.Response{
				Message:      sdkmodel.NewTextMessage(sdkmodel.RoleAssistant, "pong"),
				TurnComplete: true,
				StepComplete: true,
				Status:       sdkmodel.ResponseStatusCompleted,
				FinishReason: sdkmodel.FinishReasonStop,
			},
		}, nil)
	}
}
