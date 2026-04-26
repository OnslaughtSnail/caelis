package tuiapp

import (
	"strings"
	"testing"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func newGatewayEventTestModel() *Model {
	return NewModel(Config{
		AppName:         "CAELIS",
		Version:         "dev",
		Workspace:       "/tmp/workspace",
		ShowWelcomeCard: true,
		Commands:        DefaultCommands(),
		Wizards:         DefaultWizards(),
	})
}

func TestModelUpdateConsumesGatewayAssistantEventIntoMainTurnBlock(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:  appgateway.NarrativeRoleAssistant,
				Text:  "gateway answer",
				Final: true,
				Scope: appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)

	if got := len(m.doc.Blocks()); got != 1 {
		t.Fatalf("doc.Len() = %d, want 1", got)
	}
	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	if len(block.Events) != 1 || block.Events[0].Kind != SEAssistant || block.Events[0].Text != "gateway answer" {
		t.Fatalf("main turn events = %#v, want assistant narrative event", block.Events)
	}
}

func TestModelUpdateConsumesGatewayToolEventsWithoutTranscriptRecovery(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "READ",
				ArgsText: `/tmp/demo.txt`,
				Status:   "running",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:     "call-1",
				ToolName:   "READ",
				OutputText: "/tmp/demo.txt",
				Status:     "completed",
				Scope:      appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	if got := len(m.doc.Blocks()); got != 1 {
		t.Fatalf("doc.Len() = %d, want 1", got)
	}
	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	if len(block.Events) != 1 {
		t.Fatalf("len(block.Events) = %d, want 1", len(block.Events))
	}
	ev := block.Events[0]
	if ev.Kind != SEToolCall || ev.CallID != "call-1" || ev.Name != "READ" || !ev.Done {
		t.Fatalf("tool event = %#v, want finalized direct tool event", ev)
	}
	for _, item := range m.doc.Blocks() {
		if _, ok := item.(*TranscriptBlock); ok {
			t.Fatalf("unexpected transcript block %#v; want direct structured tool rendering", item)
		}
	}
}

func TestGatewayRunningToolResultStreamsOutputWithoutFinalizing(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "BASH",
				ArgsText: `go test ./gateway/...`,
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	model = updated.(*Model)
	updated, _ = model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:     "call-1",
				ToolName:   "BASH",
				OutputText: "stdout resolving packages",
				Status:     appgateway.ToolStatusRunning,
				Scope:      appgateway.EventScopeMain,
			},
		},
	})
	model = updated.(*Model)

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if len(block.Events) != 1 {
		t.Fatalf("len(block.Events) = %d, want 1", len(block.Events))
	}
	ev := block.Events[0]
	if ev.Done {
		t.Fatalf("tool event = %#v, want running output to remain non-final", ev)
	}
	if !strings.Contains(ev.Output, "stdout resolving packages") {
		t.Fatalf("tool event = %#v, want streaming output", ev)
	}
}

func TestGatewayCompletedExplorationToolDefaultsCollapsed(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "READ",
				ArgsText: "gateway/core/types.go",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	model = updated.(*Model)
	updated, _ = model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:     "call-1",
				ToolName:   "READ",
				OutputText: "package core\n\ntype Event struct{}",
				Status:     appgateway.ToolStatusCompleted,
				Scope:      appgateway.EventScopeMain,
			},
		},
	})
	model = updated.(*Model)

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if block.toolPanelExpanded("call-1") {
		t.Fatalf("READ tool panel should default collapsed after completion; expanded map = %#v", block.ExpandedTools)
	}
	if !model.tryToggleACPToolPanelToken(block.BlockID(), "acp_tool_panel:call-1") {
		t.Fatal("expected READ tool panel toggle token to expand collapsed panel")
	}
	if !block.toolPanelExpanded("call-1") {
		t.Fatalf("READ tool panel should expand after click; expanded map = %#v", block.ExpandedTools)
	}
}

