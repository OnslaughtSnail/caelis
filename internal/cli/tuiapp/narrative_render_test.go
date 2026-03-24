package tuiapp

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// Step 6 test coverage: stable TUI text rendering pipeline
// ---------------------------------------------------------------------------

func TestAssistantBody_NotGreenColor(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	lines := m.renderAssistantBlockLines("这是普通文本内容")
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}
	// The body text (after the "* " prefix) should use TextPrimary, not green.
	for _, line := range lines {
		body := strings.TrimPrefix(ansi.Strip(line), "* ")
		if body == "" {
			continue
		}
		// Check that body text is NOT rendered with green (AssistantFg).
		// Green ANSI would contain 38;5;77 (256-color) or 38;2;86;211;100 (truecolor).
		if strings.Contains(line, "[38;5;77m"+body) || strings.Contains(line, "[38;2;86;211;100m"+body) {
			t.Fatalf("assistant body should not use green color, got %q", line)
		}
	}
}

func TestAssistantBody_PrefixIsGreen(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	lines := m.renderAssistantBlockLines("hello")
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}
	// The "* " prefix should be styled with AssistantStyle (green).
	first := lines[0]
	stripped := ansi.Strip(first)
	if !strings.HasPrefix(stripped, "* ") {
		t.Fatalf("expected '* ' prefix, got %q", stripped)
	}
}

func TestCJKHeading_NoCharacterLoss(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手"
	rows := block.Render(ctx)

	var plainJoined strings.Builder
	for _, r := range rows {
		plainJoined.WriteString(r.Plain + "\n")
	}
	plain := plainJoined.String()

	if !strings.Contains(plain, "关于我") {
		t.Fatalf("CJK heading '关于我' lost during rendering, got:\n%s", plain)
	}
	if !strings.Contains(plain, "你好！") {
		t.Fatalf("CJK text '你好！' lost during rendering, got:\n%s", plain)
	}
	if !strings.Contains(plain, "身份") {
		t.Fatalf("CJK text '身份' lost during rendering, got:\n%s", plain)
	}
}

func TestCJKHeading_NarrowViewport(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})

	block := NewAssistantBlock()
	block.Raw = "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手"
	m.doc.Append(block)
	m.syncViewportContent()

	joined := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(joined, "关于我") {
		t.Fatalf("CJK heading lost in narrow viewport, got:\n%s", joined)
	}
}

func TestEmoji_ZWJ_Preserved(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "Hello 🇺🇸 World\n\n👨\u200d👩\u200d👧 family\n\n👍🏻 thumbs up"
	rows := block.Render(ctx)

	var plainJoined strings.Builder
	for _, r := range rows {
		plainJoined.WriteString(r.Plain + "\n")
	}
	plain := plainJoined.String()

	if !strings.Contains(plain, "🇺🇸") {
		t.Fatalf("flag emoji lost, got:\n%s", plain)
	}
	if !strings.Contains(plain, "👨\u200d👩\u200d👧") {
		t.Fatalf("ZWJ family emoji lost, got:\n%s", plain)
	}
	if !strings.Contains(plain, "👍🏻") {
		t.Fatalf("skin tone emoji lost, got:\n%s", plain)
	}
}

func TestFencedCodeBlock_PreservesOriginalText(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "示例：\n\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\n结束"
	rows := block.Render(ctx)

	var plainJoined strings.Builder
	for _, r := range rows {
		plainJoined.WriteString(r.Plain + "\n")
	}
	plain := plainJoined.String()

	if !strings.Contains(plain, "```go") {
		t.Fatalf("fence delimiter not preserved, got:\n%s", plain)
	}
	if !strings.Contains(plain, "func main()") {
		t.Fatalf("code content lost, got:\n%s", plain)
	}
	if !strings.Contains(plain, "fmt.Println") {
		t.Fatalf("code content lost, got:\n%s", plain)
	}
}

func TestPlainStyled_RowCountConsistency(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	testCases := []string{
		"plain text",
		"# Heading\n\nBody text",
		"```\ncode\n```",
		"- item 1\n- item 2",
		"> quoted text",
		"你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手",
		"Hello 🇺🇸 World",
	}

	for _, raw := range testCases {
		block := NewAssistantBlock()
		block.Raw = raw
		rows := block.Render(ctx)
		for i, r := range rows {
			styledPlain := ansi.Strip(r.Styled)
			if styledPlain != r.Plain {
				t.Errorf("raw=%q row %d: Strip(Styled)=%q != Plain=%q", raw, i, styledPlain, r.Plain)
			}
		}
	}
}

