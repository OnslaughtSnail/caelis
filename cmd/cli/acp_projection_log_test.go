package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestParticipantReplayMessage_ProjectionEvent(t *testing.T) {
	occurredAt := time.Date(2026, 4, 8, 1, 2, 3, 456000000, time.UTC)
	msg, ok := participantReplayMessage(acpProjectionPersistedEvent{
		Kind:          "projection",
		Time:          occurredAt.Format(time.RFC3339Nano),
		Scope:         string(tuievents.ACPProjectionParticipant),
		ScopeID:       "child-1",
		SessionID:     "child-1",
		Actor:         "cole(copilot)",
		Stream:        "assistant",
		DeltaText:     "hello",
		ToolCallID:    "tool-1",
		ToolName:      "READ",
		ToolArgs:      map[string]any{"path": "/tmp/demo"},
		ToolResult:    map[string]any{"summary": "done"},
		ToolStatus:    "completed",
		PlanEntries:   []tuievents.PlanEntry{{Content: "step", Status: "done"}},
		HasPlanUpdate: true,
	})
	if !ok {
		t.Fatal("expected replay message")
	}
	got, ok := msg.(tuievents.ACPProjectionMsg)
	if !ok {
		t.Fatalf("expected ACPProjectionMsg, got %T", msg)
	}
	want := tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionParticipant,
		ScopeID:       "child-1",
		Actor:         "cole(copilot)",
		OccurredAt:    occurredAt,
		Stream:        "assistant",
		DeltaText:     "hello",
		ToolCallID:    "tool-1",
		ToolName:      "READ",
		ToolArgs:      map[string]any{"path": "/tmp/demo"},
		ToolResult:    map[string]any{"summary": "done"},
		ToolStatus:    "completed",
		PlanEntries:   []tuievents.PlanEntry{{Content: "step", Status: "done"}},
		HasPlanUpdate: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected participant replay message: %#v", got)
	}
}

func TestSubagentReplayMessage_StatusRouting(t *testing.T) {
	doneAt := time.Date(2026, 4, 8, 2, 3, 4, 0, time.UTC)
	doneMsg, ok := subagentReplayMessage(acpProjectionPersistedEvent{
		Kind:    "status",
		Time:    doneAt.Format(time.RFC3339Nano),
		ScopeID: "spawn-1",
		Status:  "completed",
	})
	if !ok {
		t.Fatal("expected completed status replay message")
	}
	if got, ok := doneMsg.(tuievents.SubagentDoneMsg); !ok || got.SpawnID != "spawn-1" || got.State != "completed" || !got.OccurredAt.Equal(doneAt) {
		t.Fatalf("unexpected completed replay message: %#v", doneMsg)
	}

	waitAt := time.Date(2026, 4, 8, 2, 4, 5, 0, time.UTC)
	statusMsg, ok := subagentReplayMessage(acpProjectionPersistedEvent{
		Kind:            "status",
		Time:            waitAt.Format(time.RFC3339Nano),
		ScopeID:         "spawn-2",
		Status:          "waiting_approval",
		ApprovalTool:    "shell",
		ApprovalCommand: "rm -rf /tmp/demo",
	})
	if !ok {
		t.Fatal("expected waiting status replay message")
	}
	got, ok := statusMsg.(tuievents.SubagentStatusMsg)
	if !ok {
		t.Fatalf("expected SubagentStatusMsg, got %T", statusMsg)
	}
	want := tuievents.SubagentStatusMsg{
		SpawnID:         "spawn-2",
		State:           "waiting_approval",
		ApprovalTool:    "shell",
		ApprovalCommand: "rm -rf /tmp/demo",
		OccurredAt:      waitAt,
	}
	if got != want {
		t.Fatalf("unexpected status replay message: %#v", got)
	}
}

func TestACPProjectionStore_LoadIndexBuildsCallAndScopeViews(t *testing.T) {
	store := inmemory.New()
	console := &cliConsole{
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-1",
		sessionStore: store,
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	projectionStore := console.acpProjectionStore()
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionParticipant), ScopeID: "child-1", CallID: "call-1", Kind: "turn_start"},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "spawn-1", CallID: "call-2", Kind: "projection"},
	} {
		if err := projectionStore.AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	index, err := projectionStore.LoadIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if index == nil {
		t.Fatal("expected non-nil projection index")
	}
	if got := len(index.ByCallID["call-1"]); got != 1 {
		t.Fatalf("expected call-1 index length 1, got %d", got)
	}
	if got := len(index.ByScopeID[tuievents.ACPProjectionParticipant]["child-1"]); got != 1 {
		t.Fatalf("expected participant scope index length 1, got %d", got)
	}
	if got := len(index.ByScopeID[tuievents.ACPProjectionSubagent]["spawn-1"]); got != 1 {
		t.Fatalf("expected subagent scope index length 1, got %d", got)
	}
}

func TestProjectionNarrativeSnapshot_PrefersLatestFullText(t *testing.T) {
	assistant, reasoning := projectionNarrativeSnapshot([]acpProjectionPersistedEvent{
		{Kind: "projection", Stream: "assistant", DeltaText: "先列出仓库结构，然后继续说明。", FullText: "先列出仓库结构，然后继续说明。"},
		{Kind: "projection", ToolCallID: "tool-1", ToolName: "READ"},
		{Kind: "projection", Stream: "assistant", FullText: "先列出仓库结构，然后继续说明。最后给出总结。"},
		{Kind: "projection", Stream: "reasoning", DeltaText: "思考中", FullText: "思考中"},
	})
	if assistant != "先列出仓库结构，然后继续说明。最后给出总结。" {
		t.Fatalf("unexpected assistant snapshot %q", assistant)
	}
	if reasoning != "思考中" {
		t.Fatalf("unexpected reasoning snapshot %q", reasoning)
	}
}

func TestACPProjectionStore_PreservesParticipantWhitespace(t *testing.T) {
	store := inmemory.New()
	console := &cliConsole{
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-1",
		sessionStore: store,
	}
	if _, err := store.GetOrCreate(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	turn := &externalAgentTurn{
		callID: "call-1",
		participant: externalParticipant{
			Alias:          "cole",
			AgentID:        "copilot",
			ChildSessionID: "child-1",
			DisplayLabel:   "cole(copilot)",
		},
	}
	delta := "```go\n"
	full := "```go\nfmt.Println(\"hi\")\n"
	if err := console.acpProjectionStore().AppendParticipantProjection(context.Background(), turn, acpprojector.Projection{
		SessionID: "child-1",
		Stream:    "assistant",
		DeltaText: delta,
		FullText:  full,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := console.acpProjectionStore().LoadEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one projection event, got %d", len(events))
	}
	if events[0].DeltaText != delta {
		t.Fatalf("expected delta text %q, got %q", delta, events[0].DeltaText)
	}
	if events[0].FullText != full {
		t.Fatalf("expected full text %q, got %q", full, events[0].FullText)
	}
}
