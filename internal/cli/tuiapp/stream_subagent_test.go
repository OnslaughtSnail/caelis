package tuiapp

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
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

func TestSubagentPanelClearsWaitingApprovalWhenWorkResumes(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-1", AttachTarget: "child-1", Agent: "self", CallID: "call-1"})
	_, _ = m.Update(tuievents.SubagentStatusMsg{SpawnID: "spawn-1", State: "waiting_approval"})
	_, _ = m.Update(tuievents.SubagentToolCallMsg{SpawnID: "spawn-1", ToolName: "LIST", CallID: "tool-1", Args: "."})

	panel, _ := m.doc.Find(m.subagentBlockIDs["spawn-1"]).(*SubagentPanelBlock)
	if panel == nil {
		t.Fatal("expected panel")
	}
	if panel.Status != "running" {
		t.Fatalf("expected resumed panel state running, got %q", panel.Status)
	}
}

func TestSubagentPanelAddsLatestReferenceForSameSession(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	first := NewTranscriptBlock("▸ SPAWN first", tuikit.LineStyleDefault)
	second := NewTranscriptBlock("▸ SPAWN second", tuikit.LineStyleDefault)
	m.doc.Append(first)
	m.doc.Append(second)
	m.callAnchorIndex = map[string]string{
		"call-1": first.BlockID(),
		"call-2": second.BlockID(),
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "child-1",
		AttachTarget: "child-1",
		Agent:        "self",
		CallID:       "call-1",
	})
	panelID := m.subagentBlockIDs["child-1"]
	if panelID == "" {
		t.Fatal("expected subagent panel block id for child session")
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "child-1",
		AttachTarget: "child-1",
		Agent:        "self",
		CallID:       "call-2",
	})
	m.handleSubagentStream(tuievents.SubagentStreamMsg{
		SpawnID: "child-1",
		Stream:  "assistant",
		Chunk:   "shared output",
	})

	latestID := m.subagentBlockIDs["child-1"]
	if latestID == "" || latestID == panelID {
		t.Fatalf("expected latest reference block for child session, got %q want new block", latestID)
	}
	if got := len(m.subagentSessionRefs["child-1"]); got != 2 {
		t.Fatalf("expected two panel refs for same child session, got %d", got)
	}
	blocks := m.doc.Blocks()
	if len(blocks) != 4 {
		t.Fatalf("expected 4 blocks after adding second ref, got %d", len(blocks))
	}
	if blocks[1].BlockID() != panelID || blocks[3].BlockID() != latestID {
		t.Fatalf("expected both panel refs preserved in document order, got block order %#v", []string{blocks[0].BlockID(), blocks[1].BlockID(), blocks[2].BlockID(), blocks[3].BlockID()})
	}
	firstPanel, _ := m.doc.Find(panelID).(*SubagentPanelBlock)
	secondPanel, _ := m.doc.Find(latestID).(*SubagentPanelBlock)
	if firstPanel == nil || secondPanel == nil {
		t.Fatal("expected both panel refs to exist")
	}
	if firstPanel.sessionState() != secondPanel.sessionState() {
		t.Fatal("expected both panel refs to share same session state")
	}
	if len(firstPanel.Events) != 1 || len(secondPanel.Events) != 1 {
		t.Fatalf("expected both refs to receive shared stream state, got first=%d second=%d", len(firstPanel.Events), len(secondPanel.Events))
	}
	if first.Raw != "▾ SPAWN first" {
		t.Fatalf("expected original spawn anchor to remain attached, got %q", first.Raw)
	}
	if second.Raw != "▾ SPAWN second" {
		t.Fatalf("expected latest spawn anchor expanded, got %q", second.Raw)
	}
}

func TestSubagentPanelEventsAreIndependentCopies(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	first := NewTranscriptBlock("▸ SPAWN first", tuikit.LineStyleDefault)
	second := NewTranscriptBlock("▸ SPAWN second", tuikit.LineStyleDefault)
	m.doc.Append(first)
	m.doc.Append(second)
	m.callAnchorIndex = map[string]string{
		"call-1": first.BlockID(),
		"call-2": second.BlockID(),
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID: "child-1", AttachTarget: "child-1", Agent: "self", CallID: "call-1",
	})
	p1ID := m.subagentBlockIDs["child-1"]
	panel1, _ := m.doc.Find(p1ID).(*SubagentPanelBlock)
	if panel1 == nil {
		t.Fatal("expected first panel")
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID: "child-1", AttachTarget: "child-1", Agent: "self", CallID: "call-2",
	})
	p2ID := m.subagentBlockIDs["child-1"]
	panel2, _ := m.doc.Find(p2ID).(*SubagentPanelBlock)
	if panel2 == nil || p2ID == p1ID {
		t.Fatal("expected distinct second panel")
	}

	// Stream into the shared session.
	m.handleSubagentStream(tuievents.SubagentStreamMsg{
		SpawnID: "child-1", Stream: "assistant", Chunk: "hello",
	})
	if len(panel1.Events) != 1 || len(panel2.Events) != 1 {
		t.Fatalf("both panels should reflect session stream: p1=%d p2=%d", len(panel1.Events), len(panel2.Events))
	}

	// Verify panels have independent backing arrays (not aliased to session).
	// Appending to panel1.Events should not corrupt panel2.Events.
	panel1.Events = append(panel1.Events, SubagentEvent{Kind: SEToolCall, Name: "rogue"})
	if len(panel2.Events) != 1 {
		t.Fatalf("panel2 should be unaffected by direct mutation of panel1.Events, got %d", len(panel2.Events))
	}
}
