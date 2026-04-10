package tuiapp

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

func TestChooseRevealClusterCount_AvoidsSingleWideClusterReveal(t *testing.T) {
	clusters := splitGraphemeClusters("介绍一下你自己")
	got := chooseRevealClusterCount(clusters, 1, 5)
	if got < 3 {
		t.Fatalf("expected at least three clusters for stable wide-char reveal, got %d", got)
	}
}

func TestChooseRevealClusterCount_AvoidsEmojiOnlyBoundary(t *testing.T) {
	clusters := splitGraphemeClusters("🛠️ 工具与执行")
	got := chooseRevealClusterCount(clusters, 1, 5)
	if got < 4 {
		t.Fatalf("expected emoji batch to include following text, got %d clusters", got)
	}
}

func TestChooseRevealClusterCount_AvoidsListEmojiPrefix(t *testing.T) {
	clusters := splitGraphemeClusters("- 💻 文件操作：读取、写入、编辑文件和目录")
	got := chooseRevealClusterCount(clusters, 2, 5)
	if got < 5 {
		t.Fatalf("expected list prefix reveal to include text beyond the emoji, got %d clusters", got)
	}
}

func TestChooseRevealClusterCount_StaysWithinCurrentLine(t *testing.T) {
	clusters := splitGraphemeClusters("- 💻 文件操作\n- 🔎 代码搜索")
	got := chooseRevealClusterCount(clusters, 5, 8)
	limit := firstLogicalLineClusterLimit(clusters, 8)
	if got > limit {
		t.Fatalf("expected reveal to stay within current line limit %d, got %d", limit, got)
	}
}

func TestChooseRevealClusterCount_AllowsTinyTail(t *testing.T) {
	clusters := splitGraphemeClusters("🙂")
	got := chooseRevealClusterCount(clusters, 1, 4)
	if got != 1 {
		t.Fatalf("expected lone cluster tail to reveal immediately, got %d", got)
	}
}

func TestChooseRevealClusterCount_PrefersStableNaturalBoundary(t *testing.T) {
	clusters := splitGraphemeClusters("Hello world")
	got := chooseRevealClusterCount(clusters, 3, 5)
	if got != 5 {
		t.Fatalf("expected stable natural boundary at word edge, got %d", got)
	}
}

func TestParticipantTurnBlock_GroupsActorOutputUnderSingleHeader(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "luna(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "luna(gemini)",
		Stream:  "reasoning",
		Text:    "Examining the file",
		Final:   true,
	})
	_, _ = m.Update(tuievents.ParticipantToolMsg{
		SessionID: "child-1",
		CallID:    "call-1",
		ToolName:  "SHELL",
		Args:      "rm shanghai_weather.md",
	})
	_, _ = m.Update(tuievents.ParticipantToolMsg{
		SessionID: "child-1",
		CallID:    "call-1",
		ToolName:  "SHELL",
		Output:    "completed",
		Final:     true,
	})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "luna(gemini)",
		Stream:  "answer",
		Text:    "Done.",
	})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "luna(gemini)",
		Stream:  "answer",
		Text:    "Done.",
		Final:   true,
	})
	view := strings.Join(m.viewportPlainLines, "\n")
	if count := strings.Count(view, "luna [gemini]"); count != 1 {
		t.Fatalf("expected a single actor header for the turn, got %d occurrences:\n%s", count, view)
	}
	if !strings.Contains(view, "luna [gemini]") || !strings.Contains(view, "▾") {
		t.Fatalf("expected grouped participant turn header, got:\n%s", view)
	}
	if !strings.Contains(view, "SHELL") || !strings.Contains(view, "rm shanghai_weather.md") {
		t.Fatalf("expected grouped tool call line, got:\n%s", view)
	}
	if strings.Contains(view, "Examining the file") {
		t.Fatalf("did not expect superseded reasoning to remain visible, got:\n%s", view)
	}
	if strings.Contains(view, "✓ SHELL completed") {
		t.Fatalf("did not expect empty completion line to remain visible, got:\n%s", view)
	}
	if !strings.Contains(view, "* Done.") {
		t.Fatalf("expected grouped assistant answer, got:\n%s", view)
	}
	if count := strings.Count(view, "* Done."); count != 1 {
		t.Fatalf("expected final assistant text to replace stream preview once, got %d occurrences:\n%s", count, view)
	}
}