func TestGatewayCompletedExplorationToolsRenderAsCompactSummary(t *testing.T) {
	model := newGatewayEventTestModel()
	tools := []struct {
		id     string
		name   string
		args   string
		output string
	}{
		{id: "read-1", name: "READ", args: "gateway/core/types.go", output: "type Event struct{}"},
		{id: "rg-1", name: "RG", args: "EventKind", output: "42 matches"},
		{id: "list-1", name: "LIST", args: "tui/tuiapp", output: "transcript_event.go"},
	}
	for _, tool := range tools {
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindToolCall,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				ToolCall: &appgateway.ToolCallPayload{
					CallID:   tool.id,
					ToolName: tool.name,
					ArgsText: tool.args,
					Status:   appgateway.ToolStatusRunning,
					Scope:    appgateway.EventScopeMain,
				},
			},
		})
		model = updated.(*Model)
		updated, _ = model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindToolResult,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				ToolResult: &appgateway.ToolResultPayload{
					CallID:     tool.id,
					ToolName:   tool.name,
					OutputText: tool.output,
					Status:     appgateway.ToolStatusCompleted,
					Scope:      appgateway.EventScopeMain,
				},
			},
		})
		model = updated.(*Model)
	}

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	rows := block.Render(BlockRenderContext{Width: 96, TermWidth: 96, Theme: model.theme})
	var plain []string
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "explored 2 files, 1 search") {
		t.Fatalf("rendered rows = %q, want compact exploration summary", joined)
	}
	if strings.Contains(joined, "type Event struct{}") || strings.Contains(joined, "42 matches") {
		t.Fatalf("rendered rows = %q, want exploration details hidden while collapsed", joined)
	}
	if !model.tryToggleACPToolPanelToken(block.BlockID(), "acp_exploration_group:read-1,rg-1,list-1") {
		t.Fatal("expected exploration summary click token to expand grouped tools")
	}
	rows = block.Render(BlockRenderContext{Width: 96, TermWidth: 96, Theme: model.theme})
	plain = plain[:0]
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined = strings.Join(plain, "\n")
	if !strings.Contains(joined, "type Event struct{}") || !strings.Contains(joined, "42 matches") {
		t.Fatalf("expanded rows = %q, want concrete exploration outputs", joined)
	}
}

