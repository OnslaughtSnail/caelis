package tuiapp

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// Glamour narrative rendering
// ---------------------------------------------------------------------------

func TestGlamourRender_BasicMarkdown(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "# Hello\n\nThis is **bold** and `code`.\n\n- item one\n- item two"
	rendered := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleAssistant)
	if rendered == "" {
		t.Fatal("glamour returned empty output")
	}
	plain := ansi.Strip(rendered)
	// Must contain heading text, bold text, code text, list items.
	for _, want := range []string{"Hello", "bold", "code", "item one", "item two"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in glamour output:\n%s", want, plain)
		}
	}
}

func TestGlamourRender_Empty(t *testing.T) {
	theme := tuikit.DefaultTheme()
	if got := glamourRenderNarrative("", 60, theme, tuikit.LineStyleAssistant); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestGlamourRender_CodeBlock(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "```go\nfmt.Println(\"hello\")\n```"
	rendered := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleAssistant)
	if rendered == "" {
		t.Fatal("glamour returned empty for code block")
	}
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "fmt.Println") {
		t.Fatalf("code content lost: %s", plain)
	}
	if strings.Contains(plain, "```") {
		t.Fatalf("expected glamour to consume fence delimiters, got: %s", plain)
	}
}

func TestGlamourRender_CodeBlockUsesTighterMargin(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "before\n\n```txt\nhello\nworld\n```\n\nafter"
	rendered := glamourRenderNarrative(raw, 40, theme, tuikit.LineStyleAssistant)
	lines := strings.Split(ansi.Strip(rendered), "\n")
	for _, line := range lines {
		if strings.Contains(line, "hello") || strings.Contains(line, "world") {
			if strings.HasPrefix(line, "    ") {
				t.Fatalf("expected tightened code block indent, got %q", line)
			}
		}
	}
}

func TestGlamourRender_IndentedFencedCodeBlock(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "## 技术栈\n\n    ```python\n    languages = [\"Python\", \"Go\"]\n    frameworks = [\"FastAPI\"]\n    ```"
	rendered := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleAssistant)
	if rendered == "" {
		t.Fatal("glamour returned empty for indented fenced code block")
	}
	plain := ansi.Strip(rendered)
	if strings.Contains(plain, "```") {
		t.Fatalf("expected indented fence delimiters to be normalized away, got: %s", plain)
	}
	for _, want := range []string{"技术栈", "languages = [\"Python\", \"Go\"]", "frameworks = [\"FastAPI\"]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in rendered output:\n%s", want, plain)
		}
	}
}

func TestGlamourRender_ReasoningUsesDistinctMutedPalette(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "## Thinking\n\nThis is **reasoning** with a [link](https://example.com)."
	assistant := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleAssistant)
	reasoning := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleReasoning)
	if assistant == "" || reasoning == "" {
		t.Fatal("expected non-empty glamour output")
	}
	if assistant == reasoning {
		t.Fatalf("expected reasoning markdown output to differ from assistant palette")
	}
}

// ---------------------------------------------------------------------------
// Glamour narrative rows — plain/styled alignment
// ---------------------------------------------------------------------------

func TestGlamourNarrativeRows_PlainStyledLineCount(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "# Title\n\nHello **world**.\n\n- one\n- two"
	rows := glamourNarrativeRows("blk-1", raw, "* ", tuikit.LineStyleAssistant, 60, theme)
	if len(rows) == 0 {
		t.Fatal("no rows returned")
	}
	for i, row := range rows {
		if row.BlockID != "blk-1" {
			t.Errorf("row[%d]: wrong blockID %q", i, row.BlockID)
		}
		if !row.PreWrapped {
			t.Errorf("row[%d]: expected PreWrapped=true", i)
		}
		// Plain must not contain ANSI escapes.
		if row.Plain != ansi.Strip(row.Plain) {
			t.Errorf("row[%d]: plain contains ANSI: %q", i, row.Plain)
		}
	}
	// First row should have the role prefix.
	if !strings.HasPrefix(rows[0].Plain, "* ") {
		t.Errorf("first row missing role prefix: %q", rows[0].Plain)
	}
}

