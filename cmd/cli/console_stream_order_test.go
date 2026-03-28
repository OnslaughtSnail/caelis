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
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestEmitAssistantEventToTUI_FinalReasoningThenText(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:     sender,
		showReasoning: true,
	}
	ev := &session.Event{
		Message: model.MessageFromAssistantParts("answer", "think", nil),
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 stream messages, got %d", len(sender.msgs))
	}
	first, ok := sender.msgs[0].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected first msg RawDeltaMsg, got %T", sender.msgs[0])
	}
	second, ok := sender.msgs[1].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected second msg RawDeltaMsg, got %T", sender.msgs[1])
	}
	if first.Target != tuievents.RawDeltaTargetAssistant || first.Stream != "reasoning" || first.Text != "think" || !first.Final {
		t.Fatalf("unexpected first msg: %+v", first)
	}
	if second.Target != tuievents.RawDeltaTargetAssistant || second.Stream != "answer" || second.Text != "answer" || !second.Final {
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
		Message: model.NewReasoningMessage(model.RoleAssistant, "think only", model.ReasoningVisibilityVisible),
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 1 {
		t.Fatalf("expected exactly one reasoning message, got %d", len(sender.msgs))
	}
	msg, ok := sender.msgs[0].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected RawDeltaMsg, got %T", sender.msgs[0])
	}
	if msg.Target != tuievents.RawDeltaTargetAssistant || msg.Stream != "reasoning" || msg.Text != "think only" || !msg.Final {
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
		Message: model.MessageFromAssistantParts("a", "r", nil),
		Meta: map[string]any{
			"partial": true,
			"channel": "answer",
		},
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 2 {
		t.Fatalf("expected 2 stream messages, got %d", len(sender.msgs))
	}
	first, ok := sender.msgs[0].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected first msg RawDeltaMsg, got %T", sender.msgs[0])
	}
	second, ok := sender.msgs[1].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected second msg RawDeltaMsg, got %T", sender.msgs[1])
	}
	if first.Target != tuievents.RawDeltaTargetAssistant || first.Stream != "reasoning" || first.Final {
		t.Fatalf("unexpected first msg: %+v", first)
	}
	if second.Target != tuievents.RawDeltaTargetAssistant || second.Stream != "answer" || second.Final {
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
		Message: model.MessageFromAssistantParts("shown", "hidden", nil),
	}

	c.emitAssistantEventToTUI(ev)

	if len(sender.msgs) != 1 {
		t.Fatalf("expected one answer message, got %d", len(sender.msgs))
	}
	msg := sender.msgs[0].(tuievents.RawDeltaMsg)
	if msg.Target != tuievents.RawDeltaTargetAssistant || msg.Stream != "answer" || msg.Text != "shown" || !msg.Final {
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
		Message: model.MessageFromAssistantParts("", "think first", []model.ToolCall{
			{ID: "call_1", Name: "LIST", Args: `{"path":"."}`},
		}),
	}

	handled := c.forwardEventToTUI(ev, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected event to be handled by TUI forwarder")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected reasoning then tool call messages, got %d", len(sender.msgs))
	}
	reasoningMsg, ok := sender.msgs[0].(tuievents.RawDeltaMsg)
	if !ok {
		t.Fatalf("expected first message RawDeltaMsg, got %T", sender.msgs[0])
	}
	if reasoningMsg.Target != tuievents.RawDeltaTargetAssistant || reasoningMsg.Stream != "reasoning" || reasoningMsg.Text != "think first" || !reasoningMsg.Final {
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_1", Name: "WRITE", Args: fmt.Sprintf(`{"path":%q,"content":"new\n"}`, path)},
		}, ""),
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_1", Name: "WRITE", Args: fmt.Sprintf(`{"path":%q,"content":%q}`, path, newContent)},
		}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only call log when diff is skipped, got %d messages", len(sender.msgs))
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_1",
			Name: "WRITE",
			Result: map[string]any{
				"path":       path,
				"created":    false,
				"line_count": 500,
			},
		}),
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