func TestGatewayToolDisplayMetaRendersActionableSummaries(t *testing.T) {
	tests := []struct {
		name        string
		call        *appgateway.ToolCallPayload
		result      *appgateway.ToolResultPayload
		want        []string
		forbidden   []string
		expandPanel bool
	}{
		{
			name: "read line range",
			call: &appgateway.ToolCallPayload{
				CallID:   "read-1",
				ToolName: "READ",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/demo.py", "offset": 0, "limit": 100},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "read-1",
				ToolName: "READ",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/demo.py", "offset": 0, "limit": 100},
				RawOutput: map[string]any{
					"path":       "/tmp/workspace/demo.py",
					"start_line": 1,
					"end_line":   100,
					"content":    "1: package main",
				},
			},
			want:      []string{"READ demo.py 1~100"},
			forbidden: []string{"│   /tmp/workspace/demo.py"},
		},
		{
			name: "glob count",
			call: &appgateway.ToolCallPayload{
				CallID:   "glob-1",
				ToolName: "GLOB",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"pattern": "**/*.py"},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "glob-1",
				ToolName: "GLOB",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"pattern": "**/*.py"},
				RawOutput: map[string]any{
					"pattern": "**/*.py",
					"count":   5,
					"matches": []any{"a.py", "b.py", "c.py", "d.py", "e.py"},
				},
			},
			want: []string{"GLOB **/*.py 5 matches"},
		},
		{
			name: "bash terminal panel",
			call: &appgateway.ToolCallPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": `echo "hello"`},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": `echo "hello"`},
				RawOutput: map[string]any{
					"running":        false,
					"session_id":     "737bc26a-ff76-428f-8ca9-0fee8f2ae9ba",
					"state":          "completed",
					"supports_input": true,
					"stdout":         "hello\n",
					"exit_code":      0,
				},
			},
			want:        []string{`BASH echo "hello"`, "hello"},
			forbidden:   []string{"session_id", "supports_input", "737bc26a"},
			expandPanel: true,
		},
		{
			name: "spawn terminal panel",
			call: &appgateway.ToolCallPayload{
				CallID:   "spawn-1",
				ToolName: "SPAWN",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"prompt": "write fibonacci"},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "spawn-1",
				ToolName: "SPAWN",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"prompt": "write fibonacci"},
				RawOutput: map[string]any{
					"running": false,
					"state":   "completed",
					"task_id": "spawn-task-1",
					"result":  "child line 1\nchild line 2\n",
				},
			},
			want:        []string{"SPAWN", "child line 1", "child line 2"},
			forbidden:   []string{"task / running", "state completed", "spawn-task-1"},
			expandPanel: true,
		},
		{
			name: "bash task snapshot does not expose raw session json",
			call: &appgateway.ToolCallPayload{
				CallID:   "bash-task-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": `sleep 10`},
			},
			result: &appgateway.ToolResultPayload{
				CallID:     "bash-task-1",
				ToolName:   "BASH",
				Status:     appgateway.ToolStatusRunning,
				Scope:      appgateway.EventScopeMain,
				OutputText: `{"running":true,"session_id":"556d7447-4554-4fb9-ad1c-bb5a2e0f85ac","state":"running","supports_input":true,"task_id":"task-9"}`,
				RawInput:   map[string]any{"command": `sleep 10`},
				RawOutput: map[string]any{
					"running":        true,
					"session_id":     "556d7447-4554-4fb9-ad1c-bb5a2e0f85ac",
					"state":          "running",
					"supports_input": true,
					"task_id":        "task-9",
				},
			},
			want:        []string{`BASH sleep 10`},
			forbidden:   []string{"task / running", "task task-9", "state running", "session_id", "supports_input", "556d7447"},
			expandPanel: true,
		},
		{
			name: "task control panel",
			call: &appgateway.ToolCallPayload{
				CallID:   "task-1",
				ToolName: "TASK",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"action": "wait", "task_id": "task-9", "yield_time_ms": 5000},
			},
			result: &appgateway.ToolResultPayload{
				CallID:     "task-1",
				ToolName:   "TASK",
				Status:     appgateway.ToolStatusRunning,
				Scope:      appgateway.EventScopeMain,
				OutputText: `{"running":true,"session_id":"556d7447-4554-4fb9-ad1c-bb5a2e0f85ac","state":"running","supports_input":true,"task_id":"task-9"}`,
				RawInput:   map[string]any{"action": "wait", "task_id": "task-9", "yield_time_ms": 5000},
				RawOutput: map[string]any{
					"running":        true,
					"session_id":     "556d7447-4554-4fb9-ad1c-bb5a2e0f85ac",
					"state":          "running",
					"supports_input": true,
					"task_id":        "task-9",
				},
			},
			want:        []string{"↻ WAIT 5 s"},
			forbidden:   []string{"TASK", "task-9", "task / control", "state running", "session_id", "supports_input", "556d7447"},
			expandPanel: true,
		},
		{
			name: "write rich diff panel",
			call: &appgateway.ToolCallPayload{
				CallID:   "write-1",
				ToolName: "WRITE",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/tool_demo_summary.md", "content": "one\ntwo\n"},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "write-1",
				ToolName: "WRITE",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/tool_demo_summary.md", "content": "one\ntwo\n"},
				RawOutput: map[string]any{
					"path":          "/tmp/workspace/tool_demo_summary.md",
					"created":       true,
					"added_lines":   2,
					"removed_lines": 0,
				},
			},
			want:        []string{"WRITE tool_demo_summary.md +2 -0", "diff / hunk", "+one", "+two"},
			forbidden:   []string{"│", "╭", "╰", "tool_demo_summary.md +2 -0\n  tool_demo_summary.md +2 -0"},
			expandPanel: true,
		},
		{
			name: "patch rich diff panel",
			call: &appgateway.ToolCallPayload{
				CallID:   "patch-1",
				ToolName: "PATCH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/demo.py", "old": "old line", "new": "new line"},
			},
			result: &appgateway.ToolResultPayload{
				CallID:   "patch-1",
				ToolName: "PATCH",
				Status:   appgateway.ToolStatusCompleted,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"path": "/tmp/workspace/demo.py", "old": "old line", "new": "new line"},
				RawOutput: map[string]any{
					"path":          "/tmp/workspace/demo.py",
					"hunk":          "@@ -1,1 +1,1 @@",
					"added_lines":   1,
					"removed_lines": 1,
				},
			},
			want:        []string{"PATCH demo.py +1 -1", "diff / hunk", "-old line", "+new line"},
			forbidden:   []string{"│", "╭", "╰", "demo.py +1 -1\n  demo.py +1 -1"},
			expandPanel: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			model := newGatewayEventTestModel()
			updated, _ := model.Update(appgateway.EventEnvelope{
				Event: appgateway.Event{
					Kind:       appgateway.EventKindToolCall,
					SessionRef: sdksession.SessionRef{SessionID: "root-session"},
					ToolCall:   tt.call,
				},
			})
			model = updated.(*Model)
			updated, _ = model.Update(appgateway.EventEnvelope{
				Event: appgateway.Event{
					Kind:       appgateway.EventKindToolResult,
					SessionRef: sdksession.SessionRef{SessionID: "root-session"},
					ToolResult: tt.result,
				},
			})
			model = updated.(*Model)
			block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
			if !ok {
				t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
			}
			if tt.expandPanel {
				block.setToolPanelExpanded(tt.result.CallID, true)
			}
			rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
			plain := make([]string, 0, len(rows))
			for _, row := range rows {
				plain = append(plain, row.Plain)
			}
			joined := strings.Join(plain, "\n")
			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("rendered rows = %q, want %q", joined, want)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("rendered rows = %q, should not contain %q", joined, forbidden)
				}
			}
		})
	}
}

