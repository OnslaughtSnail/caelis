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
			Message: model.NewTextMessage(model.RoleAssistant, "hello"),
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
			Message: model.NewTextMessage(model.RoleAssistant, "hello world"),
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
			if msg.ClaimAnchor {
				t.Fatalf("expected child-event bootstrap not to claim anchors, got %+v", msg)
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

func TestProjectSubagentDomainUpdate_UsesCommonSpawnProjectorPath(t *testing.T) {
	console := &cliConsole{
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	msgs := console.projectSubagentDomainUpdate(syntheticSubagentDomainUpdate(subagentProjectionTarget{
		RootSessionID: "parent",
		SpawnID:       "child-1",
		AttachTarget:  "dlg-1",
		CallID:        "call-spawn-1",
		Agent:         "self",
	}, &session.Event{
		Message: model.NewTextMessage(model.RoleAssistant, "hello world"),
	}))

	var startFound, streamFound bool
	for _, raw := range msgs {
		switch msg := raw.(type) {
		case tuievents.SubagentStartMsg:
			if msg.SpawnID != "child-1" || msg.AttachTarget != "child-1" || msg.CallID != "call-spawn-1" {
				t.Fatalf("unexpected start msg: %+v", msg)
			}
			if msg.ClaimAnchor {
				t.Fatalf("expected synthetic child-event bootstrap not to claim anchors, got %+v", msg)
			}
			startFound = true
		case tuievents.SubagentStreamMsg:
			if msg.Stream != "assistant" || !strings.Contains(msg.Chunk, "hello") {
				t.Fatalf("unexpected stream msg: %+v", msg)
			}
			streamFound = true
		}
	}
	if !startFound {
		t.Fatal("expected SubagentStartMsg from common synthetic projector path")
	}
	if !streamFound {
		t.Fatal("expected SubagentStreamMsg from common synthetic projector path")
	}
}

func TestSubagentDomainUpdateFromSpawnToolResponse(t *testing.T) {
	update, ok := subagentDomainUpdateFromSpawnToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-spawn-1",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"child_session_id":     "child-1",
			"delegation_id":        "dlg-1",
			"agent":                "self",
			"approval_pending":     true,
			"_ui_approval_tool":    "BASH",
			"_ui_approval_command": "rm -rf /tmp/demo",
		},
	})
	if !ok {
		t.Fatal("expected spawn bootstrap to be extracted")
	}
	if update.Kind != subagentDomainBootstrap {
		t.Fatalf("expected bootstrap kind, got %q", update.Kind)
	}
	if update.Target.RootSessionID != "parent-1" {
		t.Fatalf("unexpected root session id: %+v", update.Target)
	}
	if update.Target.SpawnID != "child-1" || update.Target.AttachTarget != "child-1" {
		t.Fatalf("unexpected target ids: %+v", update.Target)
	}
	if update.Target.CallID != "call-spawn-1" || update.Target.Agent != "self" {
		t.Fatalf("unexpected target metadata: %+v", update.Target)
	}
	if !update.ClaimAnchor {
		t.Fatalf("expected spawn tool response bootstrap to claim anchor, got %+v", update)
	}
	if update.Provisional {
		t.Fatalf("expected child-session bootstrap to be authoritative, got %+v", update)
	}
	if update.Status != "waiting_approval" {
		t.Fatalf("expected waiting_approval status, got %q", update.Status)
	}
	if update.ApprovalTool != "BASH" || update.ApprovalCommand != "rm -rf /tmp/demo" {
		t.Fatalf("unexpected approval context: %+v", update)
	}
}

