package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestHandleResumeEmitsSessionHintInTUI(t *testing.T) {
	store := inmemory.New()
	target := &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}
	if _, err := store.GetOrCreate(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), target, &session.Event{
		ID:        "ev-1",
		SessionID: target.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "hello again"),
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index: resumeIndexStub{
			resolveID: "resume-me",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "current",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
		tuiSender:    sender,
	}

	if _, err := handleResume(console, []string{"resume-me"}); err != nil {
		t.Fatal(err)
	}
	if console.sessionID != "resume-me" {
		t.Fatalf("expected resumed session id, got %q", console.sessionID)
	}
	got := lastHint(sender.msgs)
	if !strings.Contains(got, "resumed session: resume-me") {
		t.Fatalf("expected resume hint with session id, got %q", got)
	}
	if _, ok := sender.msgs[len(sender.msgs)-1].(tuievents.SetHintMsg); !ok {
		t.Fatalf("expected last tui message to be a hint, got %T", sender.msgs[len(sender.msgs)-1])
	}
}

func lastHint(msgs []any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msg, ok := msgs[i].(tuievents.SetHintMsg); ok {
			return msg.Hint
		}
	}
	return ""
}

func TestHandleResume_BootstrapsRunningSubagentWithoutRemoteReplay(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT := newCLITestExecRuntime(t, toolexec.PermissionModeFullControl)
	ag, err := llmagent.New(llmagent.Config{Name: "resume-test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &consoleFlowLLM{}

	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	for _, sess := range []*session.Session{parent, child} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEvent(context.Background(), parent, &session.Event{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "self",
				"state":            "running",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    child.ID,
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
	}
	if err := store.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-child-1",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "child reply"),
		Meta:      childMeta,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-child-done",
		SessionID: child.ID,
		Message:   model.Message{Role: model.RoleSystem},
		Meta: map[string]any{
			"kind":                "lifecycle",
			"lifecycle":           map[string]any{"status": "completed"},
			"parent_session_id":   parent.ID,
			"child_session_id":    child.ID,
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-spawn-1",
			"parent_tool_name":    "SPAWN",
		},
	}); err != nil {
		t.Fatal(err)
	}

	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index: resumeIndexStub{
			resolveID: "resume-parent",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      "current",
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
		newACPAdapter: func(conn *internalacp.Conn) (internalacp.Adapter, error) {
			return newConsoleFlowAdapterFactory(rt, store, execRT, ag, llm)(conn)
		},
	}

	if _, err := handleResume(console, []string{"resume-parent"}); err != nil {
		t.Fatal(err)
	}
	foundStart := false
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.SubagentStartMsg:
			if msg.SpawnID == "child-1" {
				foundStart = true
			}
		case tuievents.ACPProjectionMsg:
			if msg.Scope == tuievents.ACPProjectionSubagent && msg.ScopeID == "child-1" && msg.Stream == "assistant" && strings.Contains(msg.DeltaText+msg.FullText, "child reply") {
				t.Fatalf("did not expect /resume to recover child stream without local projection log, got %#v", sender.Snapshot())
			}
		}
	}
	if !foundStart {
		t.Fatalf("expected /resume to bootstrap running subagent panel, got %#v", sender.Snapshot())
	}
}

