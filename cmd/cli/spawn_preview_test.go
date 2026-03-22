package main

import (
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestSpawnPreviewProjector_IgnoresNonSpawnEvents(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-1",
				"parent_tool_name":    "OTHER",
				"delegation_id":       "dlg-1",
			},
		},
	})
	if len(msgs) != 0 {
		t.Fatalf("expected no messages for non-SPAWN event, got %d", len(msgs))
	}
}

func TestSpawnPreviewProjector_ProjectsAssistantStream(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "hello world"},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	// Expect: SubagentStartMsg + SubagentStreamMsg(assistant)
	var startFound, streamFound bool
	for _, m := range msgs {
		switch msg := m.(type) {
		case tuievents.SubagentStartMsg:
			if msg.SpawnID != "child-1" || msg.AttachTarget != "child-1" || msg.Agent != "self" || msg.CallID != "call-spawn-1" {
				t.Fatalf("unexpected start msg: %+v", msg)
			}
			startFound = true
		case tuievents.SubagentStreamMsg:
			if msg.Stream != "assistant" || msg.Chunk == "" {
				t.Fatalf("unexpected stream msg: %+v", msg)
			}
			streamFound = true
		}
	}
	if !startFound {
		t.Fatal("expected SubagentStartMsg")
	}
	if !streamFound {
		t.Fatal("expected SubagentStreamMsg for assistant")
	}
}

func TestSpawnPreviewProjector_UsesAgentIDFromMeta(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "hi"},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
				"agent_id":            "codex",
			},
		},
	})
	var startMsg tuievents.SubagentStartMsg
	for _, m := range msgs {
		if msg, ok := m.(tuievents.SubagentStartMsg); ok {
			startMsg = msg
			break
		}
	}
	if startMsg.Agent != "self" {
		t.Fatalf("expected agent=self by default, got %q", startMsg.Agent)
	}
}

func TestSpawnPreviewProjector_ProjectsReasoningStream(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Reasoning: "thinking about it"},
			Meta: map[string]any{
				"partial":             true,
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	var reasoningFound bool
	for _, m := range msgs {
		if msg, ok := m.(tuievents.SubagentStreamMsg); ok && msg.Stream == "reasoning" {
			if msg.Chunk == "" {
				t.Fatal("expected non-empty reasoning chunk")
			}
			reasoningFound = true
		}
	}
	if !reasoningFound {
		t.Fatal("expected SubagentStreamMsg for reasoning")
	}
}

func TestSpawnPreviewProjector_ProjectsToolCallAndResult(t *testing.T) {
	proj := newSpawnPreviewProjector()
	// Tool call
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleTool,
				ToolCalls: []model.ToolCall{{
					ID:   "tc-1",
					Name: "READ",
					Args: `{"path":"test.txt"}`,
				}},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	var toolCallFound bool
	for _, m := range msgs {
		if msg, ok := m.(tuievents.SubagentToolCallMsg); ok {
			if msg.ToolName != "READ" || msg.CallID != "tc-1" {
				t.Fatalf("unexpected tool call msg: %+v", msg)
			}
			toolCallFound = true
		}
	}
	if !toolCallFound {
		t.Fatal("expected SubagentToolCallMsg for tool call")
	}

	// Tool result
	msgs = proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:     "tc-1",
					Name:   "READ",
					Result: map[string]any{"content": "file content"},
				},
			},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	var resultFound bool
	for _, m := range msgs {
		if msg, ok := m.(tuievents.SubagentToolCallMsg); ok && msg.Final {
			if msg.ToolName != "READ" || msg.CallID != "tc-1" || msg.Stream != "stdout" {
				t.Fatalf("unexpected tool result msg: %+v", msg)
			}
			resultFound = true
		}
	}
	if !resultFound {
		t.Fatal("expected SubagentToolCallMsg with Final=true for tool result")
	}
}

func TestSpawnPreviewProjector_ProjectsDoneOnLifecycleEvent(t *testing.T) {
	proj := newSpawnPreviewProjector()
	// First send an event to create state
	proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "working"},
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	// Now send lifecycle completed event
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleSystem},
			Meta: map[string]any{
				"kind":                "lifecycle",
				"lifecycle":           map[string]any{"status": "completed"},
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	var doneFound bool
	for _, m := range msgs {
		if msg, ok := m.(tuievents.SubagentDoneMsg); ok {
			if msg.SpawnID != "child-1" || msg.State != "completed" {
				t.Fatalf("unexpected done msg: %+v", msg)
			}
			doneFound = true
		}
	}
	if !doneFound {
		t.Fatal("expected SubagentDoneMsg")
	}
}

