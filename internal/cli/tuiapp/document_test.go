package tuiapp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

// ---------------------------------------------------------------------------
// Document model tests
// ---------------------------------------------------------------------------

func TestDocumentAppendAndRender(t *testing.T) {
	doc := NewDocument()
	b1 := &TranscriptBlock{id: nextBlockID(), Raw: "hello"}
	b2 := &TranscriptBlock{id: nextBlockID(), Raw: "world"}
	doc.Append(b1)
	doc.Append(b2)

	if got := len(doc.Blocks()); got != 2 {
		t.Fatalf("expected 2 blocks, got %d", got)
	}
	if doc.Blocks()[0].BlockID() != b1.BlockID() || doc.Blocks()[1].BlockID() != b2.BlockID() {
		t.Fatal("block order mismatch")
	}
}

func TestDocumentFindAndRemove(t *testing.T) {
	doc := NewDocument()
	b1 := &TranscriptBlock{id: nextBlockID(), Raw: "first"}
	b2 := &TranscriptBlock{id: nextBlockID(), Raw: "second"}
	b3 := &TranscriptBlock{id: nextBlockID(), Raw: "third"}
	doc.Append(b1)
	doc.Append(b2)
	doc.Append(b3)

	// Find by ID.
	found := doc.Find(b2.BlockID())
	if found == nil {
		t.Fatal("expected to find block b2")
	}
	if found.BlockID() != b2.BlockID() {
		t.Fatal("found wrong block")
	}

	// Remove b2.
	doc.Remove(b2.BlockID())
	if doc.Find(b2.BlockID()) != nil {
		t.Fatal("expected b2 to be removed")
	}
	if len(doc.Blocks()) != 2 {
		t.Fatalf("expected 2 blocks after removal, got %d", len(doc.Blocks()))
	}
	if doc.Blocks()[0].BlockID() != b1.BlockID() || doc.Blocks()[1].BlockID() != b3.BlockID() {
		t.Fatal("block order after removal is wrong")
	}
}

func TestDocumentOrderPreserved(t *testing.T) {
	doc := NewDocument()
	b1 := &TranscriptBlock{id: nextBlockID(), Raw: "first"}
	b2 := &TranscriptBlock{id: nextBlockID(), Raw: "second"}
	b3 := &TranscriptBlock{id: nextBlockID(), Raw: "third"}
	doc.Append(b1)
	doc.Append(b2)
	doc.Append(b3)

	blocks := doc.Blocks()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if blocks[0].BlockID() != b1.BlockID() || blocks[1].BlockID() != b2.BlockID() || blocks[2].BlockID() != b3.BlockID() {
		t.Fatal("block order not preserved")
	}
}

func TestDocumentRenderAll(t *testing.T) {
	doc := NewDocument()
	doc.Append(&TranscriptBlock{id: nextBlockID(), Raw: "hello world"})
	doc.Append(&DividerBlock{id: nextBlockID()})
	doc.Append(&TranscriptBlock{id: nextBlockID(), Raw: "goodbye"})

	rows := doc.RenderAll(BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()})
	if len(rows) < 3 {
		t.Fatalf("expected at least 3 rows, got %d", len(rows))
	}
	// Verify each row has consistent styled/plain.
	for i, r := range rows {
		if ansi.Strip(r.Styled) != r.Plain {
			t.Errorf("row %d: Strip(Styled)=%q != Plain=%q", i, ansi.Strip(r.Styled), r.Plain)
		}
	}
}

func TestWelcomePanelTruncatesLongWorkspaceValue(t *testing.T) {
	vm := buildWelcomePanelViewModel(buildWelcomeViewModel(
		"v0.0.34",
		"~/WorkDir/xueyongzhi/caelis [⎇ codex/tui-beautification-v0.0.34-super-long-branch-name]",
		"minimax/minimax-m2.7-highspeed",
	), 40, tuikit.DefaultTheme())

	if len(vm.Body) < 4 {
		t.Fatalf("expected welcome body rows, got %+v", vm.Body)
	}
	workspaceLine := ansi.Strip(vm.Body[3])
	if !strings.Contains(workspaceLine, "...") {
		t.Fatalf("expected welcome workspace line to truncate, got %q", workspaceLine)
	}
	if got := displayColumns(workspaceLine); got > 36 {
		t.Fatalf("expected welcome workspace line to fit panel body width, got %d cols: %q", got, workspaceLine)
	}
}

func TestWrapRenderedRowsForViewport_PropagatesClickTokenAcrossWrappedLines(t *testing.T) {
	m := newTestModel()
	row := StyledPlainClickableRow(
		"block-1",
		"▾ RUN python3 hello.py --format json --verbose --limit 20",
		"▾ RUN python3 hello.py --format json --verbose --limit 20",
		"acp_tool_panel:tool-run",
	)

	styled, plain, tokens := m.wrapRenderedRowsForViewport(&TranscriptBlock{id: "block-1"}, []RenderedRow{row}, 16)
	if len(styled) < 2 || len(plain) < 2 {
		t.Fatalf("expected wrapped viewport rows, got styled=%d plain=%d", len(styled), len(plain))
	}
	if len(tokens) != len(plain) {
		t.Fatalf("expected click tokens for each wrapped row, got %d tokens for %d lines", len(tokens), len(plain))
	}
	for i, token := range tokens {
		if token != "acp_tool_panel:tool-run" {
			t.Fatalf("expected wrapped row %d to preserve click token, got %q", i, token)
		}
	}
}

func TestBashPanelScrollbarHiddenUntilRecentScroll(t *testing.T) {
	panel := NewBashPanelBlock("BASH", "call-1")
	panel.Lines = []toolOutputLine{
		{text: "line 01", stream: "stdout"},
		{text: "line 02", stream: "stdout"},
		{text: "line 03", stream: "stdout"},
		{text: "line 04", stream: "stdout"},
		{text: "line 05", stream: "stdout"},
		{text: "line 06", stream: "stdout"},
	}
	ctx := BlockRenderContext{Width: 40, TermWidth: 80, Theme: tuikit.DefaultTheme()}

	idleRows := panel.Render(ctx)
	idle := make([]string, 0, len(idleRows))
	for _, row := range idleRows {
		idle = append(idle, row.Styled)
	}
	if joined := strings.Join(idle, "\n"); strings.Contains(joined, "▎") || strings.Contains(joined, "▏") {
		t.Fatalf("did not expect idle bash panel scrollbar, got:\n%s", joined)
	}

	panel.ScrollbarVisibleUntil = time.Now().Add(time.Second)
	activeRows := panel.Render(ctx)
	active := make([]string, 0, len(activeRows))
	for _, row := range activeRows {
		active = append(active, row.Styled)
	}
	if joined := strings.Join(active, "\n"); !strings.Contains(joined, "▎") {
		t.Fatalf("expected visible bash panel scrollbar after scroll, got:\n%s", joined)
	}
}

func TestSubagentPanelScrollbarHiddenUntilRecentScroll(t *testing.T) {
	panel := NewSubagentPanelBlock("spawn-1", "child-1", "helper", "call-1")
	for i := 0; i < 20; i++ {
		panel.AppendStreamChunk(SEAssistant, fmt.Sprintf("line %02d with enough text to wrap inside the subagent panel", i))
	}
	ctx := BlockRenderContext{Width: 50, TermWidth: 80, Theme: tuikit.DefaultTheme()}

	idleRows := panel.Render(ctx)
	idle := make([]string, 0, len(idleRows))
	for _, row := range idleRows {
		idle = append(idle, row.Styled)
	}
	if joined := strings.Join(idle, "\n"); strings.Contains(joined, "▎") || strings.Contains(joined, "▏") {
		t.Fatalf("did not expect idle subagent panel scrollbar, got:\n%s", joined)
	}

	panel.ScrollbarVisibleUntil = time.Now().Add(time.Second)
	activeRows := panel.Render(ctx)
	active := make([]string, 0, len(activeRows))
	for _, row := range activeRows {
		active = append(active, row.Styled)
	}
	if joined := strings.Join(active, "\n"); !strings.Contains(joined, "▎") {
		t.Fatalf("expected visible subagent panel scrollbar after scroll, got:\n%s", joined)
	}
}

func TestBashPanelScrollbarShowsOnHover(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	panel := NewBashPanelBlock("BASH", "call-1")
	for i := 0; i < 20; i++ {
		panel.Lines = append(panel.Lines, toolOutputLine{text: fmt.Sprintf("line %02d", i), stream: "stdout"})
	}
	m.doc.Append(panel)
	m.syncViewportContent()

	contentLine := -1
	for i, blockID := range m.viewportBlockIDs {
		if blockID == panel.BlockID() && strings.Contains(m.viewportPlainLines[i], "line") {
			contentLine = i
			break
		}
	}
	if contentLine < 0 {
		t.Fatal("expected panel block in viewport")
	}
	x := m.mainColumnX() + tuikit.GutterNarrative + maxInt(0, displayColumns(m.viewportPlainLines[contentLine])-2)
	y := contentLine - m.viewport.YOffset()
	_, _ = m.Update(mouseMotion(x, y))
	if view := renderModel(m); !strings.Contains(view, "▎") {
		t.Fatalf("expected bash panel scrollbar visible on hover, got:\n%s", view)
	}
}

func TestMousePointToContentPointRejectsOuterCenteredGutter(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	block := NewAssistantBlock()
	block.Raw = "hello centered transcript"
	m.doc.Append(block)
	m.syncViewportContent()

	contentLine := -1
	for i, line := range m.viewportPlainLines {
		if strings.Contains(line, "hello centered transcript") {
			contentLine = i
			break
		}
	}
	if contentLine < 0 {
		t.Fatal("expected assistant content in viewport")
	}
	y := contentLine - m.viewport.YOffset()
	if _, ok := m.mousePointToContentPoint(m.mainColumnX()-1, y, false); ok {
		t.Fatal("expected left outer gutter click to miss transcript")
	}
	if _, ok := m.mousePointToContentPoint(m.mainColumnX()+m.mainColumnWidth(), y, false); ok {
		t.Fatal("expected right outer gutter click to miss transcript")
	}
	if _, ok := m.mousePointToContentPoint(m.mainColumnX()+tuikit.GutterNarrative, y, false); !ok {
		t.Fatal("expected in-column click to map to transcript")
	}
}

func TestAssistantViewportWrapPreservesCJKHeading(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.viewport.SetWidth(20)
	block := NewAssistantBlock()
	block.Raw = "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手"
	m.doc.Append(block)

	m.syncViewportContent()

	joined := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(joined, "关于我") {
		t.Fatalf("expected wrapped assistant viewport to preserve CJK heading, got:\n%s", joined)
	}
}

// ---------------------------------------------------------------------------
// BASH panel lifecycle tests
// ---------------------------------------------------------------------------

func TestBashPanelCreatedOnToolStream(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})

	bid, ok := m.toolOutputBlockIDs["c1"]
	if !ok {
		t.Fatal("expected tool output block ID for call c1")
	}
	blk := m.doc.Find(bid)
	if blk == nil {
		t.Fatal("expected block in document")
	}
	bp, ok := blk.(*BashPanelBlock)
	if !ok {
		t.Fatalf("expected BashPanelBlock, got %T", blk)
	}
	if bp.State != "running" {
		t.Fatalf("expected running state, got %q", bp.State)
	}
	if !bp.Expanded {
		t.Fatal("expected panel to be expanded by default")
	}
}

func TestBashPanelStateTransitions(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Create running panel.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})

	// Send some output.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "output\n"})

	// Complete the tool.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", State: "completed", Final: true})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	if bp.State != "completed" {
		t.Fatalf("expected completed state, got %q", bp.State)
	}
	if bp.Active {
		t.Fatal("expected panel to be inactive (not receiving updates) after completion")
	}
	if !bp.Expanded {
		t.Fatal("expected completed bash panel to remain visible through the collapse delay")
	}
	driveBashPanelCollapse(m, bp)
	if bp.Expanded {
		t.Fatal("expected completed bash panel to auto-collapse after the animation")
	}
	// Panel should still exist in the document (no fade/removal).
	if m.doc.Find(bid) == nil {
		t.Fatal("expected panel to persist in document after completion (no fade)")
	}
}

