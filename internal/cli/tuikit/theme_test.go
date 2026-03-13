package tuikit

import (
	"fmt"
	"image/color"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
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

func TestResolveThemeForBackground_SelectsLightTheme(t *testing.T) {
	t.Setenv("CAELIS_THEME", "")
	t.Setenv("COLORTERM", "truecolor")

	theme := ResolveThemeForBackground(false)
	if theme.IsDark {
		t.Fatal("expected light theme for light terminal background")
	}
	if got := stringifyColor(theme.TextPrimary); got != "#111827" {
		t.Fatalf("expected readable light-theme text, got %q", got)
	}
	if got := stringifyColor(theme.PanelBorder); got != "#64748b" {
		t.Fatalf("expected light-theme border, got %q", got)
	}
}

func TestThemeUsesAutoBackground(t *testing.T) {
	t.Setenv("CAELIS_THEME", "")
	if !ThemeUsesAutoBackground() {
		t.Fatal("expected empty theme to use auto background detection")
	}

	t.Setenv("CAELIS_THEME", "auto")
	if !ThemeUsesAutoBackground() {
		t.Fatal("expected auto theme to use background detection")
	}

	t.Setenv("CAELIS_THEME", "light")
	if ThemeUsesAutoBackground() {
		t.Fatal("expected explicit light theme to disable auto background detection")
	}
}

func stringifyColor(value interface{}) string {
	switch c := value.(type) {
	case xansi.BasicColor:
		return fmt.Sprintf("%d", c)
	case xansi.IndexedColor:
		return fmt.Sprintf("%d", c)
	case color.RGBA:
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	case color.NRGBA:
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprint(value)
}