func TestGlamourNarrativeRows_SelectionCopy(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "The `FullAccess` permission is required."
	rows := glamourNarrativeRows("blk-2", raw, "* ", tuikit.LineStyleAssistant, 40, theme)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	// When copied (plain), the text must contain "FullAccess" without mid-word break.
	var plainAll strings.Builder
	for _, row := range rows {
		plainAll.WriteString(row.Plain)
	}
	if !strings.Contains(plainAll.String(), "FullAccess") {
		t.Fatalf("FullAccess lost in plain text: %s", plainAll.String())
	}
}

// ---------------------------------------------------------------------------
// Streaming fallback — inline styling with word-wrap
// ---------------------------------------------------------------------------

func TestStreamingWordWrap_NoMidWordBreak(t *testing.T) {
	// Simulate what wrapNarrativeRow does for streaming content.
	text := "This requires FullAccess permission to continue"
	segments := graphemeWordWrap(text, 20)
	joined := strings.Join(segments, "")
	if !strings.Contains(joined, "FullAccess") {
		t.Fatalf("FullAccess split across lines: %v", segments)
	}
	// No segment should end with a partial word.
	for _, seg := range segments {
		trimmed := strings.TrimRight(seg, " ")
		if strings.HasSuffix(trimmed, "Full") && !strings.HasSuffix(trimmed, "FullAccess") {
			t.Fatalf("mid-word break on 'Full': %v", segments)
		}
	}
}

func TestStreamingWordWrap_CJKMixed(t *testing.T) {
	text := "这是一个FullAccess权限的测试"
	segments := graphemeWordWrap(text, 16)
	joined := strings.Join(segments, "")
	if joined != strings.ReplaceAll(text, " ", "") && joined != text {
		// All content must be preserved (spaces may be trimmed at breaks).
		allContent := strings.ReplaceAll(strings.Join(segments, ""), " ", "")
		wantContent := strings.ReplaceAll(text, " ", "")
		if allContent != wantContent {
			t.Fatalf("content lost: want %q, got %q\nsegments: %v", text, joined, segments)
		}
	}
}

// ---------------------------------------------------------------------------
// End-to-end: block render + viewport wrapping
// ---------------------------------------------------------------------------

func TestFinalizedBlock_UsesGlamour(t *testing.T) {
	theme := tuikit.DefaultTheme()
	block := NewAssistantBlock()
	block.Raw = "# Important\n\nHello **world**.\n\n- alpha\n- beta"
	block.Streaming = false
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}
	rows := block.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	// Finalized blocks should produce PreWrapped rows from glamour.
	hasPreWrapped := false
	for _, row := range rows {
		if row.PreWrapped {
			hasPreWrapped = true
			break
		}
	}
	if !hasPreWrapped {
		t.Fatal("expected PreWrapped rows from glamour for finalized block")
	}
	// Verify essential content in plain output.
	var plain strings.Builder
	for _, row := range rows {
		plain.WriteString(row.Plain + "\n")
	}
	for _, want := range []string{"Important", "world", "alpha", "beta"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("missing %q in plain output:\n%s", want, plain.String())
		}
	}
}

func TestFinalizedBlock_LongLinkWrapsWithinViewport(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	raw := "请访问这个链接：\n\nhttps://example.com/" + strings.Repeat("a", 140)
	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: raw, Final: true})

	if len(m.viewportPlainLines) == 0 {
		t.Fatal("expected viewport lines after finalized assistant render")
	}
	maxWidth := maxInt(1, m.viewport.Width())
	for i, line := range m.viewportPlainLines {
		if w := displayColumns(line); w > maxWidth {
			t.Fatalf("viewport line %d exceeds width: got %d want <= %d\nline=%q", i, w, maxWidth, line)
		}
	}
}

