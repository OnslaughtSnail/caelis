package tuiapp

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// graphemeWordWrap — basic wrapping
// ---------------------------------------------------------------------------

func TestWordWrap_ShortLine(t *testing.T) {
	lines := graphemeWordWrap("hello world", 80)
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Fatalf("expected single line, got %v", lines)
	}
}

func TestWordWrap_AtWordBoundary(t *testing.T) {
	lines := graphemeWordWrap("hello world foo", 11)
	// "hello world" = 11 fits, "foo" overflows → two lines
	want := []string{"hello world", "foo"}
	assertLines(t, want, lines)
}

func TestWordWrap_NoMidWordBreak(t *testing.T) {
	// "FullAccess" is 10 chars, width 15 → must not split the word.
	lines := graphemeWordWrap("This FullAccess granted", 15)
	// "This FullAccess" = 15, fits; "granted" next line.
	want := []string{"This FullAccess", "granted"}
	assertLines(t, want, lines)
}

func TestWordWrap_FallbackHardBreak(t *testing.T) {
	// Single token wider than width → hard-break.
	lines := graphemeWordWrap("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	assertLines(t, want, lines)
}

func TestWordWrap_PreservesTrailingContent(t *testing.T) {
	lines := graphemeWordWrap("aa bb cc dd", 5)
	// "aa bb" = 5 → first line; "cc dd" = 5 → second line
	want := []string{"aa bb", "cc dd"}
	assertLines(t, want, lines)
}

// ---------------------------------------------------------------------------
// CJK wrapping
// ---------------------------------------------------------------------------

func TestWordWrap_CJK(t *testing.T) {
	// Each CJK char is 2 columns. "你好世界" = 8 cols, width 4 → 2 lines.
	lines := graphemeWordWrap("你好世界", 4)
	want := []string{"你好", "世界"}
	assertLines(t, want, lines)
}

func TestWordWrap_CJKMixedASCII(t *testing.T) {
	// "Hello 你好" → "Hello " + CJK tokens. Width 10:
	// "Hello " (6) + "你" (2) + "好" (2) = 10 → fits one line.
	lines := graphemeWordWrap("Hello 你好", 10)
	want := []string{"Hello 你好"}
	assertLines(t, want, lines)
}

func TestWordWrap_CJKBreaksBetween(t *testing.T) {
	// "Hello你好World" → ["Hello", "你", "好", "World"]
	// Width 8: "Hello" (5) + "你" (2) = 7, + "好" (2) = 9 > 8 → break.
	lines := graphemeWordWrap("Hello你好World", 8)
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %v", lines)
	}
	joined := strings.Join(lines, "")
	if joined != "Hello你好World" {
		t.Fatalf("content lost: %q", joined)
	}
}

func TestWordWrap_CJKWithSpace(t *testing.T) {
	// "你好 世界" → tokens: ["你", "好", " ", "世", "界"], width 5
	// "你好" (4) + " " (1) = 5, fits → "你好"; "世界" (4) → "世界"
	lines := graphemeWordWrap("你好 世界", 5)
	want := []string{"你好", "世界"}
	assertLines(t, want, lines)
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestWordWrap_Empty(t *testing.T) {
	lines := graphemeWordWrap("", 10)
	if len(lines) != 1 || lines[0] != "" {
		t.Fatalf("expected [\"\"], got %v", lines)
	}
}

func TestWordWrap_ZeroWidth(t *testing.T) {
	lines := graphemeWordWrap("hello", 0)
	if len(lines) != 1 || lines[0] != "hello" {
		t.Fatalf("expected [\"hello\"], got %v", lines)
	}
}

func TestWordWrap_TrailingSpaceTrimmed(t *testing.T) {
	lines := graphemeWordWrap("abc   def", 5)
	for _, l := range lines {
		if l != strings.TrimRight(l, " ") {
			t.Fatalf("trailing space not trimmed: %q", l)
		}
	}
}

// ---------------------------------------------------------------------------
// splitWrapTokens
// ---------------------------------------------------------------------------

func TestSplitWrapTokens_ASCII(t *testing.T) {
	tokens := splitWrapTokens("hello world foo")
	want := []string{"hello ", "world ", "foo"}
	assertTokens(t, want, tokens)
}

func TestSplitWrapTokens_CJK(t *testing.T) {
	tokens := splitWrapTokens("你好世界")
	want := []string{"你", "好", "世", "界"}
	assertTokens(t, want, tokens)
}

func TestSplitWrapTokens_Mixed(t *testing.T) {
	tokens := splitWrapTokens("Hello你好")
	want := []string{"Hello", "你", "好"}
	assertTokens(t, want, tokens)
}

func TestSplitWrapTokens_PathToken(t *testing.T) {
	// Paths should remain as single tokens (no break at /).
	tokens := splitWrapTokens("/path/to/file.go")
	want := []string{"/path/to/file.go"}
	assertTokens(t, want, tokens)
}

// ---------------------------------------------------------------------------
// isCJKRune
// ---------------------------------------------------------------------------

func TestIsCJKRune(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'你', true},
		{'a', false},
		{'1', false},
		{' ', false},
		{'。', true},  // CJK punctuation
		{'ア', true},  // Katakana
		{'가', true},  // Hangul
		{'Ａ', true},  // Fullwidth A
		{'🇺', false}, // Not CJK (flag)
	}
	for _, tt := range tests {
		if got := isCJKRune(tt.r); got != tt.want {
			t.Errorf("isCJKRune(%q) = %v, want %v", tt.r, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func assertLines(t *testing.T, want, got []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("line count: want %d, got %d\nwant: %v\ngot:  %v", len(want), len(got), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func assertTokens(t *testing.T, want, got []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("token count: want %d, got %d\nwant: %v\ngot:  %v", len(want), len(got), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}
