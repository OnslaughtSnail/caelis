package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func testUI(buf *bytes.Buffer) *ui {
	return newUI(buf, true, false) // noColor=true for deterministic output
}

func TestUI_Section(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Section("Models")
	got := buf.String()
	if got != "Models\n" {
		t.Fatalf("unexpected Section output: %q", got)
	}
}

func TestUI_KeyValue(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.KeyValue("model", "openai/gpt-4o")
	got := buf.String()
	if !strings.Contains(got, "model") || !strings.Contains(got, "openai/gpt-4o") {
		t.Fatalf("unexpected KeyValue output: %q", got)
	}
	// Verify alignment: key should be padded
	if !strings.HasPrefix(got, "  model") {
		t.Fatalf("expected leading indentation, got: %q", got)
	}
}

func TestUI_Numbered(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Numbered(1, "openai")
	got := buf.String()
	if got != "  1) openai\n" {
		t.Fatalf("unexpected Numbered output: %q", got)
	}
}

func TestUI_Success(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Success("Connected: %s\n", "openai/gpt-4o")
	got := buf.String()
	if got != "Connected: openai/gpt-4o\n" {
		t.Fatalf("unexpected Success output: %q", got)
	}
}

func TestUI_Error(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Error("something failed\n")
	got := buf.String()
	if !strings.HasPrefix(got, "error: something failed") {
		t.Fatalf("unexpected Error output: %q", got)
	}
}

func TestUI_ErrorWithHint(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ErrorWithHint("no model configured", "run /connect to add a provider")
	got := buf.String()
	if !strings.Contains(got, "error: no model configured") {
		t.Fatalf("missing error line: %q", got)
	}
	if !strings.Contains(got, "hint: run /connect") {
		t.Fatalf("missing hint line: %q", got)
	}
}

func TestUI_Warn(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Warn("sandbox unavailable\n")
	got := buf.String()
	if !strings.HasPrefix(got, "warn: sandbox unavailable") {
		t.Fatalf("unexpected Warn output: %q", got)
	}
}

func TestUI_Note(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Note("api_key saved.\n")
	got := buf.String()
	if !strings.Contains(got, "note: api_key saved.") {
		t.Fatalf("unexpected Note output: %q", got)
	}
}

func TestUI_ApprovalHeader(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ApprovalHeader("BASH", "execute")
	got := buf.String()
	if !strings.Contains(got, "? Approval: BASH") {
		t.Fatalf("missing approval header: %q", got)
	}
	if !strings.Contains(got, "(execute)") {
		t.Fatalf("missing action: %q", got)
	}
}

func TestUI_ApprovalHeader_NoAction(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ApprovalHeader("BASH", "")
	got := buf.String()
	if strings.Contains(got, "(") {
		t.Fatalf("should not contain action parens: %q", got)
	}
}

func TestUI_ToolAuthHeader(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ToolAuthHeader("WRITE")
	got := buf.String()
	if !strings.Contains(got, "? Authorize tool: WRITE") {
		t.Fatalf("unexpected ToolAuthHeader output: %q", got)
	}
}

func TestUI_ApprovalCommand(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ApprovalCommand("go test ./...")
	got := buf.String()
	if !strings.Contains(got, "$ go test ./...") {
		t.Fatalf("unexpected ApprovalCommand output: %q", got)
	}
}

func TestUI_ApprovalSessionNote(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.ApprovalSessionNote("go test")
	got := buf.String()
	if !strings.Contains(got, "Allowed for the rest of this session: go test") {
		t.Fatalf("unexpected ApprovalSessionNote output: %q", got)
	}
}

func TestUI_Separator(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	u.Separator()
	got := buf.String()
	if got != "---\n" {
		t.Fatalf("unexpected Separator output: %q", got)
	}
}

func TestUI_EventPrefixes_NoColor(t *testing.T) {
	var buf bytes.Buffer
	u := testUI(&buf)
	if got := u.AssistantPrefix(); got != "* " {
		t.Fatalf("AssistantPrefix: %q", got)
	}
	if got := u.ReasoningPrefix(); got != "│ " {
		t.Fatalf("ReasoningPrefix: %q", got)
	}
	if got := u.ToolCallPrefix(1); got != "▸ " {
		t.Fatalf("ToolCallPrefix: %q", got)
	}
	if got := u.ToolResultPrefix(); got != "✓ " {
		t.Fatalf("ToolResultPrefix: %q", got)
	}
	if got := u.SystemPrefix(); got != "! " {
		t.Fatalf("SystemPrefix: %q", got)
	}
}

func TestUI_ColorEnabled_ProducesANSI(t *testing.T) {
	// This test must not run in parallel with other color tests.
	savedEnv, hadEnv := os.LookupEnv("NO_COLOR")
	_ = os.Unsetenv("NO_COLOR")
	defer func() {
		if hadEnv {
			_ = os.Setenv("NO_COLOR", savedEnv)
			return
		}
		_ = os.Unsetenv("NO_COLOR")
	}()

	saved := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = saved }()

	var buf bytes.Buffer
	u := newUI(&buf, false, false)
	u.Section("Test")
	if !strings.Contains(buf.String(), "\033[") {
		t.Fatal("expected ANSI escape sequence in colored output")
	}
}

func TestWrapText_Short(t *testing.T) {
	input := "short line"
	got := WrapText(input, 80, "  ")
	if got != "short line" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestWrapText_LongLine(t *testing.T) {
	input := strings.Repeat("word ", 20)
	wrapped := WrapText(input, 40, "  ")
	for _, line := range strings.Split(wrapped, "\n") {
		// Allow small tolerance for word boundaries
		if len(line) > 45 {
			t.Fatalf("line too long (%d): %q", len(line), line)
		}
	}
}

func TestWrapText_PreservesNewlines(t *testing.T) {
	input := "line1\nline2\nline3"
	got := WrapText(input, 80, "  ")
	if got != input {
		t.Fatalf("expected preserved newlines: %q", got)
	}
}

func TestClosestCommand(t *testing.T) {
	commands := []string{"help", "status", "resume", "connect", "compact"}
	tests := []struct {
		input string
		want  string
	}{
		{"sta", "status"},   // distance 3, within threshold
		{"statu", "status"}, // distance 1
		{"connet", "connect"},
		{"resum", "resume"},
		{"zzzzzz", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := closestCommand(tc.input, commands)
		if got != tc.want {
			t.Errorf("closestCommand(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"abc", "abc", 0},
		{"abc", "axc", 1},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
	}
	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCommandNames(t *testing.T) {
	m := map[string]slashCommand{
		"help":   {Usage: "/help"},
		"exit":   {Usage: "/exit"},
		"status": {Usage: "/status"},
	}
	names := commandNames(m)
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	// Should be sorted
	if names[0] != "exit" || names[1] != "help" || names[2] != "status" {
		t.Fatalf("unexpected order: %v", names)
	}
}