func TestSpawnPreviewProjector_ProjectsWaitingApprovalStatus(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleSystem},
			Meta: map[string]any{
				"kind":                "lifecycle",
				"lifecycle":           map[string]any{"status": "waiting_approval"},
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	var startFound, statusFound bool
	for _, m := range msgs {
		switch msg := m.(type) {
		case tuievents.SubagentStartMsg:
			if msg.AttachTarget != "child-1" {
				t.Fatalf("unexpected start msg: %+v", msg)
			}
			startFound = true
		case tuievents.SubagentStatusMsg:
			if msg.SpawnID != "child-1" || msg.State != "waiting_approval" {
				t.Fatalf("unexpected status msg: %+v", msg)
			}
			statusFound = true
		}
	}
	if !startFound {
		t.Fatal("expected SubagentStartMsg")
	}
	if !statusFound {
		t.Fatal("expected SubagentStatusMsg")
	}
}

func TestForwardSessionEventToTUI_RoutesSpawnEventsToSubagentPanel(t *testing.T) {
	sender := &testSender{}
	c := &cliConsole{
		sessionID:      "parent-session",
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	c.forwardSessionEventToTUI("parent-session", sessionstream.Update{
		SessionID: "child-session",
		Event: &session.Event{
			Message: model.Message{Role: model.RoleAssistant, Text: "subagent answer"},
			Meta: map[string]any{
				"parent_session_id":   "parent-session",
				"child_session_id":    "child-session",
				"parent_tool_call_id": "call-spawn-1",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-1",
			},
		},
	})
	// Should get SubagentStartMsg + SubagentStreamMsg, but NOT TaskStreamMsg.
	var hasStart, hasStream bool
	for _, m := range sender.msgs {
		switch m.(type) {
		case tuievents.SubagentStartMsg:
			hasStart = true
		case tuievents.SubagentStreamMsg:
			hasStream = true
		case tuievents.TaskStreamMsg:
			t.Fatal("SPAWN events should not produce TaskStreamMsg")
		}
	}
	if !hasStart {
		t.Fatal("expected SubagentStartMsg from spawn projector")
	}
	if !hasStream {
		t.Fatal("expected SubagentStreamMsg from spawn projector")
	}
}

func TestSpawnPreviewProjector_ApprovalForwardsToolContext(t *testing.T) {
	proj := newSpawnPreviewProjector()
	spawnMeta := map[string]any{
		"parent_session_id":   "parent",
		"child_session_id":    "child-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    tool.SpawnToolName,
		"delegation_id":       "dlg-1",
	}

	// Step 1: Send a tool call event to establish tool context.
	proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "tc-1", Name: "BASH", Args: `{"command":"rm -rf /tmp/foo"}`},
				},
			},
			Meta: spawnMeta,
		},
	})

	// Step 2: Send a waiting_approval lifecycle event.
	lifecycleEv := runtime.LifecycleEvent(
		&session.Session{ID: "child-1"},
		runtime.RunLifecycleStatusWaitingApproval,
		"tool_approval",
		nil,
	)
	lifecycleEv.Meta["parent_session_id"] = "parent"
	lifecycleEv.Meta["child_session_id"] = "child-1"
	lifecycleEv.Meta["parent_tool_call_id"] = "call-spawn-1"
	lifecycleEv.Meta["parent_tool_name"] = tool.SpawnToolName
	lifecycleEv.Meta["delegation_id"] = "dlg-1"

	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event:     lifecycleEv,
	})

	var foundStatus *tuievents.SubagentStatusMsg
	for _, msg := range msgs {
		if sm, ok := msg.(tuievents.SubagentStatusMsg); ok {
			foundStatus = &sm
		}
	}
	if foundStatus == nil {
		t.Fatal("expected SubagentStatusMsg for waiting_approval")
	}
	if foundStatus.State != "waiting_approval" {
		t.Fatalf("expected state waiting_approval, got %q", foundStatus.State)
	}
	if foundStatus.ApprovalTool != "BASH" {
		t.Fatalf("expected ApprovalTool=BASH, got %q", foundStatus.ApprovalTool)
	}
	if foundStatus.ApprovalCommand == "" {
		t.Fatal("expected non-empty ApprovalCommand")
	}
}