func TestBashPanelFinalWithoutTerminalStateDoesNotCollapse(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Final: true})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	if !bp.Expanded {
		t.Fatal("expected panel to stay expanded when Final arrives without terminal state")
	}
	if bp.EndedAt.IsZero() {
		t.Fatal("expected EndedAt to be set when Final is true, even without terminal state")
	}
}

func TestBashPanelElapsedFreezesAtTerminalState(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "c1")
	bp.StartedAt = time.Now().Add(-5 * time.Second)

	before := bp.elapsed()
	if before < 4*time.Second {
		t.Fatalf("expected elapsed to be running before terminal state, got %v", before)
	}

	bp.EndedAt = bp.StartedAt.Add(3 * time.Second)
	frozen1 := bp.elapsed()
	time.Sleep(20 * time.Millisecond)
	frozen2 := bp.elapsed()
	if frozen1 != frozen2 {
		t.Fatalf("expected frozen elapsed after terminal state, got %v then %v", frozen1, frozen2)
	}
}

func TestBashPanelNoOutputShowsPlaceholder(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})

	view := stripModelView(m)
	if !strings.Contains(view, "no output") {
		t.Fatalf("expected 'no output' placeholder, got:\n%s", view)
	}
}

func TestBashPanelExpandCollapse(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "line1\nline2\n"})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)

	// Initially expanded — should show content.
	view := stripModelView(m)
	if !strings.Contains(view, "line1") {
		t.Fatalf("expected expanded panel to show content, got:\n%s", view)
	}

	// Collapse hides the inline BASH panel body; the call line remains the toggle target.
	bp.Expanded = false
	m.syncViewportContent()
	view = stripModelView(m)
	if strings.Contains(view, "line1") {
		t.Fatalf("expected collapsed panel to hide body content, got:\n%s", view)
	}

	// Re-expand.
	bp.Expanded = true
	m.syncViewportContent()
	view = stripModelView(m)
	if !strings.Contains(view, "line1") {
		t.Fatalf("expected re-expanded panel to show content, got:\n%s", view)
	}
}

func TestBashPanelInlineAnchorShowsToolName(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH echo hi\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})

	view := stripModelView(m)
	if !strings.Contains(view, "▾ BASH echo hi") {
		t.Fatalf("expected inline anchor to show expanded bash call, got:\n%s", view)
	}
	if strings.Contains(view, "shell task") {
		t.Fatalf("did not expect inline bash shell header in panel body, got:\n%s", view)
	}
}

func TestBashPanelFailedState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH fail\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stderr", Chunk: "error occurred\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", State: "failed", Final: true})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	if bp.State != "failed" {
		t.Fatalf("expected failed state, got %q", bp.State)
	}
	driveBashPanelCollapse(m, bp)
	view := stripModelView(m)
	if !strings.Contains(view, "BASH fail") || strings.Contains(view, "error occurred") {
		t.Fatalf("expected failed bash panel to collapse back to the call line, got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// SPAWN panel lifecycle tests
// ---------------------------------------------------------------------------

func TestSubagentPanelCreatedOnStart(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{
		SpawnID:      "s1",
		AttachTarget: "session-123",
		Agent:        "self",
		CallID:       "call-spawn",
	})

	bid, ok := m.subagentBlockIDs["s1"]
	if !ok {
		t.Fatal("expected subagent block ID for spawn s1")
	}
	blk := m.doc.Find(bid)
	if blk == nil {
		t.Fatal("expected block in document")
	}
	sp, ok := blk.(*SubagentPanelBlock)
	if !ok {
		t.Fatalf("expected SubagentPanelBlock, got %T", blk)
	}
	if sp.Status != "running" {
		t.Fatalf("expected running status, got %q", sp.Status)
	}
}

func TestSubagentPanelShowsWaitingWhenNoEvents(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})

	view := stripModelView(m)
	if !strings.Contains(view, "waiting for subagent output") {
		t.Fatalf("expected 'waiting for subagent output', got:\n%s", view)
	}
}

func TestSubagentPanelShowsChildAssistant(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "hello from child"})

	view := stripModelView(m)
	if !strings.Contains(view, "hello from child") {
		t.Fatalf("expected child assistant content, got:\n%s", view)
	}
}

func TestSubagentPanelShowsChildReasoning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "reasoning", Chunk: "thinking about this"})

	view := stripModelView(m)
	if !strings.Contains(view, "thinking about this") {
		t.Fatalf("expected child reasoning content, got:\n%s", view)
	}
}

func TestSubagentPanelShowsToolCalls(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentToolCallMsg{
		SpawnID:  "s1",
		ToolName: "BASH",
		CallID:   "child-tool-1",
		Stream:   "stdout",
		Chunk:    "tool output",
	})

	view := stripModelView(m)
	if !strings.Contains(view, "BASH") {
		t.Fatalf("expected child tool call BASH, got:\n%s", view)
	}
}

func TestSubagentPanelKeepsToolResultsInChronologicalOrder(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentToolCallMsg{
		SpawnID:  "s1",
		ToolName: "LIST",
		CallID:   "child-tool-1",
		Args:     ".",
	})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "after tool call"})
	_, _ = m.Update(tuievents.SubagentToolCallMsg{
		SpawnID:  "s1",
		ToolName: "LIST",
		CallID:   "child-tool-1",
		Stream:   "stdout",
		Chunk:    "listed 0 entries in .",
		Final:    true,
	})

	view := stripModelView(m)
	startIdx := strings.Index(view, "LIST")
	assistantIdx := strings.Index(view, "after tool call")
	resultIdx := strings.Index(view, "listed 0 entries in .")
	if startIdx < 0 || assistantIdx < 0 || resultIdx < 0 {
		t.Fatalf("expected start, assistant, and result lines in view, got:\n%s", view)
	}
	if startIdx >= assistantIdx || assistantIdx >= resultIdx {
		t.Fatalf("expected chronological order start < assistant < result, got:\n%s", view)
	}
}

func TestSubagentPanelStatusTransitions(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "working..."})

	// Transition to completed.
	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "completed"})

	bid := m.subagentBlockIDs["s1"]
	sp := m.doc.Find(bid).(*SubagentPanelBlock)
	if sp.Status != "completed" {
		t.Fatalf("expected completed status, got %q", sp.Status)
	}
	rows := sp.Render(BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()})
	joined := ""
	for _, row := range rows {
		joined += ansi.Strip(row.Styled) + "\n"
	}
	if !strings.Contains(joined, "completed") {
		t.Fatalf("expected completed status in panel render, got:\n%s", joined)
	}
}

func TestSubagentPanelShowsFailedState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "failed"})

	sp := m.doc.Find(m.subagentBlockIDs["s1"]).(*SubagentPanelBlock)
	rows := sp.Render(BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()})
	joined := ""
	for _, row := range rows {
		joined += ansi.Strip(row.Styled) + "\n"
	}
	if !strings.Contains(joined, "failed") {
		t.Fatalf("expected failed state in panel render, got:\n%s", joined)
	}
}

func TestSubagentPanelShowsInterrupted(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "self", CallID: "c1"})
	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "interrupted"})

	sp := m.doc.Find(m.subagentBlockIDs["s1"]).(*SubagentPanelBlock)
	rows := sp.Render(BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()})
	joined := ""
	for _, row := range rows {
		joined += ansi.Strip(row.Styled) + "\n"
	}
	if !strings.Contains(joined, "interrupted") {
		t.Fatalf("expected interrupted state in panel render, got:\n%s", joined)
	}
}

// ---------------------------------------------------------------------------
// Multi-panel concurrent positioning tests
// ---------------------------------------------------------------------------

func TestMultipleBashPanelsDontDrift(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Create two concurrent BASH panels.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* running tool A\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "output-A\n"})

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* running tool B\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c2", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c2", Stream: "stdout", Chunk: "output-B\n"})

	view := stripModelView(m)
	// Both panels should be visible.
	if !strings.Contains(view, "output-A") {
		t.Fatalf("expected output-A in view, got:\n%s", view)
	}
	if !strings.Contains(view, "output-B") {
		t.Fatalf("expected output-B in view, got:\n%s", view)
	}

	// Panel A should appear before panel B in the output.
	idxA := strings.Index(view, "output-A")
	idxB := strings.Index(view, "output-B")
	if idxA >= idxB {
		t.Fatalf("expected panel A before panel B; A at %d, B at %d", idxA, idxB)
	}
}

func TestBashAndSubagentPanelsConcurrent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	// Create a BASH panel.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* running bash\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "bash-out\n"})

	// Create a subagent panel.
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", Agent: "helper", CallID: "c2"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "subagent-out"})

	view := stripModelView(m)
	if !strings.Contains(view, "bash-out") {
		t.Fatalf("expected bash output in concurrent view, got:\n%s", view)
	}
	if !strings.Contains(view, "subagent-out") {
		t.Fatalf("expected subagent output in concurrent view, got:\n%s", view)
	}
}

func TestMultiplePanelsUpdateIndependently(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Create two panels.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c2", Reset: true, State: "running"})

	// Stream to c1 only.
	for i := range 5 {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool: "BASH", CallID: "c1", Stream: "stdout",
			Chunk: fmt.Sprintf("c1-line-%d\n", i),
		})
	}

	// Stream to c2 only.
	for i := range 3 {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool: "BASH", CallID: "c2", Stream: "stdout",
			Chunk: fmt.Sprintf("c2-line-%d\n", i),
		})
	}

	// Verify both panels have correct content.
	bid1 := m.toolOutputBlockIDs["c1"]
	bp1 := m.doc.Find(bid1).(*BashPanelBlock)
	bid2 := m.toolOutputBlockIDs["c2"]
	bp2 := m.doc.Find(bid2).(*BashPanelBlock)

	if len(bp1.Lines) != 5 {
		t.Fatalf("expected full c1 history to be retained, got %d lines", len(bp1.Lines))
	}
	if len(bp2.Lines) != 3 {
		t.Fatalf("expected 3 lines in c2 panel, got %d", len(bp2.Lines))
	}
	if bp1.Lines[0].text != "c1-line-0" || bp1.Lines[4].text != "c1-line-4" {
		t.Fatalf("expected c1 panel to keep full line history, got %+v", bp1.Lines)
	}
	if bp2.Lines[0].text != "c2-line-0" || bp2.Lines[2].text != "c2-line-2" {
		t.Fatalf("expected c2 panel history to stay isolated, got %+v", bp2.Lines)
	}
}

// ---------------------------------------------------------------------------
// Panel click-to-toggle tests
// ---------------------------------------------------------------------------

func TestPanelClickToggle(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH echo output\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "output\n"})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	if !bp.Expanded {
		t.Fatal("expected panel to start expanded")
	}

	// Find which viewport line has the tool call anchor.
	headerLine := -1
	for i, id := range m.viewportBlockIDs {
		if tb, ok := m.doc.Find(id).(*TranscriptBlock); ok && strings.Contains(tb.Raw, "BASH echo output") {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatal("could not find bash tool call anchor in viewport")
	}

	// Simulate click + release on header line (no drag = no selection).
	vy := headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if bp.Expanded {
		t.Fatal("expected panel to be collapsed after click")
	}

	// Click again to re-expand.
	m.syncViewportContent()
	headerLine = -1
	for i, id := range m.viewportBlockIDs {
		if tb, ok := m.doc.Find(id).(*TranscriptBlock); ok && strings.Contains(tb.Raw, "BASH echo output") {
			headerLine = i
			break
		}
	}
	vy = headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if !bp.Expanded {
		t.Fatal("expected panel to be re-expanded after second click")
	}
}

func TestExplorationSummaryClickToggle(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	blocks := m.doc.FindByKind(BlockActivity)
	if len(blocks) != 1 {
		t.Fatalf("expected one finalized exploration block, got %d", len(blocks))
	}
	ab, ok := blocks[0].(*ActivityBlock)
	if !ok {
		t.Fatalf("expected activity block, got %T", blocks[0])
	}
	if ab.Expanded {
		t.Fatal("expected finalized exploration summary to start collapsed")
	}

	headerLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == ab.BlockID() {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatal("could not find exploration summary in viewport")
	}

	vy := headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if !ab.Expanded {
		t.Fatal("expected exploration summary to expand after click")
	}
	view := stripModelView(m)
	if !strings.Contains(view, "▾ Explored 1 files, 1 searches") {
		t.Fatalf("expected expanded exploration header, got:\n%s", view)
	}
	if !strings.Contains(view, "Read state.go") || !strings.Contains(view, "Searched for enter") {
		t.Fatalf("expected expanded exploration details, got:\n%s", view)
	}

	m.syncViewportContent()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if ab.Expanded {
		t.Fatal("expected exploration summary to collapse after second click")
	}
	view = stripModelView(m)
	if strings.Contains(view, "Read state.go") || strings.Contains(view, "Searched for enter") {
		t.Fatalf("expected collapsed exploration summary to hide details, got:\n%s", view)
	}
}

