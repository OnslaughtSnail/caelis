package task

import (
	"strings"
	"testing"
)

func TestFormatLatestOutput_ClipsToRecentLinesAndTruncatesLongLine(t *testing.T) {
	long := strings.Repeat("x", 220)
	text := strings.Join([]string{
		"line-1",
		"line-2",
		"line-3",
		"line-4",
		long,
	}, "\n")
	got := FormatLatestOutput(text)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected latest 4 lines, got %d: %q", len(lines), got)
	}
	if lines[0] != "line-2" {
		t.Fatalf("expected oldest retained line to be line-2, got %q", lines[0])
	}
	if !strings.Contains(lines[3], "...") {
		t.Fatalf("expected long line to be truncated, got %q", lines[3])
	}
	if len([]rune(lines[3])) >= len([]rune(long)) {
		t.Fatalf("expected truncated line to be shorter than original, got %q", lines[3])
	}
	if !strings.HasPrefix(lines[3], strings.Repeat("x", 20)) || !strings.HasSuffix(lines[3], strings.Repeat("x", 20)) {
		t.Fatalf("expected truncated line to preserve head and tail, got %q", lines[3])
	}
	if strings.Contains(got, "line-1") {
		t.Fatalf("expected earliest line to be dropped, got %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected blank lines to be elided, got %q", got)
	}
	if !strings.Contains(got, "line-4") {
		t.Fatalf("expected recent lines to be preserved, got %q", got)
	}
	if strings.Count(got, "\n") != 3 {
		t.Fatalf("expected exactly 4 rendered lines, got %q", got)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("expected CRLF to normalize, got %q", got)
	}
	if lines[3] == long {
		t.Fatalf("expected long line to change, got %q", lines[3])
	}
	if strings.TrimSpace(got) != got {
		t.Fatalf("expected preview to be trimmed, got %q", got)
	}
	if strings.Contains(got, "line-1\nline-2") {
		t.Fatalf("expected only trailing window, got %q", got)
	}
	if len(lines[3]) <= 10 {
		t.Fatalf("expected useful truncated line, got %q", lines[3])
	}
	if got == "" {
		t.Fatal("expected non-empty preview")
	}
	if strings.Contains(got, long) {
		t.Fatalf("expected original long line not to survive intact, got %q", got)
	}
	if !strings.Contains(got, "line-2\nline-3\nline-4") {
		t.Fatalf("expected last short lines to remain in order, got %q", got)
	}
	if strings.Count(got, "...") != 1 {
		t.Fatalf("expected exactly one truncation marker, got %q", got)
	}
	if strings.Contains(got, "line-1\nline-2\nline-3\nline-4") {
		t.Fatalf("expected oldest line not retained, got %q", got)
	}
	if !strings.Contains(got, lines[3]) {
		t.Fatalf("expected final line to be part of output, got %q", got)
	}
}

func TestMergeLatestOutput_PreservesRecentTail(t *testing.T) {
	got := MergeLatestOutput("line-1\nline-2\nline-3", "line-4\nline-5")
	if want := "line-2\nline-3\nline-4\nline-5"; got != want {
		t.Fatalf("MergeLatestOutput() = %q, want %q", got, want)
	}
}