func TestSubagentDomainUpdateFromSpawnToolCallCreatesProvisionalBootstrap(t *testing.T) {
	update, ok := subagentDomainUpdateFromSpawnToolCall("parent-1", model.ToolCall{
		ID:   "call-spawn-1",
		Name: tool.SpawnToolName,
	}, map[string]any{"agent": "gemini"}, "self")
	if !ok {
		t.Fatal("expected spawn tool call bootstrap to be extracted")
	}
	if update.Kind != subagentDomainBootstrap {
		t.Fatalf("expected bootstrap kind, got %q", update.Kind)
	}
	if !update.ClaimAnchor || !update.Provisional {
		t.Fatalf("expected provisional anchor-claiming bootstrap, got %+v", update)
	}
	if update.Target.RootSessionID != "parent-1" || update.Target.CallID != "call-spawn-1" {
		t.Fatalf("unexpected root/call target: %+v", update.Target)
	}
	if update.Target.SpawnID != "call-spawn-1" || update.Target.AttachTarget != "call-spawn-1" {
		t.Fatalf("expected provisional spawn to key off callID, got %+v", update.Target)
	}
	if update.Target.Agent != "gemini" {
		t.Fatalf("expected agent from args, got %+v", update.Target)
	}
}

func TestSubagentDomainUpdateFromSpawnToolResponse_TaskOnlyStaysProvisional(t *testing.T) {
	update, ok := subagentDomainUpdateFromSpawnToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-spawn-1",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"task_id": "task-1",
			"agent":   "self",
		},
	})
	if !ok {
		t.Fatal("expected task-only spawn response bootstrap to be extracted")
	}
	if !update.Provisional {
		t.Fatalf("expected task-only bootstrap to remain provisional, got %+v", update)
	}
	if update.Target.SpawnID != "call-spawn-1" || update.Target.AttachTarget != "call-spawn-1" {
		t.Fatalf("expected task-only bootstrap to stay keyed by callID, got %+v", update.Target)
	}
}

func TestSubagentDomainUpdatesFromTaskToolResponse_Terminal(t *testing.T) {
	updates := subagentDomainUpdatesFromTaskToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-task-1",
		Name: tool.TaskToolName,
		Result: map[string]any{
			"child_session_id": "child-1",
			"delegation_id":    "dlg-1",
			"agent":            "gemini",
			"state":            "completed",
		},
	}, map[string]any{"action": "wait"})
	if len(updates) != 1 {
		t.Fatalf("expected one TASK wait update, got %#v", updates)
	}
	update := updates[0]
	if update.Kind != subagentDomainTerminal {
		t.Fatalf("expected terminal kind, got %q", update.Kind)
	}
	if update.Target.RootSessionID != "parent-1" || update.Target.SpawnID != "child-1" {
		t.Fatalf("unexpected target: %+v", update.Target)
	}
	if update.Target.AttachTarget != "child-1" || update.Target.Agent != "gemini" {
		t.Fatalf("unexpected target metadata: %+v", update.Target)
	}
	if update.Status != "completed" {
		t.Fatalf("expected completed status, got %q", update.Status)
	}
}

func TestSubagentDomainUpdatesFromSpawnToolError_Timeout(t *testing.T) {
	updates := subagentDomainUpdatesFromSpawnToolError("parent-1", &model.ToolResponse{
		ID:   "call-spawn-1",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"agent": "gemini",
			"error": "context deadline exceeded",
		},
	})
	if len(updates) != 0 {
		t.Fatalf("expected no detached subagent panel updates when spawn failed before child session creation, got %#v", updates)
	}
}

func TestSubagentDomainUpdateFromSpawnToolResponse_IgnoresDetachedError(t *testing.T) {
	update, ok := subagentDomainUpdateFromSpawnToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-spawn-1",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"agent": "gemini",
			"error": "context deadline exceeded",
		},
	})
	if ok {
		t.Fatalf("expected detached spawn error to skip subagent bootstrap, got %#v", update)
	}
}

func TestSubagentDomainUpdatesFromTaskToolResponse_Status(t *testing.T) {
	updates := subagentDomainUpdatesFromTaskToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-task-1",
		Name: tool.TaskToolName,
		Result: map[string]any{
			"child_session_id":     "child-1",
			"delegation_id":        "dlg-1",
			"agent":                "gemini",
			"state":                "waiting_approval",
			"_ui_approval_tool":    "BASH",
			"_ui_approval_command": "rm -rf /tmp/demo",
		},
	}, map[string]any{"action": "status"})
	if len(updates) != 1 {
		t.Fatalf("expected one TASK status update, got %#v", updates)
	}
	update := updates[0]
	if update.Kind != subagentDomainStatus {
		t.Fatalf("expected status kind, got %q", update.Kind)
	}
	if update.Status != "waiting_approval" {
		t.Fatalf("expected waiting_approval status, got %q", update.Status)
	}
	if update.ApprovalTool != "BASH" || update.ApprovalCommand != "rm -rf /tmp/demo" {
		t.Fatalf("unexpected approval context: %+v", update)
	}
}

