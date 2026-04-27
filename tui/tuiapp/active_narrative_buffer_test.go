package tuiapp

import (
	"strings"
	"testing"
	"time"
)

func TestActiveAssistantBufferDoesNotMutateRawUntilFinal(t *testing.T) {
	m := NewModel(Config{NoColor: true})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(20)

	_, _ = m.handleStreamBlock("answer", "assistant", "hello", false)
	block, ok := m.doc.Blocks()[0].(*AssistantBlock)
	if !ok {
		t.Fatalf("block = %T, want AssistantBlock", m.doc.Blocks()[0])
	}
	if got := block.Raw; got != "" {
		t.Fatalf("streaming assistant Raw = %q, want empty active buffer backing", got)
	}
	_, _ = m.Update(frameTickMsg{kind: frameTickViewportSync, at: time.Now()})
	if joined := strings.Join(m.viewportPlainLines, "\n"); !strings.Contains(joined, "hello") {
		t.Fatalf("active buffer was not rendered: %q", joined)
	}

	_, _ = m.handleStreamBlock("answer", "assistant", " world", false)
	if got := block.Raw; got != "" {
		t.Fatalf("streaming assistant Raw after append = %q, want empty active buffer backing", got)
	}
	_, _ = m.Update(frameTickMsg{kind: frameTickViewportSync, at: time.Now()})
	if joined := strings.Join(m.viewportPlainLines, "\n"); !strings.Contains(joined, "hello world") {
		t.Fatalf("active buffer append was not rendered: %q", joined)
	}

	_, _ = m.handleStreamBlock("answer", "assistant", "", true)
	if block.Streaming {
		t.Fatal("assistant block should be finalized")
	}
	if got := block.Raw; got != "hello world" {
		t.Fatalf("final assistant Raw = %q, want promoted active text", got)
	}
}

func TestActiveReasoningBufferDoesNotMutateRawUntilFinal(t *testing.T) {
	m := NewModel(Config{NoColor: true})
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(20)

	_, _ = m.handleStreamBlock("reasoning", "assistant", "think", false)
	block, ok := m.doc.Blocks()[0].(*ReasoningBlock)
	if !ok {
		t.Fatalf("block = %T, want ReasoningBlock", m.doc.Blocks()[0])
	}
	if got := block.Raw; got != "" {
		t.Fatalf("streaming reasoning Raw = %q, want empty active buffer backing", got)
	}

	_, _ = m.handleStreamBlock("reasoning", "assistant", " more", false)
	if got := block.Raw; got != "" {
		t.Fatalf("streaming reasoning Raw after append = %q, want empty active buffer backing", got)
	}

	_, _ = m.handleStreamBlock("reasoning", "assistant", "", true)
	if block.Streaming {
		t.Fatal("reasoning block should be finalized")
	}
	if got := block.Raw; got != "think more" {
		t.Fatalf("final reasoning Raw = %q, want promoted active text", got)
	}
}

func TestActiveNarrativeBufferDoesNotRerenderCompletedHistory(t *testing.T) {
	m := newPerfTestModel()
	seedLongTranscript(m, 120)

	_, _ = m.handleStreamBlock("answer", "assistant", "hello", false)
	_, _ = m.Update(frameTickMsg{kind: frameTickViewportSync, at: time.Now()})
	beforeTranscriptRenders := m.diag.BlockRenderCallsByKind[BlockTranscript]
	beforeFullSyncs := m.diag.ViewportFullSyncs

	for range 20 {
		_, _ = m.handleStreamBlock("answer", "assistant", " token", false)
		_, _ = m.Update(frameTickMsg{kind: frameTickViewportSync, at: time.Now()})
	}

	if got := m.diag.BlockRenderCallsByKind[BlockTranscript]; got != beforeTranscriptRenders {
		t.Fatalf("completed transcript block renders = %d, want %d", got, beforeTranscriptRenders)
	}
	if got := m.diag.ViewportFullSyncs; got != beforeFullSyncs {
		t.Fatalf("active stream full syncs = %d, want %d", got, beforeFullSyncs)
	}
}
