package tuiapp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

func TestAssistantStreamMsg_FrameBatchesWhenEnabled(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:          noopExecute,
		FrameBatchMainStream: true,
		StreamWarmDelay:      time.Millisecond,
		StreamNormalCPS:      200,
	})
	resizeModel(m)

	_, cmd := m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "你好", Final: false})
	if cmd == nil {
		t.Fatal("expected frame tick command for batched assistant stream")
	}
	if joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")); strings.Contains(joined, "你好") {
		t.Fatalf("expected assistant text buffered before frame tick, got %q", joined)
	}

	_, _ = m.Update(tickAt(frameTickStreamSmoothing, time.Now().Add(20*time.Millisecond)))
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if joined == "" {
		t.Fatal("expected buffered assistant text to start appearing after frame tick")
	}
	_, _ = m.Update(tickAt(frameTickStreamSmoothing, time.Now().Add(40*time.Millisecond)))
	joined = ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "你好") {
		t.Fatalf("expected assistant text after batched frame ticks, got %q", joined)
	}
}

func TestAssistantStreamMsg_FinalFlushesBufferedBatch(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:          noopExecute,
		FrameBatchMainStream: true,
		StreamTickInterval:   20 * time.Millisecond,
		StreamWarmDelay:      time.Hour,
	})
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "你", Final: false})
	if joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")); strings.Contains(joined, "你") {
		t.Fatalf("expected partial text buffered before final, got %q", joined)
	}

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "你好，世界", Final: true})
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "你好，世界") {
		t.Fatalf("expected final text flushed immediately, got %q", joined)
	}
}

func TestLogChunkMsg_FrameBatchesWhenEnabled(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:        noopExecute,
		StreamTickInterval: 20 * time.Millisecond,
	})
	resizeModel(m)

	_, cmd := m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH echo hi\n"})
	if cmd == nil {
		t.Fatal("expected frame tick command for batched log chunk")
	}
	if joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")); strings.Contains(joined, "echo hi") {
		t.Fatalf("expected log chunk buffered before frame tick, got %q", joined)
	}

	_, _ = m.Update(tickAt(frameTickDeferredBatch, time.Now().Add(20*time.Millisecond)))
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "echo hi") {
		t.Fatalf("expected buffered log chunk after frame tick, got %q", joined)
	}
}

func TestTaskStreamMsg_FrameBatchesPureOutputChunks(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:        noopExecute,
		StreamTickInterval: 20 * time.Millisecond,
	})
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH echo hi\n"})
	_, _ = m.Update(tickAt(frameTickDeferredBatch, time.Now().Add(20*time.Millisecond)))

	msg := tuievents.TaskStreamMsg{
		Label:  "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "hello world\n",
	}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("expected frame tick command for batched task stream chunk")
	}
	if joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")); strings.Contains(joined, "hello world") {
		t.Fatalf("expected task stream output buffered before frame tick, got %q", joined)
	}

	_, _ = m.Update(tickAt(frameTickDeferredBatch, time.Now().Add(40*time.Millisecond)))
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "hello world") {
		t.Fatalf("expected buffered task stream output after frame tick, got %q", joined)
	}
}

func TestAssistantStreamMsg_ScrolledUpSchedulesOffscreenSync(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	for i := 0; i < 80; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* history-%02d\n", i)})
	}
	_, _ = m.Update(keyPress(tea.KeyPgUp))
	if !m.userScrolledUp {
		t.Fatal("expected model to be scrolled away from the bottom")
	}

	before := m.lastViewportContent
	_, cmd := m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "offscreen streamed answer", Final: false})
	if cmd == nil {
		t.Fatal("expected offscreen viewport tick while scrolled up")
	}
	if m.lastViewportContent != before {
		t.Fatal("expected viewport content not to rebuild immediately while scrolled up")
	}

	_, _ = m.Update(tickAt(frameTickOffscreen, time.Now().Add(m.offscreenViewportSyncInterval())))
	if m.lastViewportContent != before {
		t.Fatal("expected offscreen frame tick to defer rebuild while still scrolled up")
	}
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "offscreen streamed answer") {
		t.Fatalf("expected assistant stream content to remain in document, got %q", joined)
	}

	m.viewport.GotoBottom()
	m.userScrolledUp = false
	_, _ = m.Update(tickAt(frameTickOffscreen, time.Now().Add(2*m.offscreenViewportSyncInterval())))
	if m.lastViewportContent == before {
		t.Fatal("expected viewport content to rebuild once user returns to bottom")
	}
}