func TestParticipantTurnBlock_CanCollapseToHeaderOnly(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "luna(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "luna(gemini)",
		Stream:  "answer",
		Text:    "Done.",
		Final:   true,
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatalf("expected participant turn block for child-1")
	}
	block.Expanded = false
	m.syncViewportContent()

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "luna [gemini]") || !strings.Contains(view, "▸") {
		t.Fatalf("expected collapsed header, got:\n%s", view)
	}
	if strings.Contains(view, "Done.") {
		t.Fatalf("expected collapsed turn body to be hidden, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_RendersCompletionFooter(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "mia(codex)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "mia(codex)",
		Stream:  "answer",
		Text:    "Done.",
		Final:   true,
	})
	_, _ = m.Update(tuievents.ParticipantStatusMsg{SessionID: "child-1", State: "completed"})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "mia [codex]") {
		t.Fatalf("expected actor header, got:\n%s", view)
	}
	if !strings.Contains(view, "─") {
		t.Fatalf("expected completion divider footer, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_ReplayUsesOccurredAtForFooterDuration(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	startedAt := time.Date(2026, 4, 8, 9, 46, 0, 0, time.UTC)
	endedAt := startedAt.Add(463 * time.Millisecond)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{
		SessionID:  "child-1",
		Actor:      "cole(copilot)",
		OccurredAt: startedAt,
	})
	_, _ = m.Update(tuievents.ParticipantStatusMsg{
		SessionID:  "child-1",
		State:      "completed",
		OccurredAt: endedAt,
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "463ms") {
		t.Fatalf("expected replay footer to use persisted duration, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_TaskResultDoesNotAppendMainDivider(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.showTurnDivider = true

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "owen(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "owen(gemini)",
		Stream:  "answer",
		Text:    "Done.",
		Final:   true,
	})
	_, _ = m.Update(tuievents.ParticipantStatusMsg{SessionID: "child-1", State: "completed"})
	_, _ = m.Update(tuievents.TaskResultMsg{SuppressTurnDivider: true})

	view := strings.Join(m.viewportPlainLines, "\n")
	if count := strings.Count(view, "─"); count < 1 {
		t.Fatalf("expected participant footer divider, got:\n%s", view)
	}
	if count := strings.Count(view, "owen [gemini]"); count != 1 {
		t.Fatalf("expected single participant turn, got:\n%s", view)
	}
}

func TestCollapseRepeatedNarrativeText_RemovesAdjacentDuplicateParagraphs(t *testing.T) {
	input := "Formulating Search Terms\n\nI will search both Chinese and English queries.\n\nI will search both Chinese and English queries."
	got := collapseRepeatedNarrativeText(input)
	if strings.Count(got, "I will search both Chinese and English queries.") != 1 {
		t.Fatalf("expected duplicate paragraph to collapse, got:\n%s", got)
	}
}

func TestParticipantTurnBlock_TaskResultFinalizesFooterWithoutStatusEvent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "owen(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "owen(gemini)",
		Stream:  "answer",
		Text:    "Done.",
		Final:   true,
	})
	_, _ = m.Update(tuievents.TaskResultMsg{SuppressTurnDivider: true})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "owen [gemini]") || !strings.Contains(view, "─") {
		t.Fatalf("expected participant turn footer to be finalized by task result, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_DoesNotDuplicateStyledReasoningRowsInViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	text := "The user says hi, so it seems like a simple greeting is enough. I'm not sure if I need tools for this, but I can just greet them."
	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "kate(copilot)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "kate(copilot)",
		Stream:  "reasoning",
		Text:    text,
		Final:   true,
	})

	styled := strings.Join(m.viewportStyledLines, "\n")
	plain := strings.Join(m.viewportPlainLines, "\n")
	normalize := func(s string) string {
		s = strings.ReplaceAll(s, "\n", "")
		s = strings.ReplaceAll(s, " ", "")
		return s
	}
	if !strings.Contains(normalize(plain), normalize("simple greeting is enough")) {
		t.Fatalf("expected plain viewport to contain reasoning, got:\n%s", plain)
	}
	if strings.Count(normalize(ansi.Strip(styled)), normalize("simple greeting is enough")) != 1 {
		t.Fatalf("expected styled viewport to contain reasoning once, got:\n%s", ansi.Strip(styled))
	}
}

