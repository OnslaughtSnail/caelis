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
