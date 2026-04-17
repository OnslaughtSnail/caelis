package tuiapp

import (
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

func normalizeRenderWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func driveSmoothingFrames(m *Model, frames int) {
	if m == nil {
		return
	}
	now := time.Now().Add(m.streamWarmDelay() + m.streamTickInterval())
	for i := 0; i < frames; i++ {
		_, _ = m.Update(tickAt(frameTickStreamSmoothing, now))
		now = now.Add(m.streamTickInterval())
	}
}

func TestAssistantStreamSmoothing_RevealsLargeChunkIncrementally(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	full := "MiniMax streaming should feel much smoother in the terminal output."
	_, _ = m.Update(tuievents.RawDeltaMsg{Target: tuievents.RawDeltaTargetAssistant, Stream: "answer", Text: full})

	if len(m.streamSmoothing) == 0 {
		t.Fatal("expected pending smoothing queue after large assistant chunk")
	}
	initial := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(initial, full) {
		t.Fatalf("expected no immediate full render, got %q", initial)
	}

	driveSmoothingFrames(m, 2)
	mid := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(mid, "Mini") || strings.Contains(mid, full) {
		t.Fatalf("expected incremental reveal after ticks, got %q", mid)
	}
	driveSmoothingFrames(m, 40)
	joined := normalizeRenderWhitespace(ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")))
	if !strings.Contains(joined, "MiniMax streaming should feel much smoother") || !strings.Contains(joined, "terminal output.") {
		t.Fatalf("expected smoothing ticks to reveal full content, got %q", joined)
	}
}

func TestSubagentStreamSmoothing_RevealsLargeChunkIncrementally(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	full := "The subagent panel should reveal this long chunk gradually instead of in one jump."
	_, _ = m.Update(tuievents.RawDeltaMsg{Target: tuievents.RawDeltaTargetSubagent, ScopeID: "spawn-1", Stream: "assistant", Text: full})

	if len(m.streamSmoothing) == 0 {
		t.Fatal("expected pending smoothing queue after large subagent chunk")
	}
	initial := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(initial, full) {
		t.Fatalf("expected no immediate full render, got %q", initial)
	}

	driveSmoothingFrames(m, 2)
	mid := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(mid, "The") || strings.Contains(mid, full) {
		t.Fatalf("expected partial subagent reveal after ticks, got %q", mid)
	}
	driveSmoothingFrames(m, 40)
	session := m.subagentSessions["spawn-1"]
	if session == nil || len(session.Events) == 0 {
		t.Fatal("expected subagent session events after smoothing ticks")
	}
	if got := session.Events[len(session.Events)-1].Text; got != full {
		t.Fatalf("expected session state to accumulate full subagent content, got %q", got)
	}
}

func TestBTWOverlaySmoothing_RevealsLargeChunkIncrementally(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.openBTWOverlay("/btw smooth?")

	full := "BTW overlay responses should stay responsive while still revealing large bursts more gracefully."
	_, _ = m.Update(tuievents.RawDeltaMsg{Target: tuievents.RawDeltaTargetBTW, Stream: "answer", Text: full})

	if len(m.streamSmoothing) == 0 {
		t.Fatal("expected pending smoothing queue for btw overlay")
	}
	if m.btwOverlay == nil || m.btwOverlay.Answer != "" {
		t.Fatalf("expected btw answer to wait for frame playback, got %+v", m.btwOverlay)
	}
	driveSmoothingFrames(m, 2)
	if m.btwOverlay == nil || !strings.Contains(m.btwOverlay.Answer, "BTW") || m.btwOverlay.Answer == full {
		t.Fatalf("expected btw overlay partial reveal after ticks, got %+v", m.btwOverlay)
	}
	driveSmoothingFrames(m, 40)
	if m.btwOverlay == nil || m.btwOverlay.Answer != full {
		t.Fatalf("expected btw overlay to converge to full answer, got %+v", m.btwOverlay)
	}
}

func TestSpawnPreviewSmoothing_RevealsLargeChunkIncrementally(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	panel := NewBashPanelBlock("SPAWN", "call-1")
	panel.Key = "spawn-preview"
	m.doc.Append(panel)

	full := "Spawn preview text should reveal quickly without dumping the whole burst in a single repaint."
	m.appendBashPanelChunk(panel, "assistant", full)

	if len(m.streamSmoothing) == 0 {
		t.Fatal("expected pending smoothing queue for spawn preview")
	}
	if panel.AssistantPartial != "" {
		t.Fatalf("expected no immediate spawn preview reveal, got %q", panel.AssistantPartial)
	}
	driveSmoothingFrames(m, 2)
	if !strings.Contains(panel.AssistantPartial, "Spawn") || panel.AssistantPartial == full {
		t.Fatalf("expected spawn preview partial after ticks, got %q", panel.AssistantPartial)
	}
	driveSmoothingFrames(m, 40)
	if panel.AssistantPartial != full {
		t.Fatalf("expected spawn preview to converge to full text, got %q", panel.AssistantPartial)
	}
}