func TestTaskWriteSummaryClickToggle(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ TASK WRITE continue the spawned worker with more detail\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	blocks := m.doc.FindByKind(BlockActivity)
	if len(blocks) != 1 {
		t.Fatalf("expected one finalized task-write activity block, got %d", len(blocks))
	}
	ab, ok := blocks[0].(*ActivityBlock)
	if !ok {
		t.Fatalf("expected activity block, got %T", blocks[0])
	}
	if ab.Expanded {
		t.Fatal("expected finalized task-write block to start collapsed")
	}

	headerLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == ab.BlockID() {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatal("could not find task-write summary in viewport")
	}

	vy := headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if !ab.Expanded {
		t.Fatal("expected task-write summary to expand after click")
	}
	view := stripModelView(m)
	if !strings.Contains(view, "SEND continue the spawned worker with more detail") {
		t.Fatalf("expected SEND summary to stay visible, got:\n%s", view)
	}

	if count := strings.Count(view, "SEND continue the spawned worker with more detail"); count < 2 {
		t.Fatalf("expected expanded task-write block to reveal SEND detail row, got:\n%s", view)
	}
}

func TestBashPanelWheelScrollUsesInternalViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH seq 0 7\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	for i := range 8 {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool:   "BASH",
			CallID: "c1",
			Stream: "stdout",
			Chunk:  fmt.Sprintf("line-%d\n", i),
		})
	}

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	view := stripModelView(m)
	if strings.Contains(view, "line-0") || strings.Contains(view, "line-1") || strings.Contains(view, "line-2") || strings.Contains(view, "line-3") {
		t.Fatalf("expected bash panel to cap visible history, got:\n%s", view)
	}
	if !strings.Contains(view, "line-7") {
		t.Fatalf("expected latest bash output visible, got:\n%s", view)
	}

	panelLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == bid {
			panelLine = i
			break
		}
	}
	if panelLine < 0 {
		t.Fatal("expected bash panel block in viewport")
	}
	vy := panelLine - m.viewport.YOffset()
	for range 4 {
		_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelUp))
	}

	view = stripModelView(m)
	if !strings.Contains(view, "line-0") {
		t.Fatalf("expected wheel scrolling to reveal early bash history, got:\n%s", view)
	}
	if bp.FollowTail {
		t.Fatal("expected wheel-up to detach bash panel from tail")
	}

	for range 10 {
		_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelDown))
	}
	view = stripModelView(m)
	if !strings.Contains(view, "line-7") {
		t.Fatalf("expected wheel-down to return bash panel to latest output, got:\n%s", view)
	}
	if !bp.FollowTail {
		t.Fatal("expected wheel-down at bottom to reattach bash panel to tail")
	}
}

func TestInlineDiffBlockTogglesFromToolCallAnchor(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ PATCH build.sh +1 -1\n"})
	_, _ = m.Update(tuievents.DiffBlockMsg{
		Tool:    "PATCH",
		Path:    "build.sh",
		Hunk:    "@@ -1,1 +1,1 @@",
		Old:     "old\n",
		New:     "new\n",
		Preview: "--- old\n+++ new\n-old\n+new\n",
	})

	var (
		anchorLine = -1
		diff       *DiffBlock
	)
	for i, id := range m.viewportBlockIDs {
		if tb, ok := m.doc.Find(id).(*TranscriptBlock); ok && strings.Contains(tb.Raw, "PATCH build.sh +1 -1") {
			anchorLine = i
			break
		}
	}
	if anchorLine < 0 {
		t.Fatal("expected PATCH tool call anchor in viewport")
	}
	for _, block := range m.doc.Blocks() {
		if candidate, ok := block.(*DiffBlock); ok {
			diff = candidate
			break
		}
	}
	if diff == nil {
		t.Fatal("expected inline diff block")
	}
	if !diff.Inline || !diff.Expanded {
		t.Fatalf("expected inline diff block to start expanded, got inline=%v expanded=%v", diff.Inline, diff.Expanded)
	}

	vy := anchorLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))
	if diff.Expanded {
		t.Fatal("expected diff block collapsed after clicking PATCH anchor")
	}

	m.syncViewportContent()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))
	if !diff.Expanded {
		t.Fatal("expected diff block re-expanded after second click")
	}
}

func TestSubagentPanelWheelScrollUsesInternalViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", CallID: "spawn-call", Agent: "self"})
	for i := range 16 {
		_, _ = m.Update(tuievents.SubagentToolCallMsg{
			SpawnID:  "s1",
			CallID:   fmt.Sprintf("tool-%d", i),
			ToolName: "BASH",
			Args:     fmt.Sprintf("step-%d", i),
		})
	}

	bid := m.subagentBlockIDs["s1"]
	view := stripModelView(m)
	if strings.Contains(view, "▸ BASH step-0") || strings.Contains(view, "▸ BASH step-3") {
		t.Fatalf("expected subagent panel to cap visible history, got:\n%s", view)
	}
	if !strings.Contains(view, "step-15") {
		t.Fatalf("expected latest subagent output visible, got:\n%s", view)
	}

	panelLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == bid {
			panelLine = i
			break
		}
	}
	if panelLine < 0 {
		t.Fatal("expected subagent panel block in viewport")
	}
	vy := panelLine - m.viewport.YOffset()
	for range 6 {
		_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelUp))
	}

	view = stripModelView(m)
	if !strings.Contains(view, "step-4") {
		t.Fatalf("expected wheel scrolling to reveal older subagent history, got:\n%s", view)
	}
}

func TestSubagentPanelAutoCollapseCanBeReopenedFromAnchor(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", AttachTarget: "child-1", Agent: "self", CallID: "spawn-call"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "child output"})
	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "completed"})

	bid := m.subagentBlockIDs["s1"]
	panel := m.doc.Find(bid).(*SubagentPanelBlock)
	if !panel.Expanded {
		t.Fatal("expected subagent panel to remain visible through the collapse delay")
	}
	driveSubagentPanelCollapse(m, panel)
	if panel.Expanded {
		t.Fatal("expected subagent panel to collapse after the animation")
	}

	view := stripModelView(m)
	if !strings.Contains(view, "SPAWN demo task") || strings.Contains(view, "child output") {
		t.Fatalf("expected collapsed subagent panel to hide body content, got:\n%s", view)
	}

	headerLine := -1
	for i, id := range m.viewportBlockIDs {
		if tb, ok := m.doc.Find(id).(*TranscriptBlock); ok && strings.Contains(tb.Raw, "SPAWN demo task") {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatal("could not find spawn tool call anchor in viewport")
	}
	vy := headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if !panel.Expanded {
		t.Fatal("expected collapsed subagent panel to reopen from the anchor")
	}
	if !panel.PinnedOpenByUser {
		t.Fatal("expected reopened terminal subagent panel to stay pinned open")
	}
	if !panel.CollapseAt.IsZero() {
		t.Fatal("expected reopened terminal subagent panel to cancel auto-collapse")
	}
	_, _ = m.Update(tickAt(frameTickPanelAnimation, time.Now().Add(inlinePanelCollapseDuration)))
	if !panel.Expanded {
		t.Fatal("expected reopened terminal subagent panel to remain expanded without new work")
	}
	view = stripModelView(m)
	if !strings.Contains(view, "child output") {
		t.Fatalf("expected reopened subagent panel to show content, got:\n%s", view)
	}
}

func TestPanelWheelScrollDoesNotMoveMainViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	for i := range 40 {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* intro-%d\n", i)})
	}
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", CallID: "spawn-call", Agent: "self"})
	for i := range 16 {
		_, _ = m.Update(tuievents.SubagentToolCallMsg{
			SpawnID:  "s1",
			CallID:   fmt.Sprintf("tool-%d", i),
			ToolName: "BASH",
			Args:     fmt.Sprintf("step-%d", i),
		})
	}

	before := m.viewport.YOffset()
	bid := m.subagentBlockIDs["s1"]
	panelLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == bid {
			panelLine = i
			break
		}
	}
	if panelLine < 0 {
		t.Fatal("expected subagent panel block in viewport")
	}
	vy := panelLine - m.viewport.YOffset()
	_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelUp))
	if got := m.viewport.YOffset(); got < before-4 {
		t.Fatalf("expected panel scroll to avoid a large main viewport jump from %d, got %d", before, got)
	}
}

func TestPanelWheelAtPanelTopBoundaryFallsBackToMainViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	for i := range 40 {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* intro-%d\n", i)})
	}
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", CallID: "spawn-call", Agent: "self"})
	for i := range 16 {
		_, _ = m.Update(tuievents.SubagentToolCallMsg{
			SpawnID:  "s1",
			CallID:   fmt.Sprintf("tool-%d", i),
			ToolName: "BASH",
			Args:     fmt.Sprintf("step-%d", i),
		})
	}

	bid := m.subagentBlockIDs["s1"]
	panel := m.doc.Find(bid).(*SubagentPanelBlock)
	panelLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == bid {
			panelLine = i
			break
		}
	}
	if panelLine < 0 {
		t.Fatal("expected subagent panel block in viewport")
	}
	panel.ScrollOffset = 0
	panel.FollowTail = false
	offset := maxInt(1, panelLine-2)
	m.viewport.SetYOffset(offset)
	vy := panelLine - m.viewport.YOffset()
	before := m.viewport.YOffset()
	_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelUp))
	if got := m.viewport.YOffset(); got >= before {
		t.Fatalf("expected main viewport offset to decrease once panel hits top boundary, got %d -> %d", before, got)
	}
}

func TestPanelWheelAtPanelBottomBoundaryFallsBackToMainViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	for i := range 24 {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* intro-%d\n", i)})
	}
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", CallID: "spawn-call", Agent: "self"})
	for i := range 16 {
		_, _ = m.Update(tuievents.SubagentToolCallMsg{
			SpawnID:  "s1",
			CallID:   fmt.Sprintf("tool-%d", i),
			ToolName: "BASH",
			Args:     fmt.Sprintf("step-%d", i),
		})
	}
	for i := range 24 {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* outro-%d\n", i)})
	}

	bid := m.subagentBlockIDs["s1"]
	panelLine := -1
	for i, id := range m.viewportBlockIDs {
		if id == bid {
			panelLine = i
			break
		}
	}
	if panelLine < 0 {
		t.Fatal("expected subagent panel block in viewport")
	}
	offset := maxInt(0, panelLine-2)
	maxOffset := maxInt(0, m.viewport.TotalLineCount()-m.viewport.Height())
	if offset >= maxOffset {
		t.Fatalf("expected room for main viewport to scroll down, got offset=%d max=%d", offset, maxOffset)
	}
	m.viewport.SetYOffset(offset)
	vy := panelLine - m.viewport.YOffset()
	before := m.viewport.YOffset()
	_, _ = m.Update(mouseWheel(5, vy, tea.MouseWheelDown))
	if got := m.viewport.YOffset(); got <= before {
		t.Fatalf("expected main viewport offset to increase once panel hits bottom boundary, got %d -> %d", before, got)
	}
}