func TestParticipantACPProjection_TracksInProgressToolUpdateWithoutSeparateToolCall(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "cole(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "cole(copilot)",
		ToolCallID: "tool-1",
		ToolName:   "READ",
		ToolArgs:   map[string]any{"path": "/tmp/demo.txt"},
		ToolStatus: "in_progress",
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatalf("expected participant block for child-1")
	}
	if len(block.Events) == 0 || block.Events[0].Kind != SEToolCall {
		t.Fatalf("expected in-progress tool event to be recorded, got %#v", block.Events)
	}

	m.syncViewportContent()
	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "READ") || !strings.Contains(view, "/tmp/demo.txt") {
		t.Fatalf("expected in-progress tool update to create participant tool row, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_FinalToolResultAttachesToOriginalCallOrder(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "evan(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-view",
		ToolName:   "VIEWING",
		ToolArgs:   map[string]any{"_display": "/tmp/a.txt"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-list",
		ToolName:   "LIST",
		ToolArgs:   map[string]any{"_display": "ls -la"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-view",
		ToolName:   "VIEWING",
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "first result"},
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-list",
		ToolName:   "LIST",
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "second result"},
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatalf("expected participant block for child-1")
	}
	if got := len(block.Events); got != 2 {
		t.Fatalf("expected final tool updates to reuse original events, got %d events: %#v", got, block.Events)
	}
	if !block.Events[0].Done || !block.Events[1].Done {
		t.Fatalf("expected both tool events completed in place, got %#v", block.Events)
	}

	m.syncViewportContent()
	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Index(view, "VIEWING") > strings.Index(view, "first result") {
		t.Fatalf("expected viewing result detail after original call row, got:\n%s", view)
	}
	if strings.Index(view, "LIST") > strings.Index(view, "second result") {
		t.Fatalf("expected list result detail after original call row, got:\n%s", view)
	}
	if strings.Index(view, "first result") > strings.Index(view, "LIST") {
		t.Fatalf("expected first tool result to stay attached before second tool row, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_ACPToolResultRendersInlinePanelUnderCall(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "evan(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-run",
		ToolName:   "RUN",
		ToolArgs:   map[string]any{"_display": "python3 hello.py"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-run",
		ToolName:   "RUN",
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "Hello, world!"},
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "RUN") || !strings.Contains(view, "python3 hello.py") {
		t.Fatalf("expected RUN call line in viewport, got:\n%s", view)
	}
	if !strings.Contains(view, "Hello, world!") {
		t.Fatalf("expected tool output in viewport, got:\n%s", view)
	}
	if strings.Contains(view, "✓ Hello, world!") {
		t.Fatalf("expected ACP tool output to render inside a panel, not as a standalone result row, got:\n%s", view)
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("expected ACP tool output to render inside a drawer panel, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_ACPToolPanelClickToggleMatchesBash(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.viewport.SetWidth(24)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "evan(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-run",
		ToolName:   "RUN",
		ToolArgs:   map[string]any{"_display": "python3 hello.py --format json --verbose --limit 20"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-run",
		ToolName:   "RUN",
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "line-1\nline-2"},
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatal("expected participant turn block")
	}

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "▾ RUN ") {
		t.Fatalf("expected expanded ACP tool call arrow, got:\n%s", view)
	}
	if !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected ACP tool output before collapse, got:\n%s", view)
	}

	token := "acp_tool_panel:tool-run"
	clickableLines := make([]int, 0, 2)
	for i, id := range m.viewportBlockIDs {
		if id == blockID && m.viewportClickTokens[i] == token {
			clickableLines = append(clickableLines, i)
		}
	}
	if len(clickableLines) == 0 {
		t.Fatal("expected ACP tool header to expose a click token")
	}

	vy := clickableLines[len(clickableLines)-1] - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if block.toolPanelExpanded("tool-run") {
		t.Fatal("expected ACP tool panel to collapse after click")
	}
	view = stripModelView(m)
	if !strings.Contains(view, "▸ RUN ") {
		t.Fatalf("expected collapsed ACP tool call arrow, got:\n%s", view)
	}
	if strings.Contains(view, "line-1") || strings.Contains(view, "line-2") {
		t.Fatalf("expected collapsed ACP tool panel to hide body, got:\n%s", view)
	}

	m.syncViewportContent()
	clickableLines = clickableLines[:0]
	for i, id := range m.viewportBlockIDs {
		if id == blockID && m.viewportClickTokens[i] == token {
			clickableLines = append(clickableLines, i)
		}
	}
	if len(clickableLines) == 0 {
		t.Fatal("expected ACP tool header to stay clickable after collapse")
	}

	vy = clickableLines[0] - m.viewport.YOffset()
	_, _ = m.Update(mouseClick(5, vy, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(5, vy, tea.MouseLeft))

	if !block.toolPanelExpanded("tool-run") {
		t.Fatal("expected ACP tool panel to re-expand after second click")
	}
	view = stripModelView(m)
	if !strings.Contains(view, "▾ RUN ") {
		t.Fatalf("expected re-expanded ACP tool call arrow, got:\n%s", view)
	}
	if !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected ACP tool output after re-expand, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_WrapsLongToolRowsInsideViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.viewport.SetWidth(48)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "evan(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "evan(copilot)",
		ToolCallID: "tool-list",
		ToolName:   "LIST",
		ToolArgs:   map[string]any{"_display": "ls -la && echo \"PWD: $(pwd)\""},
		ToolStatus: "in_progress",
	})

	m.syncViewportContent()
	for i, blockID := range m.viewportBlockIDs {
		if blockID != strings.TrimSpace(m.participantTurnIDs["child-1"]) {
			continue
		}
		if got, want := displayColumns(m.viewportPlainLines[i]), m.viewport.Width(); got > want {
			t.Fatalf("expected participant rows to wrap to viewport width %d, got %d cols: %q", want, got, m.viewportPlainLines[i])
		}
	}
}

func TestParticipantTurnBlock_HeaderOmitsCompletedMetaAndBodyStatusRow(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "ruby(copilot)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "ruby(copilot)",
		Stream:  "answer",
		Text:    "hello",
		Final:   true,
	})
	_, _ = m.Update(tuievents.ParticipantStatusMsg{SessionID: "child-1", State: "completed"})

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "· completed") {
		t.Fatalf("did not expect completed meta in participant header, got:\n%s", view)
	}
	if strings.Contains(view, "✓ completed") {
		t.Fatalf("did not expect completed body status row, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_TerminalStatusCollapsesAllToolPanels(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "ruby(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "ruby(copilot)",
		ToolCallID: "tool-find",
		ToolName:   "FIND",
		ToolArgs:   map[string]any{"_display": "src/**/*.go"},
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "match-1\nmatch-2"},
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "ruby(copilot)",
		ToolCallID: "tool-view",
		ToolName:   "VIEW",
		ToolArgs:   map[string]any{"_display": "/tmp/demo.txt"},
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "file-body"},
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatal("expected participant turn block")
	}
	if !block.toolPanelExpanded("tool-find") || !block.toolPanelExpanded("tool-view") {
		t.Fatal("expected tool panels to start expanded before terminal status")
	}

	_, _ = m.Update(tuievents.ParticipantStatusMsg{SessionID: "child-1", State: "completed"})

	if block.toolPanelExpanded("tool-find") || block.toolPanelExpanded("tool-view") {
		t.Fatal("expected terminal participant turn to collapse all tool panels")
	}
	view := stripModelView(m)
	if strings.Contains(view, "match-1") || strings.Contains(view, "file-body") {
		t.Fatalf("expected collapsed participant turn to hide panel bodies, got:\n%s", view)
	}
	if !strings.Contains(view, "▸ FIND") || !strings.Contains(view, "▸ VIEW") {
		t.Fatalf("expected collapsed tool call headers to remain visible, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_PromptingPlaceholderDisappearsAfterFirstBodyEvent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "ruby(copilot)"})
	_, _ = m.Update(tuievents.ParticipantStatusMsg{SessionID: "child-1", State: "prompting"})
	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "sending prompt") {
		t.Fatalf("did not expect prompting placeholder in participant body, got:\n%s", view)
	}

	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "ruby(copilot)",
		Stream:  "answer",
		Text:    "body",
	})
	view = strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "sending prompt") || strings.Contains(view, "waiting for agent output") {
		t.Fatalf("expected placeholder to disappear after first body event, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_KeepsPostToolAssistantChunksIncremental(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	prefix := "将并行执行四个操作以演示工具调用能力。现在并行执行这些操作。"
	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "liam(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "liam(copilot)",
		Stream:    "assistant",
		DeltaText: prefix,
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "liam(copilot)",
		ToolCallID: "tool-1",
		ToolName:   "SHOW",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionParticipant,
		ScopeID:    "child-1",
		Actor:      "liam(copilot)",
		ToolCallID: "tool-1",
		ToolName:   "SHOW",
		ToolStatus: "completed",
		ToolResult: map[string]any{"summary": "done"},
	})
	for _, chunk := range []string{"已", "并", "行", "运行", " ", "4", " 项"} {
		_, _ = m.Update(tuievents.ACPProjectionMsg{
			Scope:     tuievents.ACPProjectionParticipant,
			ScopeID:   "child-1",
			Actor:     "liam(copilot)",
			Stream:    "assistant",
			DeltaText: chunk,
		})
	}

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Count(view, prefix) != 1 {
		t.Fatalf("expected initial assistant prefix to appear once, got:\n%s", view)
	}
	if !strings.Contains(view, "已并行运行 4 项") {
		t.Fatalf("expected post-tool assistant text to remain incremental, got:\n%s", view)
	}
	if strings.Count(view, prefix) != 1 {
		t.Fatalf("did not expect post-tool chunks to re-expand the prior prefix, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_WhitespaceDeltaDoesNotTriggerFullTextReplay(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "liam(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "liam(copilot)",
		Stream:    "assistant",
		DeltaText: "已并行运行",
		FullText:  "前缀已并行运行",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "liam(copilot)",
		Stream:    "assistant",
		DeltaText: " ",
		FullText:  "前缀已并行运行 ",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "liam(copilot)",
		Stream:    "assistant",
		DeltaText: "4 项",
		FullText:  "前缀已并行运行 4 项",
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "前缀") {
		t.Fatalf("did not expect whitespace delta to trigger full-text replay, got:\n%s", view)
	}
	if !strings.Contains(view, "已并行运行 4 项") {
		t.Fatalf("expected whitespace delta to remain incremental, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_EmptyPlanUpdateClearsPriorPlan(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "liam(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionParticipant,
		ScopeID:       "child-1",
		Actor:         "liam(copilot)",
		HasPlanUpdate: true,
		PlanEntries: []tuievents.PlanEntry{
			{Content: "step 1", Status: "pending"},
		},
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionParticipant,
		ScopeID:       "child-1",
		Actor:         "liam(copilot)",
		HasPlanUpdate: true,
		PlanEntries:   []tuievents.PlanEntry{},
	})

	blockID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	block, ok := m.doc.Find(blockID).(*ParticipantTurnBlock)
	if !ok || block == nil {
		t.Fatalf("expected participant block for child-1")
	}
	if got := len(block.Events); got != 1 || block.Events[0].Kind != SEPlan || len(block.Events[0].PlanEntries) != 0 {
		t.Fatalf("expected empty plan update to clear existing entries, got %#v", block.Events)
	}
}

func TestSubagentACPProjection_EmptyPlanUpdateClearsPriorPlan(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionSubagent,
		ScopeID:       "spawn-1",
		HasPlanUpdate: true,
		PlanEntries: []tuievents.PlanEntry{
			{Content: "step 1", Status: "pending"},
		},
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:         tuievents.ACPProjectionSubagent,
		ScopeID:       "spawn-1",
		HasPlanUpdate: true,
		PlanEntries:   []tuievents.PlanEntry{},
	})

	sessionKey, state := m.ensureSubagentSessionState("spawn-1", "", "")
	if sessionKey == "" || state == nil {
		t.Fatalf("expected subagent session state")
	}
	if got := len(state.Events); got != 1 || state.Events[0].Kind != SEPlan || len(state.Events[0].PlanEntries) != 0 {
		t.Fatalf("expected empty subagent plan update to clear existing entries, got %#v", state.Events)
	}
}

