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
		return
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

func TestSubagentStartUpdatesExistingPanelAgentWithoutReanchoring(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	anchor := NewTranscriptBlock("▸ SPAWN child", tuikit.LineStyleDefault)
	m.doc.Append(anchor)
	m.callAnchorIndex = map[string]string{
		"call-1": anchor.BlockID(),
	}

	_, _ = m.handleSubagentStream(tuievents.SubagentStreamMsg{
		SpawnID: "child-1",
		Stream:  "assistant",
		Chunk:   "hello",
	})
	panelID := m.subagentBlockIDs["child-1"]
	if panelID == "" {
		t.Fatal("expected panel to be created from stream event")
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "child-1",
		AttachTarget: "child-1",
		Agent:        "self",
		CallID:       "call-1",
	})
	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "child-1",
		AttachTarget: "child-1",
		Agent:        "copilot",
		CallID:       "call-1",
	})

	if got := m.subagentBlockIDs["child-1"]; got != panelID {
		t.Fatalf("expected agent correction to reuse existing panel, got %q want %q", got, panelID)
	}
	panel, _ := m.doc.Find(panelID).(*SubagentPanelBlock)
	if panel == nil {
		t.Fatal("expected subagent panel")
		return
	}
	if panel.Agent != "copilot" {
		t.Fatalf("expected authoritative agent update, got %q", panel.Agent)
	}
	if panel.CallID != "call-1" {
		t.Fatalf("expected call anchor to stay attached, got %q", panel.CallID)
	}
	blocks := m.doc.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("expected anchor plus one panel block, got %d blocks", len(blocks))
	}
	if blocks[0].BlockID() != anchor.BlockID() || blocks[1].BlockID() != panelID {
		t.Fatalf("expected panel to remain anchored after the same call block, got %#v", []string{blocks[0].BlockID(), blocks[1].BlockID()})
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
	if strings.Contains(joined, "waiting for user confirmation") || strings.Contains(joined, "approval needed") {
		t.Fatalf("did not expect approval body hint in subagent panel, got %q", joined)
	}
}

func TestSubagentStartClaimsWriteTranscriptAnchor(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	anchor := NewTranscriptBlock("▸ TASK WRITE 你好", tuikit.LineStyleTool)
	m.doc.Append(anchor)

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "call-task-write-1",
		AttachTarget: "child-1",
		Agent:        "copilot",
		CallID:       "call-task-write-1",
		AnchorTool:   "TASK WRITE",
		ClaimAnchor:  true,
	})

	panelID := m.subagentBlockIDs["call-task-write-1"]
	if panelID == "" {
		t.Fatal("expected continuation panel block id")
	}
	if got := strings.TrimSpace(m.callAnchorIndex["call-task-write-1"]); got != anchor.BlockID() {
		t.Fatalf("expected WRITE line to be claimed as call anchor, got %q want %q", got, anchor.BlockID())
	}
	blocks := m.doc.Blocks()
	if len(blocks) != 2 || blocks[0].BlockID() != anchor.BlockID() || blocks[1].BlockID() != panelID {
		t.Fatalf("expected continuation panel directly after WRITE line, got %#v", []string{blocks[0].BlockID(), blocks[1].BlockID()})
	}
}

func TestSubagentStartDoesNotClaimRegularWriteTranscriptAnchor(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	taskAnchor := NewTranscriptBlock("▸ TASK WRITE continue child", tuikit.LineStyleTool)
	fileAnchor := NewTranscriptBlock("▸ WRITE notes.txt", tuikit.LineStyleTool)
	m.doc.Append(taskAnchor)
	m.doc.Append(fileAnchor)

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "call-task-write-2",
		AttachTarget: "child-2",
		Agent:        "copilot",
		CallID:       "call-task-write-2",
		AnchorTool:   "TASK WRITE",
		ClaimAnchor:  true,
	})

	if got := strings.TrimSpace(m.callAnchorIndex["call-task-write-2"]); got != taskAnchor.BlockID() {
		t.Fatalf("expected continuation to anchor on TASK WRITE line, got %q want %q", got, taskAnchor.BlockID())
	}
}

