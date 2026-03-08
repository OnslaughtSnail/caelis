package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestEmitAssistantEventToTUI_FinalReasoningThenText(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: true,
	}
	ev := &session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "think",
			Text:      "answer",
		},
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 stream messages, got %d", len(sender.msgs))
	}
	first, ok := sender.msgs[0].(tuievents.AssistantStreamMsg)
	if !ok {
		t.Fatalf("expected first msg AssistantStreamMsg, got %T", sender.msgs[0])
	}
	second, ok := sender.msgs[1].(tuievents.AssistantStreamMsg)
	if !ok {
		t.Fatalf("expected second msg AssistantStreamMsg, got %T", sender.msgs[1])
	}
	if first.Kind != "reasoning" || first.Text != "think" || !first.Final {
		t.Fatalf("unexpected first msg: %+v", first)
	}
	if second.Kind != "answer" || second.Text != "answer" || !second.Final {
		t.Fatalf("unexpected second msg: %+v", second)
	}
}

func TestEmitAssistantEventToTUI_FinalReasoningOnly_NoAnswerFallback(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: true,
	}
	ev := &session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "think only",
		},
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 1 {
		t.Fatalf("expected exactly one reasoning message, got %d", len(sender.msgs))
	}
	msg, ok := sender.msgs[0].(tuievents.AssistantStreamMsg)
	if !ok {
		t.Fatalf("expected AssistantStreamMsg, got %T", sender.msgs[0])
	}
	if msg.Kind != "reasoning" || msg.Text != "think only" || !msg.Final {
		t.Fatalf("unexpected msg: %+v", msg)
	}
}

func TestEmitAssistantEventToTUI_PartialMixedReasoningAndText(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: true,
	}
	ev := &session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "r",
			Text:      "a",
		},
		Meta: map[string]any{
			"partial": true,
			"channel": "answer",
		},
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 stream messages, got %d", len(sender.msgs))
	}
	first := sender.msgs[0].(tuievents.AssistantStreamMsg)
	second := sender.msgs[1].(tuievents.AssistantStreamMsg)
	if first.Kind != "reasoning" || first.Final {
		t.Fatalf("unexpected first msg: %+v", first)
	}
	if second.Kind != "answer" || second.Final {
		t.Fatalf("unexpected second msg: %+v", second)
	}
}

func TestEmitAssistantEventToTUI_HideReasoningWhenDisabled(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: false,
	}
	ev := &session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "hidden",
			Text:      "shown",
		},
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 1 {
		t.Fatalf("expected one answer message, got %d", len(sender.msgs))
	}
	msg := sender.msgs[0].(tuievents.AssistantStreamMsg)
	if msg.Kind != "answer" || msg.Text != "shown" || !msg.Final {
		t.Fatalf("unexpected msg: %+v", msg)
	}
}

func TestForwardEventToTUI_AssistantReasoningBeforeToolCall(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: true,
	}
	ev := &session.Event{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Reasoning: "think first",
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "LIST", Args: `{"path":"."}`},
			},
		},
	}

	handled := c.forwardEventToTUI(ev, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected event to be handled by TUI forwarder")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected reasoning then tool call messages, got %d", len(sender.msgs))
	}
	reasoningMsg, ok := sender.msgs[0].(tuievents.AssistantStreamMsg)
	if !ok {
		t.Fatalf("expected first message AssistantStreamMsg, got %T", sender.msgs[0])
	}
	if reasoningMsg.Kind != "reasoning" || reasoningMsg.Text != "think first" || !reasoningMsg.Final {
		t.Fatalf("unexpected reasoning message: %+v", reasoningMsg)
	}
	callMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second message LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(callMsg.Chunk, "▸ LIST") {
		t.Fatalf("expected tool call chunk, got %q", callMsg.Chunk)
	}
}

func TestForwardEventToTUI_FileToolCallEmitsDiffPreviewBeforeToolResponse(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}
	ev := &session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "WRITE", Args: fmt.Sprintf(`{"path":%q,"content":"new\n"}`, path)},
			},
		},
	}

	handled := c.forwardEventToTUI(ev, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected event to be handled by TUI forwarder")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected tool call and diff block messages, got %d", len(sender.msgs))
	}
	if _, ok := sender.msgs[0].(tuievents.LogChunkMsg); !ok {
		t.Fatalf("expected first message LogChunkMsg, got %T", sender.msgs[0])
	}
	diffMsg, ok := sender.msgs[1].(tuievents.DiffBlockMsg)
	if !ok {
		t.Fatalf("expected second message DiffBlockMsg, got %T", sender.msgs[1])
	}
	if diffMsg.Tool != "WRITE" || diffMsg.Path != "a.txt" {
		t.Fatalf("unexpected diff message: %+v", diffMsg)
	}
}

func TestForwardEventToTUI_FileToolResponseFallsBackToSummaryWhenDiffSkipped(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	oldContent := strings.Repeat("old\n", 500)
	newContent := strings.Repeat("new\n", 500)
	if err := os.WriteFile(path, []byte(oldContent), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "WRITE", Args: fmt.Sprintf(`{"path":%q,"content":%q}`, path, newContent)},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only call log when diff is skipped, got %d messages", len(sender.msgs))
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_1",
				Name: "WRITE",
				Result: map[string]any{
					"path":       path,
					"created":    false,
					"line_count": 500,
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected tool response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected summary fallback after tool response, got %d messages", len(sender.msgs))
	}
	summaryMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected summary LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(summaryMsg.Chunk, "✓ WRITE wrote a.txt (500 lines)") {
		t.Fatalf("unexpected summary chunk %q", summaryMsg.Chunk)
	}
}