func TestParseApprovalToolFromError(t *testing.T) {
	tests := []struct {
		name   string
		errStr string
		want   string
	}{
		{"policy error", `tool: approval required: tool "BASH" requires authorization: host escalation`, "BASH"},
		{"workspace boundary", `tool: approval required: tool "WRITE" targets "/etc/passwd" which is outside workspace`, "WRITE"},
		{"no tool name", `tool: approval required: host escalation required; approve in interactive mode`, ""},
		{"empty", "", ""},
		{"partial", `tool "`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseApprovalToolFromError(tt.errStr)
			if got != tt.want {
				t.Errorf("parseApprovalToolFromError(%q) = %q, want %q", tt.errStr, got, tt.want)
			}
		})
	}
}

func TestResolveApprovalContext_SinglePending(t *testing.T) {
	proj := newSpawnPreviewProjector()
	proj.mu.Lock()
	proj.states["spawn-1"] = &spawnPreviewState{
		toolCalls: map[string]toolCallSnapshot{
			"tc-1": {Name: "BASH", Args: map[string]any{"command": "rm -rf /tmp/foo"}},
		},
		lastToolCallName: "BASH",
		lastToolCallArgs: "rm -rf /tmp/foo",
	}
	proj.mu.Unlock()

	info := runtime.LifecycleInfo{
		Status:    runtime.RunLifecycleStatusWaitingApproval,
		Error:     "", // No tool name in error
		ErrorCode: "approval_required",
	}

	tool, command := resolveApprovalContext(proj, "spawn-1", info)
	if tool != "BASH" {
		t.Fatalf("expected tool BASH, got %q", tool)
	}
	if command == "" {
		t.Fatal("expected non-empty command")
	}
}

func TestResolveApprovalContext_ErrorStringParsing(t *testing.T) {
	proj := newSpawnPreviewProjector()
	proj.mu.Lock()
	proj.states["spawn-1"] = &spawnPreviewState{
		toolCalls: map[string]toolCallSnapshot{
			"tc-1": {Name: "BASH", Args: map[string]any{"command": "do_thing"}},
			"tc-2": {Name: "WRITE", Args: map[string]any{"file": "secret.txt"}},
		},
		lastToolCallName: "BASH",
		lastToolCallArgs: "do_thing",
	}
	proj.mu.Unlock()

	info := runtime.LifecycleInfo{
		Status:    runtime.RunLifecycleStatusWaitingApproval,
		Error:     `tool: approval required: tool "WRITE" targets "/etc/passwd"`,
		ErrorCode: "approval_required",
	}

	tool, _ := resolveApprovalContext(proj, "spawn-1", info)
	// Should extract WRITE from error string, NOT use lastToolCallName=BASH.
	if tool != "WRITE" {
		t.Fatalf("expected tool WRITE from error string, got %q", tool)
	}
}

func TestResolveApprovalContext_SinglePendingUsesSnapshotName(t *testing.T) {
	// Scenario: BASH started (pending), then READ started+finished (not pending).
	// lastToolCallName is now READ, but the only PENDING call is BASH.
	proj := newSpawnPreviewProjector()
	proj.mu.Lock()
	proj.states["spawn-1"] = &spawnPreviewState{
		toolCalls: map[string]toolCallSnapshot{
			"tc-1": {Name: "BASH", Args: map[string]any{"command": "rm -rf /tmp/danger"}},
		},
		lastToolCallName: "READ", // stale — last seen tool was READ, but it already resolved
		lastToolCallArgs: "file.txt",
	}
	proj.mu.Unlock()

	info := runtime.LifecycleInfo{
		Status: runtime.RunLifecycleStatusWaitingApproval,
		Error:  "", // No error string (tool-generated, not policy)
	}

	tool, command := resolveApprovalContext(proj, "spawn-1", info)
	// Should use the snapshot's Name (BASH), NOT lastToolCallName (READ).
	if tool != "BASH" {
		t.Fatalf("expected tool BASH from snapshot, got %q", tool)
	}
	if !strings.Contains(command, "rm -rf") {
		t.Fatalf("expected command from BASH snapshot, got %q", command)
	}
}