func TestHandleResume_RestoresExternalParticipantTurnFromProjectionLog(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	route := routeMirrorUserEvent("/copilot 介绍一下你自己", externalParticipant{
		Alias:          "cole",
		AgentID:        "copilot",
		ChildSessionID: "child-1",
		DisplayLabel:   "cole(copilot)",
	}, "slash_create")
	if route.Meta == nil {
		route.Meta = map[string]any{}
	}
	route.Meta[metaRouteCallID] = "call-1"
	if err := store.AppendEvent(context.Background(), parent, route); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index: resumeIndexStub{
			resolveID: parent.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	if err := console.acpProjectionStore().AppendEvent(context.Background(), acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   "child-1",
		CallID:    "call-1",
		Kind:      "turn_start",
		SessionID: "child-1",
		Actor:     "cole(copilot)",
	}); err != nil {
		t.Fatal(err)
	}
	if err := console.acpProjectionStore().AppendEvent(context.Background(), acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   "child-1",
		CallID:    "call-1",
		Kind:      "projection",
		SessionID: "child-1",
		Actor:     "cole(copilot)",
		Stream:    "assistant",
		DeltaText: "hello from copilot",
	}); err != nil {
		t.Fatal(err)
	}
	if err := console.acpProjectionStore().AppendEvent(context.Background(), acpProjectionPersistedEvent{
		Scope:      string(tuievents.ACPProjectionParticipant),
		ScopeID:    "child-1",
		CallID:     "call-1",
		Kind:       "projection",
		SessionID:  "child-1",
		Actor:      "cole(copilot)",
		ToolCallID: "tool-1",
		ToolName:   "READ",
		ToolArgs:   map[string]any{"path": "README.md"},
		ToolResult: map[string]any{"summary": "README.md"},
		ToolStatus: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := console.acpProjectionStore().AppendEvent(context.Background(), acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   "child-1",
		CallID:    "call-1",
		Kind:      "projection",
		SessionID: "child-1",
		Actor:     "cole(copilot)",
		Stream:    "reasoning",
		DeltaText: "checking tools",
	}); err != nil {
		t.Fatal(err)
	}
	if err := console.acpProjectionStore().AppendEvent(context.Background(), acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   "child-1",
		CallID:    "call-1",
		Kind:      "status",
		SessionID: "child-1",
		Status:    "completed",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := handleResume(console, []string{parent.ID}); err != nil {
		t.Fatal(err)
	}

	var (
		foundStart     bool
		foundAssistant bool
		foundReasoning bool
		foundTool      bool
		foundCompleted bool
	)
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.ParticipantTurnStartMsg:
			if msg.SessionID == "child-1" && msg.Actor == "cole(copilot)" {
				foundStart = true
			}
		case tuievents.ACPProjectionMsg:
			if msg.Scope != tuievents.ACPProjectionParticipant || msg.ScopeID != "child-1" {
				continue
			}
			if msg.Stream == "assistant" && strings.Contains(msg.DeltaText+msg.FullText, "hello from copilot") {
				foundAssistant = true
			}
			if msg.Stream == "reasoning" && strings.Contains(msg.DeltaText+msg.FullText, "checking tools") {
				foundReasoning = true
			}
			if msg.ToolCallID == "tool-1" && msg.ToolStatus == "completed" && strings.EqualFold(msg.ToolName, "READ") {
				foundTool = true
			}
		case tuievents.ParticipantStatusMsg:
			if msg.SessionID == "child-1" && msg.State == "completed" {
				foundCompleted = true
			}
		}
	}
	if !foundStart || !foundAssistant || !foundReasoning || !foundTool || !foundCompleted {
		t.Fatalf("expected cached external participant replay, got %#v", sender.Snapshot())
	}
}

func TestHandleResume_RestoresExternalParticipantProjectionOrder(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent-ordered"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	route := routeMirrorUserEvent("@cole 演示一下你的工具调用能力", externalParticipant{
		Alias:          "cole",
		AgentID:        "copilot",
		ChildSessionID: "child-1",
		DisplayLabel:   "cole(copilot)",
	}, "participant_route")
	if route.Meta == nil {
		route.Meta = map[string]any{}
	}
	route.Meta[metaRouteCallID] = "call-ordered"
	if err := store.AppendEvent(context.Background(), parent, route); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index:   resumeIndexStub{resolveID: parent.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionParticipant), ScopeID: "child-1", CallID: "call-ordered", Kind: "turn_start", SessionID: "child-1", Actor: "cole(copilot)"},
		{Scope: string(tuievents.ACPProjectionParticipant), ScopeID: "child-1", CallID: "call-ordered", Kind: "projection", SessionID: "child-1", Actor: "cole(copilot)", Stream: "assistant", DeltaText: "先看文件。"},
		{Scope: string(tuievents.ACPProjectionParticipant), ScopeID: "child-1", CallID: "call-ordered", Kind: "projection", SessionID: "child-1", Actor: "cole(copilot)", ToolCallID: "tool-1", ToolName: "READ", ToolArgs: map[string]any{"_display": "/tmp/demo"}, ToolStatus: "completed", ToolResult: map[string]any{"summary": "found target file"}},
		{Scope: string(tuievents.ACPProjectionParticipant), ScopeID: "child-1", CallID: "call-ordered", Kind: "projection", SessionID: "child-1", Actor: "cole(copilot)", Stream: "assistant", DeltaText: "读取完成，下面总结。"},
	} {
		if err := console.acpProjectionStore().AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := handleResume(console, []string{parent.ID}); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.ACPProjectionMsg)
		if !ok || msg.ScopeID != "child-1" {
			continue
		}
		switch {
		case msg.Stream == "assistant" && msg.DeltaText != "":
			got = append(got, "assistant:"+msg.DeltaText)
		case msg.ToolCallID != "":
			got = append(got, "tool:"+msg.ToolName)
		}
	}
	want := []string{"assistant:先看文件。", "tool:READ", "assistant:读取完成，下面总结。"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected ordered projection replay %v, got %v", want, got)
	}
}