func TestReopenedSubagentPanelAutoCollapseResetsWhenWorkResumes(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "s1", AttachTarget: "child-1", Agent: "self", CallID: "spawn-call"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "child output"})
	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "completed"})

	panel := m.doc.Find(m.subagentBlockIDs["s1"]).(*SubagentPanelBlock)
	driveSubagentPanelCollapse(m, panel)

	headerLine := -1
	for i, id := range m.viewportBlockIDs {
		if tb, ok := m.doc.Find(id).(*TranscriptBlock); ok && strings.Contains(tb.Raw, "SPAWN demo task") {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatal("could not find spawn tool call anchor in viewport")
	}
	vy := headerLine - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))
	if !panel.PinnedOpenByUser {
		t.Fatal("expected reopened panel to set pinned-open state")
	}

	_, _ = m.Update(tuievents.SubagentStatusMsg{SpawnID: "s1", State: "running"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "s1", Stream: "assistant", Chunk: "more output"})
	if panel.PinnedOpenByUser {
		t.Fatal("expected resumed work to clear pinned-open state")
	}
	if panel.Terminal {
		t.Fatal("expected resumed work to clear terminal state")
	}

	_, _ = m.Update(tuievents.SubagentDoneMsg{SpawnID: "s1", State: "completed"})
	if panel.CollapseAt.IsZero() {
		t.Fatal("expected resumed terminal panel to schedule auto-collapse again")
	}
	driveSubagentPanelCollapse(m, panel)
	if panel.Expanded {
		t.Fatal("expected terminal panel to auto-collapse again after work resumes")
	}
}

// ---------------------------------------------------------------------------
// viewportBlockIDs tracking tests
// ---------------------------------------------------------------------------

func TestViewportBlockIDsTracked(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* hello\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "out\n"})

	if len(m.viewportBlockIDs) == 0 {
		t.Fatal("expected viewportBlockIDs to be populated")
	}
	if len(m.viewportBlockIDs) != len(m.viewportStyledLines) {
		t.Fatalf("viewportBlockIDs length (%d) != viewportStyledLines length (%d)",
			len(m.viewportBlockIDs), len(m.viewportStyledLines))
	}

	// At least some entries should have a non-empty block ID.
	hasNonEmpty := false
	for _, id := range m.viewportBlockIDs {
		if id != "" {
			hasNonEmpty = true
			break
		}
	}
	if !hasNonEmpty {
		t.Fatal("expected at least one non-empty viewportBlockID")
	}
}

// ---------------------------------------------------------------------------
// Composer/Overlay state isolation tests
// ---------------------------------------------------------------------------

func TestComposerEmbeddingFieldAccess(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Verify composer fields accessible via embedding.
	m.historyIndex = -1
	if m.historyIndex != -1 {
		t.Fatal("embedded Composer.historyIndex not accessible")
	}
	m.cursor = 5
	if m.cursor != 5 {
		t.Fatal("embedded Composer.cursor not accessible")
	}
}

func TestOverlayStateEmbeddingFieldAccess(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Verify overlay fields accessible via embedding.
	m.showPalette = true
	if !m.showPalette {
		t.Fatal("embedded OverlayState.showPalette not accessible")
	}
	m.showPalette = false
}

func TestOverlayHasActiveOverlay(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	if m.HasActiveOverlay() {
		t.Fatal("expected no active overlay initially")
	}

	m.showPalette = true
	if !m.HasActiveOverlay() {
		t.Fatal("expected HasActiveOverlay to be true when palette is open")
	}
	m.showPalette = false

	m.slashCandidates = []string{"/help"}
	if !m.HasActiveOverlay() {
		t.Fatal("expected HasActiveOverlay for slash candidates")
	}
	m.slashCandidates = nil
}

// ---------------------------------------------------------------------------
// Selection with grapheme clusters in viewport
// ---------------------------------------------------------------------------

func TestGraphemeSelectionAcrossBashPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* 你好世界\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "测试输出\n"})

	view := stripModelView(m)
	// Verify CJK content is rendered.
	if !strings.Contains(view, "你好世界") {
		t.Fatalf("expected CJK log line in view, got:\n%s", view)
	}
	if !strings.Contains(view, "测试输出") {
		t.Fatalf("expected CJK tool output in view, got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Emoji display in panels
// ---------------------------------------------------------------------------

func TestEmojiInBashPanelRendersCorrectly(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", CallID: "c1", Stream: "stdout",
		Chunk: "🚀 deploying...\n🎉 done!\n",
	})

	view := stripModelView(m)
	if !strings.Contains(view, "🚀") || !strings.Contains(view, "🎉") {
		t.Fatalf("expected emoji in panel output, got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// BTW drawer tests
// ---------------------------------------------------------------------------

func TestBTWDrawerOpensOnMessage(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "hello btw", Final: false})
	if m.btwOverlay == nil {
		t.Fatal("expected btwOverlay to be set")
	}
}

func TestBTWDrawerFinalMessage(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "partial", Final: false})
	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "partial complete", Final: true})
	if m.btwOverlay == nil {
		t.Fatal("expected btwOverlay to be set even after final")
	}
}

func TestBTWDrawerDoesNotModifyDocument(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	blocksBefore := len(m.doc.Blocks())
	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "btw info", Final: true})
	blocksAfter := len(m.doc.Blocks())

	if blocksAfter != blocksBefore {
		t.Fatalf("BTW overlay should not add document blocks; before=%d, after=%d",
			blocksBefore, blocksAfter)
	}
}

// ---------------------------------------------------------------------------
// No fade/closing legacy mechanism
// ---------------------------------------------------------------------------

func TestNoFadeConstantsUsed(t *testing.T) {
	// This test documents that toolOutputFade constants have been removed.
	// If they're re-introduced, this test should be updated.
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true, State: "running"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Stream: "stdout", Chunk: "x\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", State: "completed", Final: true})

	// Add new content after completion — panel should NOT disappear.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* next line\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* another line\n"})

	bid := m.toolOutputBlockIDs["c1"]
	bp := m.doc.Find(bid).(*BashPanelBlock)
	if bp == nil {
		t.Fatal("panel should still exist after new content")
	}

	view := stripModelView(m)
	if !strings.Contains(view, "x") {
		t.Fatalf("expected completed bash output panel to persist (no fade), got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Slash command completion tests
// ---------------------------------------------------------------------------

func TestSlashCompletionOverlayDoesNotModifyDocument(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	blocksBefore := len(m.doc.Blocks())
	typeRunes(m, "/he")
	blocksAfter := len(m.doc.Blocks())

	if blocksAfter != blocksBefore {
		t.Fatalf("slash overlay should not modify document blocks; before=%d, after=%d",
			blocksBefore, blocksAfter)
	}
}

// ---------------------------------------------------------------------------
// BashPanelBlock Render tests (unit level)
// ---------------------------------------------------------------------------

func TestBashPanelBlockRenderExpanded(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "c1")
	bp.Lines = []toolOutputLine{
		{stream: "stdout", text: "hello"},
		{stream: "stdout", text: "world"},
	}
	bp.State = "completed"

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 rows of inline bash output, got %d", len(rows))
	}

	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(combined, "hello") || !strings.Contains(combined, "world") {
		t.Fatalf("expected content in expanded render, got:\n%s", combined)
	}
}

func TestBashPanelBlockRenderCollapsed(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "c1")
	bp.Lines = []toolOutputLine{
		{stream: "stdout", text: "hello"},
	}
	bp.Expanded = false

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)
	if len(rows) != 0 {
		t.Fatalf("expected collapsed inline bash panel to render no rows, got %d", len(rows))
	}
}

func TestBashPanelBlockRenderNoOutput(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "c1")
	bp.State = "completed"

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)

	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(combined, "no output") {
		t.Fatalf("expected 'no output' in rendered panel, got:\n%s", combined)
	}
	if strings.Contains(combined, "BASH · no output") {
		t.Fatalf("expected placeholder on body line, not fused into header, got:\n%s", combined)
	}
}

// ---------------------------------------------------------------------------
// SubagentPanelBlock Render tests (unit level)
// ---------------------------------------------------------------------------

func TestSubagentPanelBlockRenderWaiting(t *testing.T) {
	sp := NewSubagentPanelBlock("s1", "", "self", "c1")

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := sp.Render(ctx)

	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(strings.ReplaceAll(combined, "\n", " "), "waiting for subagent output") {
		t.Fatalf("expected waiting message in empty panel, got:\n%s", combined)
	}
}

func TestSubagentPanelBlockRenderWithContent(t *testing.T) {
	sp := NewSubagentPanelBlock("s1", "attach-id", "helper", "c1")
	sp.AppendStreamChunk(SEAssistant, "I found the answer")
	sp.Status = "completed"

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := sp.Render(ctx)

	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(combined, "I found the answer") {
		t.Fatalf("expected assistant content, got:\n%s", combined)
	}
	if !strings.Contains(combined, "completed") {
		t.Fatalf("expected completed status, got:\n%s", combined)
	}
}

func TestSubagentPanelPrefersRenderWidthOverTerminalWidth(t *testing.T) {
	sp := NewSubagentPanelBlock("s1", "attach-id", "helper", "c1")
	sp.AppendStreamChunk(SEAssistant, "line 01 with enough extra text to force wrapping inside the subagent panel viewport")
	sp.AppendStreamChunk(SEAssistant, "line 02 with enough extra text to force wrapping inside the subagent panel viewport")
	sp.Status = "completed"

	narrow := sp.Render(BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()})
	wideTerm := sp.Render(BlockRenderContext{Width: 80, TermWidth: 140, Theme: tuikit.DefaultTheme()})

	var narrowText strings.Builder
	for _, row := range narrow {
		narrowText.WriteString(ansi.Strip(row.Styled))
		narrowText.WriteByte('\n')
	}
	var wideText strings.Builder
	for _, row := range wideTerm {
		wideText.WriteString(ansi.Strip(row.Styled))
		wideText.WriteByte('\n')
	}
	if narrowText.String() != wideText.String() {
		t.Fatalf("expected subagent panel render width to stay anchored to ctx.Width, got narrow:\n%s\nwide-term:\n%s", narrowText.String(), wideText.String())
	}
}

// ---------------------------------------------------------------------------
// P1: InsertAfter anchor tests
// ---------------------------------------------------------------------------

func TestInsertAfterBasic(t *testing.T) {
	doc := NewDocument()
	b1 := &TranscriptBlock{id: "b1", Raw: "first"}
	b2 := &DividerBlock{id: "b2"} // different kind so same-kind skip doesn't apply
	b3 := &TranscriptBlock{id: "b3", Raw: "third"}
	doc.Append(b1)
	doc.Append(b2)
	doc.Append(b3)

	// Insert a BashPanel after b1 — panel is different kind from DividerBlock, so no skip.
	panel := NewBashPanelBlock("BASH", "c1")
	panel.id = "new"
	idx := doc.InsertAfter("b1", panel)
	if idx != 1 {
		t.Fatalf("expected insert at 1, got %d", idx)
	}
	if doc.Blocks()[1].BlockID() != "new" {
		t.Fatal("inserted block not at position 1")
	}
	if len(doc.Blocks()) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(doc.Blocks()))
	}
}

func TestInsertAfterFallbackAppend(t *testing.T) {
	doc := NewDocument()
	b1 := &TranscriptBlock{id: "b1", Raw: "only"}
	doc.Append(b1)

	// Anchor doesn't exist → falls back to append.
	inserted := &TranscriptBlock{id: "new", Raw: "appended"}
	idx := doc.InsertAfter("nonexistent", inserted)
	if idx != 1 {
		t.Fatalf("expected fallback append at 1, got %d", idx)
	}
	if doc.Blocks()[1].BlockID() != "new" {
		t.Fatal("block should be appended at end")
	}
}

func TestInsertAfterSkipsSameKind(t *testing.T) {
	doc := NewDocument()
	callLine := &TranscriptBlock{id: "call1", Raw: "▸ BASH echo hello"}
	panel1 := NewBashPanelBlock("BASH", "c1")
	panel1.id = "panel1"
	doc.Append(callLine)
	doc.Append(panel1)
	nextLine := &TranscriptBlock{id: "next", Raw: "next line"}
	doc.Append(nextLine)

	// Insert another bash panel after call1 — should skip over panel1 (same kind).
	panel2 := NewBashPanelBlock("BASH", "c2")
	panel2.id = "panel2"
	idx := doc.InsertAfter("call1", panel2)
	// panel2 should land after panel1 but before nextLine.
	if idx != 2 {
		t.Fatalf("expected insert at 2 (after existing panel), got %d", idx)
	}
	ids := blockIDs(doc)
	expected := []string{"call1", "panel1", "panel2", "next"}
	for i, want := range expected {
		if ids[i] != want {
			t.Fatalf("block %d: want %s, got %s; full: %v", i, want, ids[i], ids)
		}
	}
}