func TestGatewayConsecutiveTaskControlsMergeIntoOneInstructionRow(t *testing.T) {
	model := newGatewayEventTestModel()
	for _, item := range []struct {
		callID  string
		action  string
		input   string
		yieldMS int
	}{
		{callID: "task-0", action: "write", input: "Alice"},
		{callID: "task-1", action: "wait", yieldMS: 5000},
		{callID: "task-2", action: "wait", yieldMS: 8000},
	} {
		rawInput := map[string]any{"action": item.action, "task_id": "task-9", "yield_time_ms": item.yieldMS}
		if item.input != "" {
			rawInput["input"] = item.input
		}
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindToolCall,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				ToolCall: &appgateway.ToolCallPayload{
					CallID:   item.callID,
					ToolName: "TASK",
					Status:   appgateway.ToolStatusRunning,
					Scope:    appgateway.EventScopeMain,
					RawInput: rawInput,
				},
			},
		})
		model = updated.(*Model)
		updated, _ = model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindToolResult,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				ToolResult: &appgateway.ToolResultPayload{
					CallID:   item.callID,
					ToolName: "TASK",
					Status:   appgateway.ToolStatusRunning,
					Scope:    appgateway.EventScopeMain,
					RawInput: rawInput,
					RawOutput: map[string]any{
						"running": true,
						"state":   "running",
						"task_id": "task-9",
					},
				},
			},
		})
		model = updated.(*Model)
	}
	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, `↻ WRITE "Alice" · WAIT 5 s · WAIT 8 s`) {
		t.Fatalf("rendered rows = %q, want merged TASK controls", joined)
	}
	if strings.Contains(joined, "TASK") || strings.Contains(joined, "task-9") {
		t.Fatalf("rendered rows = %q, should hide raw TASK tool and task id", joined)
	}
}

func TestGatewayTaskSnapshotRefreshesBashPanelOutput(t *testing.T) {
	model := newGatewayEventTestModel()
	for _, env := range []appgateway.EventEnvelope{
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": "for i in $(seq 1 30); do echo $i; sleep 1; done"},
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": "for i in $(seq 1 30); do echo $i; sleep 1; done"},
				RawOutput: map[string]any{
					"running":        true,
					"state":          "running",
					"task_id":        "task-7",
					"output_preview": "进度: 1/30\n",
				},
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "task-wait-1",
				ToolName: "TASK",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"action": "wait", "task_id": "task-7", "yield_time_ms": 5000},
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "task-wait-1",
				ToolName: "TASK",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"action": "wait", "task_id": "task-7", "yield_time_ms": 5000},
				RawOutput: map[string]any{
					"running":        true,
					"state":          "running",
					"task_id":        "task-7",
					"output_preview": "进度: 1/30\n进度: 2/30\n进度: 3/30\n",
				},
			},
		}},
	} {
		updated, _ := model.Update(env)
		model = updated.(*Model)
	}
	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	block.setToolPanelExpanded("bash-1", true)
	rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"  进度: 1/30", "  进度: 3/30", "↻ WAIT 5 s"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered rows = %q, want %q", joined, want)
		}
	}
	for _, forbidden := range []string{"|_", "BASH output", "│", "task / running", "state running", "stdout 进度", "task-7"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("rendered rows = %q, should not contain %q", joined, forbidden)
		}
	}
}

