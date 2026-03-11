package tuikit

import (
	"fmt"
	"testing"
)

func TestComposeFooter(t *testing.T) {
	got := ComposeFooter(20, "left", "right")
	if len(got) != 20 {
		t.Fatalf("expected width 20, got %d", len(got))
	}
	if got[:4] != "left" {
		t.Fatalf("expected left prefix, got %q", got)
	}
}

func TestResolveThemeFromEnv_UsesNamedThemeAndAccentOverride(t *testing.T) {
	t.Setenv("CAELIS_THEME", "nord")
	t.Setenv("CAELIS_ACCENT", "#ff9900")
	t.Setenv("COLORTERM", "truecolor")

	theme := ResolveThemeFromEnv()
	if got := stringifyColor(theme.AppBg); got != "#2e3440" {
		t.Fatalf("expected nord app bg, got %q", got)
	}
	if got := stringifyColor(theme.Accent); got != "#ff9900" {
		t.Fatalf("expected accent override, got %q", got)
	}
	if got := stringifyColor(theme.ComposerBorderFocus); got != "#ff9900" {
		t.Fatalf("expected composer focus override, got %q", got)
	}
}

func TestResolveThemeFromEnv_FallsBackTo256Palette(t *testing.T) {
	t.Setenv("CAELIS_THEME", "dracula")
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM", "xterm-256color")

	theme := ResolveThemeFromEnv()
	if got := stringifyColor(theme.AppBg); got != "236" {
		t.Fatalf("expected 256-color fallback app bg, got %q", got)
	}
	if got := stringifyColor(theme.Focus); got != "123" {
		t.Fatalf("expected 256-color fallback focus, got %q", got)
	}
}

func stringifyColor(color interface{}) string {
	return fmt.Sprint(color)
}