func TestAssistantStreamMsg_ScrolledUpDefersMainSmoothingFrames(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:          noopExecute,
		FrameBatchMainStream: true,
		StreamTickInterval:   20 * time.Millisecond,
		StreamWarmDelay:      time.Millisecond,
		StreamNormalCPS:      200,
	})
	resizeModel(m)

	for i := 0; i < 80; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* history-%02d\n", i)})
	}
	m.userScrolledUp = true

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "hidden streamed answer", Final: false})
	if m.activeAssistantID != "" {
		t.Fatal("expected hidden smoothing not to create a partial assistant block immediately")
	}
	if m.hasImmediateStreamSmoothingWork() {
		t.Fatal("expected hidden main-stream smoothing to avoid scheduling immediate frame playback")
	}

	_, _ = m.Update(tickAt(frameTickOffscreen, time.Now().Add(m.offscreenViewportSyncInterval())))
	if m.activeAssistantID == "" {
		t.Fatal("expected offscreen tick to flush hidden stream chunk into the assistant block")
	}
	block, _ := m.doc.Find(m.activeAssistantID).(*AssistantBlock)
	if block == nil || !strings.Contains(block.Raw, "hidden streamed answer") {
		t.Fatalf("expected hidden stream text flushed in one shot, got %#v", block)
	}
	if m.hasImmediateStreamSmoothingWork() {
		t.Fatal("expected hidden main-stream smoothing backlog to be drained after offscreen tick")
	}
}

func TestAssistantFinalDuplicateAcrossTurnsStillRenders(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "same final answer", Final: true})
	firstCount := strings.Count(ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")), "same final answer")
	if firstCount != 1 {
		t.Fatalf("expected first finalized answer once, got %d", firstCount)
	}

	m.commitUserDisplayLine("next turn")
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "same final answer", Final: true})
	secondCount := strings.Count(ansi.Strip(strings.Join(m.renderedStyledLines(), "\n")), "same final answer")
	if secondCount != 2 {
		t.Fatalf("expected duplicate finalized answer across turns to render twice, got %d", secondCount)
	}
}

func TestStreamingAllowsMainViewportScroll(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	for i := 0; i < 80; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* history-%02d\n", i)})
	}
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "streaming", Final: false})
	startOffset := m.viewport.YOffset()

	_, _ = m.handleKey(keyPress(tea.KeyPgUp))
	afterPageUp := m.viewport.YOffset()
	if afterPageUp >= startOffset {
		t.Fatalf("expected page-up to scroll during streaming, got offset %d -> %d", startOffset, afterPageUp)
	}
	if !m.userScrolledUp {
		t.Fatal("expected userScrolledUp after page-up during streaming")
	}

	_, _ = m.handleMouse(mouseWheel(0, 0, tea.MouseWheelUp))
	if got := m.viewport.YOffset(); got >= afterPageUp {
		t.Fatalf("expected wheel-up to continue scrolling during streaming, got offset %d -> %d", afterPageUp, got)
	}
}

func TestConsecutiveUserMessagesStillDedup(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	m.commitUserDisplayLine("same user text")
	m.commitUserDisplayLine("  same   user   text  ")

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if got := strings.Count(joined, "same user text"); got != 1 {
		t.Fatalf("expected consecutive equivalent user messages to dedup, got count=%d in %q", got, joined)
	}
}