func TestGatewayBashTerminalDeltasPreserveLineBreaks(t *testing.T) {
	model := newGatewayEventTestModel()
	for _, env := range []appgateway.EventEnvelope{
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": "for i in $(seq 1 10); do echo $i; done"},
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": "for i in $(seq 1 10); do echo $i; done"},
				RawOutput: map[string]any{
					"running": true,
					"state":   "running",
					"task_id": "task-7",
					"stream":  "stdout",
					"text":    "[步骤 8/10] 正在处理... 09:05:53\n",
				},
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "bash-1",
				ToolName: "BASH",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: map[string]any{"command": "for i in $(seq 1 10); do echo $i; done"},
				RawOutput: map[string]any{
					"running": true,
					"state":   "running",
					"task_id": "task-7",
					"stream":  "stdout",
					"text":    "[步骤 9/10] 正在处理... 09:05:55\n",
				},
			},
		}},
	} {
		updated, _ := model.Update(env)
		model = updated.(*Model)
	}
	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	block.setToolPanelExpanded("bash-1", true)
	rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	if strings.Contains(joined, "09:05:53 [步骤 9/10]") {
		t.Fatalf("rendered rows = %q, terminal delta lines were merged", joined)
	}
	for _, want := range []string{"  [步骤 8/10] 正在处理... 09:05:53", "  [步骤 9/10] 正在处理... 09:05:55"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered rows = %q, want %q", joined, want)
		}
	}
}

func TestGatewayPlanToolRendersOnlyPlanEntries(t *testing.T) {
	model := newGatewayEventTestModel()
	rawInput := map[string]any{
		"entries": []any{
			map[string]any{"content": "Inspect files", "status": "completed"},
			map[string]any{"content": "Run validation", "status": "in_progress"},
		},
	}
	rawOutput := map[string]any{
		"message": "Plan updated",
		"entries": rawInput["entries"],
	}
	for _, env := range []appgateway.EventEnvelope{
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "plan-1",
				ToolName: "PLAN",
				Status:   appgateway.ToolStatusRunning,
				Scope:    appgateway.EventScopeMain,
				RawInput: rawInput,
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:    "plan-1",
				ToolName:  "PLAN",
				Status:    appgateway.ToolStatusCompleted,
				Scope:     appgateway.EventScopeMain,
				RawInput:  rawInput,
				RawOutput: rawOutput,
			},
		}},
		{Event: appgateway.Event{
			Kind:       appgateway.EventKindPlanUpdate,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Plan: &appgateway.PlanPayload{Entries: []appgateway.PlanEntryPayload{
				{Content: "Inspect files", Status: "completed"},
				{Content: "Run validation", Status: "in_progress"},
			}},
		}},
	} {
		updated, _ := model.Update(env)
		model = updated.(*Model)
	}
	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
	plain := make([]string, 0, len(rows))
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"✓ Inspect files", "▸ Run validation"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered rows = %q, want %q", joined, want)
		}
	}
	for _, forbidden := range []string{"PLAN", `"entries"`, "Plan updated"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("rendered rows = %q, should not contain %q", joined, forbidden)
		}
	}
}

func TestGatewayBashPanelRendersRawTerminalOutput(t *testing.T) {
	tests := []struct {
		name      string
		status    appgateway.ToolStatus
		isErr     bool
		rawOutput map[string]any
		want      []string
		forbid    []string
	}{
		{
			name:   "running preview",
			status: appgateway.ToolStatusRunning,
			rawOutput: map[string]any{
				"running":        true,
				"state":          "running",
				"task_id":        "task-7",
				"supports_input": true,
				"output_preview": "进度: 1/5\n",
			},
			want:   []string{"BASH for i in 1 2", "  进度: 1/5"},
			forbid: []string{"|_", "BASH output", "│", "task / running", "task task-7", "state running", "stdout 进度", "supports_input"},
		},
		{
			name:   "failed stderr",
			status: appgateway.ToolStatusFailed,
			isErr:  true,
			rawOutput: map[string]any{
				"stderr":    "permission denied\n",
				"stdout":    "ignored stdout\n",
				"exit_code": 1,
			},
			want:   []string{"  permission denied"},
			forbid: []string{"|_", "BASH output", "│", "stderr permission denied", "ignored stdout", "exit 1"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			model := newGatewayEventTestModel()
			callID := "bash-" + strings.ReplaceAll(tt.name, " ", "-")
			updated, _ := model.Update(appgateway.EventEnvelope{
				Event: appgateway.Event{
					Kind:       appgateway.EventKindToolCall,
					SessionRef: sdksession.SessionRef{SessionID: "root-session"},
					ToolCall: &appgateway.ToolCallPayload{
						CallID:   callID,
						ToolName: "BASH",
						Status:   appgateway.ToolStatusRunning,
						Scope:    appgateway.EventScopeMain,
						RawInput: map[string]any{"command": "for i in 1 2; do echo $i; done"},
					},
				},
			})
			model = updated.(*Model)
			updated, _ = model.Update(appgateway.EventEnvelope{
				Event: appgateway.Event{
					Kind:       appgateway.EventKindToolResult,
					SessionRef: sdksession.SessionRef{SessionID: "root-session"},
					ToolResult: &appgateway.ToolResultPayload{
						CallID:    callID,
						ToolName:  "BASH",
						Status:    tt.status,
						Error:     tt.isErr,
						Scope:     appgateway.EventScopeMain,
						RawInput:  map[string]any{"command": "for i in 1 2; do echo $i; done"},
						RawOutput: tt.rawOutput,
					},
				},
			})
			model = updated.(*Model)
			block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
			if !ok {
				t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
			}
			block.setToolPanelExpanded(callID, true)
			rows := block.Render(BlockRenderContext{Width: 110, TermWidth: 110, Theme: model.theme})
			plain := make([]string, 0, len(rows))
			for _, row := range rows {
				plain = append(plain, row.Plain)
			}
			joined := strings.Join(plain, "\n")
			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("rendered rows = %q, want %q", joined, want)
				}
			}
			for _, forbidden := range tt.forbid {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("rendered rows = %q, should not contain %q", joined, forbidden)
				}
			}
		})
	}
}