func TestBashPanelAnchorAfterCallLine(t *testing.T) {
	doc := NewDocument()

	// Simulate: a log line, then a tool call line, then more log.
	log1 := &TranscriptBlock{id: "log1", Raw: "some log"}
	callLine := &TranscriptBlock{id: "call1", Raw: "▸ BASH echo hello", Style: tuikit.LineStyleTool}
	log2 := &TranscriptBlock{id: "log2", Raw: "more log"}
	doc.Append(log1)
	doc.Append(callLine)
	doc.Append(log2)

	// Create bash panel anchored to call line.
	panel := NewBashPanelBlock("BASH", "c1")
	panel.id = "bash-panel"
	doc.InsertAfter("call1", panel)

	ids := blockIDs(doc)
	expected := []string{"log1", "call1", "bash-panel", "log2"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d blocks, got %d: %v", len(expected), len(ids), ids)
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Fatalf("block %d: want %s, got %s; full: %v", i, want, ids[i], ids)
		}
	}
}

func TestSubagentPanelAnchorAfterCallLine(t *testing.T) {
	doc := NewDocument()

	log1 := &TranscriptBlock{id: "log1", Raw: "some log"}
	callLine := &TranscriptBlock{id: "spawn-call", Raw: "▸ SPAWN agent", Style: tuikit.LineStyleTool}
	log2 := &TranscriptBlock{id: "log2", Raw: "more log"}
	doc.Append(log1)
	doc.Append(callLine)
	doc.Append(log2)

	panel := NewSubagentPanelBlock("s1", "child-1", "self", "c1")
	panel.id = "spawn-panel"
	doc.InsertAfter("spawn-call", panel)

	ids := blockIDs(doc)
	expected := []string{"log1", "spawn-call", "spawn-panel", "log2"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d blocks, got %d: %v", len(expected), len(ids), ids)
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Fatalf("block %d: want %s, got %s; full: %v", i, want, ids[i], ids)
		}
	}
}

func TestMultiPanelConcurrentAnchor(t *testing.T) {
	doc := NewDocument()

	// Two tool calls with content between them.
	call1 := &TranscriptBlock{id: "call-bash", Raw: "▸ BASH echo a", Style: tuikit.LineStyleTool}
	mid := &TranscriptBlock{id: "mid", Raw: "between"}
	call2 := &TranscriptBlock{id: "call-spawn", Raw: "▸ SPAWN agent", Style: tuikit.LineStyleTool}
	tail := &TranscriptBlock{id: "tail", Raw: "after all"}
	doc.Append(call1)
	doc.Append(mid)
	doc.Append(call2)
	doc.Append(tail)

	// Anchor bash panel to call1.
	bashPanel := NewBashPanelBlock("BASH", "c1")
	bashPanel.id = "bp"
	doc.InsertAfter("call-bash", bashPanel)

	// Anchor spawn panel to call2.
	spawnPanel := NewSubagentPanelBlock("s1", "child", "self", "c2")
	spawnPanel.id = "sp"
	doc.InsertAfter("call-spawn", spawnPanel)

	ids := blockIDs(doc)
	expected := []string{"call-bash", "bp", "mid", "call-spawn", "sp", "tail"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d blocks, got %d: %v", len(expected), len(ids), ids)
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Fatalf("block %d: want %s, got %s; full: %v", i, want, ids[i], ids)
		}
	}
}

func TestPanelDoesNotDriftOnAppend(t *testing.T) {
	doc := NewDocument()

	call := &TranscriptBlock{id: "call", Raw: "▸ BASH echo hi", Style: tuikit.LineStyleTool}
	doc.Append(call)

	panel := NewBashPanelBlock("BASH", "c1")
	panel.id = "panel"
	doc.InsertAfter("call", panel)

	// Append more content after — panel position shouldn't change.
	for i := range 5 {
		doc.Append(&TranscriptBlock{id: fmt.Sprintf("extra-%d", i), Raw: fmt.Sprintf("extra %d", i)})
	}

	ids := blockIDs(doc)
	if ids[0] != "call" || ids[1] != "panel" {
		t.Fatalf("panel drifted from position 1: %v", ids)
	}
	if len(ids) != 7 { // call + panel + 5 extras
		t.Fatalf("expected 7 blocks, got %d", len(ids))
	}
}

func TestResolveCallAnchorByCallID(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Simulate two concurrent tool call lines.
	m.pendingToolAnchors = append(m.pendingToolAnchors,
		toolAnchor{blockID: "block-bash-1", toolName: "BASH"},
		toolAnchor{blockID: "block-bash-2", toolName: "BASH"},
	)

	// First resolution: claims oldest BASH anchor.
	got1 := m.resolveCallAnchor("call-1", "BASH")
	if got1 != "block-bash-1" {
		t.Fatalf("expected block-bash-1, got %s", got1)
	}

	// Second resolution: claims next BASH anchor.
	got2 := m.resolveCallAnchor("call-2", "BASH")
	if got2 != "block-bash-2" {
		t.Fatalf("expected block-bash-2, got %s", got2)
	}

	// Repeat lookup by same callID: returns cached result.
	got1Again := m.resolveCallAnchor("call-1", "BASH")
	if got1Again != "block-bash-1" {
		t.Fatalf("expected cached block-bash-1, got %s", got1Again)
	}

	// No more pending anchors.
	if len(m.pendingToolAnchors) != 0 {
		t.Fatalf("expected 0 pending anchors, got %d", len(m.pendingToolAnchors))
	}
}

func TestResolveCallAnchorMixedTools(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// BASH and SPAWN anchors interleaved.
	m.pendingToolAnchors = append(m.pendingToolAnchors,
		toolAnchor{blockID: "block-bash", toolName: "BASH"},
		toolAnchor{blockID: "block-spawn", toolName: "SPAWN"},
	)

	// SPAWN resolution should skip the BASH anchor.
	got := m.resolveCallAnchor("spawn-call-1", "SPAWN")
	if got != "block-spawn" {
		t.Fatalf("expected block-spawn, got %s", got)
	}

	// BASH anchor should still be available.
	got2 := m.resolveCallAnchor("bash-call-1", "BASH")
	if got2 != "block-bash" {
		t.Fatalf("expected block-bash, got %s", got2)
	}
}

func TestExtractToolCallName(t *testing.T) {
	tests := []struct {
		line    string
		name    string
		isStart bool
	}{
		{"▸ BASH echo hello", "BASH", true},
		{"▾ PATCH build.sh +1 -1", "PATCH", true},
		{"▸ read_file {path: /foo}", "READ_FILE", true},
		{"▸ SPAWN delegate", "SPAWN", true},
		{"✓ BASH completed", "", false}, // result, not start
		{"? Approval: run", "", false},  // approval prompt, not start
		{"normal log line", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		name, ok := extractToolCallName(tt.line)
		if ok != tt.isStart {
			t.Errorf("extractToolCallName(%q): isStart=%v, want %v", tt.line, ok, tt.isStart)
		}
		if name != tt.name {
			t.Errorf("extractToolCallName(%q): name=%q, want %q", tt.line, name, tt.name)
		}
	}
}

// ---------------------------------------------------------------------------
// P1: Subagent panel child event viewer
// ---------------------------------------------------------------------------

func TestSubagentPanelRendersAllEvents(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "analyzer", "c1")
	panel.Status = "running"

	// Build chronological event stream: plan → reasoning → assistant → tools
	panel.UpdatePlan([]planEntryState{
		{Content: "Read file", Status: "done"},
		{Content: "Write tests", Status: "in_progress"},
		{Content: "Review output", Status: "pending"},
	})
	panel.AppendStreamChunk(SEReasoning, "I need to understand the file structure before writing tests.")
	panel.AppendStreamChunk(SEAssistant, "Let me read the file first.")
	panel.UpdateToolCall("tc1", "ReadFile", "", "stdout", "line1\nline2\nline3", true)
	panel.UpdateToolCall("tc2", "BASH", "", "stdout", "running...", false)

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	// Plan entries
	if !strings.Contains(combined, "Read file") {
		t.Errorf("missing done plan entry in:\n%s", combined)
	}
	if !strings.Contains(combined, "Write tests") {
		t.Errorf("missing in_progress plan entry in:\n%s", combined)
	}
	if !strings.Contains(combined, "Review output") {
		t.Errorf("missing pending plan entry in:\n%s", combined)
	}
	// Superseded reasoning should collapse out of the rendered panel.
	if strings.Contains(combined, "I need to understand the file structure") {
		t.Errorf("did not expect stale reasoning text to remain visible in:\n%s", combined)
	}
	// Assistant
	if !strings.Contains(combined, "* Let me read the file first") {
		t.Errorf("missing assistant text in:\n%s", combined)
	}
	// Tool calls
	if !strings.Contains(combined, "ReadFile") {
		t.Errorf("missing tool name ReadFile in:\n%s", combined)
	}
	if !strings.Contains(combined, "BASH") {
		t.Errorf("missing tool name BASH in:\n%s", combined)
	}
	// Tool output
	if !strings.Contains(combined, "line1") || !strings.Contains(combined, "line3") {
		t.Errorf("missing tool output lines in:\n%s", combined)
	}
}

func TestSubagentPanelDoesNotRenderACPSectionLabels(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "analyzer", "c1")
	panel.Status = "running"
	panel.UpdatePlan([]planEntryState{{Content: "Read file", Status: "done"}})
	panel.AppendStreamChunk(SEAssistant, "I will inspect the file first.")
	panel.UpdateToolCall("tc1", "READ", "/tmp/demo", "stdout", "found target file", true)

	ctx := BlockRenderContext{TermWidth: 80, Width: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	for _, label := range []string{"plan", "response", "activity"} {
		if strings.Contains(combined, label+" ") || strings.Contains(combined, label+"-") {
			t.Fatalf("did not expect ACP section label %q in:\n%s", label, combined)
		}
	}
}

func TestSubagentPanelPreservesChronologicalTranscriptOrderWithoutSectionLabels(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "analyzer", "c1")
	panel.Status = "completed"
	panel.AppendStreamChunk(SEAssistant, "I will inspect the file first.")
	panel.UpdateToolCall("tc1", "READ", "/tmp/demo", "stdout", "found target file", true)
	panel.AppendStreamChunk(SEAssistant, "The read completed; here is the summary.")

	ctx := BlockRenderContext{TermWidth: 80, Width: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	firstResponse := strings.Index(combined, "I will inspect the file first.")
	toolCall := strings.Index(combined, "READ /tmp/demo")
	toolResult := strings.Index(combined, "found target file")
	finalResponse := strings.Index(combined, "The read completed; here is the summary.")
	if firstResponse < 0 || toolCall < 0 || toolResult < 0 || finalResponse < 0 {
		t.Fatalf("missing chronological transcript content:\n%s", combined)
	}
	if firstResponse >= toolCall || toolCall >= toolResult || toolResult >= finalResponse {
		t.Fatalf("expected assistant -> tool -> assistant order, got:\n%s", combined)
	}
}

func TestSubagentPanelDoesNotRenderSectionDividerForSingleSection(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "analyzer", "c1")
	panel.Status = "running"
	panel.AppendStreamChunk(SEAssistant, "Only one response section should stay clean.")

	ctx := BlockRenderContext{TermWidth: 80, Width: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if strings.Contains(combined, "response ─") || strings.Contains(combined, "response -") {
		t.Fatalf("did not expect response divider for single-section transcript:\n%s", combined)
	}
}

func TestSubagentPanelChronologicalOrder(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "self", "c1")
	panel.Status = "running"

	// Simulate interleaved events: reasoning → tool → more reasoning → assistant
	panel.AppendStreamChunk(SEReasoning, "Thinking about step 1.")
	panel.UpdateToolCall("tc1", "ReadFile", "", "stdout", "content", true)
	panel.AppendStreamChunk(SEReasoning, "Now considering step 2.")
	panel.AppendStreamChunk(SEAssistant, "Here is my answer.")

	// Verify event order is preserved: reasoning, tool, reasoning, assistant
	if len(panel.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(panel.Events))
	}
	if panel.Events[0].Kind != SEReasoning {
		t.Errorf("event 0 should be reasoning, got %d", panel.Events[0].Kind)
	}
	if panel.Events[1].Kind != SEToolCall {
		t.Errorf("event 1 should be tool_call, got %d", panel.Events[1].Kind)
	}
	if panel.Events[2].Kind != SEReasoning {
		t.Errorf("event 2 should be reasoning, got %d", panel.Events[2].Kind)
	}
	if panel.Events[3].Kind != SEAssistant {
		t.Errorf("event 3 should be assistant, got %d", panel.Events[3].Kind)
	}

	// Verify rendered output hides superseded reasoning while preserving the
	// remaining visible event order.
	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	readFileIdx := strings.Index(combined, "ReadFile")
	answerIdx := strings.Index(combined, "Here is my answer")
	if readFileIdx < 0 || answerIdx < 0 {
		t.Fatalf("missing content in:\n%s", combined)
	}
	if strings.Contains(combined, "step 1") || strings.Contains(combined, "step 2") {
		t.Errorf("did not expect superseded reasoning to remain visible:\n%s", combined)
	}
	if readFileIdx >= answerIdx {
		t.Errorf("visible events not in chronological order: readFile=%d answer=%d\n%s",
			readFileIdx, answerIdx, combined)
	}
}