func TestSubagentACPProjection_InProgressToolPreviewStillRenders(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-1", Agent: "self", CallID: "call-1"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionSubagent,
		ScopeID:    "spawn-1",
		ToolCallID: "tool-1",
		ToolName:   "BASH",
		ToolArgs:   map[string]any{"_display": "tail -f /tmp/demo.log"},
		ToolResult: map[string]any{
			"summary": "heartbeat 1/6",
			"stream":  "stdout",
		},
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "BASH tail -f /tmp/demo.log") {
		t.Fatalf("expected subagent tool start in viewport, got:\n%s", view)
	}
	if !strings.Contains(view, "heartbeat 1/6") {
		t.Fatalf("expected subagent tool preview in viewport, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_StreamingClosedFenceHidesDelimiters(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "amy(copilot)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "amy(copilot)",
		Stream:  "answer",
		Text:    "```python\ndef hello():\n    return 1\n```",
		Final:   false,
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "```python") || strings.Contains(view, "\n```") {
		t.Fatalf("expected active participant code fence delimiters to be hidden, got:\n%s", view)
	}
	if !strings.Contains(view, "def hello():") {
		t.Fatalf("expected code block body to remain visible, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_CoalescesAssistantChunksAcrossHiddenReasoning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	prefix := "我是 Gemini CLI，专注于软件工程任务的交互式 AI"
	full := prefix + " 代理。我以高级软件工程师的身份协助你进行代码分析。"
	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "leo(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "leo(gemini)",
		Stream:  "answer",
		Text:    prefix,
	})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "leo(gemini)",
		Stream:  "reasoning",
		Text:    "thinking",
	})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "leo(gemini)",
		Stream:  "answer",
		Text:    full,
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "thinking") {
		t.Fatalf("did not expect hidden reasoning preview to remain visible, got:\n%s", view)
	}
	if count := strings.Count(view, prefix); count != 1 {
		t.Fatalf("expected assistant prefix to render once after coalescing, got %d occurrences:\n%s", count, view)
	}
	if !strings.Contains(view, "代理。我以高级软件工程师的身份协助你进行代码分析。") {
		t.Fatalf("expected coalesced assistant content, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_SanitizesStreamingControlCharacters(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "leo(gemini)"})
	_, _ = m.Update(tuievents.RawDeltaMsg{
		Target:  tuievents.RawDeltaTargetAssistant,
		ScopeID: "child-1",
		Actor:   "leo(gemini)",
		Stream:  "answer",
		Text:    "hello\x1b[31m\rworld",
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if strings.Contains(view, "\x1b") || strings.Contains(view, "[31m") {
		t.Fatalf("did not expect ANSI escape content in participant stream, got:\n%s", view)
	}
	if !strings.Contains(view, "hello") || !strings.Contains(view, "world") {
		t.Fatalf("expected sanitized participant text to remain visible, got:\n%s", view)
	}
}

func TestParticipantTurnBlock_AddsNewReferenceForSameSession(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "cole(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "cole(copilot)",
		Stream:    "assistant",
		DeltaText: "first turn",
	})
	firstID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	if firstID == "" {
		t.Fatal("expected first participant turn block id")
	}

	_, _ = m.Update(tuievents.ParticipantTurnStartMsg{SessionID: "child-1", Actor: "cole(copilot)"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionParticipant,
		ScopeID:   "child-1",
		Actor:     "cole(copilot)",
		Stream:    "assistant",
		DeltaText: "second turn",
	})
	secondID := strings.TrimSpace(m.participantTurnIDs["child-1"])
	if secondID == "" || secondID == firstID {
		t.Fatalf("expected latest participant reference block for same session, got %q want new block", secondID)
	}

	firstBlock, _ := m.doc.Find(firstID).(*ParticipantTurnBlock)
	secondBlock, _ := m.doc.Find(secondID).(*ParticipantTurnBlock)
	if firstBlock == nil || secondBlock == nil {
		t.Fatal("expected both participant turn blocks to exist")
	}
	if got := len(firstBlock.Events); got != 1 || firstBlock.Events[0].Text != "first turn" {
		t.Fatalf("expected first block content preserved, got %#v", firstBlock.Events)
	}
	if got := len(secondBlock.Events); got != 1 || secondBlock.Events[0].Text != "second turn" {
		t.Fatalf("expected second block content preserved, got %#v", secondBlock.Events)
	}
}

func TestRenderMentionList_UsesAgentsTitleForAtPrefix(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.mentionPrefix = "@"
	m.mentionCandidates = []string{"mia(codex)"}

	view := m.renderMentionList()
	if !strings.Contains(view, "Agents") {
		t.Fatalf("expected Agents title for @ mention overlay, got:\n%s", view)
	}
	if strings.Contains(view, "Files") {
		t.Fatalf("did not expect Files title for @ mention overlay, got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// MainACPTurnBlock regression tests
// ---------------------------------------------------------------------------

func TestMainACPTurnBlock_MultiChunkAssistantStreamAccumulates(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})

	chunks := []string{"Hello", ", I", " am", " an", " AI", " assistant."}
	for _, chunk := range chunks {
		_, _ = m.Update(tuievents.ACPProjectionMsg{
			Scope:     tuievents.ACPProjectionMain,
			ScopeID:   "root-session",
			Stream:    "assistant",
			DeltaText: chunk,
			FullText:  "", // delta-only, FullText intentionally omitted
		})
	}

	blockID := strings.TrimSpace(m.activeMainACPTurnID)
	block, ok := m.doc.Find(blockID).(*MainACPTurnBlock)
	if !ok || block == nil {
		t.Fatalf("expected MainACPTurnBlock for root-session")
	}
	if len(block.Events) != 1 || block.Events[0].Kind != SEAssistant {
		t.Fatalf("expected single assistant event, got %d events: %#v", len(block.Events), block.Events)
	}
	if got := block.Events[0].Text; got != "Hello, I am an AI assistant." {
		t.Fatalf("expected accumulated text, got %q", got)
	}

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "Hello, I am an AI assistant.") {
		t.Fatalf("expected full accumulated text in viewport, got:\n%s", view)
	}
}

func TestMainACPTurnBlock_ToolCallAndResultRealTimeProjection(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})

	// Assistant text first
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Let me check that file.",
	})

	// Tool call (in-progress)
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-1",
		ToolName:   "READ",
		ToolArgs:   map[string]any{"path": "/tmp/demo.txt"},
		ToolStatus: "in_progress",
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "READ") {
		t.Fatalf("expected in-progress tool call visible in viewport, got:\n%s", view)
	}

	// Tool result (completed)
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-1",
		ToolName:   "READ",
		ToolArgs:   map[string]any{"path": "/tmp/demo.txt"},
		ToolResult: map[string]any{"content": "file contents here"},
		ToolStatus: "completed",
	})

	view = strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "Let me check that file.") {
		t.Fatalf("expected assistant text in viewport after tool result, got:\n%s", view)
	}
	if !strings.Contains(view, "READ") {
		t.Fatalf("expected tool name in viewport after tool result, got:\n%s", view)
	}

	blockID := strings.TrimSpace(m.activeMainACPTurnID)
	block, _ := m.doc.Find(blockID).(*MainACPTurnBlock)
	if block == nil {
		t.Fatal("expected MainACPTurnBlock")
	}
	toolEvents := 0
	for _, ev := range block.Events {
		if ev.Kind == SEToolCall {
			toolEvents++
		}
	}
	if toolEvents == 0 {
		t.Fatalf("expected at least one tool event in block, got events: %#v", block.Events)
	}
}

