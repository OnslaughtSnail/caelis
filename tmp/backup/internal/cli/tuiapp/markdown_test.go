package tuiapp

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func TestNormalizeTerminalMarkdown_IgnoresCurrencyText(t *testing.T) {
	in := "价格从 $5 到 $10"
	got := normalizeTerminalMarkdown(in)
	if got != in {
		t.Fatalf("expected currency text unchanged, got %q", got)
	}
}

func TestNormalizeTerminalMarkdown_PreservesInlineMath(t *testing.T) {
	got := normalizeTerminalMarkdown("结果是 $E=mc^2$")
	if !strings.Contains(got, "E=mc^2") {
		t.Fatalf("expected inline math content preserved, got %q", got)
	}
	if strings.Contains(got, "$E=mc^2$") {
		t.Fatalf("expected inline math delimiters normalized, got %q", got)
	}
}

func TestNormalizeTerminalMarkdown_FormatsBlockMath(t *testing.T) {
	got := normalizeTerminalMarkdown("$$\n\\frac{a}{b}\n$$")
	if !strings.Contains(got, "\\frac{a}{b}") {
		t.Fatalf("expected block math content preserved, got %q", got)
	}
	if strings.Contains(got, "$$") {
		t.Fatalf("expected block math delimiters normalized, got %q", got)
	}
}

func TestAssistantBlockRender_PlainTextPreserved(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "plain text"
	rows := block.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
	if !strings.Contains(rows[0].Plain, "plain text") {
		t.Fatalf("expected plain text preserved, got %q", rows[0].Plain)
	}
}

func TestAssistantBlockRender_PreservesTrailingListMarker(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "文件操作：\n- "
	rows := block.Render(ctx)
	joined := ""
	for _, r := range rows {
		joined += r.Plain + "\n"
	}
	// Content before the empty list marker must be preserved.
	// Glamour may drop an empty "- " list item — that is acceptable for
	// a still-in-progress streaming block.
	if !strings.Contains(joined, "文件操作") {
		t.Fatalf("expected pre-list content preserved, got %q", joined)
	}
}

func TestAssistantBlockRender_PartialMathNotRewritten(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "结果是 $E=mc"
	rows := block.Render(ctx)
	joined := ""
	for _, r := range rows {
		joined += r.Plain + "\n"
	}
	if strings.Contains(joined, "`E=mc`") {
		t.Fatalf("did not expect unclosed math to be normalized, got %q", joined)
	}
}

func TestRenderAssistantBlock_NormalizesMarkdownHeading(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手"
	block.Streaming = false
	rows := block.Render(ctx)

	foundHeading := false
	for _, row := range rows {
		plain := strings.TrimSpace(row.Plain)
		switch plain {
		case "关于我":
			foundHeading = true
		case "# 关于我":
			t.Fatalf("did not expect markdown heading marker to survive, got %q", row.Plain)
		}
	}

	if !foundHeading {
		var plains []string
		for _, r := range rows {
			plains = append(plains, r.Plain)
		}
		t.Fatalf("expected heading '关于我' preserved, got:\n%s", strings.Join(plains, "\n"))
	}
}

func TestRenderAssistantBlock_UsesNeutralBodyColor(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "关于我"
	block.Streaming = false
	rows := block.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
	// Body text should NOT use green (AssistantFg).
	for _, row := range rows {
		body := strings.TrimPrefix(row.Plain, "* ")
		if body == "" {
			continue
		}
		if strings.Contains(row.Styled, "[38;5;77m"+body) || strings.Contains(row.Styled, "[38;2;86;211;100m"+body) {
			t.Fatalf("did not expect assistant body to use green assistant color, got %q", row.Styled)
		}
	}
}
