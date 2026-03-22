package tuiapp

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

func TestSubagentStartBackfillsExistingPanelMetadata(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})

	_, _ = m.handleSubagentStream(tuievents.SubagentStreamMsg{
		SpawnID: "spawn-1",
		Stream:  "assistant",
		Chunk:   "hello",
	})
	blockID := m.subagentBlockIDs["spawn-1"]
	if blockID == "" {
		t.Fatal("expected subagent panel to be created by stream event")
	}
	panel, _ := m.doc.Find(blockID).(*SubagentPanelBlock)
	if panel == nil {
		t.Fatal("expected subagent panel block in document")
	}
	if panel.Agent != "" || panel.CallID != "" {
		t.Fatalf("expected initial panel metadata to be empty, got agent=%q callID=%q", panel.Agent, panel.CallID)
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "spawn-1",
		AttachTarget: "child-1",
		Agent:        "codex-acp",
		CallID:       "call-1",
	})

	panel, _ = m.doc.Find(blockID).(*SubagentPanelBlock)
	if panel.AttachID != "child-1" {
		t.Fatalf("expected attach target to be backfilled, got %q", panel.AttachID)
	}
	if panel.Agent != "codex-acp" {
		t.Fatalf("expected agent metadata to be backfilled, got %q", panel.Agent)
	}
	if panel.CallID != "call-1" {
		t.Fatalf("expected callID metadata to be backfilled, got %q", panel.CallID)
	}
}

func TestSubagentPanelCoexistsWithAssistantBlock(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "main answer",
		Final: false,
	})
	if m.activeAssistantID == "" {
		t.Fatal("expected assistant block")
	}

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-1", AttachTarget: "child-1", Agent: "self", CallID: "call-1"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "spawn-1", Stream: "assistant", Chunk: "child output"})

	// Both assistant and subagent content should be present.
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "main answer") {
		t.Fatalf("expected main assistant output preserved, got %q", joined)
	}
	if !strings.Contains(joined, "child output") {
		t.Fatalf("expected subagent panel rendered, got %q", joined)
	}
}

func TestSubagentPanelOmitsAttachHint(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-1", AttachTarget: "child-1", Agent: "self", CallID: "call-1"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "/attach child-1") {
		t.Fatalf("did not expect attach hint in subagent panel, got %q", joined)
	}
}

func TestSubagentPanelShowsApprovalState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-1", AttachTarget: "child-1", Agent: "self", CallID: "call-1"})
	_, _ = m.Update(tuievents.SubagentStatusMsg{SpawnID: "spawn-1", State: "waiting_approval"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "waiting approval") {
		t.Fatalf("expected waiting approval state in subagent panel, got %q", joined)
	}
	if !strings.Contains(joined, "waiting for user confirmation") {
		t.Fatalf("expected approval hint in subagent panel, got %q", joined)
	}
}