func TestRenderSessionEvents_ChildAssistantHistoryIsSuppressedWithoutProjectionLog(t *testing.T) {
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "completed",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    child.ID,
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-answer",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "child reply"),
		Meta:      childMeta,
	})
	events = append(events, &session.Event{
		ID:        "ev-child-reasoning",
		SessionID: child.ID,
		Message:   model.NewReasoningMessage(model.RoleAssistant, "child reasoning", model.ReasoningVisibilityVisible),
		Meta:      childMeta,
	})
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
		showReasoning:  true,
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	var foundChildInResume bool
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.ACPProjectionMsg:
			if strings.Contains(msg.DeltaText, "child reply") || strings.Contains(msg.DeltaText, "child reasoning") || strings.Contains(msg.FullText, "child reply") || strings.Contains(msg.FullText, "child reasoning") {
				foundChildInResume = true
			}
		case tuievents.LogChunkMsg:
			if strings.Contains(msg.Chunk, "child reply") || strings.Contains(msg.Chunk, "child reasoning") {
				foundChildInResume = true
			}
		case tuievents.RawDeltaMsg:
			if strings.Contains(msg.Text, "child reply") || strings.Contains(msg.Text, "child reasoning") {
				foundChildInResume = true
			}
		}
	}
	if foundChildInResume {
		t.Fatalf("did not expect child assistant history to replay without a local projection log, got %#v", sender.Snapshot())
	}
}

func TestRenderSessionEvents_UsesOrderedProjectionLogForRunningSubagent(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent-subagent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "running",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    "child-1",
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-answer",
		SessionID: "child-1",
		Message:   model.NewTextMessage(model.RoleAssistant, "canonical child reply"),
		Meta:      childMeta,
	})

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "turn_start", SessionID: "child-1", Agent: "codex", AttachTarget: "child-1", AnchorTool: "SPAWN", ClaimAnchor: true},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", Stream: "assistant", DeltaText: "ordered first"},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", ToolCallID: "tool-1", ToolName: "READ", ToolStatus: "completed", ToolResult: map[string]any{"summary": "ordered tool"}},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", Stream: "assistant", DeltaText: "ordered second"},
	} {
		if err := console.acpProjectionStore().AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.ACPProjectionMsg)
		if !ok || msg.Scope != tuievents.ACPProjectionSubagent || msg.ScopeID != "child-1" {
			continue
		}
		switch {
		case msg.Stream == "assistant" && msg.DeltaText != "":
			got = append(got, "assistant:"+msg.DeltaText)
		case msg.ToolCallID != "":
			got = append(got, "tool:"+msg.ToolName)
		}
		if strings.Contains(msg.DeltaText+msg.FullText, "canonical child reply") {
			t.Fatalf("did not expect canonical child replay when ordered projection log exists, got %#v", sender.Snapshot())
		}
	}
	want := []string{"assistant:ordered first", "tool:READ", "assistant:ordered second"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected ordered subagent projection replay %v, got %v", want, got)
	}
}

