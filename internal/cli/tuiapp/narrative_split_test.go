package tuiapp

import (
	"strings"
	"testing"
)

func TestSplitNarrativeBlocks_Plain(t *testing.T) {
	lines := SplitNarrativeBlocks("hello\nworld")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for _, l := range lines {
		if l.Kind != NarrativePlain {
			t.Fatalf("expected NarrativePlain, got %d", l.Kind)
		}
	}
}

func TestSplitNarrativeBlocks_Heading(t *testing.T) {
	lines := SplitNarrativeBlocks("# Hello\n## World\n### Three")
	for _, l := range lines {
		if l.Kind != NarrativeHeading {
			t.Fatalf("expected NarrativeHeading for %q, got %d", l.Text, l.Kind)
		}
	}
}

func TestSplitNarrativeBlocks_CJKHeading(t *testing.T) {
	lines := SplitNarrativeBlocks("# 关于我")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0].Kind != NarrativeHeading {
		t.Fatalf("expected NarrativeHeading, got %d", lines[0].Kind)
	}
	if lines[0].Text != "# 关于我" {
		t.Fatalf("expected original text preserved, got %q", lines[0].Text)
	}
}

func TestSplitNarrativeBlocks_CodeFence(t *testing.T) {
	input := "before\n```python\ndef foo():\n    pass\n```\nafter"
	lines := SplitNarrativeBlocks(input)

	expected := []NarrativeBlockKind{
		NarrativePlain,          // before
		NarrativeCodeFenceDelim, // ```python
		NarrativeCodeFence,      // def foo():
		NarrativeCodeFence,      //     pass
		NarrativeCodeFenceDelim, // ```
		NarrativePlain,          // after
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %+v", len(expected), len(lines), lines)
	}
	for i, exp := range expected {
		if lines[i].Kind != exp {
			t.Errorf("line %d: expected kind %d, got %d (text=%q)", i, exp, lines[i].Kind, lines[i].Text)
		}
	}
}

func TestSplitNarrativeBlocks_UnclosedFence(t *testing.T) {
	input := "hello\n```\ncode here\nmore code"
	lines := SplitNarrativeBlocks(input)
	if lines[0].Kind != NarrativePlain {
		t.Fatalf("line 0: expected plain")
	}
	if lines[1].Kind != NarrativeCodeFenceDelim {
		t.Fatalf("line 1: expected fence delim")
	}
	if lines[2].Kind != NarrativeCodeFence || lines[3].Kind != NarrativeCodeFence {
		t.Fatalf("lines 2,3: expected code fence body")
	}
}

func TestSplitNarrativeBlocks_ListItems(t *testing.T) {
	input := "- item one\n* item two\n1. item three"
	lines := SplitNarrativeBlocks(input)
	for _, l := range lines {
		if l.Kind != NarrativeListItem {
			t.Fatalf("expected NarrativeListItem for %q, got %d", l.Text, l.Kind)
		}
	}
}

func TestSplitNarrativeBlocks_Blockquote(t *testing.T) {
	lines := SplitNarrativeBlocks("> quote text\n> more")
	for _, l := range lines {
		if l.Kind != NarrativeBlockquote {
			t.Fatalf("expected NarrativeBlockquote for %q, got %d", l.Text, l.Kind)
		}
	}
}

func TestSplitNarrativeBlocks_Mixed(t *testing.T) {
	input := "你好！\n\n# 关于我\n\n- 身份：我是你的个人 AI 助手\n\n```\ncode\n```"
	lines := SplitNarrativeBlocks(input)

	expected := []NarrativeBlockKind{
		NarrativePlain,          // 你好！
		NarrativePlain,          // (empty)
		NarrativeHeading,        // # 关于我
		NarrativePlain,          // (empty)
		NarrativeListItem,       // - 身份：…
		NarrativePlain,          // (empty)
		NarrativeCodeFenceDelim, // ```
		NarrativeCodeFence,      // code
		NarrativeCodeFenceDelim, // ```
	}
	if len(lines) != len(expected) {
		for i, l := range lines {
			t.Logf("  line[%d]: kind=%d text=%q", i, l.Kind, l.Text)
		}
		t.Fatalf("expected %d lines, got %d", len(expected), len(lines))
	}
	for i, exp := range expected {
		if lines[i].Kind != exp {
			t.Errorf("line %d: expected kind %d, got %d (text=%q)", i, exp, lines[i].Kind, lines[i].Text)
		}
	}
}

