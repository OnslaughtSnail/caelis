package tuiapp

import (
	"strings"
	"testing"

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