func TestSubagentDomainUpdatesFromTaskToolResponse_IdleTimeoutPromotesTimedOut(t *testing.T) {
	updates := subagentDomainUpdatesFromTaskToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-task-1",
		Name: tool.TaskToolName,
		Result: map[string]any{
			"child_session_id":   "child-1",
			"delegation_id":      "dlg-1",
			"agent":              "self",
			"state":              "failed",
			"_ui_idle_timed_out": true,
		},
	}, map[string]any{"action": "wait"})
	if len(updates) != 1 {
		t.Fatalf("expected one TASK wait update, got %#v", updates)
	}
	update := updates[0]
	if update.Kind != subagentDomainTerminal {
		t.Fatalf("expected terminal kind, got %#v", update)
	}
	if update.Status != "timed_out" {
		t.Fatalf("expected idle timed out state to project as timed_out, got %#v", update)
	}
}

func TestSubagentDomainUpdatesFromTaskToolResponse_WriteContinuationBootstrapsNewPanel(t *testing.T) {
	updates := subagentDomainUpdatesFromTaskToolResponse("parent-1", &model.ToolResponse{
		ID:   "call-task-write-1",
		Name: tool.TaskToolName,
		Result: map[string]any{
			"child_session_id":        "child-1",
			"delegation_id":           "dlg-1",
			"agent":                   "copilot",
			"state":                   "running",
			"_ui_spawn_id":            "call-task-write-1",
			"_ui_parent_tool_call_id": "call-task-write-1",
			"_ui_parent_tool_name":    tool.TaskToolName,
			"_ui_anchor_tool":         runtime.SubagentContinuationAnchorTool,
		},
	}, map[string]any{"action": "write"})
	if len(updates) != 2 {
		t.Fatalf("expected bootstrap + status for TASK write continuation, got %#v", updates)
	}
	if updates[0].Kind != subagentDomainBootstrap {
		t.Fatalf("expected first update bootstrap, got %#v", updates[0])
	}
	if updates[0].Target.SpawnID != "call-task-write-1" || updates[0].Target.AttachTarget != "child-1" {
		t.Fatalf("unexpected continuation bootstrap target %+v", updates[0].Target)
	}
	if updates[0].Target.AnchorTool != runtime.SubagentContinuationAnchorTool || !updates[0].ClaimAnchor {
		t.Fatalf("expected WRITE anchor claim, got %#v", updates[0])
	}
	if updates[1].Kind != subagentDomainStatus || updates[1].Status != "running" {
		t.Fatalf("expected running status update, got %#v", updates[1])
	}
}

func TestSpawnPreviewProjector_UsesAgentIDFromMeta(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "hi"),
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
	if startMsg.Agent != "codex" {
		t.Fatalf("expected agent from event metadata, got %q", startMsg.Agent)
	}
}