func TestMainACPTurnBlock_ACPToolResultRendersInlinePanelUnderCall(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-run",
		ToolName:   "RUN",
		ToolArgs:   map[string]any{"_display": "python3 hello.py"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-run",
		ToolName:   "RUN",
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "Hello from main ACP"},
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "RUN") || !strings.Contains(view, "python3 hello.py") {
		t.Fatalf("expected RUN call line in viewport, got:\n%s", view)
	}
	if !strings.Contains(view, "Hello from main ACP") {
		t.Fatalf("expected tool output in viewport, got:\n%s", view)
	}
	if strings.Contains(view, "✓ Hello from main ACP") {
		t.Fatalf("expected ACP main tool output to render inside a panel, not as a standalone result row, got:\n%s", view)
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("expected ACP main tool output to render inside a drawer panel, got:\n%s", view)
	}
}

func TestMainACPTurnBlock_TerminalStatusCollapsesAllToolPanels(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-find",
		ToolName:   "FIND",
		ToolArgs:   map[string]any{"_display": "src/**/*.go"},
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "match-1\nmatch-2"},
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-view",
		ToolName:   "VIEW",
		ToolArgs:   map[string]any{"_display": "/tmp/demo.txt"},
		ToolStatus: "completed",
		ToolResult: map[string]any{"content": "file-body"},
	})

	blockID := strings.TrimSpace(m.activeMainACPTurnID)
	block, ok := m.doc.Find(blockID).(*MainACPTurnBlock)
	if !ok || block == nil {
		t.Fatal("expected main ACP turn block")
	}
	if !block.toolPanelExpanded("call-find") || !block.toolPanelExpanded("call-view") {
		t.Fatal("expected main ACP tool panels to start expanded before terminal status")
	}

	_, _ = m.Update(tuievents.TaskResultMsg{})

	if block.toolPanelExpanded("call-find") || block.toolPanelExpanded("call-view") {
		t.Fatal("expected finalized main ACP turn to collapse all tool panels")
	}
	view := stripModelView(m)
	if strings.Contains(view, "match-1") || strings.Contains(view, "file-body") {
		t.Fatalf("expected collapsed main ACP turn to hide panel bodies, got:\n%s", view)
	}
	if !strings.Contains(view, "▸ FIND") || !strings.Contains(view, "▸ VIEW") {
		t.Fatalf("expected collapsed main ACP tool headers to remain visible, got:\n%s", view)
	}
}

