package core

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

func TestCanonicalApprovalPayloadTableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *sdkruntime.ApprovalRequest
		want func(*testing.T, *ApprovalPayload)
	}{
		{
			name: "runtime call fallback",
			req: &sdkruntime.ApprovalRequest{
				Call: sdktool.Call{
					Name:  "bash",
					Input: json.RawMessage(`{"command":"echo hi"}`),
				},
			},
			want: func(t *testing.T, payload *ApprovalPayload) {
				t.Helper()
				if payload == nil {
					t.Fatal("canonicalApprovalPayload() = nil, want payload")
				}
				if payload.ToolName != "bash" {
					t.Fatalf("payload.ToolName = %q, want %q", payload.ToolName, "bash")
				}
				if payload.CommandPreview != "echo hi" {
					t.Fatalf("payload.CommandPreview = %q, want %q", payload.CommandPreview, "echo hi")
				}
				if payload.Status != ApprovalStatusPending {
					t.Fatalf("payload.Status = %q, want %q", payload.Status, ApprovalStatusPending)
				}
			},
		},
		{
			name: "protocol approval options",
			req: &sdkruntime.ApprovalRequest{
				Approval: &sdksession.ProtocolApproval{
					ToolCall: sdksession.ProtocolToolCall{
						Name:     "BASH",
						RawInput: map[string]any{"command": "rm -rf /tmp/demo"},
					},
					Options: []sdksession.ProtocolApprovalOption{
						{ID: "allow_once", Name: "Allow once", Kind: "allow_once"},
					},
				},
			},
			want: func(t *testing.T, payload *ApprovalPayload) {
				t.Helper()
				if payload == nil {
					t.Fatal("canonicalApprovalPayload() = nil, want payload")
				}
				if payload.ToolName != "BASH" {
					t.Fatalf("payload.ToolName = %q, want %q", payload.ToolName, "BASH")
				}
				if payload.CommandPreview != "rm -rf /tmp/demo" {
					t.Fatalf("payload.CommandPreview = %q, want %q", payload.CommandPreview, "rm -rf /tmp/demo")
				}
				if len(payload.Options) != 1 || payload.Options[0].ID != "allow_once" {
					t.Fatalf("payload.Options = %#v, want allow_once option", payload.Options)
				}
				if payload.Status != ApprovalStatusPending {
					t.Fatalf("payload.Status = %q, want %q", payload.Status, ApprovalStatusPending)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.want(t, canonicalApprovalPayload(tt.req))
		})
	}
}

