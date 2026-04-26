package tuiapp

import (
	"fmt"
	"strings"
	"testing"
)

func TestTerminalToolPanelCapsHeightAndScrollsInternally(t *testing.T) {
	model := newGatewayEventTestModel()
	ctx := BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme}

	for _, toolName := range []string{"BASH", "SPAWN"} {
		t.Run(toolName, func(t *testing.T) {
			block := NewMainACPTurnBlock("session-1")
			callID := strings.ToLower(toolName) + "-1"
			lines := make([]string, 0, 30)
			for i := 1; i <= 30; i++ {
				lines = append(lines, fmt.Sprintf("Step %02d/30", i))
			}
			block.UpdateTool(callID, toolName, "run long task", strings.Join(lines, "\n"), false, false)

			rows := block.Render(ctx)
			plain := renderedPlainRows(rows)
			if got := countRowsContaining(plain, "Step "); got != acpTerminalPanelMaxLines {
				t.Fatalf("visible terminal rows = %d, want %d\n%s", got, acpTerminalPanelMaxLines, strings.Join(plain, "\n"))
			}
			joined := strings.Join(plain, "\n")
			if strings.Contains(joined, "Step 01/30") {
				t.Fatalf("initial panel should follow tail, got\n%s", joined)
			}
			if !strings.Contains(joined, "Step 30/30") {
				t.Fatalf("initial panel missing tail output, got\n%s", joined)
			}

			if !block.ScrollToolPanel(callID, -30, ctx) {
				t.Fatal("ScrollToolPanel returned false, want scroll to top")
			}
			rows = block.Render(ctx)
			plain = renderedPlainRows(rows)
			joined = strings.Join(plain, "\n")
			if !strings.Contains(joined, "Step 01/30") {
				t.Fatalf("scrolled panel missing top output, got\n%s", joined)
			}
			if strings.Contains(joined, "Step 30/30") {
				t.Fatalf("scrolled panel should hide tail output, got\n%s", joined)
			}
		})
	}
}

func renderedPlainRows(rows []RenderedRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Plain)
	}
	return out
}

func countRowsContaining(lines []string, needle string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, needle) {
			count++
		}
	}
	return count
}
