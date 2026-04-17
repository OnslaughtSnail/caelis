package execenv

import (
	"strings"
	"testing"
)

func TestCommandOutputSummary_UsesStdoutWhenStderrEmpty(t *testing.T) {
	got := commandOutputSummary(CommandResult{
		Stdout: "line1\nline2\nline3\nline4\nline5",
	})
	if !strings.Contains(got, "stderr=<empty>") {
		t.Fatalf("expected empty stderr marker, got %q", got)
	}
	if !strings.Contains(got, "stdout=...") {
		t.Fatalf("expected truncated stdout summary, got %q", got)
	}
}

func TestCommandOutputSummary_ReportsEmptyStreams(t *testing.T) {
	got := commandOutputSummary(CommandResult{})
	if !strings.Contains(got, "stderr=<empty>; stdout=<empty>") {
		t.Fatalf("unexpected summary: %q", got)
	}
}