func TestSubagentStartIgnoresPendingSpawnAnchorsForTaskWrite(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	spawnAnchor := NewTranscriptBlock("▸ SPAWN helper", tuikit.LineStyleTool)
	taskAnchor := NewTranscriptBlock("▸ TASK WRITE continue child", tuikit.LineStyleTool)
	m.doc.Append(spawnAnchor)
	m.doc.Append(taskAnchor)
	m.pendingToolAnchors = []toolAnchor{{toolName: "SPAWN", blockID: spawnAnchor.BlockID()}}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "call-task-write-3",
		AttachTarget: "child-3",
		Agent:        "copilot",
		CallID:       "call-task-write-3",
		AnchorTool:   "TASK WRITE",
		ClaimAnchor:  true,
	})

	if got := strings.TrimSpace(m.callAnchorIndex["call-task-write-3"]); got != taskAnchor.BlockID() {
		t.Fatalf("expected continuation to ignore pending SPAWN anchors, got %q want %q", got, taskAnchor.BlockID())
	}
	if got := len(m.pendingToolAnchors); got != 1 {
		t.Fatalf("expected TASK write anchor claim not to consume SPAWN pending anchors, got %d", got)
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
		return
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
		return
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
		return
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID: "child-1", AttachTarget: "child-1", Agent: "self", CallID: "call-2",
	})
	p2ID := m.subagentBlockIDs["child-1"]
	panel2, _ := m.doc.Find(p2ID).(*SubagentPanelBlock)
	if panel2 == nil || p2ID == p1ID {
		t.Fatal("expected distinct second panel")
		return
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

func TestSubagentStartPromotesProvisionalPanelToActualSession(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	anchor := NewTranscriptBlock("▸ SPAWN [gemini]", tuikit.LineStyleTool)
	m.doc.Append(anchor)
	m.pendingToolAnchors = []toolAnchor{
		{blockID: anchor.BlockID(), toolName: "SPAWN"},
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "call-gemini",
		AttachTarget: "call-gemini",
		Agent:        "gemini",
		CallID:       "call-gemini",
		ClaimAnchor:  true,
		Provisional:  true,
	})

	panelID := m.subagentBlockIDs["call-gemini"]
	if panelID == "" {
		t.Fatal("expected provisional panel to be created")
	}
	if got := len(m.pendingToolAnchors); got != 0 {
		t.Fatalf("expected provisional bootstrap to claim anchor, got %d pending", got)
	}

	m.handleSubagentStart(tuievents.SubagentStartMsg{
		SpawnID:      "child-gemini",
		AttachTarget: "child-gemini",
		Agent:        "gemini",
		CallID:       "call-gemini",
		ClaimAnchor:  false,
	})
	m.handleSubagentStream(tuievents.SubagentStreamMsg{
		SpawnID: "child-gemini",
		Stream:  "assistant",
		Chunk:   "hello from gemini",
	})

	if stale := m.subagentBlockIDs["call-gemini"]; stale != "" {
		t.Fatalf("expected provisional session key to be retired, got %q", stale)
	}
	if got := m.subagentBlockIDs["child-gemini"]; got != panelID {
		t.Fatalf("expected actual child session to reuse provisional panel, got %q want %q", got, panelID)
	}
	blocks := m.doc.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("expected anchor plus one promoted panel, got %d blocks", len(blocks))
	}
	if blocks[0].BlockID() != anchor.BlockID() || blocks[1].BlockID() != panelID {
		t.Fatalf("expected promoted panel to stay inline under its SPAWN anchor, got %#v", []string{blocks[0].BlockID(), blocks[1].BlockID()})
	}
	panel, _ := m.doc.Find(panelID).(*SubagentPanelBlock)
	if panel == nil {
		t.Fatal("expected promoted panel to exist")
		return
	}
	if panel.SpawnID != "child-gemini" || panel.AttachID != "child-gemini" {
		t.Fatalf("expected promoted panel to reflect actual child session ids, got %+v", panel)
	}
	if panel.CallID != "call-gemini" {
		t.Fatalf("expected promoted panel to keep original call anchor, got %+v", panel)
	}
	if len(panel.Events) != 1 || !strings.Contains(panel.Events[0].Text, "gemini") {
		t.Fatalf("expected stream output to land in promoted inline panel, got %+v", panel.Events)
	}
}