func TestSubagentPanelStreamChunkCoalescing(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")

	// Consecutive chunks of the same kind should coalesce.
	panel.AppendStreamChunk(SEAssistant, "Hello ")
	panel.AppendStreamChunk(SEAssistant, "world")
	if len(panel.Events) != 1 {
		t.Fatalf("expected 1 event (coalesced), got %d", len(panel.Events))
	}
	if panel.Events[0].Text != "Hello world" {
		t.Errorf("expected coalesced text 'Hello world', got %q", panel.Events[0].Text)
	}

	// A different kind breaks the coalescing.
	panel.AppendStreamChunk(SEReasoning, "hmm")
	if len(panel.Events) != 2 {
		t.Fatalf("expected 2 events after different kind, got %d", len(panel.Events))
	}

	// Back to assistant creates a new event (not coalesced with the first).
	panel.AppendStreamChunk(SEAssistant, "continued")
	if len(panel.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(panel.Events))
	}
}

func TestSubagentPanelStreamChunkCoalescingMergesCumulativeChunks(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")

	panel.AppendStreamChunk(SEAssistant, "我建议默认删 2 个 log 文件给你。")
	panel.AppendStreamChunk(SEAssistant, "我建议默认删 2 个 log 文件给你。\n如果想进一步身瘦，再额外删 node_modules。")

	if len(panel.Events) != 1 {
		t.Fatalf("expected 1 assistant event, got %d", len(panel.Events))
	}
	want := "我建议默认删 2 个 log 文件给你。\n如果想进一步身瘦，再额外删 node_modules。"
	if panel.Events[0].Text != want {
		t.Fatalf("expected cumulative chunks to collapse to the latest full text,\n got: %q\nwant: %q", panel.Events[0].Text, want)
	}
}

func TestSubagentPanelStreamChunkCoalescingDeduplicatesOverlapInDeltaChunks(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")

	panel.AppendStreamChunk(SEAssistant, "如果想进一步身瘦，再额外删")
	panel.AppendStreamChunk(SEAssistant, "进一步身瘦，再额外删 node_modules。")

	if len(panel.Events) != 1 {
		t.Fatalf("expected 1 assistant event, got %d", len(panel.Events))
	}
	want := "如果想进一步身瘦，再额外删 node_modules。"
	if panel.Events[0].Text != want {
		t.Fatalf("expected overlapping delta chunks to merge without duplicating the shared suffix,\n got: %q\nwant: %q", panel.Events[0].Text, want)
	}
}

func TestSubagentPanelApprovalWithToolContext(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.Status = "running"

	// Simulate: tool call starts, then approval needed
	panel.UpdateToolCall("tc1", "BASH", "rm -rf /tmp/test", "stdout", "", false)
	panel.AddApprovalEvent("", "") // should derive context from unfinished tool

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "▸ BASH rm -rf /tmp/test") && !strings.Contains(combined, "BASH rm -rf /tmp/test") {
		t.Errorf("expected unfinished tool call to remain visible in:\n%s", combined)
	}
	if strings.Contains(combined, "approval needed") || strings.Contains(combined, "waiting for user confirmation") {
		t.Errorf("did not expect approval body line in:\n%s", combined)
	}
}

func TestSubagentPanelNoEvents(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child-1", "self", "c1")
	panel.Status = "running"

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(strings.ReplaceAll(combined, "\n", " "), "waiting for subagent output") {
		t.Errorf("expected 'waiting for subagent output' placeholder, got:\n%s", combined)
	}
}

func TestSubagentPanelTerminalStates(t *testing.T) {
	tests := []struct {
		status string
		label  string
	}{
		{"completed", "completed"},
		{"failed", "failed"},
		{"interrupted", "interrupted"},
		{"timed_out", "timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			panel := NewSubagentPanelBlock("s1", "c1", "self", "c1")
			panel.Status = tt.status
			ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
			rows := panel.Render(ctx)
			combined := ""
			for _, r := range rows {
				combined += ansi.Strip(r.Styled) + "\n"
			}
			if !strings.Contains(combined, tt.label) {
				t.Errorf("expected %q in %s output, got:\n%s", tt.label, tt.status, combined)
			}
		})
	}
}

func TestSubagentPanelApprovalState(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.Status = "waiting_approval"
	panel.AddApprovalEvent("", "") // no tool context → generic message

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "waiting approval") {
		t.Errorf("expected waiting approval state in:\n%s", combined)
	}
	if strings.Contains(combined, "waiting for user confirmation") {
		t.Errorf("did not expect approval body line in:\n%s", combined)
	}
}

func TestSubagentPanelApprovalTemporarilyHidesPlan(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdatePlan([]planEntryState{
		{Content: "review pending approval", Status: "in_progress"},
	})

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(combined, "review pending approval") {
		t.Fatalf("expected plan content before approval, got:\n%s", combined)
	}

	panel.Status = "waiting_approval"
	panel.AddApprovalEvent("BASH", "rm -rf /tmp/demo")
	rows = panel.Render(ctx)
	combined = ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if strings.Contains(combined, "review pending approval") {
		t.Fatalf("did not expect plan content while approval is active, got:\n%s", combined)
	}
	if !strings.Contains(combined, "waiting approval") {
		t.Fatalf("expected approval status while approval is active, got:\n%s", combined)
	}

	panel.Status = "running"
	rows = panel.Render(ctx)
	combined = ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}
	if !strings.Contains(combined, "review pending approval") {
		t.Fatalf("expected plan content to return after approval clears, got:\n%s", combined)
	}
}

func TestSubagentPanelAssistantMarkdownDoesNotInjectRolePrefix(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.AppendStreamChunk(SEAssistant, "能做什么：\n\n- 文件操作\n- 搜索与分析\n- 自然语言交互")

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if strings.Contains(combined, "* - 文件操作") || strings.Contains(combined, "* 文件操作") {
		t.Fatalf("did not expect injected assistant marker before markdown list:\n%s", combined)
	}
	// glamour converts "- " list markers to "• " bullets
	if !strings.Contains(combined, "文件操作") {
		t.Fatalf("expected markdown list item to remain visible:\n%s", combined)
	}
}

func TestSubagentPanelStreamingClosedFenceHidesDelimiters(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.AppendStreamChunk(SEAssistant, "```python\ndef hello():\n    return 1\n```")

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if strings.Contains(combined, "```python") || strings.Contains(combined, "\n```") {
		t.Fatalf("expected active subagent code fence delimiters to be hidden, got:\n%s", combined)
	}
	if !strings.Contains(combined, "def hello():") {
		t.Fatalf("expected code block body to remain visible, got:\n%s", combined)
	}
}

func TestSubagentPanelFiltersEmptyToolCompletionRows(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "VIEWING", "/tmp/demo", "stdout", "", false)
	panel.UpdateToolCall("tc1", "VIEWING", "/tmp/demo", "stdout", "completed", true)

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "▸ VIEWING /tmp/demo") {
		t.Fatalf("expected tool start line to remain visible:\n%s", combined)
	}
	if strings.Contains(combined, "✓ VIEWING completed") {
		t.Fatalf("did not expect empty completion line to remain visible:\n%s", combined)
	}
}

func TestSubagentPanelGroupsToolLifecycleWithoutRepeatingToolName(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "READ", "/tmp/demo", "stdout", "", false)
	panel.UpdateToolCall("tc1", "READ", "/tmp/demo", "stdout", "found target file", true)
	if len(panel.Events) != 2 {
		t.Fatalf("expected tool lifecycle to preserve chronological start/result events, got %#v", panel.Events)
	}

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "▸ READ /tmp/demo") {
		t.Fatalf("expected tool start row in:\n%s", combined)
	}
	if !strings.Contains(combined, "✓ found target file") {
		t.Fatalf("expected compact tool result detail in:\n%s", combined)
	}
	if strings.Contains(combined, "✓ READ found target file") {
		t.Fatalf("did not expect repeated tool name in final result row:\n%s", combined)
	}
}

func TestSubagentPanelShowsStreamingToolPreviewUnderCall(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "BASH", "tail -f /tmp/demo.log", "stdout", "heartbeat 1/6", false)

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "▸ BASH tail -f /tmp/demo.log") {
		t.Fatalf("expected tool start row in:\n%s", combined)
	}
	if !strings.Contains(combined, "· heartbeat 1/6") {
		t.Fatalf("expected streaming preview detail under tool call in:\n%s", combined)
	}
}

func TestSubagentPanelMergesCumulativeStreamingToolPreview(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "BASH", "python3 script.py", "stdout", "先看各 HTML 文件的前 200 行", false)
	panel.UpdateToolCall("tc1", "BASH", "python3 script.py", "stdout", "先看各 HTML 文件的前 200 行，然后基于这些信息生成 SUMMARY.md。", false)

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if strings.Count(combined, "先看各 HTML 文件的前 200 行") != 1 {
		t.Fatalf("expected cumulative tool preview to deduplicate prior prefix, got:\n%s", combined)
	}
	if !strings.Contains(combined, "然后基于这些信息生成 SUMMARY.md。") {
		t.Fatalf("expected merged cumulative tool preview suffix, got:\n%s", combined)
	}
}

func TestNormalizeSubagentChunkBoundary_StripsContinuationReplacementRunePrefix(t *testing.T) {
	got := mergeSubagentStreamChunk("准备写入", "�� docs/SUMMARY.md")
	if got != "准备写入 docs/SUMMARY.md" {
		t.Fatalf("expected replacement-rune continuation prefix stripped, got %q", got)
	}
}

func TestSubagentPanelTruncatesLongToolArgsInline(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "BASH", "python3 -c \"print('alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau')\"", "stdout", "", false)

	ctx := BlockRenderContext{Width: 52, TermWidth: 52, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "▸ BASH") {
		t.Fatalf("expected tool start row in:\n%s", combined)
	}
	if !strings.Contains(combined, "...") {
		t.Fatalf("expected truncated inline args in:\n%s", combined)
	}
}

func TestSubagentPanelTruncatesLongToolDetailPreview(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.UpdateToolCall("tc1", "READ", "/tmp/demo", "stdout", strings.Join([]string{
		"line 1 long preview content",
		"line 2 long preview content",
		"line 3 long preview content",
		"line 4 long preview content",
		"line 5 long preview content",
		"line 6 long preview content",
	}, "\n"), true)

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "✓ line 1 long preview content") {
		t.Fatalf("expected first detail line in:\n%s", combined)
	}
	if !strings.Contains(combined, "… 3 more lines") {
		t.Fatalf("expected truncation hint in:\n%s", combined)
	}
	if strings.Contains(combined, "line 6 long preview content") {
		t.Fatalf("did not expect full tool detail body to remain visible:\n%s", combined)
	}
}

func TestSubagentPanelRetainsUnsupersededReasoningOnFailure(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.AppendStreamChunk(SEReasoning, "Checking the repo state before patching.")
	panel.Status = "failed"

	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "Checking the repo state before patching.") {
		t.Fatalf("expected unsuperseded reasoning to remain visible:\n%s", combined)
	}
	if !strings.Contains(combined, "failed") {
		t.Fatalf("expected failed status in panel:\n%s", combined)
	}
}