func TestStreamingBlock_UsesGlamour(t *testing.T) {
	theme := tuikit.DefaultTheme()
	block := NewAssistantBlock()
	block.Raw = "# Hello\n\nSome text"
	block.Streaming = true // still streaming
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}
	rows := block.Render(ctx)
	// Streaming blocks should now use PreWrapped glamour rows, matching
	// the finalized rendering path for visual continuity.
	hasPreWrapped := false
	for _, row := range rows {
		if row.PreWrapped {
			hasPreWrapped = true
			break
		}
	}
	if !hasPreWrapped {
		t.Fatal("streaming block should produce PreWrapped rows via glamour")
	}
}

// ---------------------------------------------------------------------------
// Streaming → finalized visual continuity
// ---------------------------------------------------------------------------

func TestStreamingAssistant_VisualContinuity(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "# Important\n\nHello **world**.\n\n- alpha\n- beta"
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}

	// Streaming version.
	streaming := NewAssistantBlock()
	streaming.Raw = raw
	streaming.Streaming = true
	streamRows := streaming.Render(ctx)

	// Finalized version.
	finalized := NewAssistantBlock()
	finalized.Raw = raw
	finalized.Streaming = false
	finalRows := finalized.Render(ctx)

	if len(streamRows) == 0 || len(finalRows) == 0 {
		t.Fatal("expected rows from both streaming and finalized blocks")
	}

	// Line counts should be identical for the same input.
	if len(streamRows) != len(finalRows) {
		t.Fatalf("line count mismatch: streaming=%d finalized=%d", len(streamRows), len(finalRows))
	}

	// Plain text of each line should match.
	for i := range streamRows {
		sp := streamRows[i].Plain
		fp := finalRows[i].Plain
		if sp != fp {
			t.Errorf("line %d plain mismatch:\n  streaming:  %q\n  finalized: %q", i, sp, fp)
		}
	}
}

func TestStreamingReasoning_VisualContinuity(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "Let me think about **this**.\n\n1. First step\n2. Second step"
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}

	streaming := NewReasoningBlock()
	streaming.Raw = raw
	streaming.Streaming = true
	streamRows := streaming.Render(ctx)

	finalized := NewReasoningBlock()
	finalized.Raw = raw
	finalized.Streaming = false
	finalRows := finalized.Render(ctx)

	if len(streamRows) == 0 || len(finalRows) == 0 {
		t.Fatal("expected rows from both streaming and finalized blocks")
	}
	if len(streamRows) != len(finalRows) {
		t.Fatalf("line count mismatch: streaming=%d finalized=%d", len(streamRows), len(finalRows))
	}
	for i := range streamRows {
		if streamRows[i].Plain != finalRows[i].Plain {
			t.Errorf("line %d plain mismatch:\n  streaming:  %q\n  finalized: %q", i, streamRows[i].Plain, finalRows[i].Plain)
		}
	}
}

func TestSplitStableStreamingMarkdown_PicksParagraphBoundary(t *testing.T) {
	raw := strings.Join([]string{
		"# Summary",
		"",
		"First stable paragraph.",
		"",
		"Second stable paragraph.",
		"",
		strings.Repeat("tail ", 40),
	}, "\n")
	stable, tail := splitStableStreamingMarkdown(raw)
	if stable == "" {
		t.Fatal("expected a stable prefix boundary")
	}
	if !strings.Contains(stable, "First stable paragraph") || !strings.Contains(stable, "Second stable paragraph") {
		t.Fatalf("unexpected stable prefix: %q", stable)
	}
	if !strings.Contains(tail, "tail tail") {
		t.Fatalf("expected tail content to remain in tail segment, got %q", tail)
	}
}

