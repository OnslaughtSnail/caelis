package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
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

func TestForwardEventToTUI_FileToolResponseEmitsCompactSummaryWhenDiffSkipped(t *testing.T) {
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
		t.Fatalf("expected call log and compact summary after tool response, got %d messages", len(sender.msgs))
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second message LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WRITE +500 -500") {
		t.Fatalf("unexpected compact WRITE summary: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_PatchUsesDiffStatsForInsertedLineSummary(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUIWithOptions(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_patch_insert", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"a\nb","new":"a\nx\nb"}`, path)},
			},
		},
	}, pending, tuiForwardOptions{ShowMutationDiff: false})
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only call log when rich diff is disabled, got %#v", sender.msgs)
	}
	sender.msgs = nil

	handled = c.forwardEventToTUIWithOptions(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_patch_insert",
				Name: "PATCH",
				Result: map[string]any{
					"path":          path,
					"created":       false,
					"replaced":      1,
					"old_count":     1,
					"added_lines":   1,
					"removed_lines": 0,
				},
			},
		},
	}, pending, tuiForwardOptions{ShowMutationDiff: false})
	if !handled {
		t.Fatal("expected tool response to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected one compact summary, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[0].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected LogChunkMsg, got %T", sender.msgs[0])
	}
	if !strings.Contains(logMsg.Chunk, "✓ PATCH +1 -0") {
		t.Fatalf("unexpected compact PATCH summary: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_LargeFileSmallPatchStillEmitsRichDiff(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "large.txt")
	lines := make([]string, 0, 1400)
	for i := 1; i <= 1400; i++ {
		lines = append(lines, fmt.Sprintf("line-%04d", i))
	}
	content := strings.Join(lines, "\n") + "\n"
	oldSnippet := "line-1200\nline-1201\nline-1202"
	newSnippet := "line-1200\ninserted\nline-1201\nline-1202"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_patch_large", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":%q,"new":%q}`, path, oldSnippet, newSnippet)},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call log and rich diff, got %#v", sender.msgs)
	}
	if _, ok := sender.msgs[1].(tuievents.DiffBlockMsg); !ok {
		t.Fatalf("expected rich diff block, got %T", sender.msgs[1])
	}
}