func TestPlainStyled_ViewportConsistency(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})

	block := NewAssistantBlock()
	block.Raw = "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手\n\n```\ncode here\n```"
	m.doc.Append(block)
	m.syncViewportContent()

	if len(m.viewportStyledLines) != len(m.viewportPlainLines) {
		t.Fatalf("styled line count (%d) != plain line count (%d)",
			len(m.viewportStyledLines), len(m.viewportPlainLines))
	}
}

func TestSelection_PlainRowsMatchDisplay(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	block := NewAssistantBlock()
	block.Raw = "你好！我是 caelis。\n\n# 关于我\n\n- 我是一个 AI 助手"
	m.doc.Append(block)
	m.syncViewportContent()

	// Plain lines should contain the actual display text.
	joined := strings.Join(m.viewportPlainLines, "\n")
	if !strings.Contains(joined, "关于我") {
		t.Fatalf("plain lines missing heading text, got:\n%s", joined)
	}
	if !strings.Contains(joined, "你好！") {
		t.Fatalf("plain lines missing body text, got:\n%s", joined)
	}
}

func TestStreaming_AssistantPartial(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Stream assistant answer in chunks.
	chunks := []string{"你好", "！我是", " caelis", "。"}
	for _, chunk := range chunks {
		m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: chunk})
	}

	full := strings.Join(chunks, "")
	m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: full, Final: true})

	vpView := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpView, "你好") {
		t.Fatalf("streaming assistant: missing '你好', got:\n%s", vpView)
	}
	if !strings.Contains(vpView, "caelis") {
		t.Fatalf("streaming assistant: missing 'caelis', got:\n%s", vpView)
	}
}

func TestStreaming_ReasoningPartial(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	chunks := []string{"让我", "思考", "一下"}
	for _, chunk := range chunks {
		m.Update(tuievents.AssistantStreamMsg{Kind: "reasoning", Text: chunk})
	}

	// Reasoning is visible while streaming (before final).
	vpView := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpView, "让我思考一下") {
		t.Fatalf("streaming reasoning: missing content during stream, got:\n%s", vpView)
	}

	// After final, reasoning block is removed (by design).
	full := strings.Join(chunks, "")
	m.Update(tuievents.AssistantStreamMsg{Kind: "reasoning", Text: full, Final: true})
	vpViewAfter := ansi.Strip(m.viewport.View())
	if strings.Contains(vpViewAfter, "让我思考一下") {
		t.Fatalf("reasoning should be removed after final, but still present")
	}
}

func TestStreaming_CodeFencePartial(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Stream a code fence incrementally.
	tokens := []string{
		"示例", "：", "\n", "\n",
		"```", "go", "\n",
		"func", " main", "()", " {}", "\n",
		"```",
	}
	for _, tok := range tokens {
		m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: tok})
	}

	full := strings.Join(tokens, "")
	m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: full, Final: true})

	vpView := ansi.Strip(m.viewport.View())
	if !strings.Contains(vpView, "```go") {
		t.Fatalf("streaming code fence: fence delimiter missing, got:\n%s", vpView)
	}
	if !strings.Contains(vpView, "func main()") {
		t.Fatalf("streaming code fence: code content missing, got:\n%s", vpView)
	}
}

func TestReasoningBlock_PlainStyledConsistent(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewReasoningBlock()
	block.Raw = "分析问题\n\n# 步骤一\n\n- 检查输入"
	rows := block.Render(ctx)

	for i, r := range rows {
		styledPlain := ansi.Strip(r.Styled)
		if styledPlain != r.Plain {
			t.Errorf("reasoning row %d: Strip(Styled)=%q != Plain=%q", i, styledPlain, r.Plain)
		}
	}
}

