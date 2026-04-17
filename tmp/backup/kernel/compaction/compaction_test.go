package compaction

import (
	"errors"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestSplitTargetKeepsLatestUserTurnAsTail(t *testing.T) {
	events := []*session.Event{
		{ID: "1", Message: model.NewTextMessage(model.RoleUser, "old user")},
		{ID: "2", Message: model.NewTextMessage(model.RoleAssistant, "old reply")},
		{ID: "3", Message: model.NewTextMessage(model.RoleUser, "new user")},
		{ID: "4", Message: model.NewTextMessage(model.RoleAssistant, "new reply")},
	}

	head, tail := SplitTarget(events)
	if len(head) != 2 || len(tail) != 2 {
		t.Fatalf("expected 2/2 split, got head=%d tail=%d", len(head), len(tail))
	}
	if tail[0].ID != "3" || tail[1].ID != "4" {
		t.Fatalf("unexpected tail ids: %#v", tail)
	}
}

func TestIsContextOverflowErrorMatchesStructuredAndFallbackErrors(t *testing.T) {
	if !IsContextOverflowError(&model.ContextOverflowError{Cause: errors.New("structured")}) {
		t.Fatal("expected structured overflow error to match")
	}
	if !IsContextOverflowError(errors.New("prompt is too long for model")) {
		t.Fatal("expected fallback overflow text to match")
	}
	if IsContextOverflowError(errors.New("network timeout")) {
		t.Fatal("did not expect unrelated error to match")
	}
}

func TestCheckpointRoundTripPreservesDurableState(t *testing.T) {
	cp := NormalizeCheckpoint(Checkpoint{
		Objective:             "Finish kernel compaction rewrite",
		UserConstraints:       []string{"Do not lose user constraints across repeated compactions"},
		DurableDecisions:      []string{"Use a structured checkpoint instead of prose-only summary"},
		VerifiedFacts:         []string{"kernel/runtime/compaction.go", "task-1"},
		CurrentProgress:       []string{"Extracted structured checkpoint state"},
		OpenQuestionsAndRisks: []string{"Need to validate long autonomous turns"},
		NextActions:           []string{"Run targeted tests"},
		ActiveTasks:           []string{"task-1 running"},
		LatestBlockers:        []string{"none"},
	})
	rendered := RenderCheckpointMarkdown(cp)
	parsed := ParseCheckpointMarkdown(rendered)
	if parsed.Objective != cp.Objective {
		t.Fatalf("expected objective to round-trip, got %#v", parsed)
	}
	if len(parsed.UserConstraints) == 0 || parsed.UserConstraints[0] != cp.UserConstraints[0] {
		t.Fatalf("expected user constraints to round-trip, got %#v", parsed)
	}
	if len(parsed.NextActions) == 0 || parsed.NextActions[0] != cp.NextActions[0] {
		t.Fatalf("expected next actions to round-trip, got %#v", parsed)
	}
}

func TestMergeCheckpointsPreservesPriorConstraints(t *testing.T) {
	base := Checkpoint{
		Objective:        "Finish auth flow",
		UserConstraints:  []string{"Keep tests green"},
		DurableDecisions: []string{"Reuse auth route"},
		NextActions:      []string{"Inspect middleware"},
	}
	update := Checkpoint{
		CurrentProgress: []string{"Opened auth middleware"},
		NextActions:     []string{"Patch middleware"},
	}
	merged := MergeCheckpoints(base, update, RuntimeState{
		LatestBlockerSummary: "approval pending",
	})
	if len(merged.UserConstraints) == 0 || merged.UserConstraints[0] != "Keep tests green" {
		t.Fatalf("expected prior constraints to survive merge, got %#v", merged)
	}
	if len(merged.LatestBlockers) == 0 || merged.LatestBlockers[0] != "approval pending" {
		t.Fatalf("expected runtime blockers to merge in, got %#v", merged)
	}
	if len(merged.NextActions) == 0 || merged.NextActions[0] != "Patch middleware" {
		t.Fatalf("expected new next actions to take priority, got %#v", merged.NextActions)
	}
}

func TestHeuristicFallbackCheckpointPreservesPriorObjectiveWithoutNewUserTurn(t *testing.T) {
	prior := Checkpoint{
		Objective:       "Finish auth flow",
		UserConstraints: []string{"Keep tests green"},
	}
	events := []*session.Event{
		{ID: "a1", Message: model.NewTextMessage(model.RoleAssistant, "reading auth middleware")},
	}
	cp := HeuristicFallbackCheckpoint(events, prior, RuntimeState{}, 2048)
	if cp.Objective != "Finish auth flow" {
		t.Fatalf("expected prior objective to survive heuristic fallback, got %#v", cp)
	}
}

func TestHeuristicFallbackCheckpointUsesLatestUserObjectiveWhenAvailable(t *testing.T) {
	prior := Checkpoint{
		Objective:       "Finish auth flow",
		UserConstraints: []string{"Keep tests green"},
	}
	events := []*session.Event{
		{ID: "u1", Message: model.NewTextMessage(model.RoleUser, "Switch to the billing flow and patch the invoice bug.")},
		{ID: "a1", Message: model.NewTextMessage(model.RoleAssistant, "Investigating billing handlers")},
	}
	cp := HeuristicFallbackCheckpoint(events, prior, RuntimeState{}, 2048)
	if cp.Objective != "Switch to the billing flow and patch the invoice bug." {
		t.Fatalf("expected latest user objective to win, got %#v", cp)
	}
}

func TestParseCheckpointMarkdownAcceptsHeadingAliases(t *testing.T) {
	text := `
## Active Objectives
- Finish compaction rewrite

## User Constraints
- Keep tests green

## Decisions
- Use structured checkpoints

## Verified Facts
- kernel/runtime/compaction.go

## Current Status
- editing runtime compaction

## Risks
- need more tests

## Next Steps
1. run make quality
`
	cp := ParseCheckpointMarkdown(text)
	if cp.Objective != "Finish compaction rewrite" {
		t.Fatalf("expected objective alias to parse, got %#v", cp)
	}
	if len(cp.UserConstraints) == 0 || cp.UserConstraints[0] != "Keep tests green" {
		t.Fatalf("expected constraints alias to parse, got %#v", cp)
	}
	if len(cp.NextActions) == 0 || cp.NextActions[0] != "run make quality" {
		t.Fatalf("expected next-steps alias to parse, got %#v", cp)
	}
}

func TestSplitTargetWithOptionsCanCompactLongSingleTurn(t *testing.T) {
	events := []*session.Event{
		{ID: "u1", Message: model.NewTextMessage(model.RoleUser, "finish the refactor")},
	}
	for i := 0; i < 10; i++ {
		events = append(events, &session.Event{
			ID:      "a",
			Message: model.NewTextMessage(model.RoleAssistant, strings.Repeat("assistant-progress ", 80)),
		})
	}
	head, tail := SplitTargetWithOptions(events, SplitOptions{
		SoftTailTokens: 600,
		HardTailTokens: 900,
		MinTailEvents:  2,
	})
	if len(head) == 0 {
		t.Fatalf("expected head to contain summarized events")
	}
	if len(tail) == 0 {
		t.Fatalf("expected tail to preserve a recent suffix")
	}
}
