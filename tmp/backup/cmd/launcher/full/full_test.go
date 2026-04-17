package full

import (
	"context"
	"strings"
	"testing"
)

func TestNewLauncherExposesOnlyConsoleAndACP(t *testing.T) {
	launcher := NewLauncher(
		func(context.Context, []string) error { return nil },
		func(context.Context, []string) error { return nil },
	)
	syntax := launcher.CommandLineSyntax()
	if !strings.Contains(syntax, "console") || !strings.Contains(syntax, "acp") {
		t.Fatalf("expected console and acp in launcher syntax, got %q", syntax)
	}
	if strings.Contains(syntax, "api") || strings.Contains(syntax, "web") {
		t.Fatalf("did not expect api/web in launcher syntax, got %q", syntax)
	}
}