func TestMainACPTurnBlock_SpawnToolRenderingRemainsUnchanged(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-spawn",
		ToolName:   "SPAWN",
		ToolArgs:   map[string]any{"_display": "delegate work"},
		ToolStatus: "in_progress",
	})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:      tuievents.ACPProjectionMain,
		ScopeID:    "root-session",
		ToolCallID: "call-spawn",
		ToolName:   "SPAWN",
		ToolStatus: "completed",
		ToolResult: map[string]any{"summary": "spawned helper"},
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "SPAWN") || !strings.Contains(view, "spawned helper") {
		t.Fatalf("expected spawn lifecycle to remain visible, got:\n%s", view)
	}
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Fatalf("expected SPAWN lifecycle to keep existing non-panel rendering, got:\n%s", view)
	}
}

func TestMainACPTurnBlock_FinalSnapshotFillsMissedContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})

	// First chunk only
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Hello",
	})

	// Final snapshot with full text (simulates Finalize catch-up)
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:    tuievents.ACPProjectionMain,
		ScopeID:  "root-session",
		Stream:   "assistant",
		FullText: "Hello, I am an AI.",
	})

	view := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(view, "Hello, I am an AI.") {
		t.Fatalf("expected final snapshot to replace incomplete stream, got:\n%s", view)
	}
}

