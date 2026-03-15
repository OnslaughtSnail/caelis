package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestTailLines_Empty(t *testing.T) {
	if got := tailLines("", 5); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestTailLines_FewerThanN(t *testing.T) {
	got := tailLines("a\nb\nc", 5)
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestTailLines_ExactlyN(t *testing.T) {
	got := tailLines("a\nb\nc", 3)
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestTailLines_MoreThanN(t *testing.T) {
	got := tailLines("a\nb\nc\nd\ne\nf", 3)
	want := "...\nd\ne\nf"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTailLines_TrailingNewlines(t *testing.T) {
	got := tailLines("a\nb\nc\n\n\n", 5)
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestTailLines_OnlyNewlines(t *testing.T) {
	got := tailLines("\n\n\n", 5)
	if got != "" {
		t.Fatalf("expected empty after stripping trailing newlines, got %q", got)
	}
}

func TestLooksLikeMarkdown(t *testing.T) {
	if !looksLikeMarkdown("## title\n\n- a\n- b") {
		t.Fatal("expected markdown to be detected")
	}
	if !looksLikeMarkdown("行内公式 $E=mc^2$") {
		t.Fatal("expected inline math markdown to be detected")
	}
	if looksLikeMarkdown("价格从 $5 到 $10") {
		t.Fatal("did not expect currency text to be detected as markdown")
	}
	if looksLikeMarkdown("plain response without markdown markers") {
		t.Fatal("did not expect plain text to be detected as markdown")
	}
}

func TestRenderAssistantMarkdownKeepsPlainText(t *testing.T) {
	in := "plain response"
	if got := renderAssistantMarkdown(in); got != in {
		t.Fatalf("expected plain text unchanged, got %q", got)
	}
}

func TestRenderAssistantMarkdownHidesHeadingMarkers(t *testing.T) {
	in := "## Heading"
	got := renderAssistantMarkdown(in)
	if strings.Contains(got, "## Heading") {
		t.Fatalf("expected heading marker to be hidden, got %q", got)
	}
	if !strings.Contains(got, "Heading") {
		t.Fatalf("expected heading text preserved, got %q", got)
	}
}

func TestRenderAssistantMarkdownFormatsInlineMathAsCode(t *testing.T) {
	got := ansi.Strip(renderAssistantMarkdown("结果是 $E=mc^2$"))
	if !strings.Contains(got, "E=mc^2") {
		t.Fatalf("expected inline math content preserved, got %q", got)
	}
	if strings.Contains(got, "$E=mc^2$") {
		t.Fatalf("expected inline math delimiters normalized, got %q", got)
	}
}

func TestRenderAssistantMarkdownKeepsCurrencyTextPlain(t *testing.T) {
	in := "价格从 $5 到 $10"
	if got := ansi.Strip(renderAssistantMarkdown(in)); got != in {
		t.Fatalf("expected currency text unchanged, got %q", got)
	}
}

func TestRenderAssistantMarkdownFormatsBlockMathAsCodeBlock(t *testing.T) {
	got := ansi.Strip(renderAssistantMarkdown("$$\nx = {-b \\pm \\sqrt{b^2-4ac} \\over 2a}\n$$"))
	if !strings.Contains(got, "x = {-b \\pm \\sqrt{b^2-4ac} \\over 2a}") {
		t.Fatalf("expected block math content preserved, got %q", got)
	}
	if strings.Contains(got, "$$") {
		t.Fatalf("expected block math delimiters normalized, got %q", got)
	}
}

func TestPrintEventBuffersAnswerPartialsUntilFinal(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}

	printEvent(&session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "## He"},
		Meta:    map[string]any{"partial": true, "channel": "answer"},
	}, state)
	printEvent(&session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "ading"},
		Meta:    map[string]any{"partial": true, "channel": "answer"},
	}, state)

	if got := out.String(); got != "" {
		t.Fatalf("expected no assistant partial output before final, got %q", got)
	}

	printEvent(&session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "## Heading\n\n- item"},
	}, state)

	got := ansi.Strip(out.String())
	if strings.Count(got, "Heading") != 1 {
		t.Fatalf("expected one final rendered answer, got %q", got)
	}
	if !strings.Contains(got, "• item") {
		t.Fatalf("expected list markdown to be rendered, got %q", got)
	}
}

func TestPrintEvent_AssistantReasoningRendersBeforeToolCall(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		showReasoning:    true,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(&session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "think first",
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "LIST", Args: `{"path":"."}`},
			},
		},
	}, state)

	got := ansi.Strip(out.String())
	reasoningPos := strings.Index(got, "│ think first")
	callPos := strings.Index(got, "▸ LIST")
	if reasoningPos < 0 || callPos < 0 {
		t.Fatalf("expected reasoning and tool call output, got %q", got)
	}
	if reasoningPos > callPos {
		t.Fatalf("expected reasoning to render before tool call, got %q", got)
	}
}

func TestPrintEvent_NoticeWarningRenders(t *testing.T) {
	var out bytes.Buffer
	state := &renderState{
		out:              &out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	printEvent(session.MarkNotice(&session.Event{}, session.NoticeLevelWarn, "llm request failed, retrying in 2s (1/5)"), state)

	got := ansi.Strip(out.String())
	if !strings.Contains(got, "! llm request failed, retrying in 2s (1/5)") {
		t.Fatalf("expected warning output, got %q", got)
	}
}

func TestVisibleUserTextCombinesImagesAndText(t *testing.T) {
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentPartImage, FileName: "clipboard.png"},
			{Type: model.ContentPartText, Text: "猜猜这是什么 APP"},
		},
	}
	if got := visibleUserText(msg); got != "[image: clipboard.png] 猜猜这是什么 APP" {
		t.Fatalf("unexpected visible user text %q", got)
	}
}