func TestAssistantBlock_InlineMarkdownStripped(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "这是 **加粗**、*斜体* 和 `代码` 的文本"
	rows := block.Render(ctx)

	for _, r := range rows {
		if strings.Contains(r.Plain, "**") {
			t.Fatalf("bold markers not stripped from plain, got %q", r.Plain)
		}
		if strings.Contains(r.Plain, "*斜体*") || strings.Contains(r.Plain, "`代码`") {
			t.Fatalf("inline markdown markers not stripped from plain, got %q", r.Plain)
		}
		if ansi.Strip(r.Styled) != r.Plain {
			t.Fatalf("expected styled/plain parity, got styled=%q plain=%q", ansi.Strip(r.Styled), r.Plain)
		}
		if !strings.Contains(r.Plain, "加粗") || !strings.Contains(r.Plain, "斜体") || !strings.Contains(r.Plain, "代码") {
			t.Fatalf("inline markdown content missing, got %q", r.Plain)
		}
	}
}

func TestAssistantBlock_EmptyRaw(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = ""
	rows := block.Render(ctx)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for empty block, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Plain, "* ") {
		t.Fatalf("expected '* ' prefix for empty block, got %q", rows[0].Plain)
	}
}

func TestAssistantBlock_BlockMathNormalized(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "公式：\n\n$$\n\\frac{a}{b}\n$$"
	rows := block.Render(ctx)

	var plainJoined strings.Builder
	for _, r := range rows {
		plainJoined.WriteString(r.Plain + "\n")
	}
	plain := plainJoined.String()

	if !strings.Contains(plain, "\\frac{a}{b}") {
		t.Fatalf("block math content lost, got:\n%s", plain)
	}
	if strings.Contains(plain, "$$") {
		t.Fatalf("block math delimiters should be normalized away, got:\n%s", plain)
	}
}

func TestAssistantBlock_CodeFenceDollarSignsPreserved(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "Here is code:\n\n```bash\necho $HOME\nlet x=$((1+2))\n```"
	rows := block.Render(ctx)

	var plains []string
	for _, r := range rows {
		plains = append(plains, r.Plain)
	}
	joined := strings.Join(plains, "\n")
	if !strings.Contains(joined, "echo $HOME") {
		t.Errorf("code fence dollar sign was mutated, got:\n%s", joined)
	}
	if !strings.Contains(joined, "let x=$((1+2))") {
		t.Errorf("code fence arithmetic was mutated, got:\n%s", joined)
	}
}

func TestAssistantBlock_InlineMathInsideCodeFencePreserved(t *testing.T) {
	ctx := BlockRenderContext{Width: 80, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = "Outside $x^2$ math\n\n```\n$alpha$ inside fence\n```"
	rows := block.Render(ctx)

	var plains []string
	for _, r := range rows {
		plains = append(plains, r.Plain)
	}
	joined := strings.Join(plains, "\n")

	// Outside fence: math should be normalized (dollar signs removed).
	if strings.Contains(joined, "$x^2$") {
		t.Errorf("inline math outside fence should be normalized, got:\n%s", joined)
	}
	// Inside fence: dollar signs must be preserved verbatim.
	if !strings.Contains(joined, "$alpha$ inside fence") {
		t.Errorf("code fence content should be preserved, got:\n%s", joined)
	}
}

func TestStreaming_PlainFirstWrap_AssistantLine(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Simulate streaming: set streamLine to a long assistant line that will wrap.
	longText := "* " + strings.Repeat("你好世界", 20) // ~160 CJK chars
	m.streamLine = longText
	m.lastCommittedStyle = tuikit.LineStyleAssistant

	m.syncViewportContent()

	// After sync, viewport should have plain and styled lines in sync.
	if len(m.viewportStyledLines) == 0 {
		t.Fatal("expected viewport lines after sync")
	}
	if len(m.viewportStyledLines) != len(m.viewportPlainLines) {
		t.Fatalf("styled/plain line count mismatch: styled=%d, plain=%d",
			len(m.viewportStyledLines), len(m.viewportPlainLines))
	}

	// Each plain line should be derivable from (equal to ansi.Strip of) its styled counterpart.
	for i, styled := range m.viewportStyledLines {
		plain := m.viewportPlainLines[i]
		stripped := ansi.Strip(styled)
		if stripped != plain {
			t.Errorf("line %d: plain %q != stripped styled %q", i, plain, stripped)
		}
	}
}