func TestRenderSessionEvents_UsesOrderedProjectionLogForCompletedSubagent(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent-completed-projection"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "completed",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    "child-1",
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-answer",
		SessionID: "child-1",
		Message:   model.NewTextMessage(model.RoleAssistant, "canonical completed child reply"),
		Meta:      childMeta,
	})

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "turn_start", SessionID: "child-1", Agent: "codex", AttachTarget: "child-1", AnchorTool: "SPAWN", ClaimAnchor: true},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", Stream: "assistant", DeltaText: "ordered completed first"},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", ToolCallID: "tool-1", ToolName: "READ", ToolStatus: "completed", ToolResult: map[string]any{"summary": "ordered completed tool"}},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", Stream: "assistant", DeltaText: "ordered completed second"},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "status", SessionID: "child-1", Status: "completed"},
	} {
		if err := console.acpProjectionStore().AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.ACPProjectionMsg)
		if !ok || msg.Scope != tuievents.ACPProjectionSubagent || msg.ScopeID != "child-1" {
			continue
		}
		switch {
		case msg.Stream == "assistant" && msg.DeltaText != "":
			got = append(got, "assistant:"+msg.DeltaText)
		case msg.ToolCallID != "":
			got = append(got, "tool:"+msg.ToolName)
		}
		if strings.Contains(msg.DeltaText+msg.FullText, "canonical completed child reply") {
			t.Fatalf("did not expect canonical completed child replay when ordered projection log exists, got %#v", sender.Snapshot())
		}
	}
	want := []string{"assistant:ordered completed first", "tool:READ", "assistant:ordered completed second"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected ordered completed subagent projection replay %v, got %v", want, got)
	}
}

func TestRenderSessionEvents_PartialSubagentProjectionLogDoesNotReplayChildHistory(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent-partial-log"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "running",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    "child-1",
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-answer",
		SessionID: "child-1",
		Message:   model.NewTextMessage(model.RoleAssistant, "canonical child reply"),
		Meta:      childMeta,
	})

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "turn_start", SessionID: "child-1", Agent: "codex", AttachTarget: "child-1", AnchorTool: "SPAWN", ClaimAnchor: true},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "status", SessionID: "child-1", Status: "running"},
	} {
		if err := console.acpProjectionStore().AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.ACPProjectionMsg)
		if ok && msg.Scope == tuievents.ACPProjectionSubagent && msg.ScopeID == "child-1" && msg.Stream == "assistant" && strings.Contains(msg.DeltaText+msg.FullText, "canonical child reply") {
			t.Fatalf("did not expect canonical child history replay when a local subagent projection log exists, got %#v", sender.Snapshot())
		}
	}
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.SubagentStartMsg)
		if ok && msg.SpawnID == "child-1" {
			return
		}
	}
	t.Fatalf("expected subagent bootstrap/replay state even when projection log has no output, got %#v", sender.Snapshot())
}

func TestRenderSessionEvents_ChildToolHistoryIsSuppressedWithoutProjectionLog(t *testing.T) {
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "completed",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    child.ID,
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-call",
		SessionID: child.ID,
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "child-read-1",
			Name: "READ",
			Args: `{"path":"demo.py"}`,
		}}, ""),
		Meta: childMeta,
	})
	events = append(events, &session.Event{
		ID:        "ev-child-result",
		SessionID: child.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:     "child-read-1",
			Name:   "READ",
			Result: map[string]any{"result": "ok"},
		}),
		Meta: childMeta,
	})
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	var foundChildToolReplay bool
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.ACPProjectionMsg:
			if msg.Scope == tuievents.ACPProjectionSubagent && msg.ScopeID == "child-1" && msg.ToolCallID == "child-read-1" && strings.EqualFold(msg.ToolName, "READ") {
				foundChildToolReplay = true
			}
		case tuievents.LogChunkMsg:
			if strings.Contains(msg.Chunk, "READ demo.py") || strings.Contains(msg.Chunk, "✓ READ") {
				foundChildToolReplay = true
			}
		}
	}
	if foundChildToolReplay {
		t.Fatalf("did not expect child tool history to replay without a local projection log, got %#v", sender.Snapshot())
	}
}