func TestStreamingNarrativeRows_FrozenPrefixMatchesCachedGlamourPrefix(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := strings.Join([]string{
		"# Summary",
		"",
		"Stable paragraph one with **markdown**.",
		"",
		"Stable paragraph two with `inline code`.",
		"",
		strings.Repeat("tail content ", 20),
	}, "\n")
	stable, tail := splitStableStreamingMarkdown(raw)
	if stable == "" || tail == "" {
		t.Fatal("expected both stable prefix and live tail")
	}
	prefixRows := cachedStreamingNarrativePrefixRows("blk", stable, "* ", tuikit.LineStyleAssistant, 60, theme)
	streamRows := glamourStreamingNarrativeRows("blk", raw, "* ", tuikit.LineStyleAssistant, 60, theme)
	if len(prefixRows) == 0 || len(streamRows) <= len(prefixRows) {
		t.Fatal("expected hybrid streaming rows with cached prefix and live tail")
	}
	for i := range prefixRows {
		if prefixRows[i].Plain != streamRows[i].Plain {
			t.Fatalf("prefix row %d mismatch:\ncache=%q\nstream=%q", i, prefixRows[i].Plain, streamRows[i].Plain)
		}
	}
	joined := strings.Join(narrativeRowsPlain(streamRows), "\n")
	if !strings.Contains(joined, "tail content") {
		t.Fatalf("expected live tail content in hybrid renderer, got:\n%s", joined)
	}
}

func TestRenderStreamingNarrativeTailRows_PreservesPrefixAndInlinePlainText(t *testing.T) {
	theme := tuikit.DefaultTheme()
	rows := renderStreamingNarrativeTailRows("blk", "**bold** tail with `code`", "* ", tuikit.LineStyleAssistant, 24, theme)
	if len(rows) == 0 {
		t.Fatal("expected lightweight tail rows")
	}
	if !rows[0].PreWrapped {
		t.Fatal("expected lightweight tail rows to be prewrapped")
	}
	plain := strings.Join(narrativeRowsPlain(rows), "\n")
	if !strings.Contains(plain, "* bold tail with code") {
		t.Fatalf("expected inline markdown stripped in plain output, got %q", plain)
	}
	if ansi.Strip(rows[0].Styled) != rows[0].Plain {
		t.Fatalf("expected styled/plain parity, got styled=%q plain=%q", ansi.Strip(rows[0].Styled), rows[0].Plain)
	}
}

func TestAssistantBlock_RenderCacheReusesRowsUntilContentChanges(t *testing.T) {
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: tuikit.DefaultTheme()}
	block := NewAssistantBlock()
	block.Raw = strings.Repeat("paragraph ", 20)
	block.Streaming = false

	first := block.Render(ctx)
	second := block.Render(ctx)
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("expected rendered rows")
	}
	if &first[0] != &second[0] {
		t.Fatal("expected second render to reuse cached row slice")
	}

	block.Raw += "updated"
	third := block.Render(ctx)
	if len(third) == 0 {
		t.Fatal("expected rows after cache invalidation")
	}
	if &third[0] == &second[0] {
		t.Fatal("expected cache invalidation after content change")
	}
}

func narrativeRowsPlain(rows []RenderedRow) []string {
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	return plain
}

// ---------------------------------------------------------------------------
// Unclosed markdown during streaming
// ---------------------------------------------------------------------------

func TestStreamingBlock_UnclosedCodeFence(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "Here is code:\n\n```python\ndef hello():\n    print(\"hi\")"
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}

	block := NewAssistantBlock()
	block.Raw = raw
	block.Streaming = true
	rows := block.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("expected rows for streaming block with unclosed fence")
	}

	// Verify glamour was used (PreWrapped rows).
	hasPreWrapped := false
	for _, row := range rows {
		if row.PreWrapped {
			hasPreWrapped = true
			break
		}
	}
	if !hasPreWrapped {
		t.Fatal("unclosed code fence should still render through glamour")
	}

	// Plain text must contain code content.
	var plain strings.Builder
	for _, row := range rows {
		plain.WriteString(row.Plain + "\n")
	}
	for _, want := range []string{"code", "def hello", "print"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("missing %q in plain output:\n%s", want, plain.String())
		}
	}
	// Fence delimiters should be consumed by glamour, not rendered literally.
	if strings.Contains(plain.String(), "```") {
		t.Errorf("expected glamour to consume fence delimiters, got:\n%s", plain.String())
	}
}