func TestProjectSessionEventsCanonicalPayloadsTableDriven(t *testing.T) {
	t.Parallel()

	reasoningMessage := sdkmodel.NewReasoningMessage(sdkmodel.RoleAssistant, "think through options", sdkmodel.ReasoningVisibilityVisible)

	tests := []struct {
		name string
		ev   *sdksession.Event
		want func(*testing.T, EventEnvelope)
	}{
		{
			name: "assistant text",
			ev: &sdksession.Event{
				ID:         "assistant-1",
				Type:       sdksession.EventTypeAssistant,
				Text:       "done",
				Visibility: sdksession.VisibilityCanonical,
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Kind != EventKindAssistantMessage {
					t.Fatalf("event.Kind = %q, want %q", env.Event.Kind, EventKindAssistantMessage)
				}
				if env.Event.Narrative == nil {
					t.Fatal("event.Narrative = nil, want payload")
				}
				if env.Event.Narrative.Role != NarrativeRoleAssistant || env.Event.Narrative.Text != "done" || !env.Event.Narrative.Final {
					t.Fatalf("event.Narrative = %+v", env.Event.Narrative)
				}
			},
		},
		{
			name: "reasoning",
			ev: &sdksession.Event{
				ID:         "reasoning-1",
				Type:       sdksession.EventTypeAssistant,
				Text:       "think through options",
				Visibility: sdksession.VisibilityUIOnly,
				Message:    &reasoningMessage,
				Protocol: &sdksession.EventProtocol{
					UpdateType: string(sdksession.ProtocolUpdateTypeAgentThought),
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Narrative == nil {
					t.Fatal("event.Narrative = nil, want payload")
				}
				if env.Event.Narrative.Text != "" {
					t.Fatalf("event.Narrative.Text = %q, want empty reasoning-only answer text", env.Event.Narrative.Text)
				}
				if env.Event.Narrative.ReasoningText != "think through options" {
					t.Fatalf("event.Narrative.ReasoningText = %q, want %q", env.Event.Narrative.ReasoningText, "think through options")
				}
			},
		},
		{
			name: "plan",
			ev: &sdksession.Event{
				ID:   "plan-1",
				Type: sdksession.EventTypePlan,
				Protocol: &sdksession.EventProtocol{
					Plan: &sdksession.ProtocolPlan{
						Entries: []sdksession.ProtocolPlanEntry{
							{Content: "Inspect gateway event flow", Status: "in_progress", Priority: "high"},
						},
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Kind != EventKindPlanUpdate {
					t.Fatalf("event.Kind = %q, want %q", env.Event.Kind, EventKindPlanUpdate)
				}
				if env.Event.Plan == nil || len(env.Event.Plan.Entries) != 1 {
					t.Fatalf("event.Plan = %+v, want one entry", env.Event.Plan)
				}
				if entry := env.Event.Plan.Entries[0]; entry.Content != "Inspect gateway event flow" || entry.Status != "in_progress" || entry.Priority != "high" {
					t.Fatalf("event.Plan.Entries[0] = %+v", entry)
				}
			},
		},
		{
			name: "tool call started",
			ev: &sdksession.Event{
				ID:   "tool-call-started",
				Type: sdksession.EventTypeToolCall,
				Protocol: &sdksession.EventProtocol{
					ToolCall: &sdksession.ProtocolToolCall{
						ID:       "call-1",
						Name:     "READ",
						RawInput: map[string]any{"path": "/tmp/demo.txt"},
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.ToolCall == nil {
					t.Fatal("event.ToolCall = nil, want payload")
				}
				if env.Event.ToolCall.Status != ToolStatusStarted {
					t.Fatalf("event.ToolCall.Status = %q, want %q", env.Event.ToolCall.Status, ToolStatusStarted)
				}
				if env.Event.ToolCall.CommandPreview != "/tmp/demo.txt" {
					t.Fatalf("event.ToolCall.CommandPreview = %q, want %q", env.Event.ToolCall.CommandPreview, "/tmp/demo.txt")
				}
			},
		},
		{
			name: "tool call running",
			ev: &sdksession.Event{
				ID:   "tool-call-running",
				Type: sdksession.EventTypeToolCall,
				Protocol: &sdksession.EventProtocol{
					ToolCall: &sdksession.ProtocolToolCall{
						ID:       "call-2",
						Name:     "BASH",
						Status:   "running",
						RawInput: map[string]any{"command": "sleep 1"},
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.ToolCall == nil {
					t.Fatal("event.ToolCall = nil, want payload")
				}
				if env.Event.ToolCall.Status != ToolStatusRunning {
					t.Fatalf("event.ToolCall.Status = %q, want %q", env.Event.ToolCall.Status, ToolStatusRunning)
				}
			},
		},
		{
			name: "tool result completed",
			ev: &sdksession.Event{
				ID:   "tool-result-completed",
				Type: sdksession.EventTypeToolResult,
				Protocol: &sdksession.EventProtocol{
					ToolCall: &sdksession.ProtocolToolCall{
						ID:        "call-3",
						Name:      "READ",
						Status:    "completed",
						RawInput:  map[string]any{"path": "/tmp/demo.txt"},
						RawOutput: map[string]any{"path": "/tmp/demo.txt"},
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.ToolResult == nil {
					t.Fatal("event.ToolResult = nil, want payload")
				}
				if env.Event.ToolResult.Status != ToolStatusCompleted || env.Event.ToolResult.Error {
					t.Fatalf("event.ToolResult = %+v", env.Event.ToolResult)
				}
			},
		},
		{
			name: "tool result failed",
			ev: &sdksession.Event{
				ID:   "tool-result-failed",
				Type: sdksession.EventTypeToolResult,
				Protocol: &sdksession.EventProtocol{
					ToolCall: &sdksession.ProtocolToolCall{
						ID:        "call-4",
						Name:      "BASH",
						Status:    "error",
						RawInput:  map[string]any{"command": "exit 1"},
						RawOutput: map[string]any{"error": "exit status 1"},
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.ToolResult == nil {
					t.Fatal("event.ToolResult = nil, want payload")
				}
				if env.Event.ToolResult.Status != ToolStatusFailed || !env.Event.ToolResult.Error {
					t.Fatalf("event.ToolResult = %+v", env.Event.ToolResult)
				}
			},
		},
		{
			name: "participant subagent",
			ev: &sdksession.Event{
				ID:   "participant-1",
				Type: sdksession.EventTypeParticipant,
				Scope: &sdksession.EventScope{
					TurnID: "turn-1",
					Participant: sdksession.ParticipantRef{
						ID:           "participant-1",
						Kind:         sdksession.ParticipantKindSubagent,
						Role:         sdksession.ParticipantRoleSidecar,
						DelegationID: "delegation-1",
					},
					ACP: sdksession.ACPRef{SessionID: "remote-session-1"},
				},
				Protocol: &sdksession.EventProtocol{
					Participant: &sdksession.ProtocolParticipant{Action: "attached"},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Participant == nil {
					t.Fatal("event.Participant = nil, want payload")
				}
				if env.Event.Participant.Action != ParticipantActionAttached || env.Event.Participant.Scope != EventScopeSubagent {
					t.Fatalf("event.Participant = %+v", env.Event.Participant)
				}
				if env.Event.Origin == nil || env.Event.Origin.Scope != EventScopeSubagent || env.Event.Origin.ScopeID != "remote-session-1" {
					t.Fatalf("event.Origin = %+v, want subagent origin", env.Event.Origin)
				}
			},
		},
		{
			name: "lifecycle",
			ev: &sdksession.Event{
				ID:   "lifecycle-1",
				Type: sdksession.EventTypeLifecycle,
				Scope: &sdksession.EventScope{
					Participant: sdksession.ParticipantRef{ID: "participant-1"},
				},
				Lifecycle: &sdksession.EventLifecycle{
					Status: "waiting_approval",
					Reason: "tool gate",
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Lifecycle == nil {
					t.Fatal("event.Lifecycle = nil, want payload")
				}
				if env.Event.Lifecycle.Status != LifecycleStatusWaitingApproval || env.Event.Lifecycle.Reason != "tool gate" {
					t.Fatalf("event.Lifecycle = %+v", env.Event.Lifecycle)
				}
			},
		},
		{
			name: "usage snapshot",
			ev: &sdksession.Event{
				ID:   "usage-1",
				Type: sdksession.EventTypeAssistant,
				Text: "done",
				Meta: map[string]any{
					"usage": map[string]any{
						"prompt_tokens":     12,
						"completion_tokens": 5,
						"total_tokens":      17,
					},
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Usage == nil {
					t.Fatal("event.Usage = nil, want payload")
				}
				if env.Event.Usage.PromptTokens != 12 || env.Event.Usage.CompletionTokens != 5 || env.Event.Usage.TotalTokens != 17 {
					t.Fatalf("event.Usage = %+v", env.Event.Usage)
				}
			},
		},
		{
			name: "top-level usage snapshot",
			ev: &sdksession.Event{
				ID:   "usage-2",
				Type: sdksession.EventTypeAssistant,
				Text: "done",
				Meta: map[string]any{
					"prompt_tokens":     12,
					"completion_tokens": 5,
					"total_tokens":      17,
				},
			},
			want: func(t *testing.T, env EventEnvelope) {
				t.Helper()
				if env.Event.Usage == nil {
					t.Fatal("event.Usage = nil, want payload")
				}
				if env.Event.Usage.PromptTokens != 12 || env.Event.Usage.CompletionTokens != 5 || env.Event.Usage.TotalTokens != 17 {
					t.Fatalf("event.Usage = %+v", env.Event.Usage)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projected := projectSessionEvents(sdksession.SessionRef{SessionID: "root-session"}, []*sdksession.Event{tt.ev})
			if len(projected) != 1 {
				t.Fatalf("projectSessionEvents() len = %d, want 1", len(projected))
			}
			tt.want(t, projected[0])
		})
	}
}

func TestProjectSessionEventsPreservesMessageToolCallID(t *testing.T) {
	t.Parallel()

	message := sdkmodel.MessageFromToolCalls(sdkmodel.RoleAssistant, []sdkmodel.ToolCall{{
		ID:   "call-1",
		Name: "BASH",
		Args: `{"command":"echo hi"}`,
	}}, "")

	events := projectSessionEvents(sdksession.SessionRef{SessionID: "root-session"}, []*sdksession.Event{{
		ID:      "tool-1",
		Message: &message,
	}})
	if len(events) != 1 {
		t.Fatalf("projectSessionEvents() len = %d, want 1", len(events))
	}
	if events[0].Event.Kind != EventKindToolCall {
		t.Fatalf("event kind = %q, want %q", events[0].Event.Kind, EventKindToolCall)
	}
	payload := events[0].Event.ToolCall
	if payload == nil {
		t.Fatal("tool call payload = nil, want canonical payload")
	}
	if payload.CallID != "call-1" {
		t.Fatalf("payload.CallID = %q, want %q", payload.CallID, "call-1")
	}
	if payload.ToolName != "BASH" {
		t.Fatalf("payload.ToolName = %q, want %q", payload.ToolName, "BASH")
	}
	if payload.Status != ToolStatusStarted {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, ToolStatusStarted)
	}
}

func TestReplayAfterCursorReturnsCursorNotFound(t *testing.T) {
	t.Parallel()

	_, err := replayAfterCursor([]EventEnvelope{
		{Cursor: "e1"},
		{Cursor: "e2"},
	}, "missing", 0)
	if err == nil {
		t.Fatal("replayAfterCursor() error = nil, want cursor_not_found")
	}
	var gwErr *Error
	if !As(err, &gwErr) || gwErr.Code != CodeCursorNotFound {
		t.Fatalf("replayAfterCursor() error = %v, want cursor_not_found", err)
	}
}

func TestCompactStringValueTruncatesUTF8Safely(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("你", 130)
	got := compactStringValue(input)
	if !utf8.ValidString(got) {
		t.Fatalf("compactStringValue() produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("compactStringValue() = %q, want ellipsis suffix", got)
	}
	if len([]rune(got)) != 120 {
		t.Fatalf("len([]rune(got)) = %d, want 120", len([]rune(got)))
	}
}