func TestRenderSessionEvents_ReplayedChildUserEventIsSuppressedFromMainTranscript(t *testing.T) {
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	events := []*session.Event{{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "codex",
				"state":            "running",
			},
		}),
	}}
	childMeta := map[string]any{
		"parent_session_id":   parent.ID,
		"child_session_id":    child.ID,
		"delegation_id":       "dlg-1",
		"parent_tool_call_id": "call-spawn-1",
		"parent_tool_name":    "SPAWN",
		"_ui_agent":           "codex",
	}
	events = append(events, &session.Event{
		ID:        "ev-child-user",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleUser, "child internal input"),
		Meta:      childMeta,
	})

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parent.ID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.UserMessageMsg:
			if strings.Contains(msg.Text, "child internal input") {
				t.Fatalf("did not expect child user replay in main transcript, got %#v", sender.Snapshot())
			}
		case tuievents.LogChunkMsg:
			if strings.Contains(msg.Chunk, "child internal input") {
				t.Fatalf("did not expect child user replay as log chunk, got %#v", sender.Snapshot())
			}
		}
	}
}

func TestRenderSessionEvents_ReplayedBashTaskStreamUsesOriginalCallID(t *testing.T) {
	events := []*session.Event{
		{
			ID:        "ev-bash-call",
			SessionID: "resume-parent",
			Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
				ID:   "call-bash-1",
				Name: "BASH",
				Args: `{"command":"echo hello"}`,
			}}, ""),
		},
		{
			ID:        "ev-bash-result",
			SessionID: "resume-parent",
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:   "call-bash-1",
				Name: "BASH",
				Result: taskstream.AppendResultEvent(map[string]any{
					"task_id": "task-bash-1",
					"state":   "completed",
				}, taskstream.Event{
					Label:  "BASH",
					TaskID: "task-bash-1",
					CallID: "task-bash-1",
					Stream: "stdout",
					Chunk:  "hello from replay\n",
					State:  "completed",
				}),
			}),
		},
	}

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:   context.Background(),
		appName:   "app",
		userID:    "u",
		sessionID: "resume-parent",
		workspace: workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender: sender,
	}

	if err := console.renderSessionEvents(events); err != nil {
		t.Fatal(err)
	}

	var (
		foundReplayStdout bool
		foundWrongCallID  bool
	)
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.TaskStreamMsg)
		if !ok {
			continue
		}
		if msg.Stream == "stdout" && strings.Contains(msg.Chunk, "hello from replay") {
			if msg.CallID == "call-bash-1" {
				foundReplayStdout = true
			}
			if msg.CallID == "task-bash-1" {
				foundWrongCallID = true
			}
		}
	}
	if !foundReplayStdout {
		t.Fatalf("expected replayed bash stdout to be routed to original call id, got %#v", sender.Snapshot())
	}
	if foundWrongCallID {
		t.Fatalf("did not expect replayed bash stdout to stay bound to self-referential task id, got %#v", sender.Snapshot())
	}
}

func TestHandleResume_ReplaysRunningSubagentOnlyFromLocalProjectionLog(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), parent, &session.Event{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-1",
				"delegation_id":    "dlg-1",
				"agent":            "self",
				"state":            "running",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index: resumeIndexStub{
			resolveID: "resume-parent",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      "current",
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}
	console.sessionID = "resume-parent"
	for _, ev := range []acpProjectionPersistedEvent{
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "turn_start", SessionID: "child-1", Agent: "self", AttachTarget: "child-1", AnchorTool: "SPAWN", ClaimAnchor: true},
		{Scope: string(tuievents.ACPProjectionSubagent), ScopeID: "child-1", CallID: "call-spawn-1", Kind: "projection", SessionID: "child-1", Stream: "assistant", DeltaText: "child reply"},
	} {
		if err := console.acpProjectionStore().AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	console.sessionID = "current"

	if _, err := handleResume(console, []string{"resume-parent"}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.ACPProjectionMsg)
		if !ok {
			continue
		}
		if msg.Scope == tuievents.ACPProjectionSubagent && msg.ScopeID == "child-1" && msg.Stream == "assistant" && strings.Contains(msg.DeltaText+msg.FullText, "child reply") {
			return
		}
	}
	t.Fatal("expected static projection replay to populate running subagent panel")
}

