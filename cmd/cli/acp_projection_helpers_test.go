package main

import (
	"reflect"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

func TestProjectionToACPMsg_MainScope(t *testing.T) {
	t.Parallel()
	proj := acpprojector.Projection{
		SessionID:  "remote-1",
		Stream:     "assistant",
		DeltaText:  "hello world",
		FullText:   "hello world",
		ToolCallID: "tc-1",
		ToolName:   "READ",
		ToolArgs:   map[string]any{"path": "/tmp"},
		ToolResult: map[string]any{"data": "ok"},
		ToolStatus: "completed",
		PlanEntries: []internalacp.PlanEntry{
			{Content: "step 1", Status: "done"},
		},
	}
	msg := projectionToACPMsg(proj, tuievents.ACPProjectionMain, "fallback-session", "")
	if msg.Scope != tuievents.ACPProjectionMain {
		t.Fatalf("expected Main scope, got %q", msg.Scope)
	}
	if msg.ScopeID != "remote-1" {
		t.Fatalf("expected ScopeID from projection SessionID, got %q", msg.ScopeID)
	}
	if msg.Stream != "assistant" || msg.DeltaText != "hello world" || msg.FullText != "hello world" {
		t.Fatalf("unexpected text fields: stream=%q delta=%q full=%q", msg.Stream, msg.DeltaText, msg.FullText)
	}
	if msg.ToolCallID != "tc-1" || msg.ToolName != "READ" || msg.ToolStatus != "completed" {
		t.Fatalf("unexpected tool fields: id=%q name=%q status=%q", msg.ToolCallID, msg.ToolName, msg.ToolStatus)
	}
	if !reflect.DeepEqual(msg.ToolArgs, map[string]any{"path": "/tmp"}) {
		t.Fatalf("unexpected ToolArgs: %v", msg.ToolArgs)
	}
	if !reflect.DeepEqual(msg.ToolResult, map[string]any{"data": "ok"}) {
		t.Fatalf("unexpected ToolResult: %v", msg.ToolResult)
	}
	if len(msg.PlanEntries) != 1 || msg.PlanEntries[0].Content != "step 1" {
		t.Fatalf("unexpected PlanEntries: %v", msg.PlanEntries)
	}
	if !msg.HasPlanUpdate {
		t.Fatal("expected HasPlanUpdate=true when PlanEntries is non-nil")
	}
}

func TestProjectionToACPMsg_FallbackScopeID(t *testing.T) {
	t.Parallel()
	proj := acpprojector.Projection{
		Stream:    "reasoning",
		DeltaText: "thinking",
	}
	msg := projectionToACPMsg(proj, tuievents.ACPProjectionParticipant, "child-1", "cole(copilot)")
	if msg.ScopeID != "child-1" {
		t.Fatalf("expected fallback ScopeID, got %q", msg.ScopeID)
	}
	if msg.Actor != "cole(copilot)" {
		t.Fatalf("expected actor, got %q", msg.Actor)
	}
}

func TestProjectionToACPMsg_NoPlanEntries(t *testing.T) {
	t.Parallel()
	proj := acpprojector.Projection{Stream: "assistant", DeltaText: "hi"}
	msg := projectionToACPMsg(proj, tuievents.ACPProjectionMain, "s1", "")
	if msg.HasPlanUpdate {
		t.Fatal("expected HasPlanUpdate=false when PlanEntries is nil")
	}
	if msg.PlanEntries != nil {
		t.Fatalf("expected nil PlanEntries, got %v", msg.PlanEntries)
	}
}

func TestReplayProjectionMsgFromEvent_AllScopes(t *testing.T) {
	t.Parallel()
	occurredAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ev := acpProjectionPersistedEvent{
		Kind:          "projection",
		Time:          occurredAt.Format(time.RFC3339Nano),
		SessionID:     "sess-1",
		ScopeID:       "scope-1",
		Actor:         "agent-x",
		Stream:        "assistant",
		DeltaText:     "partial",
		FullText:      "full answer",
		ToolCallID:    "call-A",
		ToolName:      "WRITE",
		ToolArgs:      map[string]any{"path": "file.go"},
		ToolResult:    map[string]any{"ok": true},
		ToolStatus:    "completed",
		PlanEntries:   []tuievents.PlanEntry{{Content: "p1", Status: "done"}},
		HasPlanUpdate: true,
	}
	for _, scope := range []tuievents.ACPProjectionScope{
		tuievents.ACPProjectionMain,
		tuievents.ACPProjectionParticipant,
		tuievents.ACPProjectionSubagent,
	} {
		msg := replayProjectionMsgFromEvent(ev, scope)
		if msg.Scope != scope {
			t.Errorf("scope %s: expected scope %s, got %s", scope, scope, msg.Scope)
		}
		if msg.ScopeID != "sess-1" {
			t.Errorf("scope %s: expected ScopeID sess-1, got %q", scope, msg.ScopeID)
		}
		if msg.Actor != "agent-x" {
			t.Errorf("scope %s: expected Actor agent-x, got %q", scope, msg.Actor)
		}
		if !msg.OccurredAt.Equal(occurredAt) {
			t.Errorf("scope %s: time mismatch", scope)
		}
		if msg.DeltaText != "partial" || msg.FullText != "full answer" {
			t.Errorf("scope %s: text mismatch", scope)
		}
		if msg.ToolCallID != "call-A" || msg.ToolName != "WRITE" || msg.ToolStatus != "completed" {
			t.Errorf("scope %s: tool fields mismatch", scope)
		}
		if !msg.HasPlanUpdate || len(msg.PlanEntries) != 1 {
			t.Errorf("scope %s: plan mismatch", scope)
		}
	}
}

func TestReplayProjectionMsgFromEvent_ScopeIDFallback(t *testing.T) {
	t.Parallel()
	ev := acpProjectionPersistedEvent{
		Kind:    "projection",
		ScopeID: "fallback-scope",
		Stream:  "reasoning",
	}
	msg := replayProjectionMsgFromEvent(ev, tuievents.ACPProjectionSubagent)
	if msg.ScopeID != "fallback-scope" {
		t.Fatalf("expected ScopeID fallback, got %q", msg.ScopeID)
	}
}

func TestAcpMsgToPersistedProjection_RoundTrip(t *testing.T) {
	t.Parallel()
	occurredAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	original := tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionMain,
		ScopeID:       "sess-1",
		Actor:         "agent-x",
		OccurredAt:    occurredAt,
		Stream:        "assistant",
		DeltaText:     "hello",
		FullText:      "hello world",
		ToolCallID:    "tc-1",
		ToolName:      "READ",
		ToolArgs:      map[string]any{"path": "/tmp"},
		ToolResult:    map[string]any{"data": "ok"},
		ToolStatus:    "completed",
		PlanEntries:   []tuievents.PlanEntry{{Content: "step", Status: "done"}},
		HasPlanUpdate: true,
	}
	persisted := acpMsgToPersistedProjection(original)
	if persisted.Scope != "main" || persisted.ScopeID != "sess-1" || persisted.Kind != "projection" {
		t.Fatalf("unexpected scope/kind: scope=%q scopeID=%q kind=%q", persisted.Scope, persisted.ScopeID, persisted.Kind)
	}
	if persisted.DeltaText != "hello" || persisted.FullText != "hello world" {
		t.Fatalf("text mismatch: delta=%q full=%q", persisted.DeltaText, persisted.FullText)
	}
	if persisted.Time != occurredAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("time mismatch: got %q", persisted.Time)
	}
	// Round-trip: persisted → replay → should match original (minus OccurredAt precision)
	reconstructed := replayProjectionMsgFromEvent(persisted, tuievents.ACPProjectionMain)
	if reconstructed.Scope != original.Scope || reconstructed.ScopeID != original.ScopeID {
		t.Fatalf("round-trip scope mismatch")
	}
	if reconstructed.DeltaText != original.DeltaText || reconstructed.FullText != original.FullText {
		t.Fatalf("round-trip text mismatch")
	}
	if reconstructed.ToolCallID != original.ToolCallID || reconstructed.ToolName != original.ToolName {
		t.Fatalf("round-trip tool mismatch")
	}
	if !reconstructed.OccurredAt.Equal(original.OccurredAt) {
		t.Fatalf("round-trip time mismatch: got %v want %v", reconstructed.OccurredAt, original.OccurredAt)
	}
}

func TestAcpMsgToPersistedProjection_ZeroTime(t *testing.T) {
	t.Parallel()
	msg := tuievents.ACPProjectionMsg{
		Scope:   tuievents.ACPProjectionSubagent,
		ScopeID: "spawn-1",
		Stream:  "assistant",
	}
	persisted := acpMsgToPersistedProjection(msg)
	if persisted.Time != "" {
		t.Fatalf("expected empty time for zero OccurredAt, got %q", persisted.Time)
	}
}
