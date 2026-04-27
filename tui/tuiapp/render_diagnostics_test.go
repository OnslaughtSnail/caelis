package tuiapp

import (
	"testing"
	"time"
)

func TestRenderDiagnosticsCountsMessageLaneAndViewportSetContent(t *testing.T) {
	m := NewModel(Config{NoColor: true})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(20)

	updated, cmd := m.Update(LogChunkMsg{Chunk: "hello\n"})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("LogChunkMsg should schedule a viewport sync")
	}
	updated, _ = m.Update(frameTickMsg{kind: frameTickViewportSync, at: time.Now()})
	m = updated.(*Model)

	if m.diag.UpdateMessagesByLane[renderLaneLog] == 0 {
		t.Fatal("log lane update counter was not incremented")
	}
	if m.diag.ViewportSetContentLines == 0 {
		t.Fatal("SetContentLines counter was not incremented")
	}
	if m.diag.ViewportSetContentReason["full_sync"] == 0 && m.diag.ViewportSetContentReason["incremental_sync"] == 0 {
		t.Fatalf("missing SetContentLines reason counts: %#v", m.diag.ViewportSetContentReason)
	}
	if m.diag.ViewportSetContentLineCount == 0 {
		t.Fatal("SetContentLines line counter was not incremented")
	}
	if m.diag.ViewportSetContentBytes == 0 {
		t.Fatal("SetContentLines byte counter was not incremented")
	}
	if m.diag.BlockRenderCallsByKind[BlockTranscript] == 0 {
		t.Fatal("transcript block render counter was not incremented")
	}
}

func TestRenderDiagnosticsCountsSmoothingFlushReason(t *testing.T) {
	m := NewModel(Config{NoColor: true})
	_, _ = m.enqueueMainDelta("answer", "assistant", "hello", false)

	m.flushAllPendingStreamSmoothingWithReason("semantic_barrier")

	if got := m.diag.StreamSmoothingFlushReason["semantic_barrier"]; got != 1 {
		t.Fatalf("semantic_barrier flush count = %d, want 1", got)
	}
}