func TestGatewayAssistantFinalKeepsReasoningVisible(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:          appgateway.NarrativeRoleAssistant,
				ReasoningText: "thinking through the plan",
				Final:         false,
				Scope:         appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:          appgateway.NarrativeRoleAssistant,
				ReasoningText: "thinking through the plan",
				Text:          "final answer",
				Final:         true,
				Scope:         appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	rows := block.Render(BlockRenderContext{
		Width:     80,
		TermWidth: 80,
		Theme:     m.theme,
	})
	var plain []string
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "thinking through the plan") {
		t.Fatalf("rendered rows = %q, want reasoning text to remain visible", joined)
	}
	if !strings.Contains(joined, "final answer") {
		t.Fatalf("rendered rows = %q, want assistant text", joined)
	}
}

func TestGatewayStreamingNarrativeKeepsReasoningAnswerBoundaries(t *testing.T) {
	model := newGatewayEventTestModel()

	send := func(payload *appgateway.NarrativePayload) *Model {
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative:  payload,
			},
		})
		model = updated.(*Model)
		return model
	}

	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "think-1 ",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "answer-1 ",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "think-2 ",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "answer-2",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if got := len(block.Events); got != 2 {
		t.Fatalf("len(block.Events) = %d, want 2 active narrative streams; got %#v", got, block.Events)
	}
	wantKinds := []SubagentEventKind{SEReasoning, SEAssistant}
	wantTexts := []string{"think-1think-2", "answer-1answer-2"}
	for i := range wantKinds {
		if block.Events[i].Kind != wantKinds[i] || block.Events[i].Text != wantTexts[i] {
			t.Fatalf("block.Events[%d] = %#v, want kind=%v text=%q", i, block.Events[i], wantKinds[i], wantTexts[i])
		}
	}
}

func TestGatewayInterleavedStreamingFinalReplacesMatchingNarrativeOnly(t *testing.T) {
	model := newGatewayEventTestModel()

	send := func(payload *appgateway.NarrativePayload) *Model {
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative:  payload,
			},
		})
		model = updated.(*Model)
		return model
	}

	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r1",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "a1",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r2-partial",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "a2-partial",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r2-final",
		Text:          "a2-final",
		Final:         true,
		Scope:         appgateway.EventScopeMain,
	})

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if got := len(block.Events); got != 2 {
		t.Fatalf("len(block.Events) = %d, want 2; got %#v", got, block.Events)
	}
	wantKinds := []SubagentEventKind{SEReasoning, SEAssistant}
	wantTexts := []string{"r2-final", "a2-final"}
	for i := range wantKinds {
		if block.Events[i].Kind != wantKinds[i] || block.Events[i].Text != wantTexts[i] {
			t.Fatalf("block.Events[%d] = %#v, want kind=%v text=%q", i, block.Events[i], wantKinds[i], wantTexts[i])
		}
	}
}