func TestMainACPTurnBlock_PreviousTurnBaselineDoesNotPolluteCurrent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Simulate previous turn
	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Previous turn answer.",
	})

	// Finalize the turn via TaskResultMsg (mirrors real lifecycle)
	_, _ = m.Update(tuievents.TaskResultMsg{})

	// Verify first turn block is finalized
	if m.activeMainACPTurnID != "" {
		t.Fatal("expected activeMainACPTurnID to be cleared after TaskResultMsg")
	}

	// Start new turn — must create a fresh block
	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Current turn answer.",
	})

	newBlockID := strings.TrimSpace(m.activeMainACPTurnID)
	newBlock, _ := m.doc.Find(newBlockID).(*MainACPTurnBlock)
	if newBlock == nil {
		t.Fatal("expected new MainACPTurnBlock for second turn")
	}

	view := strings.Join(m.viewportPlainLines, "\n")
	currCount := strings.Count(view, "Current turn answer.")
	if currCount != 1 {
		t.Fatalf("expected current turn answer exactly once, got %d in:\n%s", currCount, view)
	}
	// The new turn block should only contain the new text, not the old
	if len(newBlock.Events) != 1 || newBlock.Events[0].Kind != SEAssistant {
		t.Fatalf("expected single assistant event in new block, got %d events", len(newBlock.Events))
	}
	if got := newBlock.Events[0].Text; got != "Current turn answer." {
		t.Fatalf("expected only current turn text in new block, got %q", got)
	}
}

func TestMainACPTurnBlock_UserMessageFinalizesTurnBeforeNextReplay(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Previous turn answer.",
	})
	_, _ = m.Update(tuievents.UserMessageMsg{Text: "next question"})

	if m.activeMainACPTurnID != "" {
		t.Fatal("expected user message to finalize the previous ACP turn")
	}

	_, _ = m.Update(tuievents.ACPMainTurnStartMsg{SessionID: "root-session"})
	_, _ = m.Update(tuievents.ACPProjectionMsg{
		Scope:     tuievents.ACPProjectionMain,
		ScopeID:   "root-session",
		Stream:    "assistant",
		DeltaText: "Current turn answer.",
	})

	view := stripModelView(m)
	prevIdx := strings.Index(view, "Previous turn answer.")
	userIdx := strings.Index(view, "> next question")
	currIdx := strings.Index(view, "Current turn answer.")
	if prevIdx < 0 || userIdx < 0 || currIdx < 0 {
		t.Fatalf("expected previous answer, user question, and current answer in view, got:\n%s", view)
	}
	if prevIdx >= userIdx || userIdx >= currIdx {
		t.Fatalf("expected ACP replay to stay ordered across user boundary, got:\n%s", view)
	}
}