func TestSpawnPreviewProjector_InterleavedBootstrapAndChildEventsKeepAgentsAligned(t *testing.T) {
	proj := newSpawnPreviewProjector()
	var starts []tuievents.SubagentStartMsg
	collectStarts := func(msgs []any) {
		for _, raw := range msgs {
			if msg, ok := raw.(tuievents.SubagentStartMsg); ok {
				starts = append(starts, msg)
			}
		}
	}

	collectStarts(proj.Project(sessionstream.Update{
		SessionID: "child-copilot",
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "copilot child"),
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-copilot",
				"parent_tool_call_id": "call-copilot",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-copilot",
				"agent_id":            "copilot",
			},
		},
	}))

	selfBootstrap, ok := subagentDomainUpdateFromSpawnToolResponse("parent", &model.ToolResponse{
		ID:   "call-self",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"child_session_id": "child-self",
			"delegation_id":    "dlg-self",
			"agent":            "self",
		},
	})
	if !ok {
		t.Fatal("expected self bootstrap update")
	}
	collectStarts(renderSubagentDomainUpdates([]subagentDomainUpdate{selfBootstrap}))

	collectStarts(proj.Project(sessionstream.Update{
		SessionID: "child-self",
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "self child"),
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-self",
				"parent_tool_call_id": "call-self",
				"parent_tool_name":    tool.SpawnToolName,
				"delegation_id":       "dlg-self",
				"agent_id":            "self",
			},
		},
	}))

	copilotBootstrap, ok := subagentDomainUpdateFromSpawnToolResponse("parent", &model.ToolResponse{
		ID:   "call-copilot",
		Name: tool.SpawnToolName,
		Result: map[string]any{
			"child_session_id": "child-copilot",
			"delegation_id":    "dlg-copilot",
			"agent":            "copilot",
		},
	})
	if !ok {
		t.Fatal("expected copilot bootstrap update")
	}
	collectStarts(renderSubagentDomainUpdates([]subagentDomainUpdate{copilotBootstrap}))

	if len(starts) < 4 {
		t.Fatalf("expected interleaved starts from child events and bootstraps, got %d", len(starts))
	}
	for _, msg := range starts {
		switch msg.SpawnID {
		case "child-self":
			if msg.Agent != "self" || msg.CallID != "call-self" {
				t.Fatalf("expected self spawn to keep self/call-self mapping, got %+v", msg)
			}
		case "child-copilot":
			if msg.Agent != "copilot" || msg.CallID != "call-copilot" {
				t.Fatalf("expected copilot spawn to keep copilot/call-copilot mapping, got %+v", msg)
			}
		default:
			t.Fatalf("unexpected spawn start %+v", msg)
		}
	}
}

func TestSpawnPreviewProjector_ProjectsReasoningStream(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.MessageFromAssistantParts("", "thinking about it", nil),
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

func TestSpawnPreviewProjector_ProjectsTaskWriteContinuationToNewPanel(t *testing.T) {
	proj := newSpawnPreviewProjector()
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "continued"),
			Meta: map[string]any{
				"parent_session_id":   "parent",
				"child_session_id":    "child-1",
				"parent_tool_call_id": "call-task-write-1",
				"parent_tool_name":    tool.TaskToolName,
				"delegation_id":       "dlg-2",
				"agent_id":            "copilot",
			},
		},
	})
	var start tuievents.SubagentStartMsg
	var stream tuievents.SubagentStreamMsg
	for _, raw := range msgs {
		switch msg := raw.(type) {
		case tuievents.SubagentStartMsg:
			start = msg
		case tuievents.SubagentStreamMsg:
			stream = msg
		}
	}
	if start.SpawnID != "call-task-write-1" || start.CallID != "call-task-write-1" {
		t.Fatalf("expected continuation to key off TASK write call, got %+v", start)
	}
	if start.AttachTarget != "child-1" || start.AnchorTool != runtime.SubagentContinuationAnchorTool {
		t.Fatalf("unexpected continuation start metadata %+v", start)
	}
	if stream.SpawnID != "call-task-write-1" || stream.Stream != "assistant" || stream.Chunk == "" {
		t.Fatalf("unexpected continuation stream %+v", stream)
	}
}

func TestSpawnPreviewProjector_ProjectsToolCallAndResult(t *testing.T) {
	proj := newSpawnPreviewProjector()
	// Tool call
	msgs := proj.Project(sessionstream.Update{
		SessionID: "child-1",
		Event: &session.Event{
			Message: model.MessageFromToolCalls(model.RoleTool, []model.ToolCall{{
				ID:   "tc-1",
				Name: "READ",
				Args: `{"path":"test.txt"}`,
			}}, ""),
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
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:     "tc-1",
				Name:   "READ",
				Result: map[string]any{"content": "file content"},
			}),
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
			Message: model.NewTextMessage(model.RoleAssistant, "working"),
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
			Message: model.NewTextMessage(model.RoleAssistant, "subagent answer"),
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
			Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
				{ID: "tc-1", Name: "BASH", Args: `{"command":"rm -rf /tmp/foo"}`},
			}, ""),
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
