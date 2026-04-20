package core

import (
	"encoding/json"
	"testing"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

func TestCanonicalApprovalPayloadFallsBackToRuntimeCall(t *testing.T) {
	t.Parallel()

	payload := canonicalApprovalPayload(&sdkruntime.ApprovalRequest{
		Call: sdktool.Call{
			Name:  "bash",
			Input: json.RawMessage(`{"command":"echo hi"}`),
		},
	})
	if payload == nil {
		t.Fatal("canonicalApprovalPayload() = nil, want payload")
	}
	if payload.ToolName != "bash" {
		t.Fatalf("payload.ToolName = %q, want %q", payload.ToolName, "bash")
	}
	if payload.CommandPreview != "echo hi" {
		t.Fatalf("payload.CommandPreview = %q, want %q", payload.CommandPreview, "echo hi")
	}
}

func TestProjectSessionEventsMapsSubagentToolCallScope(t *testing.T) {
	t.Parallel()

	events := projectSessionEvents(sdksession.SessionRef{SessionID: "s1"}, []*sdksession.Event{{
		ID:   "e1",
		Type: sdksession.EventTypeToolCall,
		Scope: &sdksession.EventScope{
			Participant: sdksession.ParticipantRef{
				ID:   "participant-1",
				Kind: sdksession.ParticipantKindSubagent,
			},
		},
		Protocol: &sdksession.EventProtocol{
			ToolCall: &sdksession.ProtocolToolCall{
				ID:       "call-1",
				Name:     "READ",
				Status:   "running",
				RawInput: map[string]any{"path": "/tmp/demo.txt"},
			},
		},
	}})
	if len(events) != 1 {
		t.Fatalf("projectSessionEvents() len = %d, want 1", len(events))
	}
	payload := events[0].Event.ToolCall
	if payload == nil {
		t.Fatal("tool call payload = nil, want canonical tool call")
	}
	if payload.Scope != EventScopeSubagent {
		t.Fatalf("payload.Scope = %q, want %q", payload.Scope, EventScopeSubagent)
	}
	if payload.ParticipantID != "participant-1" {
		t.Fatalf("payload.ParticipantID = %q, want %q", payload.ParticipantID, "participant-1")
	}
	if payload.CommandPreview != "/tmp/demo.txt" {
		t.Fatalf("payload.CommandPreview = %q, want %q", payload.CommandPreview, "/tmp/demo.txt")
	}
	if origin := events[0].Event.Origin; origin == nil || origin.Scope != EventScopeSubagent || origin.ScopeID != "participant-1" || origin.ParticipantID != "participant-1" {
		t.Fatalf("event origin = %+v, want subagent participant scope", origin)
	}
}

func TestProjectSessionEventsCanonicalizesPlanParticipantAndLifecycle(t *testing.T) {
	t.Parallel()

	events := projectSessionEvents(sdksession.SessionRef{SessionID: "s1"}, []*sdksession.Event{{
		ID:   "plan-1",
		Type: sdksession.EventTypePlan,
		Scope: &sdksession.EventScope{
			TurnID: "turn-1",
			Participant: sdksession.ParticipantRef{
				ID:   "participant-1",
				Kind: sdksession.ParticipantKindACP,
				Role: sdksession.ParticipantRoleSidecar,
			},
			ACP: sdksession.ACPRef{SessionID: "remote-session-1"},
		},
		Protocol: &sdksession.EventProtocol{
			Plan: &sdksession.ProtocolPlan{
				Entries: []sdksession.ProtocolPlanEntry{
					{Content: "Inspect gateway event flow", Status: "in_progress", Priority: "high"},
				},
			},
		},
	}, {
		ID:   "participant-1",
		Type: sdksession.EventTypeParticipant,
		Scope: &sdksession.EventScope{
			TurnID: "turn-1",
			Participant: sdksession.ParticipantRef{
				ID:           "participant-1",
				Kind:         sdksession.ParticipantKindACP,
				Role:         sdksession.ParticipantRoleSidecar,
				DelegationID: "delegation-1",
			},
			ACP: sdksession.ACPRef{SessionID: "remote-session-1"},
		},
		Protocol: &sdksession.EventProtocol{
			Participant: &sdksession.ProtocolParticipant{Action: "attached"},
		},
	}, {
		ID:   "life-1",
		Type: sdksession.EventTypeLifecycle,
		Scope: &sdksession.EventScope{
			Participant: sdksession.ParticipantRef{ID: "participant-1"},
		},
		Lifecycle: &sdksession.EventLifecycle{
			Status: "running",
			Reason: "resume",
		},
	}})
	if len(events) != 3 {
		t.Fatalf("projectSessionEvents() len = %d, want 3", len(events))
	}
	if events[0].Event.Plan == nil || len(events[0].Event.Plan.Entries) != 1 {
		t.Fatalf("plan payload = %+v, want 1 entry", events[0].Event.Plan)
	}
	if entry := events[0].Event.Plan.Entries[0]; entry.Content != "Inspect gateway event flow" || entry.Status != "in_progress" || entry.Priority != "high" {
		t.Fatalf("plan entry = %+v", entry)
	}
	if events[1].Event.Participant == nil {
		t.Fatal("participant payload = nil, want canonical participant payload")
	}
	if payload := events[1].Event.Participant; payload.Action != "attached" || payload.ParticipantID != "participant-1" || payload.ParticipantKind != string(sdksession.ParticipantKindACP) || payload.Role != string(sdksession.ParticipantRoleSidecar) || payload.SessionID != "remote-session-1" || payload.DelegationID != "delegation-1" {
		t.Fatalf("participant payload = %+v", payload)
	}
	if origin := events[1].Event.Origin; origin == nil || origin.Scope != EventScopeParticipant || origin.ScopeID != "remote-session-1" || origin.ParticipantID != "participant-1" {
		t.Fatalf("participant origin = %+v, want participant scope", origin)
	}
	if events[2].Event.Lifecycle == nil {
		t.Fatal("lifecycle payload = nil, want canonical lifecycle payload")
	}
	if payload := events[2].Event.Lifecycle; payload.Status != "running" || payload.Reason != "resume" || payload.ParticipantID != "participant-1" || payload.Scope != EventScopeParticipant {
		t.Fatalf("lifecycle payload = %+v", payload)
	}
	if origin := events[2].Event.Origin; origin == nil || origin.Scope != EventScopeParticipant || origin.ScopeID != "participant-1" || origin.ParticipantID != "participant-1" {
		t.Fatalf("lifecycle origin = %+v, want participant scope fallback to participant id", origin)
	}
}

func TestCanonicalNarrativePayloadReasoningChunkDoesNotPopulateAnswerText(t *testing.T) {
	t.Parallel()

	reasoning := "用户让我"
	message := sdkmodel.NewReasoningMessage(sdkmodel.RoleAssistant, reasoning, sdkmodel.ReasoningVisibilityVisible)

	payload := canonicalNarrativePayload(&sdksession.Event{
		Type:       sdksession.EventTypeAssistant,
		Visibility: sdksession.VisibilityUIOnly,
		Message:    &message,
		Text:       reasoning,
		Protocol: &sdksession.EventProtocol{
			UpdateType: string(sdksession.ProtocolUpdateTypeAgentThought),
		},
	})
	if payload == nil {
		t.Fatal("canonicalNarrativePayload() = nil, want payload")
	}
	if payload.ReasoningText != reasoning {
		t.Fatalf("payload.ReasoningText = %q, want %q", payload.ReasoningText, reasoning)
	}
	if payload.Text != "" {
		t.Fatalf("payload.Text = %q, want empty for reasoning-only chunk", payload.Text)
	}
}

func TestProjectSessionEventsCanonicalizesNarrativeOrigin(t *testing.T) {
	t.Parallel()

	events := projectSessionEvents(sdksession.SessionRef{SessionID: "root-session"}, []*sdksession.Event{{
		ID:   "assistant-1",
		Type: sdksession.EventTypeAssistant,
		Scope: &sdksession.EventScope{
			TurnID: "turn-1",
			Participant: sdksession.ParticipantRef{
				ID:   "participant-1",
				Kind: sdksession.ParticipantKindACP,
				Role: sdksession.ParticipantRoleSidecar,
			},
			ACP: sdksession.ACPRef{SessionID: "remote-session-1"},
		},
		Actor: sdksession.ActorRef{ID: "assistant"},
		Text:  "done",
	}})
	if len(events) != 1 {
		t.Fatalf("projectSessionEvents() len = %d, want 1", len(events))
	}
	if payload := events[0].Event.Narrative; payload == nil || payload.Text != "done" {
		t.Fatalf("narrative payload = %+v, want assistant text", payload)
	}
	if origin := events[0].Event.Origin; origin == nil || origin.Scope != EventScopeParticipant || origin.ScopeID != "remote-session-1" || origin.Actor != "assistant" {
		t.Fatalf("event origin = %+v, want participant narrative origin", origin)
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
}
