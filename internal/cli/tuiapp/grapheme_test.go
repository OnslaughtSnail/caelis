package tuiapp

import (
	"testing"
)

func TestGraphemeWidthASCII(t *testing.T) {
	if w := graphemeWidth("hello"); w != 5 {
		t.Fatalf("expected 5, got %d", w)
	}
}

func TestGraphemeWidthCJK(t *testing.T) {
	// Each CJK char is 2 columns wide.
	if w := graphemeWidth("你好"); w != 4 {
		t.Fatalf("expected 4, got %d", w)
	}
}

func TestGraphemeWidthEmoji(t *testing.T) {
	// Flag emoji (multi-codepoint grapheme cluster).
	if w := graphemeWidth("🇺🇸"); w != 2 {
		t.Fatalf("expected 2 for flag emoji, got %d", w)
	}
	// ZWJ emoji (family).
	if w := graphemeWidth("👨‍👩‍👧"); w != 2 {
		t.Fatalf("expected 2 for ZWJ family emoji, got %d", w)
	}
	// Skin tone modifier emoji.
	if w := graphemeWidth("👍🏻"); w != 2 {
		t.Fatalf("expected 2 for skin tone emoji, got %d", w)
	}
}

func TestGraphemeWidthMixed(t *testing.T) {
	// "Hi" (2) + flag (2) + "!" (1) = 5
	if w := graphemeWidth("Hi🇺🇸!"); w != 5 {
		t.Fatalf("expected 5, got %d", w)
	}
}

func TestGraphemeSliceASCII(t *testing.T) {
	if got := graphemeSlice("hello", 1, 3); got != "el" {
		t.Fatalf("expected %q, got %q", "el", got)
	}
}

func TestGraphemeSliceCJK(t *testing.T) {
	// "你好世界" → each 2 cols: 你(0-1) 好(2-3) 世(4-5) 界(6-7)
	if got := graphemeSlice("你好世界", 2, 6); got != "好世" {
		t.Fatalf("expected %q, got %q", "好世", got)
	}
}

func TestGraphemeSliceEmoji(t *testing.T) {
	// "A🇺🇸B" → A(0) 🇺🇸(1-2) B(3)
	got := graphemeSlice("A🇺🇸B", 1, 3)
	if got != "🇺🇸" {
		t.Fatalf("expected flag emoji, got %q", got)
	}
}

func TestGraphemeSliceEmpty(t *testing.T) {
	if got := graphemeSlice("", 0, 5); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := graphemeSlice("abc", 2, 2); got != "" {
		t.Fatalf("expected empty for zero-width range, got %q", got)
	}
}

func TestGraphemeHardWrapASCII(t *testing.T) {
	lines := graphemeHardWrap("abcdefgh", 3)
	want := []string{"abc", "def", "gh"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(lines), lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line[%d]: expected %q, got %q", i, w, lines[i])
		}
	}
}

func TestGraphemeHardWrapCJK(t *testing.T) {
	// "你好世界" → 8 cols, wrap at 4 → "你好" + "世界"
	lines := graphemeHardWrap("你好世界", 4)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "你好" || lines[1] != "世界" {
		t.Fatalf("expected [你好, 世界], got %v", lines)
	}
}

func TestGraphemeHardWrapEmoji(t *testing.T) {
	// "A🇺🇸B" → 4 cols, wrap at 3 → "A🇺🇸" (3 cols) + "B" (1 col)
	lines := graphemeHardWrap("A🇺🇸B", 3)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "A🇺🇸" {
		t.Fatalf("line[0]: expected %q, got %q", "A🇺🇸", lines[0])
	}
	if lines[1] != "B" {
		t.Fatalf("line[1]: expected %q, got %q", "B", lines[1])
	}
}

func TestGraphemeHardWrapNoWrap(t *testing.T) {
	lines := graphemeHardWrap("short", 80)
	if len(lines) != 1 || lines[0] != "short" {
		t.Fatalf("expected no wrap, got %v", lines)
	}
}

func TestDisplayColumnsGraphemeAware(t *testing.T) {
	// displayColumns now delegates to graphemeWidth.
	if w := displayColumns("🇺🇸"); w != 2 {
		t.Fatalf("expected 2, got %d", w)
	}
}

func TestSliceByDisplayColumnsGraphemeAware(t *testing.T) {
	// sliceByDisplayColumns now delegates to graphemeSlice.
	got := sliceByDisplayColumns("A🇺🇸B", 1, 3)
	if got != "🇺🇸" {
		t.Fatalf("expected flag emoji, got %q", got)
	}
}

func TestSelectionTextWithGraphemeClusters(t *testing.T) {
	lines := []string{"Hello 🇺🇸 World"}
	// "Hello " = 6 cols, "🇺🇸" = 2 cols (cols 6-7), " World" starts at col 8
	got := selectionTextFromLines(lines, textSelectionPoint{0, 6}, textSelectionPoint{0, 8})
	if got != "🇺🇸" {
		t.Fatalf("expected flag emoji selected, got %q", got)
	}
}
