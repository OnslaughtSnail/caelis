package tuiapp

import (
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

func TestAssistantStreamMsg_FrameBatchesWhenEnabled(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:          noopExecute,
		FrameBatchMainStream: true,
		StreamTickInterval:   20 * time.Millisecond,
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

	_, _ = m.Update(frameTickMsg{at: time.Now().Add(20 * time.Millisecond)})
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if joined == "" {
		t.Fatal("expected buffered assistant text to start appearing after frame tick")
	}
	_, _ = m.Update(frameTickMsg{at: time.Now().Add(40 * time.Millisecond)})
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