func TestNarrativeToPlainRows_HeadingStripped(t *testing.T) {
	nls := SplitNarrativeBlocks("# 关于我\n## Sub Heading")
	rows := NarrativeToPlainRows(nls)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0] != "关于我" {
		t.Fatalf("expected heading marker stripped, got %q", rows[0])
	}
	if rows[1] != "Sub Heading" {
		t.Fatalf("expected heading marker stripped, got %q", rows[1])
	}
}

func TestNarrativeToPlainRows_CodeFencePreserved(t *testing.T) {
	input := "```go\nfunc main() {}\n```"
	nls := SplitNarrativeBlocks(input)
	rows := NarrativeToPlainRows(nls)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0] != "```go" {
		t.Fatalf("expected fence delimiter preserved, got %q", rows[0])
	}
	if rows[1] != "func main() {}" {
		t.Fatalf("expected code preserved, got %q", rows[1])
	}
	if rows[2] != "```" {
		t.Fatalf("expected closing fence preserved, got %q", rows[2])
	}
}

func TestNarrativeToPlainRows_EmptyLinePreserved(t *testing.T) {
	input := "hello\n\nworld"
	nls := SplitNarrativeBlocks(input)
	rows := NarrativeToPlainRows(nls)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[1] != "" {
		t.Fatalf("expected empty line preserved, got %q", rows[1])
	}
}

func TestSplitNarrativeBlocks_StreamingPartial(t *testing.T) {
	// Simulate streaming: partial text ending mid-line
	partial := "hello\n# head"
	lines := SplitNarrativeBlocks(partial)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].Kind != NarrativePlain {
		t.Fatalf("expected plain, got %d", lines[0].Kind)
	}
	// "# head" is a valid heading even if more text might come
	if lines[1].Kind != NarrativeHeading {
		t.Fatalf("expected heading for partial, got %d", lines[1].Kind)
	}

	// Partial fence: unclosed
	partial2 := "text\n```\ncode so far"
	lines2 := SplitNarrativeBlocks(partial2)
	if lines2[2].Kind != NarrativeCodeFence {
		t.Fatalf("expected code fence for partial, got %d", lines2[2].Kind)
	}
}

func TestNarrativeToPlainRows_EmojiPreserved(t *testing.T) {
	input := "Hello 🇺🇸 World\n👨\u200d👩\u200d👧 family"
	nls := SplitNarrativeBlocks(input)
	rows := NarrativeToPlainRows(nls)
	if !strings.Contains(rows[0], "🇺🇸") {
		t.Fatalf("expected flag emoji preserved, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "👨\u200d👩\u200d👧") {
		t.Fatalf("expected ZWJ emoji preserved, got %q", rows[1])
	}
}

func TestSplitNarrativeBlocks_TildeFence(t *testing.T) {
	input := "~~~\ncode\n~~~"
	lines := SplitNarrativeBlocks(input)
	if lines[0].Kind != NarrativeCodeFenceDelim {
		t.Fatalf("expected tilde fence delimiter, got %d", lines[0].Kind)
	}
	if lines[1].Kind != NarrativeCodeFence {
		t.Fatalf("expected code, got %d", lines[1].Kind)
	}
	if lines[2].Kind != NarrativeCodeFenceDelim {
		t.Fatalf("expected closing tilde fence, got %d", lines[2].Kind)
	}
}

// ---------------------------------------------------------------------------
// Fix 1: Fence-aware math normalization — code fence content is never mutated.
// ---------------------------------------------------------------------------

func TestApplyMathNormalization_SkipsCodeFence(t *testing.T) {
	// Inline math in a code fence must NOT be stripped.
	input := "text with $x^2$ outside\n```\necho $HOME$\nprice is $100$\n```\nmore $y^2$ text"
	nls := SplitNarrativeBlocks(input)
	nls = applyMathNormalization(nls)

	// Code fence body lines must be unchanged.
	for _, nl := range nls {
		if nl.Kind == NarrativeCodeFence {
			if nl.Text != "echo $HOME$" && nl.Text != "price is $100$" {
				t.Errorf("code fence line was mutated: %q", nl.Text)
			}
		}
	}

	// Non-fence lines should have math normalized.
	rows := NarrativeToPlainRows(nls)
	if strings.Contains(rows[0], "$x^2$") {
		t.Errorf("expected inline math stripped outside fence, got %q", rows[0])
	}
	if !strings.Contains(rows[0], "x^2") {
		t.Errorf("expected math body preserved outside fence, got %q", rows[0])
	}
}

func TestApplyMathNormalization_BlockMathOutsideFence(t *testing.T) {
	input := "intro\n$$\n\\frac{a}{b}\n$$\nend"
	nls := SplitNarrativeBlocks(input)
	nls = applyMathNormalization(nls)
	rows := NarrativeToPlainRows(nls)

	// Block math $$ delimiters should be collapsed.
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "$$") {
		t.Errorf("expected block math delimiters removed, got:\n%s", joined)
	}
	if !strings.Contains(joined, "\\frac{a}{b}") {
		t.Errorf("expected math body preserved, got:\n%s", joined)
	}
}