func TestParticipantTurnToolRowsReuseMainToolStyling(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := renderParticipantTurnToolRows("p1", SubagentEvent{
		Kind: SEToolCall,
		Name: "FINDING",
		Args: "/Users/x/demo",
	}, 80, ctx)

	if len(rows) == 0 {
		t.Fatal("expected tool rows")
	}
	got := rows[0].Styled
	want := tuikit.ColorizeLogLine("▸ FINDING /Users/x/demo", tuikit.LineStyleTool, ctx.Theme)
	if got != want {
		t.Fatalf("expected tool call styling to match main transcript\n got: %q\nwant: %q", got, want)
	}
}

func TestSubagentPanelToolWithError(t *testing.T) {
	panel := NewSubagentPanelBlock("s1", "child", "self", "c1")
	panel.Status = "running"
	panel.UpdateToolCall("tc1", "BASH", "", "stderr", "error: command not found", true)

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	combined := ""
	for _, r := range rows {
		combined += ansi.Strip(r.Styled) + "\n"
	}

	if !strings.Contains(combined, "✗ BASH") {
		t.Errorf("expected error icon ✗ in:\n%s", combined)
	}
	if !strings.Contains(combined, "error: command not found") {
		t.Errorf("expected error output in:\n%s", combined)
	}
}

// ---------------------------------------------------------------------------
// P1: RenderedRow plain/styled consistency
// ---------------------------------------------------------------------------

func TestRenderedRowPlainMatchesStripped(t *testing.T) {
	// All block types should produce RenderedRows where
	// ansi.Strip(Styled) == Plain
	blocks := []Block{
		&TranscriptBlock{id: "t1", Raw: "hello world"},
		&TranscriptBlock{id: "t2", Raw: "▸ BASH echo test", Style: tuikit.LineStyleTool},
		&DividerBlock{id: "d1"},
	}

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	for _, b := range blocks {
		rows := b.Render(ctx)
		for i, r := range rows {
			stripped := ansi.Strip(r.Styled)
			if stripped != r.Plain {
				t.Errorf("block %s row %d: Strip(Styled)=%q != Plain=%q",
					b.BlockID(), i, stripped, r.Plain)
			}
		}
	}
}

func TestBashPanelRenderedRowConsistency(t *testing.T) {
	panel := NewBashPanelBlock("BASH", "c1")
	panel.State = "running"
	panel.Expanded = true
	panel.Lines = []toolOutputLine{
		{text: "hello from bash", stream: "stdout"},
		{text: "error line", stream: "stderr"},
	}

	ctx := BlockRenderContext{TermWidth: 80, Theme: tuikit.DefaultTheme()}
	rows := panel.Render(ctx)
	for i, r := range rows {
		stripped := ansi.Strip(r.Styled)
		if stripped != r.Plain {
			t.Errorf("bash panel row %d: Strip(Styled)=%q != Plain=%q", i, stripped, r.Plain)
		}
	}
}

// ---------------------------------------------------------------------------
// P2: Grapheme wrap unification test
// ---------------------------------------------------------------------------

func TestGraphemeWrapCJKAndEmoji(t *testing.T) {
	// Emoji family (ZWJ sequence) takes 2 display columns.
	emoji := "👨‍👩‍👧‍👦"
	w := graphemeWidth(emoji)
	if w != 2 {
		t.Logf("ZWJ family emoji width=%d (may vary by platform)", w)
	}

	// CJK characters: each takes 2 columns.
	cjk := "你好世界"
	lines := graphemeHardWrap(cjk, 4)
	if len(lines) != 2 {
		t.Errorf("expected 2 wrapped lines from 4-col CJK in width=4, got %d: %v", len(lines), lines)
	}
	if lines[0] != "你好" || lines[1] != "世界" {
		t.Errorf("CJK wrap incorrect: %v", lines)
	}
}

func TestHardWrapDisplayLineUsesGrapheme(t *testing.T) {
	// Verify hardWrapDisplayLine doesn't split a CJK character.
	line := "你好世界测试"
	wrapped := hardWrapDisplayLine(line, 6)
	parts := strings.Split(wrapped, "\n")
	if len(parts) != 2 {
		t.Errorf("expected 2 lines from 12-col CJK in width=6, got %d: %v", len(parts), parts)
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func blockIDs(doc *Document) []string {
	var ids []string
	for _, b := range doc.Blocks() {
		ids = append(ids, b.BlockID())
	}
	return ids
}

// ---------------------------------------------------------------------------
// P1: resetConversationView clears anchor state
// ---------------------------------------------------------------------------

func TestResetClearsAnchorState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Simulate anchor state from a previous session.
	m.pendingToolAnchors = append(m.pendingToolAnchors, toolAnchor{
		blockID:  "old-block",
		toolName: "BASH",
	})
	m.callAnchorIndex = map[string]string{"old-call": "old-block"}
	m.toolOutputBlockIDs = map[string]string{"k": "v"}
	m.subagentBlockIDs = map[string]string{"s": "v"}
	m.subagentSessions = map[string]*SubagentSessionState{"s": NewSubagentSessionState("s", "child", "self")}
	m.subagentSessionRefs = map[string][]string{"s": {"v"}}

	m.resetConversationView()

	if len(m.pendingToolAnchors) != 0 {
		t.Fatalf("expected pendingToolAnchors cleared, got %d", len(m.pendingToolAnchors))
	}
	if len(m.callAnchorIndex) != 0 {
		t.Fatalf("expected callAnchorIndex cleared, got %v", m.callAnchorIndex)
	}
	if m.toolOutputBlockIDs != nil {
		t.Fatal("expected toolOutputBlockIDs nil")
	}
	if m.subagentBlockIDs != nil {
		t.Fatal("expected subagentBlockIDs nil")
	}
	if m.subagentSessions != nil {
		t.Fatal("expected subagentSessions nil")
	}
	if m.subagentSessionRefs != nil {
		t.Fatal("expected subagentSessionRefs nil")
	}
}

// ---------------------------------------------------------------------------
// P1: SEPlan append-only with coalescing
// ---------------------------------------------------------------------------

func TestSubagentPlanAppendsChronologically(t *testing.T) {
	b := &SubagentPanelBlock{id: "sp1", SpawnID: "spawn-1", Status: "running"}

	// First plan event.
	b.UpdatePlan([]planEntryState{{Content: "step 1", Status: "pending"}})
	if len(b.Events) != 1 || b.Events[0].Kind != SEPlan {
		t.Fatalf("expected 1 plan event, got %d events", len(b.Events))
	}

	// Consecutive plan update (no intervening event) → coalesce.
	b.UpdatePlan([]planEntryState{{Content: "step 1", Status: "done"}, {Content: "step 2", Status: "pending"}})
	if len(b.Events) != 1 {
		t.Fatalf("expected coalesced to 1 event, got %d", len(b.Events))
	}
	if len(b.Events[0].PlanEntries) != 2 {
		t.Fatalf("expected 2 entries in coalesced plan, got %d", len(b.Events[0].PlanEntries))
	}

	// Interleave with a tool call.
	b.Events = append(b.Events, SubagentEvent{Kind: SEToolCall, Name: "BASH", CallID: "c1"})

	// New plan after tool call → should be a NEW event (not coalesced with first plan).
	b.UpdatePlan([]planEntryState{{Content: "step 1", Status: "done"}, {Content: "step 2", Status: "done"}, {Content: "step 3", Status: "pending"}})
	if len(b.Events) != 3 {
		t.Fatalf("expected 3 events (plan, tool, plan), got %d", len(b.Events))
	}
	if b.Events[0].Kind != SEPlan || b.Events[1].Kind != SEToolCall || b.Events[2].Kind != SEPlan {
		t.Fatalf("wrong event order: %v, %v, %v", b.Events[0].Kind, b.Events[1].Kind, b.Events[2].Kind)
	}
	// First plan should still have the coalesced 2 entries.
	if len(b.Events[0].PlanEntries) != 2 {
		t.Fatalf("first plan should have 2 entries, got %d", len(b.Events[0].PlanEntries))
	}
	// Third plan should have 3 entries.
	if len(b.Events[2].PlanEntries) != 3 {
		t.Fatalf("third plan should have 3 entries, got %d", len(b.Events[2].PlanEntries))
	}
}

// ---------------------------------------------------------------------------
// P1: Approval event rendered with tool context
// ---------------------------------------------------------------------------

func TestSubagentApprovalRendersToolContext(t *testing.T) {
	b := &SubagentPanelBlock{id: "sp1", SpawnID: "spawn-1", Status: "running"}

	// Add a tool call event first.
	b.Events = append(b.Events, SubagentEvent{
		Kind:   SEToolCall,
		Name:   "BASH",
		CallID: "call-1",
		Args:   "rm -rf /tmp/foo",
	})

	// Add approval with explicit tool context (from spawn projector).
	b.AddApprovalEvent("BASH", "rm -rf /tmp/foo")

	if len(b.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(b.Events))
	}
	if b.Events[1].Kind != SEApproval {
		t.Fatalf("expected approval event, got %v", b.Events[1].Kind)
	}
	if b.Events[1].ApprovalTool != "BASH" || b.Events[1].ApprovalCommand != "rm -rf /tmp/foo" {
		t.Fatalf("approval context mismatch: tool=%q command=%q", b.Events[1].ApprovalTool, b.Events[1].ApprovalCommand)
	}

	// Check rendering contains both tool name and command.
	ctx := BlockRenderContext{Width: 80, TermWidth: 80}
	lines := renderSubagentPanelLines(b, ctx)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "BASH") {
		t.Fatalf("expected unfinished tool call to mention BASH, got:\n%s", joined)
	}
	if !strings.Contains(joined, "rm -rf /tmp/foo") {
		t.Fatalf("expected unfinished tool call to mention command, got:\n%s", joined)
	}
	if strings.Contains(joined, "approval needed") || strings.Contains(joined, "waiting for user confirmation") {
		t.Fatalf("did not expect approval body line, got:\n%s", joined)
	}
}

func TestSubagentPanelHeightStableWhileScrolling(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	b := &SubagentPanelBlock{
		id:         "sp1",
		SpawnID:    "spawn-1",
		Status:     "running",
		Expanded:   true,
		FollowTail: true,
	}
	for i := range 24 {
		b.Events = append(b.Events, SubagentEvent{
			Kind: SEAssistant,
			Text: fmt.Sprintf("line %02d with enough extra text to force wrapping inside the subagent panel viewport", i),
		})
	}

	before := renderSubagentPanelLines(b, ctx)
	if len(before) != subagentOutputPreviewLines+2 {
		t.Fatalf("expected capped subagent panel height, got %d lines", len(before))
	}

	if !b.Scroll(-5, ctx) {
		t.Fatal("expected subagent panel scroll to change position")
	}
	after := renderSubagentPanelLines(b, ctx)
	if len(after) != len(before) {
		t.Fatalf("expected stable subagent panel height while scrolling, got %d then %d", len(before), len(after))
	}
}

func TestBashPanelHeightStableWhileScrolling(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	b := NewBashPanelBlock("BASH", "call-1")
	b.Expanded = true
	for i := range 18 {
		b.Lines = append(b.Lines, toolOutputLine{
			text:   fmt.Sprintf("stdout line %02d with enough extra text to overflow the bash panel width and keep wrapping stable", i),
			stream: "stdout",
		})
	}

	before := b.renderPanelLines(ctx, b.currentLines())
	if len(before) != toolOutputPreviewLines+2 {
		t.Fatalf("expected capped bash panel height, got %d lines", len(before))
	}

	if !b.Scroll(-2, ctx) {
		t.Fatal("expected bash panel scroll to change position")
	}
	after := b.renderPanelLines(ctx, b.currentLines())
	if len(after) != len(before) {
		t.Fatalf("expected stable bash panel height while scrolling, got %d then %d", len(before), len(after))
	}
}

// ---------------------------------------------------------------------------
// P2: renderedStyledLines replaces historyLines
// ---------------------------------------------------------------------------

func TestRenderedStyledLinesFromDocument(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Commit some lines via log chunk.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* hello\n* world\n"})

	lines := m.renderedStyledLines()
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	// Lines should contain the committed text.
	joined := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "world") {
		t.Fatalf("expected hello and world in rendered lines, got:\n%s", joined)
	}
}

// ---------------------------------------------------------------------------
// SEApproval append-only with coalescing
// ---------------------------------------------------------------------------