func TestForwardEventToTUI_ParticipantMirrorToolCallEmitsStructuredTurnToolMsg(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	ev := &session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_1", Name: "SEARCHING", Args: `{"_acp_kind":"search","query":"Shanghai weather"}`},
		}, ""),
		Meta: map[string]any{
			"visibility":           "mirror",
			metaParticipantDisplay: "luna(gemini)",
			metaChildSessionID:     "child-1",
		},
	}

	if !c.forwardEventToTUI(ev, map[string]toolCallSnapshot{}) {
		t.Fatal("expected participant mirror event to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected one structured participant tool message, got %d", len(sender.msgs))
	}
	msg, ok := sender.msgs[0].(tuievents.ParticipantToolMsg)
	if !ok {
		t.Fatalf("expected ParticipantToolMsg, got %T", sender.msgs[0])
	}
	if msg.SessionID != "child-1" || msg.CallID != "call_1" || msg.ToolName != "SEARCHING" {
		t.Fatalf("unexpected participant tool message: %+v", msg)
	}
	if !strings.Contains(msg.Args, "Shanghai weather") {
		t.Fatalf("expected user-facing ACP args summary, got %+v", msg)
	}
}

func TestForwardEventToTUI_BashReplayEmitsOutputChunksBeforeFinal(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-bash-1",
			Name: "BASH",
			Result: map[string]any{
				"stdout":    "hello\nworld\n",
				"stderr":    "warn\n",
				"exit_code": 0,
			},
		}),
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected bash tool response to be handled")
	}
	if len(sender.msgs) < 3 {
		t.Fatalf("expected replayed stdout/stderr chunks and final panel event, got %d", len(sender.msgs))
	}
	first, ok := sender.msgs[0].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected first msg TaskStreamMsg, got %T", sender.msgs[0])
	}
	second, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second msg TaskStreamMsg, got %T", sender.msgs[1])
	}
	last, ok := sender.msgs[len(sender.msgs)-1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected final msg TaskStreamMsg, got %T", sender.msgs[len(sender.msgs)-1])
	}
	if first.Stream != "stdout" || !strings.Contains(first.Chunk, "hello") {
		t.Fatalf("expected stdout replay chunk, got %+v", first)
	}
	if second.Stream != "stderr" || !strings.Contains(second.Chunk, "warn") {
		t.Fatalf("expected stderr replay chunk, got %+v", second)
	}
	if !last.Final {
		t.Fatalf("expected final bash panel event, got %+v", last)
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_patch_insert", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"a\nb","new":"a\nx\nb"}`, path)},
		}, ""),
	}, pending, tuiForwardOptions{ShowMutationDiff: false})
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only call log when rich diff is disabled, got %#v", sender.msgs)
	}
	sender.msgs = nil

	handled = c.forwardEventToTUIWithOptions(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
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
		}),
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_patch_large", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":%q,"new":%q}`, path, oldSnippet, newSnippet)},
		}, ""),
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

func TestForwardEventToTUI_HugeFileTinyPatchStillUsesRichDiffWithoutCompactSummary(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "huge.txt")
	lines := make([]string, 0, 6000)
	for i := 1; i <= 6000; i++ {
		lines = append(lines, fmt.Sprintf("line-%04d", i))
	}
	content := strings.Join(lines, "\n") + "\n"
	oldSnippet := "line-3200\nline-3201\nline-3202"
	newSnippet := "line-3200\ninserted\nline-3201\nline-3202"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_patch_huge", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":%q,"new":%q}`, path, oldSnippet, newSnippet)},
		}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected tool call to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected tool call log and rich diff block, got %#v", sender.msgs)
	}
	if _, ok := sender.msgs[1].(tuievents.DiffBlockMsg); !ok {
		t.Fatalf("expected rich diff block, got %T", sender.msgs[1])
	}

	sender.msgs = nil
	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_patch_huge",
			Name: "PATCH",
			Result: map[string]any{
				"path":          path,
				"created":       false,
				"replaced":      1,
				"old_count":     1,
				"added_lines":   1,
				"removed_lines": 0,
			},
		}),
	}, pending)
	if !handled {
		t.Fatal("expected tool response to be handled")
	}
	if len(sender.msgs) != 0 {
		t.Fatalf("did not expect compact +1 -0 summary after rich diff, got %#v", sender.msgs)
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_1", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"one\ntwo","new":"one\nmid\ntwo"}`, path)},
			{ID: "call_2", Name: "PATCH", Args: fmt.Sprintf(`{"path":%q,"old":"one\nmid\ntwo","new":"one\nmid\ntwo\nend"}`, path)},
		}, ""),
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
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "call_read_1", Name: "READ", Args: `{"path":"state.go"}`},
		}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected READ call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_read_1",
			Name: "READ",
			Result: map[string]any{
				"path":       "state.go",
				"line_count": 120,
				"start_line": 1,
				"end_line":   120,
			},
		}),
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

