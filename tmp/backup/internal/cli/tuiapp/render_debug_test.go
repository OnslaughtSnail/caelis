package tuiapp

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/charmbracelet/x/ansi"
)

// TestEndToEndStreamAndSelect simulates the exact real-world scenario:
// 1. Resize (initialize viewport)
// 2. Stream assistant answer in chunks
// 3. Receive final message
// 4. Mouse select and release
// 5. Verify "我" is preserved at each step
func TestEndToEndStreamAndSelect(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	target := "我是一个AI助手"

	chunks := []string{
		"你好！", "我是 ", "caelis", "。\n\n",
		"我是", "一个", "AI", "助手", "，请问", "有什么",
		"我可以", "帮助", "您的？",
	}
	fullText := strings.Join(chunks, "")

	for _, chunk := range chunks {
		m.Update(tuievents.AssistantStreamMsg{
			Kind: "answer",
			Text: chunk,
		})
	}

	// Final message
	m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  fullText,
		Final: true,
	})

	vpView := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpView, target) {
		t.Errorf("after final: viewport.View() missing %q", target)
		for i, l := range m.renderedStyledLines() {
			t.Logf("  hist[%d]: %q", i, ansi.Strip(l))
		}
	}

	// Simulate mouse select: press at (2,0), drag to (20,2), release
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseMotionMsg(tea.Mouse{X: 20, Y: 2}))
	m.Update(tea.MouseReleaseMsg(tea.Mouse{X: 20, Y: 2}))

	vpViewSel := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpViewSel, target) {
		t.Errorf("after selection: viewport.View() missing %q", target)
		t.Logf("hasSelectionRange=%v", m.hasSelectionRange())
		for i, l := range m.viewportPlainLines {
			if strings.Contains(l, "我") || strings.Contains(l, "助手") || strings.Contains(l, "你好") {
				t.Logf("  vpPlain[%d]: %q", i, l)
			}
		}
	}

	// Click to clear selection, then release
	m.Update(tea.MouseClickMsg(tea.Mouse{X: 2, Y: 0, Button: tea.MouseLeft}))
	m.Update(tea.MouseReleaseMsg(tea.Mouse{X: 2, Y: 0}))

	vpViewAfter := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpViewAfter, target) {
		t.Errorf("after clear: viewport.View() missing %q", target)
	}
}

// TestResizeAfterFinalMessage tests that content is preserved after resize.
func TestResizeAfterFinalMessage(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	target := "我是一个AI助手"
	fullText := "你好！我是 caelis。\n\n我是一个AI助手，请问有什么我可以帮助您的？"

	m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  fullText,
		Final: true,
	})

	for _, w := range []int{60, 50, 120, 40, 80} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		vpView := ansi.Strip(m.viewport.View())
		if !strings.Contains(vpView, target) {
			t.Errorf("after resize to %d: missing %q", w, target)
			for i, l := range m.renderedStyledLines() {
				t.Logf("  hist[%d]: %q", i, ansi.Strip(l))
			}
		}
	}
}