func TestForwardEventToTUI_StagesEarlierPatchForLaterPreviewInSameEvent(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "chain.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"one\ntwo","new":"one\nmid\ntwo"}`, path)},
				{ID: "call_2", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"one\nmid\ntwo","new":"one\nmid\ntwo\nend"}`, path)},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected tool calls to be handled")
	}
	diffBlocks := 0
	for _, raw := range sender.msgs {
		if _, ok := raw.(tuievents.DiffBlockMsg); ok {
			diffBlocks++
		}
	}
	if diffBlocks != 2 {
		t.Fatalf("expected both PATCH calls to render rich diffs, got %#v", sender.msgs)
	}
}

func TestForwardEventToTUI_ReadResponseEmitsCompactSummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_read_1", Name: "READ", Args: `{"path":"state.go"}`},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected READ call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_read_1",
				Name: "READ",
				Result: map[string]any{
					"path":       "state.go",
					"line_count": 120,
					"start_line": 1,
					"end_line":   120,
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected READ response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call and compact read summary, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second message LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ READ 1-120") {
		t.Fatalf("unexpected compact READ summary: %q", logMsg.Chunk)
	}
}

func TestForwardSessionEventToTUI_ProjectsDelegatedAssistantAndReasoning(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:         "parent-session",
		tuiSender:         sender,
		delegatePreviewer: newDelegatePreviewProjector(),
	}

	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Reasoning: "thinking"},
			Meta: map[string]any{
				"partial":             true,
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})
	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "done"},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 projected delegate stream messages, got %d", len(sender.msgs))
	}
	first, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected first message TaskStreamMsg, got %T", sender.msgs[0])
	}
	second, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second message TaskStreamMsg, got %T", sender.msgs[1])
	}
	if first.Label != "DELEGATE" || first.CallID != "call-delegate-1" || first.Stream != "reasoning" || first.Chunk != "thinking" {
		t.Fatalf("unexpected projected reasoning msg: %+v", first)
	}
	if second.Label != "DELEGATE" || second.CallID != "call-delegate-1" || second.Stream != "assistant" || second.Chunk != "done\n" {
		t.Fatalf("unexpected projected assistant msg: %+v", second)
	}
}

func TestForwardSessionEventToTUI_ProjectsDelegatedReasoningPartialWithLeadingSpace(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:         "parent-session",
		tuiSender:         sender,
		delegatePreviewer: newDelegatePreviewProjector(),
	}

	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Reasoning: "I'll run"},
			Meta: map[string]any{
				"partial":             true,
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})
	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Reasoning: "I'll run the bash command"},
			Meta: map[string]any{
				"partial":             true,
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 projected delegate reasoning messages, got %d", len(sender.msgs))
	}
	second, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second message TaskStreamMsg, got %T", sender.msgs[1])
	}
	if second.Chunk != " the bash command" {
		t.Fatalf("expected leading space preserved in reasoning delta, got %q", second.Chunk)
	}
}

func TestForwardSessionEventToTUI_ProjectsNestedChildToolEvents(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:         "parent-session",
		tuiSender:         sender,
		delegatePreviewer: newDelegatePreviewProjector(),
	}

	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-bash-1",
					Name: "BASH",
					Args: `{"command":"echo hi"}`,
				}},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})
	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{
				Role:         model.RoleTool,
				ToolResponse: &model.ToolResponse{ID: "call-bash-1", Name: "BASH", Result: map[string]any{"stdout": "hi\n"}},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})

	if len(sender.msgs) != 2 {
		t.Fatalf("expected projected child tool activity, got %+v", sender.msgs)
	}
	callMsg, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected first message TaskStreamMsg, got %T", sender.msgs[0])
	}
	resultMsg, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second message TaskStreamMsg, got %T", sender.msgs[1])
	}
	if callMsg.Stream != "assistant" || !strings.Contains(callMsg.Chunk, "▸ BASH") {
		t.Fatalf("unexpected child tool call projection: %+v", callMsg)
	}
	if resultMsg.Stream != "assistant" || !strings.Contains(resultMsg.Chunk, "✓ BASH") {
		t.Fatalf("unexpected child tool response projection: %+v", resultMsg)
	}
}

func TestForwardSessionEventToTUI_DelegateWriteKeepsVerboseSummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:         "parent-session",
		tuiSender:         sender,
		delegatePreviewer: newDelegatePreviewProjector(),
	}

	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call-write-1", Name: "WRITE", Args: `{"path":"a.txt","content":"new\n"}`},
				},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})
	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:   "call-write-1",
					Name: "WRITE",
					Result: map[string]any{
						"path":       "a.txt",
						"created":    false,
						"line_count": 1,
					},
				},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})

	if len(sender.msgs) != 2 {
		t.Fatalf("expected projected child write call/response, got %+v", sender.msgs)
	}
	resultMsg, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second message TaskStreamMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(resultMsg.Chunk, "✓ WRITE wrote a.txt (1 lines)") {
		t.Fatalf("expected verbose write summary in delegate preview, got %+v", resultMsg)
	}
	if strings.Contains(resultMsg.Chunk, "+1 -") {
		t.Fatalf("did not expect compact diff counts in delegate preview, got %+v", resultMsg)
	}
}

func TestForwardSessionEventToTUI_FinalizesDelegatedPanelOnChildLifecycle(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:         "parent-session",
		tuiSender:         sender,
		delegatePreviewer: newDelegatePreviewProjector(),
	}

	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			SessionID: "child-session",
			Meta: map[string]any{
				runtime.MetaLifecycle: map[string]any{"status": string(runtime.RunLifecycleStatusCompleted)},
				"kind":                "lifecycle",
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-delegate-1",
				"delegation_id":       "dlg-1",
			},
		},
	})

	if len(sender.msgs) != 1 {
		t.Fatalf("expected one final projected lifecycle message, got %d", len(sender.msgs))
	}
	msg, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected TaskStreamMsg, got %T", sender.msgs[0])
	}
	if msg.Label != "DELEGATE" || msg.CallID != "call-delegate-1" || !msg.Final {
		t.Fatalf("unexpected final delegate lifecycle msg: %+v", msg)
	}
}

func TestForwardEventToTUI_BashSuccessWithoutOutputDoesNotEmitExitCodeLine(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolResponse: &model.ToolResponse{
				ID:   "call_bash",
				Name: "BASH",
				Result: map[string]any{
					"exit_code": 0,
				},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected bash tool response to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only final task-stream marker, got %#v", sender.msgs)
	}
	msg, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected TaskStreamMsg, got %T", sender.msgs[0])
	}
	if !msg.Final || msg.CallID != "call_bash" {
		t.Fatalf("unexpected task stream message: %+v", msg)
	}
}

func TestForwardEventToTUI_BashErrorWithoutOutputEmitsToolResultSummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolResponse: &model.ToolResponse{
				ID:   "call_bash",
				Name: "BASH",
				Result: map[string]any{
					"error": "tool: BASH failed (route=sandbox): tool: sandbox runner is unavailable",
				},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected bash tool response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected final task-stream marker and tool result summary, got %#v", sender.msgs)
	}
	finalMsg, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected TaskStreamMsg, got %T", sender.msgs[0])
	}
	if !finalMsg.Final || finalMsg.Label != "BASH" {
		t.Fatalf("unexpected final bash task message: %#v", finalMsg)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ BASH") || strings.Contains(logMsg.Chunk, "! BASH") {
		t.Fatalf("expected unified tool result line, got %q", logMsg.Chunk)
	}
	if !strings.Contains(logMsg.Chunk, "sandbox runner is unavailable") {
		t.Fatalf("expected bash error details in summary, got %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_PlanSkipsTranscriptAndOnlyUpdatesPanel(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_plan",
				Name: tool.PlanToolName,
				Args: `{"entries":[{"content":"Inspect repo","status":"pending"}]}`,
			}},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected plan tool call to be handled")
	}
	if len(sender.msgs) != 0 {
		t.Fatalf("expected plan tool call to avoid transcript output, got %#v", sender.msgs)
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_plan",
				Name: tool.PlanToolName,
				Result: map[string]any{
					"entries": []any{
						map[string]any{"content": "Inspect repo", "status": "in_progress"},
						map[string]any{"content": "Run tests", "status": "pending"},
					},
				},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected plan tool response to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only one plan update message, got %#v", sender.msgs)
	}
	msg, ok := sender.msgs[0].(tuievents.PlanUpdateMsg)
	if !ok {
		t.Fatalf("expected PlanUpdateMsg, got %T", sender.msgs[0])
	}
	if len(msg.Entries) != 2 || msg.Entries[0].Status != "in_progress" {
		t.Fatalf("unexpected plan update payload: %+v", msg)
	}
}

func TestForwardEventToTUI_DelegateYieldDoesNotEmitTranscriptResultLine(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolResponse: &model.ToolResponse{
				ID:   "call_delegate",
				Name: "DELEGATE",
				Result: map[string]any{
					"task_id":       "t-1234567890ab",
					"yield_time_ms": 30000,
					"running":       true,
				},
			},
		},
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected delegate tool response to be handled")
	}
	for _, raw := range sender.msgs {
		if msg, ok := raw.(tuievents.LogChunkMsg); ok && strings.Contains(msg.Chunk, "DELEGATE") {
			t.Fatalf("expected no delegate result line in transcript, got %q", msg.Chunk)
		}
	}
}

func TestForwardEventToTUI_TaskWaitEmitsVirtualCallAndFriendlySummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_task_1",
				Name: "TASK",
				Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
			}},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_1",
				Name: "TASK",
				Result: map[string]any{
					"task_id":   "t-1234567890ab",
					"state":     "running",
					"running":   true,
					"waited_ms": 5000,
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call and summary messages, got %#v", sender.msgs)
	}
	callMsg, ok := sender.msgs[0].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected first LogChunkMsg, got %T", sender.msgs[0])
	}
	if !strings.Contains(callMsg.Chunk, "▸ WAIT 5 s") {
		t.Fatalf("unexpected TASK wait call chunk: %q", callMsg.Chunk)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WAITED 5 s") {
		t.Fatalf("unexpected TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitWithoutYieldUsesDefaultSummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_task_default",
				Name: "TASK",
				Args: `{"action":"wait","task_id":"t-1234567890ab"}`,
			}},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_default",
				Name: "TASK",
				Result: map[string]any{
					"task_id": "t-1234567890ab",
					"state":   "running",
					"running": true,
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call and summary messages, got %#v", sender.msgs)
	}
	callMsg, ok := sender.msgs[0].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected first LogChunkMsg, got %T", sender.msgs[0])
	}
	if !strings.Contains(callMsg.Chunk, "▸ WAIT 5 s") || strings.Contains(callMsg.Chunk, "t-1234567890ab") {
		t.Fatalf("unexpected TASK wait call chunk: %q", callMsg.Chunk)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WAITED 5 s") {
		t.Fatalf("unexpected TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitImmediateReturnPrefersState(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_task_fast",
				Name: "TASK",
				Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":30000}`,
			}},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_fast",
				Name: "TASK",
				Result: map[string]any{
					"task_id":   "t-1234567890ab",
					"state":     "running",
					"running":   true,
					"waited_ms": 0,
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call and summary messages, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WAITED Running") {
		t.Fatalf("unexpected fast TASK wait log chunk: %q", logMsg.Chunk)
	}
	if strings.Contains(logMsg.Chunk, "30 s") {
		t.Fatalf("did not expect requested wait duration in fast TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitErrorStillUsesFriendlyLabel(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_task_err",
				Name: "TASK",
				Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
			}},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_err",
				Name: "TASK",
				Result: map[string]any{
					"error": "task manager is unavailable",
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait error response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected call and error summary messages, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected second LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "! WAITED 5 s") {
		t.Fatalf("expected friendly TASK error label, got %q", logMsg.Chunk)
	}
	if strings.Contains(logMsg.Chunk, "! TASK 5 s") {
		t.Fatalf("did not expect raw TASK fallback label, got %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskListEmitsFriendlySummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{
		"call_task_list": {
			Args: map[string]any{
				"action": "list",
			},
		},
	}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call_task_list",
				Name: "TASK",
				Result: map[string]any{
					"count": 2,
					"tasks": []any{
						map[string]any{"task_id": "t-1", "state": "running", "running": true},
						map[string]any{"task_id": "t-2", "state": "cancelled", "running": false},
					},
				},
			},
		},
	}, pending)
	if !handled {
		t.Fatal("expected TASK list response to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected one summary message, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[0].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected LogChunkMsg, got %T", sender.msgs[0])
	}
	if !strings.Contains(logMsg.Chunk, "✓ LIST Listed 2 tasks (1 running)") {
		t.Fatalf("unexpected TASK list log chunk: %q", logMsg.Chunk)
	}
}