func TestForwardEventToTUI_BashSuccessWithoutOutputDoesNotEmitExitCodeLine(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.NewMessage(model.RoleAssistant, model.NewToolResultJSONPart("call_bash", "BASH", map[string]any{
			"exit_code": 0,
		}, false)),
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
		Message: model.NewMessage(model.RoleAssistant, model.NewToolResultJSONPart("call_bash", "BASH", map[string]any{
			"error": "tool: BASH failed (route=sandbox): tool: sandbox runner is unavailable",
		}, false)),
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
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_plan",
			Name: tool.PlanToolName,
			Args: `{"entries":[{"content":"Inspect repo","status":"in_progress"},{"content":"Run tests","status":"pending"}]}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected plan tool call to be handled")
	}
	if len(sender.msgs) != 0 {
		t.Fatalf("expected plan tool call to avoid transcript output, got %#v", sender.msgs)
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_plan",
			Name: tool.PlanToolName,
			Result: map[string]any{
				"message": "Plan updated",
			},
		}),
	}, pending)
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

func TestForwardEventToTUI_NoOpWriteSkipsRichDiffAndUsesUnchangedSummary(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	content := "same\ncontent\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sender := &testSender{}
	c := &cliConsole{
		tuiSender:   sender,
		execRuntime: previewTestRuntime{cwd: ws},
	}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_write",
			Name: "WRITE",
			Args: fmt.Sprintf(`{"path":%q,"content":%q}`, path, content),
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected write tool call to be handled")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("expected only tool call log when rich diff is skipped, got %#v", sender.msgs)
	}
	if _, ok := sender.msgs[0].(tuievents.DiffBlockMsg); ok {
		t.Fatalf("did not expect no-op write to emit a rich diff: %#v", sender.msgs)
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_write",
			Name: "WRITE",
			Result: map[string]any{
				"path":          path,
				"created":       false,
				"line_count":    2,
				"added_lines":   0,
				"removed_lines": 0,
			},
		}),
	}, pending)
	if !handled {
		t.Fatal("expected write tool response to be handled")
	}
	if len(sender.msgs) != 2 {
		t.Fatalf("expected tool response summary after call log, got %#v", sender.msgs)
	}
	logMsg, ok := sender.msgs[1].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected LogChunkMsg, got %T", sender.msgs[1])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WRITE unchanged a.txt") {
		t.Fatalf("unexpected no-op write summary: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_SpawnYieldDoesNotEmitTranscriptResultLine(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.NewMessage(model.RoleAssistant, model.NewToolResultJSONPart("call_spawn", tool.SpawnToolName, map[string]any{
			"task_id":              "t-1234567890ab",
			"_ui_child_session_id": "s-child-1",
			"_ui_agent":            "self",
			"state":                "running",
		}, false)),
	}, map[string]toolCallSnapshot{})
	if !handled {
		t.Fatal("expected spawn tool response to be handled")
	}
	for _, raw := range sender.msgs {
		if msg, ok := raw.(tuievents.LogChunkMsg); ok && strings.Contains(msg.Chunk, tool.SpawnToolName) {
			t.Fatalf("expected no spawn result line in transcript, got %q", msg.Chunk)
		}
	}
}

func TestForwardEventToTUI_TaskWaitEmitsVirtualCallAndFriendlySummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_task_1",
			Name: "TASK",
			Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_1",
			Name: "TASK",
			Result: map[string]any{
				"task_id": "t-1234567890ab",
				"state":   "running",
				"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
				"result":  "still running",
			},
		}),
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
	if !strings.Contains(logMsg.Chunk, "✓ WAITED task yielded before completion") {
		t.Fatalf("unexpected TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitWithoutYieldUsesDefaultSummary(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_task_default",
			Name: "TASK",
			Args: `{"action":"wait","task_id":"t-1234567890ab"}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_default",
			Name: "TASK",
			Result: map[string]any{
				"task_id": "t-1234567890ab",
				"state":   "running",
				"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
			},
		}),
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
	if !strings.Contains(logMsg.Chunk, "✓ WAITED task yielded before completion") {
		t.Fatalf("unexpected TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitImmediateReturnPrefersState(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_task_fast",
			Name: "TASK",
			Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":30000}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_fast",
			Name: "TASK",
			Result: map[string]any{
				"task_id": "t-1234567890ab",
				"state":   "running",
				"msg":     "task yielded before completion; use TASK with task_id t-1234567890ab",
			},
		}),
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
	if !strings.Contains(logMsg.Chunk, "✓ WAITED task yielded before completion") {
		t.Fatalf("unexpected fast TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitWaitingInputEmitsSyntheticPanelState(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_task_input",
			Name: "TASK",
			Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_input",
			Name: "TASK",
			Result: map[string]any{
				"task_id": "t-1234567890ab",
				"state":   "waiting_input",
				"msg":     "waiting for input; use TASK write with task_id t-1234567890ab",
			},
		}),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait response to be handled")
	}
	if len(sender.msgs) != 3 {
		t.Fatalf("expected call, panel state, and summary messages, got %#v", sender.msgs)
	}
	stateMsg, ok := sender.msgs[1].(tuievents.TaskStreamMsg)
	if !ok {
		t.Fatalf("expected second msg TaskStreamMsg, got %T", sender.msgs[1])
	}
	if stateMsg.TaskID != "t-1234567890ab" || stateMsg.State != "waiting_input" {
		t.Fatalf("unexpected synthetic panel state: %#v", stateMsg)
	}
	if strings.TrimSpace(stateMsg.Chunk) != "" {
		t.Fatalf("expected no synthetic chunk for generic waiting_input state, got %q", stateMsg.Chunk)
	}
	logMsg, ok := sender.msgs[2].(tuievents.LogChunkMsg)
	if !ok {
		t.Fatalf("expected third msg LogChunkMsg, got %T", sender.msgs[2])
	}
	if !strings.Contains(logMsg.Chunk, "✓ WAITED task yielded before completion") {
		t.Fatalf("unexpected TASK wait log chunk: %q", logMsg.Chunk)
	}
}

func TestForwardEventToTUI_TaskWaitErrorStillUsesFriendlyLabel(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{tuiSender: sender}
	pending := map[string]toolCallSnapshot{}

	handled := c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call_task_err",
			Name: "TASK",
			Args: `{"action":"wait","task_id":"t-1234567890ab","yield_time_ms":5000}`,
		}}, ""),
	}, pending)
	if !handled {
		t.Fatal("expected TASK wait call to be handled")
	}

	handled = c.forwardEventToTUI(&session.Event{
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_err",
			Name: "TASK",
			Result: map[string]any{
				"error": "task manager is unavailable",
			},
		}),
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
	if !strings.Contains(logMsg.Chunk, "! WAITED task manager is unavailable") {
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
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call_task_list",
			Name: "TASK",
			Result: map[string]any{
				"tasks": []any{
					map[string]any{"task_id": "t-1", "state": "running", "summary": "task yielded before completion"},
					map[string]any{"task_id": "t-2", "state": "cancelled", "summary": "cancelled"},
				},
			},
		}),
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
	if !strings.Contains(logMsg.Chunk, "✓ LIST Listed 2 tasks (1 active)") {
		t.Fatalf("unexpected TASK list log chunk: %q", logMsg.Chunk)
	}
}
