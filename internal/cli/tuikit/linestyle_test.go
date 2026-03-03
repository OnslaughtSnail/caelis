package tuikit

import (
	"strings"
	"testing"
)

func TestDetectLineStyle(t *testing.T) {
	tests := []struct {
		line string
		want LineStyle
	}{
		{"* Hello world", LineStyleAssistant},
		{"│ thinking about it", LineStyleReasoning},
		{"> user input", LineStyleUser},
		{"▸ tool_call {}", LineStyleTool},
		{"✓ tool_result ok", LineStyleTool},
		{"? Approval: run", LineStyleTool},
		{"error: something broke", LineStyleError},
		{"warn: be careful", LineStyleWarn},
		{"note: fyi", LineStyleNote},
		{"  +added line", LineStyleDiffAdd},
		{"  -removed line", LineStyleDiffRemove},
		{"  --- old/file.go", LineStyleDiffHeader},
		{"  +++ new/file.go", LineStyleDiffHeader},
		{"  @@ -5,1 +5,1 @@", LineStyleDiffHunk},
		{"  key  value", LineStyleKeyValue},
		{"Section Title", LineStyleSection},
		{"", LineStyleDefault},
		{"   ", LineStyleDefault},
		{"some: mixed text", LineStyleDefault},
	}
	for _, tt := range tests {
		got := DetectLineStyle(tt.line)
		if got != tt.want {
			t.Errorf("DetectLineStyle(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestIsConversationStyle(t *testing.T) {
	if !IsConversationStyle(LineStyleAssistant) {
		t.Error("expected Assistant to be conversation style")
	}
	if !IsConversationStyle(LineStyleUser) {
		t.Error("expected User to be conversation style")
	}
	if !IsConversationStyle(LineStyleReasoning) {
		t.Error("expected Reasoning to be conversation style")
	}
	if IsConversationStyle(LineStyleTool) {
		t.Error("expected Tool to NOT be conversation style")
	}
	if IsConversationStyle(LineStyleDefault) {
		t.Error("expected Default to NOT be conversation style")
	}
}

func TestShouldInsertGap(t *testing.T) {
	// No previous line — no gap.
	if ShouldInsertGap(false, LineStyleDefault, LineStyleAssistant) {
		t.Error("no gap expected without previous")
	}
	// Conversation turn after something — gap.
	if !ShouldInsertGap(true, LineStyleTool, LineStyleAssistant) {
		t.Error("expected gap before assistant after tool")
	}
	if !ShouldInsertGap(true, LineStyleAssistant, LineStyleUser) {
		t.Error("expected gap before user after assistant")
	}
	// Non-conversation line — no gap.
	if ShouldInsertGap(true, LineStyleAssistant, LineStyleTool) {
		t.Error("no gap expected before tool line")
	}
}

func TestColorizeLogLine(t *testing.T) {
	theme := DefaultTheme()
	// Just verify the function doesn't panic and returns non-empty for each style.
	for style := LineStyleDefault; style <= LineStyleDiffHunk; style++ {
		line := "test line"
		result := ColorizeLogLine(line, style, theme)
		if result == "" {
			t.Errorf("ColorizeLogLine returned empty for style %d", style)
		}
	}
}

func TestColorizeAssistantPrefix(t *testing.T) {
	theme := DefaultTheme()
	result := ColorizeLogLine("* hello", LineStyleAssistant, theme)
	// In non-TTY (CI) environments lipgloss may strip colors; just ensure
	// the textual content is preserved.
	if result == "" {
		t.Error("expected non-empty output")
	}
	if len(result) < len("* hello") {
		t.Errorf("expected at least original length, got %d", len(result))
	}
}

func TestColorizeToolCall(t *testing.T) {
	theme := DefaultTheme()
	result := ColorizeLogLine("▸ read_file {path: /foo}", LineStyleTool, theme)
	if result == "" {
		t.Error("expected non-empty colored tool call output")
	}
}

func TestColorizeToolResult(t *testing.T) {
	theme := DefaultTheme()
	result := ColorizeLogLine("✓ read_file success", LineStyleTool, theme)
	if result == "" {
		t.Error("expected non-empty colored tool result output")
	}
}

func TestColorizeUserLine_PreservesMentionToken(t *testing.T) {
	theme := DefaultTheme()
	result := ColorizeLogLine("> please check @deploy/build.sh, now", LineStyleUser, theme)
	if result == "" {
		t.Fatal("expected non-empty user line")
	}
	if !strings.Contains(result, "@deploy/build.sh") {
		t.Fatalf("expected mention token preserved, got %q", result)
	}
}

func TestColorizeDiffLines(t *testing.T) {
	theme := DefaultTheme()
	add := ColorizeLogLine("  +new code", LineStyleDiffAdd, theme)
	if add == "" {
		t.Error("expected non-empty diff add")
	}
	remove := ColorizeLogLine("  -old code", LineStyleDiffRemove, theme)
	if remove == "" {
		t.Error("expected non-empty diff remove")
	}
}

func TestCountLeadingSpaces(t *testing.T) {
	if countLeadingSpaces("  hello") != 2 {
		t.Error("expected 2")
	}
	if countLeadingSpaces("\thello") != 4 {
		t.Error("expected 4 for tab")
	}
	if countLeadingSpaces("hello") != 0 {
		t.Error("expected 0")
	}
}

// ---------------------------------------------------------------------------
// Block continuation tests (DetectLineStyleWithContext)
// ---------------------------------------------------------------------------

func TestBlockContinuationFromReasoning(t *testing.T) {
	// First line detected as reasoning.
	first := DetectLineStyleWithContext("│ thinking about it", LineStyleDefault)
	if first != LineStyleReasoning {
		t.Fatalf("expected reasoning, got %d", first)
	}
	// Continuation line with no prefix should inherit reasoning.
	cont := DetectLineStyleWithContext("still thinking here", LineStyleReasoning)
	if cont != LineStyleReasoning {
		t.Fatalf("expected reasoning continuation, got %d", cont)
	}
}

func TestBlockContinuationFromAssistant(t *testing.T) {
	first := DetectLineStyleWithContext("* hello world", LineStyleDefault)
	if first != LineStyleAssistant {
		t.Fatalf("expected assistant, got %d", first)
	}
	cont := DetectLineStyleWithContext("more text from assistant", LineStyleAssistant)
	if cont != LineStyleAssistant {
		t.Fatalf("expected assistant continuation, got %d", cont)
	}
}

func TestBlockContinuationFromTool(t *testing.T) {
	first := DetectLineStyleWithContext("▸ read_file {}", LineStyleDefault)
	if first != LineStyleTool {
		t.Fatalf("expected tool, got %d", first)
	}
	cont := DetectLineStyleWithContext("  some tool output", LineStyleTool)
	if cont != LineStyleTool {
		t.Fatalf("expected tool continuation, got %d", cont)
	}
}

func TestBlockContinuationBreaksOnNewPrefix(t *testing.T) {
	// Reasoning followed by explicit assistant line.
	next := DetectLineStyleWithContext("* new assistant response", LineStyleReasoning)
	if next != LineStyleAssistant {
		t.Fatalf("expected new prefix to override continuation, got %d", next)
	}
}

func TestBlockContinuationNotFromUser(t *testing.T) {
	// User style should NOT be continuable.
	cont := DetectLineStyleWithContext("plain text", LineStyleUser)
	if cont == LineStyleUser {
		t.Fatal("expected plain text after user NOT to inherit user style")
	}
}

func TestBlockContinuationEmptyLineResets(t *testing.T) {
	// Empty line should always be default, not inherit.
	cont := DetectLineStyleWithContext("", LineStyleReasoning)
	if cont != LineStyleDefault {
		t.Fatalf("expected default for empty line, got %d", cont)
	}
}

func TestIsBlockContinuable(t *testing.T) {
	if !isBlockContinuable(LineStyleAssistant) {
		t.Error("assistant should be continuable")
	}
	if !isBlockContinuable(LineStyleReasoning) {
		t.Error("reasoning should be continuable")
	}
	if !isBlockContinuable(LineStyleTool) {
		t.Error("tool should be continuable")
	}
	if isBlockContinuable(LineStyleUser) {
		t.Error("user should NOT be continuable")
	}
	if isBlockContinuable(LineStyleDefault) {
		t.Error("default should NOT be continuable")
	}
}