func TestSubagentStartDefersAnchorClaimUntilAuthoritativeBootstrap(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg { return tuievents.TaskResultMsg{} },
	})
	codexAnchor := NewTranscriptBlock("▸ SPAWN [codex]", tuikit.LineStyleTool)
	geminiAnchor := NewTranscriptBlock("▸ SPAWN [gemini]", tuikit.LineStyleTool)
	selfAnchor := NewTranscriptBlock("▸ SPAWN [self]", tuikit.LineStyleTool)
	m.doc.Append(codexAnchor)
	m.doc.Append(geminiAnchor)
	m.doc.Append(selfAnchor)
	m.pendingToolAnchors = []toolAnchor{
		{blockID: codexAnchor.BlockID(), toolName: "SPAWN"},
		{blockID: geminiAnchor.BlockID(), toolName: "SPAWN"},
		{blockID: selfAnchor.BlockID(), toolName: "SPAWN"},
	}

	// Child streams arrive out of order before parent spawn tool responses.
	m.handleSubagentStream(tuievents.SubagentStreamMsg{SpawnID: "child-self", Stream: "assistant", Chunk: "self output"})
	m.handleSubagentStream(tuievents.SubagentStreamMsg{SpawnID: "child-codex", Stream: "assistant", Chunk: "codex output"})
	m.handleSubagentStream(tuievents.SubagentStreamMsg{SpawnID: "child-gemini", Stream: "assistant", Chunk: "gemini output"})

	// Child-event bootstrap should record metadata but must not claim FIFO anchors.
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-self", AttachTarget: "child-self", Agent: "self", CallID: "call-self", ClaimAnchor: false})
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-codex", AttachTarget: "child-codex", Agent: "codex", CallID: "call-codex", ClaimAnchor: false})
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-gemini", AttachTarget: "child-gemini", Agent: "gemini", CallID: "call-gemini", ClaimAnchor: false})

	if got := len(m.pendingToolAnchors); got != 3 {
		t.Fatalf("expected child-event starts to leave SPAWN anchors unclaimed, got %d", got)
	}

	// Parent spawn tool responses arrive in the original call order and claim anchors authoritatively.
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-codex", AttachTarget: "child-codex", Agent: "codex", CallID: "call-codex", ClaimAnchor: true})
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-gemini", AttachTarget: "child-gemini", Agent: "gemini", CallID: "call-gemini", ClaimAnchor: true})
	m.handleSubagentStart(tuievents.SubagentStartMsg{SpawnID: "child-self", AttachTarget: "child-self", Agent: "self", CallID: "call-self", ClaimAnchor: true})

	if got := len(m.pendingToolAnchors); got != 0 {
		t.Fatalf("expected authoritative bootstraps to claim all SPAWN anchors, got %d", got)
	}

	codexPanelID := m.subagentBlockIDs["child-codex"]
	geminiPanelID := m.subagentBlockIDs["child-gemini"]
	selfPanelID := m.subagentBlockIDs["child-self"]
	if codexPanelID == "" || geminiPanelID == "" || selfPanelID == "" {
		t.Fatalf("expected panels for all children, got codex=%q gemini=%q self=%q", codexPanelID, geminiPanelID, selfPanelID)
	}

	blocks := m.doc.Blocks()
	if len(blocks) != 6 {
		t.Fatalf("expected 3 anchors + 3 panels, got %d blocks", len(blocks))
	}
	gotOrder := []string{
		blocks[0].BlockID(),
		blocks[1].BlockID(),
		blocks[2].BlockID(),
		blocks[3].BlockID(),
		blocks[4].BlockID(),
		blocks[5].BlockID(),
	}
	wantOrder := []string{
		codexAnchor.BlockID(), codexPanelID,
		geminiAnchor.BlockID(), geminiPanelID,
		selfAnchor.BlockID(), selfPanelID,
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("unexpected document order: got %#v want %#v", gotOrder, wantOrder)
		}
	}

	codexPanel, _ := m.doc.Find(codexPanelID).(*SubagentPanelBlock)
	geminiPanel, _ := m.doc.Find(geminiPanelID).(*SubagentPanelBlock)
	selfPanel, _ := m.doc.Find(selfPanelID).(*SubagentPanelBlock)
	if codexPanel == nil || geminiPanel == nil || selfPanel == nil {
		t.Fatal("expected all subagent panels to exist")
		return
	}
	if codexPanel.Agent != "codex" || codexPanel.CallID != "call-codex" {
		t.Fatalf("expected codex panel metadata to stay aligned, got %+v", codexPanel)
	}
	if geminiPanel.Agent != "gemini" || geminiPanel.CallID != "call-gemini" {
		t.Fatalf("expected gemini panel metadata to stay aligned, got %+v", geminiPanel)
	}
	if selfPanel.Agent != "self" || selfPanel.CallID != "call-self" {
		t.Fatalf("expected self panel metadata to stay aligned, got %+v", selfPanel)
	}
}
