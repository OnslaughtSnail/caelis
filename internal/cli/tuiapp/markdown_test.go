package tuiapp

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLooksLikeMarkdown_IgnoresCurrencyText(t *testing.T) {
	if looksLikeMarkdown("价格从 $5 到 $10") {
		t.Fatal("did not expect currency text to be detected as markdown")
	}
}

func TestRenderAssistantMarkdown_KeptCurrencyTextPlain(t *testing.T) {
	in := "价格从 $5 到 $10"
	if got := ansi.Strip(renderAssistantMarkdown(in, 80)); got != in {
		t.Fatalf("expected currency text unchanged, got %q", got)
	}
}

func TestRenderAssistantMarkdown_FormatsInlineMath(t *testing.T) {
	got := ansi.Strip(renderAssistantMarkdown("结果是 $E=mc^2$", 80))
	if !strings.Contains(got, "E=mc^2") {
		t.Fatalf("expected inline math content preserved, got %q", got)
	}
	if strings.Contains(got, "$E=mc^2$") {
		t.Fatalf("expected inline math delimiters normalized, got %q", got)
	}
}