func TestCollectResumedSubagentTargets_PrefersTaskWriteContinuation(t *testing.T) {
	events := []*session.Event{
		{
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:   "call-spawn-1",
				Name: tool.SpawnToolName,
				Result: map[string]any{
					"child_session_id": "child-1",
					"delegation_id":    "dlg-1",
					"agent":            "copilot",
					"state":            "running",
				},
			}),
		},
		{
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:   "call-task-status-1",
				Name: tool.TaskToolName,
				Result: map[string]any{
					"child_session_id":        "child-1",
					"delegation_id":           "dlg-2",
					"agent":                   "copilot",
					"state":                   "running",
					"_ui_spawn_id":            "call-task-write-1",
					"_ui_parent_tool_call_id": "call-task-write-1",
					"_ui_parent_tool_name":    tool.TaskToolName,
					"_ui_anchor_tool":         runtime.SubagentContinuationAnchorTool,
				},
			}),
		},
	}

	targets := collectResumedSubagentTargets(events, false)
	if len(targets) != 1 {
		t.Fatalf("expected one resumed target, got %#v", targets)
	}
	target := targets[0]
	if target.SpawnID != "call-task-write-1" || target.SessionID != "child-1" {
		t.Fatalf("expected continuation target to replace original spawn, got %+v", target)
	}
	if target.CallID != "call-task-write-1" || target.AnchorTool != runtime.SubagentContinuationAnchorTool {
		t.Fatalf("expected continuation anchor metadata, got %+v", target)
	}
}

func TestHandleResume_ReplaysCompletedTaskWaitWithoutACPAttach(t *testing.T) {
	store := inmemory.New()
	parent := &session.Session{AppName: "app", UserID: "u", ID: "resume-parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), parent, &session.Event{
		ID:        "ev-parent-spawn",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-spawn-1",
			Name: "SPAWN",
			Result: map[string]any{
				"child_session_id": "child-uuid-1",
				"delegation_id":    "dlg-1",
				"agent":            "gemini",
				"state":            "running",
				"task_id":          "task-1",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), parent, &session.Event{
		ID:        "ev-parent-task-wait",
		SessionID: parent.ID,
		Message: model.MessageFromToolResponse(&model.ToolResponse{
			ID:   "call-task-1",
			Name: "TASK",
			Result: map[string]any{
				"child_session_id": "child-uuid-1",
				"delegation_id":    "dlg-1",
				"agent":            "gemini",
				"state":            "completed",
				"result":           "done",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
		Index: resumeIndexStub{
			resolveID: "resume-parent",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      "current",
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	if _, err := handleResume(console, []string{"resume-parent"}); err != nil {
		t.Fatal(err)
	}
	foundDone := false
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.SubagentDoneMsg)
		if !ok {
			continue
		}
		if msg.SpawnID == "child-uuid-1" && msg.State == "completed" {
			foundDone = true
			break
		}
	}
	if !foundDone {
		t.Fatal("expected completed TASK wait result to finalize the original SPAWN panel")
	}
}

type resumeIndexStub struct {
	resolveID string
}

func (s resumeIndexStub) ResolveWorkspaceSessionID(_ context.Context, _ string, prefix string) (string, bool, error) {
	if strings.TrimSpace(prefix) == strings.TrimSpace(s.resolveID) {
		return s.resolveID, true, nil
	}
	return "", false, nil
}

func (s resumeIndexStub) MostRecentWorkspaceSessionID(_ context.Context, _ string, _ string) (string, bool, error) {
	return "", false, nil
}

func (s resumeIndexStub) ListWorkspaceSessionsPage(_ context.Context, _ string, _ int, _ int) ([]sessionsvc.SessionSummary, error) {
	return nil, nil
}