func TestSubagentApprovalAppendsChronologically(t *testing.T) {
	b := &SubagentPanelBlock{id: "sp1", SpawnID: "spawn-1", Status: "running"}

	// First tool call + approval.
	b.Events = append(b.Events, SubagentEvent{Kind: SEToolCall, Name: "BASH", CallID: "c1", Args: "ls"})
	b.AddApprovalEvent("BASH", "ls")
	if len(b.Events) != 2 {
		t.Fatalf("expected 2 events after first approval, got %d", len(b.Events))
	}

	// Consecutive approval (no intervening event) → coalesce.
	b.AddApprovalEvent("BASH", "ls -la")
	if len(b.Events) != 2 {
		t.Fatalf("expected coalesced to 2 events, got %d", len(b.Events))
	}
	if b.Events[1].ApprovalCommand != "ls -la" {
		t.Fatalf("expected coalesced command, got %q", b.Events[1].ApprovalCommand)
	}

	// Tool resolves, then a new tool call + new approval.
	b.Events = append(b.Events, SubagentEvent{Kind: SEToolCall, Name: "WRITE", CallID: "c2", Args: "file.txt"})
	b.AddApprovalEvent("WRITE", "file.txt")

	// Should be 4 events: tool, approval, tool, approval
	if len(b.Events) != 4 {
		t.Fatalf("expected 4 events (tool, approval, tool, approval), got %d", len(b.Events))
	}
	if b.Events[1].Kind != SEApproval || b.Events[3].Kind != SEApproval {
		t.Fatal("expected approval events at positions 1 and 3")
	}
	if b.Events[1].ApprovalTool != "BASH" || b.Events[3].ApprovalTool != "WRITE" {
		t.Fatalf("approval tools mismatch: %q %q", b.Events[1].ApprovalTool, b.Events[3].ApprovalTool)
	}
}

// ---------------------------------------------------------------------------
// Anchor filter: only panel-producing tools tracked
// ---------------------------------------------------------------------------

func TestPendingAnchorsOnlyTrackPanelTools(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Commit tool call lines for various tools.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH echo hello\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ file.txt\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN agent-1\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WRITE output.txt\n"})

	// Only BASH and SPAWN should be in pending anchors (not READ, WRITE).
	if len(m.pendingToolAnchors) != 2 {
		t.Fatalf("expected 2 pending anchors (BASH, SPAWN), got %d", len(m.pendingToolAnchors))
	}
	if m.pendingToolAnchors[0].toolName != "BASH" {
		t.Fatalf("expected first anchor BASH, got %q", m.pendingToolAnchors[0].toolName)
	}
	if m.pendingToolAnchors[1].toolName != "SPAWN" {
		t.Fatalf("expected second anchor SPAWN, got %q", m.pendingToolAnchors[1].toolName)
	}
}

// ---------------------------------------------------------------------------
// BASH Panel Identity & Lifecycle Tests
// ---------------------------------------------------------------------------

func TestTaskIDMapsToOriginalCallID(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Initial BASH creates panel keyed by callID.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	if _, ok := m.toolOutputBlockIDs["call-1"]; !ok {
		t.Fatal("expected panel keyed by call-1 after initial BASH")
	}

	// TaskID arrives with CallID — registers mapping, routes to same panel.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1",
		Stream: "stdout", Chunk: "hello\n",
	})
	if m.taskOriginCallID["task-1"] != "call-1" {
		t.Fatalf("expected taskOriginCallID[task-1]=call-1, got %q", m.taskOriginCallID["task-1"])
	}
	// Only one panel should exist.
	if len(m.toolOutputBlockIDs) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(m.toolOutputBlockIDs))
	}
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected to find panel for call-1")
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "hello") {
		t.Fatal("expected panel to contain 'hello' from task stream")
	}
}

func TestTaskWaitResponseIDDoesNotPoisonOriginMapping(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Original BASH call creates the panel.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})

	// Yielded kernel metadata first arrives self-referential.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1", CallID: "task-1", State: "running", Reset: true,
	})
	if got := m.taskOriginCallID["task-1"]; got != "task-1" {
		t.Fatalf("expected provisional self mapping, got %q", got)
	}

	// TASK wait/status response should NOT replace mapping with its own call ID.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1", CallID: "wait-call-1", Stream: "stdout", Chunk: "from-wait\n",
	})
	if got := m.taskOriginCallID["task-1"]; got != "task-1" {
		t.Fatalf("expected wait response ID not to poison mapping, got %q", got)
	}

	// Real watch output with the original BASH call ID should correct the mapping and land in the original panel.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1", CallID: "call-1", Stream: "stdout", Chunk: "from-watch\n",
	})
	if got := m.taskOriginCallID["task-1"]; got != "call-1" {
		t.Fatalf("expected corrected origin mapping, got %q", got)
	}
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected panel for call-1")
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "from-watch") {
		t.Fatal("expected watch output in original panel")
	}
}

func TestWaitDoesNotGetOwnPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Initial BASH.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1", State: "running",
	})

	// WAIT result sends events with TaskID only (no Reset).
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1",
		Stream: "stdout", Chunk: "from-wait\n",
	})

	// Only original panel should exist.
	if len(m.toolOutputBlockIDs) != 1 {
		t.Fatalf("expected 1 panel, got %d (WAIT created its own)", len(m.toolOutputBlockIDs))
	}
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected original panel for call-1")
		return
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "from-wait") {
		t.Fatal("expected original panel to contain 'from-wait'")
	}
}

func TestCancelDoesNotGetOwnPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Initial BASH.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1", State: "running",
	})

	// CANCEL result: final with cancelled state.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1",
		State: "cancelled", Final: true,
	})

	// Only original panel should exist, now in cancelled state.
	if len(m.toolOutputBlockIDs) != 1 {
		t.Fatalf("expected 1 panel, got %d (CANCEL created its own)", len(m.toolOutputBlockIDs))
	}
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected original panel for call-1")
	}
	if bp.State != "cancelled" {
		t.Fatalf("expected state 'cancelled', got %q", bp.State)
	}
	if bp.Active {
		t.Fatal("expected panel to be inactive after cancel")
	}
}

func TestWaitingInputStateShownInPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1",
		State: "waiting_input",
	})

	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected panel for call-1")
		return
	}
	if bp.State != "waiting_input" {
		t.Fatalf("expected state 'waiting_input', got %q", bp.State)
	}

	// Panel body should show "waiting for input" instead of "no output".
	ctx := BlockRenderContext{Width: 60, Theme: m.theme}
	rows := bp.Render(ctx)
	rendered := ""
	for _, r := range rows {
		rendered += r.Plain + "\n"
	}
	if !strings.Contains(rendered, "waiting for input") {
		t.Fatalf("expected 'waiting for input' in panel, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "no output") {
		t.Fatalf("should not show 'no output' when waiting for input, got:\n%s", rendered)
	}
}

func TestTaskWriteUpdatesOriginalPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Initial BASH, yields task.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1", State: "running",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1",
		State: "waiting_input",
	})

	// WRITE sends new output back via task stream (no Reset).
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1",
		Stream: "stdout", Chunk: "write-response\n",
	})

	// Should update original panel, not create a new one.
	if len(m.toolOutputBlockIDs) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(m.toolOutputBlockIDs))
	}
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected panel for call-1")
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "write-response") {
		t.Fatal("expected original panel to contain 'write-response' from WRITE")
	}
}

func TestBashPanelHidesElapsedHeader(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "call-1")
	bp.State = "running"
	bp.StartedAt = time.Now().Add(-5 * time.Second)

	ctx := BlockRenderContext{Width: 60, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("expected rendered rows")
	}
	// Inline bash panels no longer render header tool name or elapsed time inside the box.
	allPlain := ""
	for _, r := range rows {
		allPlain += r.Plain + "\n"
	}
	if strings.Contains(allPlain, "BASH") || strings.Contains(allPlain, "5s") {
		t.Fatalf("did not expect inline bash panel header content, got:\n%s", allPlain)
	}
	if !strings.Contains(allPlain, "no output") {
		t.Fatalf("expected placeholder on body line, got:\n%s", allPlain)
	}
}

func TestResetClearsTaskOriginCallID(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "c1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "t1", CallID: "c1", State: "running",
	})
	if len(m.taskOriginCallID) == 0 {
		t.Fatal("expected taskOriginCallID to be populated")
	}

	m.resetConversationView()

	if len(m.taskOriginCallID) != 0 {
		t.Fatalf("expected taskOriginCallID to be cleared after reset, got %d entries", len(m.taskOriginCallID))
	}
}

func TestFirstMappingWins(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	// First mapping: task-1 → call-1
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool: "BASH", TaskID: "task-1", CallID: "call-1",
		Stream: "stdout", Chunk: "hello\n",
	})

	// Later message with same TaskID but different CallID (e.g., from WAIT tool).
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-1", CallID: "call-wait",
		Stream: "stdout", Chunk: "from-wait\n",
	})

	// First mapping should be preserved.
	if m.taskOriginCallID["task-1"] != "call-1" {
		t.Fatalf("expected first mapping to win, got %q", m.taskOriginCallID["task-1"])
	}
	// Output should route to original panel.
	bp := m.findBashPanelBlock("call-1")
	if bp == nil {
		t.Fatal("expected panel for call-1")
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "from-wait") {
		t.Fatal("expected from-wait content in original panel")
	}
}

// bashPanelTexts returns all line texts from a BashPanelBlock.
func bashPanelTexts(bp *BashPanelBlock) []string {
	var texts []string
	for _, l := range bp.Lines {
		texts = append(texts, l.text)
	}
	if bp.StdoutPartial != "" {
		texts = append(texts, bp.StdoutPartial)
	}
	return texts
}

func TestSelfReferentialMappingOverwrittenByRealCallID(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Step 1: initial BASH creates panel keyed by call-abc.
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-abc", Reset: true})

	// Step 2: kernel yield event — CallID == TaskID (self-referential).
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-123", CallID: "task-123",
		State: "running", Reset: true,
	})
	// Self-referential mapping registered.
	if m.taskOriginCallID["task-123"] != "task-123" {
		t.Fatalf("expected self-referential mapping, got %q", m.taskOriginCallID["task-123"])
	}

	// Step 3: bash_watch arrives with real CallID.
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Label: "BASH", TaskID: "task-123", CallID: "call-abc",
		Stream: "stdout", Chunk: "real output\n",
	})

	// Mapping should be corrected.
	if m.taskOriginCallID["task-123"] != "call-abc" {
		t.Fatalf("expected corrected mapping to call-abc, got %q", m.taskOriginCallID["task-123"])
	}

	// Output should route to the original panel.
	bp := m.findBashPanelBlock("call-abc")
	if bp == nil {
		t.Fatal("expected panel for call-abc")
	}
	if !strings.Contains(strings.Join(bashPanelTexts(bp), "\n"), "real output") {
		t.Fatal("expected 'real output' in original panel")
	}
}

func TestCompactLayoutNoOutputOnHeaderLine(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "call-1")
	bp.State = "running"

	ctx := BlockRenderContext{Width: 80, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)

	allPlain := ""
	for _, r := range rows {
		allPlain += r.Plain + "\n"
	}
	if !strings.Contains(allPlain, "no output") {
		t.Fatalf("expected 'no output' in panel, got:\n%s", allPlain)
	}
	if len(rows) < 1 {
		t.Fatalf("expected placeholder rows, got:\n%s", allPlain)
	}
	if strings.Contains(allPlain, "BASH") || strings.Contains(allPlain, "<1s") {
		t.Fatalf("did not expect inline bash header metadata in panel, got:\n%s", allPlain)
	}
	if !strings.Contains(allPlain, "no output") {
		t.Fatalf("expected placeholder inside box body, got:\n%s", allPlain)
	}
}

func TestCompactLayoutWaitingInputOnHeaderLine(t *testing.T) {
	bp := NewBashPanelBlock("BASH", "call-1")
	bp.State = "waiting_input"

	ctx := BlockRenderContext{Width: 60, Theme: tuikit.DefaultTheme()}
	rows := bp.Render(ctx)

	allPlain := ""
	for _, r := range rows {
		allPlain += r.Plain + "\n"
	}
	if !strings.Contains(allPlain, "waiting for input") {
		t.Fatalf("expected 'waiting for input' in panel, got:\n%s", allPlain)
	}
	if strings.Contains(allPlain, "no output") {
		t.Fatalf("should not show 'no output' when waiting_input, got:\n%s", allPlain)
	}
}
