package tuiapp

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

func TestLooksLikeMarkdown_IgnoresCurrencyText(t *testing.T) {
	if looksLikeMarkdown("价格从 $5 到 $10") {
		t.Fatal("did not expect currency text to be detected as markdown")
	}
}

func TestRenderAssistantMarkdown_KeptCurrencyTextPlain(t *testing.T) {
	in := "价格从 $5 到 $10"
	if got := strings.TrimSpace(ansi.Strip(renderAssistantMarkdown(in, 80, tuikit.DefaultTheme()))); got != in {
		t.Fatalf("expected currency text unchanged, got %q", got)
	}
}

func TestRenderAssistantMarkdown_FormatsInlineMath(t *testing.T) {
	got := ansi.Strip(renderAssistantMarkdown("结果是 $E=mc^2$", 80, tuikit.DefaultTheme()))
	if !strings.Contains(got, "E=mc^2") {
		t.Fatalf("expected inline math content preserved, got %q", got)
	}
	if strings.Contains(got, "$E=mc^2$") {
		t.Fatalf("expected inline math delimiters normalized, got %q", got)
	}
}

func TestRenderAssistantMarkdown_FormatsBlockMath(t *testing.T) {
	got := ansi.Strip(renderAssistantMarkdown("$$\n\\frac{a}{b}\n$$", 80, tuikit.DefaultTheme()))
	if !strings.Contains(got, "\\frac{a}{b}") {
		t.Fatalf("expected block math content preserved, got %q", got)
	}
	if strings.Contains(got, "$$") {
		t.Fatalf("expected block math delimiters normalized, got %q", got)
	}
}

func TestRenderNarrativeMarkdown_AlwaysPassesThroughRenderer(t *testing.T) {
	got, isMarkdown := renderNarrativeMarkdown("plain text", 80, tuikit.DefaultTheme())
	if isMarkdown {
		t.Fatal("did not expect plain text to be classified as markdown")
	}
	if strings.TrimSpace(ansi.Strip(got)) != "plain text" {
		t.Fatalf("expected plain text preserved after renderer pass, got %q", got)
	}
}

func TestRenderNarrativeMarkdown_PreservesTrailingListMarkerWhitespace(t *testing.T) {
	_, isMarkdown := renderNarrativeMarkdown("文件操作：\n- ", 80, tuikit.DefaultTheme())
	if !isMarkdown {
		t.Fatal("expected trailing list marker whitespace to be preserved for partial markdown detection")
	}
}

func TestRenderNarrativeMarkdown_PreservesPartialMathUntilClosed(t *testing.T) {
	got, isMarkdown := renderNarrativeMarkdown("结果是 $E=mc", 80, tuikit.DefaultTheme())
	if strings.Contains(ansi.Strip(got), "`E=mc`") {
		t.Fatalf("did not expect unclosed math to be normalized, got %q", got)
	}
	if isMarkdown && strings.Contains(ansi.Strip(got), "$E=mc") {
		t.Fatalf("did not expect unclosed math to be rewritten while still incomplete, got %q", got)
	}
}