func TestStreamingBlock_UnclosedList(t *testing.T) {
	theme := tuikit.DefaultTheme()
	// Partial list — last item is being typed.
	raw := "Steps:\n\n- First thing\n- Second thing\n- Third"
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}

	block := NewAssistantBlock()
	block.Raw = raw
	block.Streaming = true
	rows := block.Render(ctx)
	if len(rows) == 0 {
		t.Fatal("expected rows")
	}

	var plain strings.Builder
	for _, row := range rows {
		plain.WriteString(row.Plain + "\n")
	}
	for _, want := range []string{"First thing", "Second thing", "Third"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, plain.String())
		}
	}
}

func TestCloseUnclosedCodeFences(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		closed bool // expect fence to be appended
	}{
		{"no fence", "hello world", false},
		{"closed fence", "```go\ncode\n```", false},
		{"unclosed backtick", "```python\ndef f():", true},
		{"unclosed tilde", "~~~\ncode", true},
		{"nested closed", "```\ninner\n```\ntext", false},
		{"two fences one open", "```\ncode\n```\n\n```js\nmore", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := closeUnclosedCodeFences(tt.input)
			hasSuffix := got != tt.input
			if hasSuffix != tt.closed {
				t.Errorf("closeUnclosedCodeFences: expected appended=%v, got result:\n%s", tt.closed, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Copy/selection regression — plain text must be ANSI-free
// ---------------------------------------------------------------------------

func TestStreamingBlock_CopyPlainNoANSI(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "Use `FullAccess` and **bold text**."
	ctx := BlockRenderContext{Width: 60, TermWidth: 80, Theme: theme}

	block := NewAssistantBlock()
	block.Raw = raw
	block.Streaming = true
	rows := block.Render(ctx)
	for i, row := range rows {
		stripped := ansi.Strip(row.Plain)
		if stripped != row.Plain {
			t.Errorf("row[%d] plain contains ANSI: %q (stripped: %q)", i, row.Plain, stripped)
		}
	}
}

// ---------------------------------------------------------------------------
// Glamour cache invalidation — theme/profile changes must produce fresh renderer
// ---------------------------------------------------------------------------

func TestGlamourCache_InvalidatedOnThemeChange(t *testing.T) {
	theme := tuikit.DefaultTheme()
	raw := "## Title\n\nHello **world**"

	// Prime the cache.
	r1 := getGlamourRenderer(60, theme, tuikit.LineStyleAssistant)
	if r1 == nil {
		t.Fatal("expected non-nil renderer")
	}
	// Same params → cache hit → same pointer.
	r2 := getGlamourRenderer(60, theme, tuikit.LineStyleAssistant)
	if r1 != r2 {
		t.Fatal("expected cache hit (same pointer)")
	}

	// Clear cache (simulates applyTheme path).
	clearGlamourCache()

	// Same params → cache was cleared → new renderer.
	r3 := getGlamourRenderer(60, theme, tuikit.LineStyleAssistant)
	if r3 == nil {
		t.Fatal("expected non-nil renderer after cache clear")
	}
	if r3 == r1 {
		t.Fatal("expected new renderer after clearGlamourCache, got same pointer")
	}

	// Smoke check: rendering still works after cache rebuild.
	out := glamourRenderNarrative(raw, 60, theme, tuikit.LineStyleAssistant)
	if !strings.Contains(ansi.Strip(out), "Title") {
		t.Fatalf("render broken after cache clear: %q", out)
	}
}