func TestApplyMathNormalization_FenceDelimNotNormalized(t *testing.T) {
	// Ensure fence delimiter lines themselves are not fed to math normalization.
	input := "```\n$x^2$\n```"
	nls := SplitNarrativeBlocks(input)
	nls = applyMathNormalization(nls)
	for _, nl := range nls {
		if nl.Kind == NarrativeCodeFence && nl.Text != "$x^2$" {
			t.Errorf("code fence body mutated: %q", nl.Text)
		}
	}
}

func TestBuildNarrativeRows_PreservesCodeFenceWithDollarSigns(t *testing.T) {
	// End-to-end: dollar signs inside code fences must survive the full pipeline.
	input := "```bash\necho $HOME\nlet x=$((1+2))\n```"
	nls, rows := buildNarrativeRows(input)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	if rows[1] != "echo $HOME" {
		t.Errorf("expected 'echo $HOME', got %q", rows[1])
	}
	if rows[2] != "let x=$((1+2))" {
		t.Errorf("expected 'let x=$((1+2))', got %q", rows[2])
	}
	if nls[1].Kind != NarrativeCodeFence || nls[2].Kind != NarrativeCodeFence {
		t.Errorf("expected code fence kind for body lines")
	}
}

// ---------------------------------------------------------------------------
// Fix 2: Lockstep trimming — nls and plainRows stay synchronized.
// ---------------------------------------------------------------------------

func TestBuildNarrativeRows_LockstepTrimLeading(t *testing.T) {
	// Leading blank lines must be trimmed from both nls and plainRows.
	input := "\n\n# Heading\nBody text"
	nls, rows := buildNarrativeRows(input)
	if len(rows) == 0 {
		t.Fatal("expected non-empty rows")
	}
	if nls[0].Kind != NarrativeHeading {
		t.Errorf("first nls should be heading after trim, got kind=%d", nls[0].Kind)
	}
	if rows[0] != "Heading" {
		t.Errorf("first row should be stripped heading, got %q", rows[0])
	}
	if len(nls) != len(rows) {
		t.Errorf("nls and rows must have same length: nls=%d, rows=%d", len(nls), len(rows))
	}
}

func TestBuildNarrativeRows_LockstepTrimTrailing(t *testing.T) {
	// Trailing blank lines must be trimmed from both.
	input := "Hello\n\n\n"
	nls, rows := buildNarrativeRows(input)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after trailing trim, got %d", len(rows))
	}
	if len(nls) != 1 {
		t.Fatalf("expected 1 nls after trailing trim, got %d", len(nls))
	}
	if rows[0] != "Hello" {
		t.Errorf("expected 'Hello', got %q", rows[0])
	}
}

func TestBuildNarrativeRows_LockstepTrimBoth(t *testing.T) {
	// Leading blank + heading + trailing blank: kind must match row.
	input := "\n\n- list item\n\n"
	nls, rows := buildNarrativeRows(input)
	if len(rows) != 1 || len(nls) != 1 {
		t.Fatalf("expected 1 row/nls, got rows=%d nls=%d", len(rows), len(nls))
	}
	if nls[0].Kind != NarrativeListItem {
		t.Errorf("expected list item kind, got %d", nls[0].Kind)
	}
}
