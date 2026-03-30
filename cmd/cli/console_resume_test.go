package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
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

func TestHandleResume_ReplaysSpawnedSubagentPanelsFromChildSessions(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range sender.Snapshot() {
			if msg, ok := raw.(tuievents.RawDeltaMsg); ok && msg.Target == tuievents.RawDeltaTargetSubagent && msg.ScopeID == "child-1" && msg.Stream == "assistant" && strings.Contains(msg.Text, "child reply") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected resumed replay to restore child subagent stream")
}

func TestHandleResume_DoesNotBlockOnAsyncSubagentLoadReplay(t *testing.T) {
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, onUpdate func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		return &blockingResumeClient{
			loadDelay: 200 * time.Millisecond,
			updates: []acpclient.UpdateEnvelope{
				resumedACPTextUpdate("child-1", "child reply"),
			},
			onUpdate: onUpdate,
		}, func() {}, nil
	}

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
		gateway:        gw,
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	start := time.Now()
	if _, err := handleResume(console, []string{"resume-parent"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 150*time.Millisecond {
		t.Fatalf("expected handleResume to return before ACP load replay finishes, took %s", elapsed)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range sender.Snapshot() {
			if msg, ok := raw.(tuievents.RawDeltaMsg); ok && msg.Target == tuievents.RawDeltaTargetSubagent && msg.ScopeID == "child-1" && msg.Stream == "assistant" && strings.Contains(msg.Text, "child reply") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected async ACP load replay to populate subagent panel")
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

	targets := collectResumedSubagentTargets(events)
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

func TestHandleResume_SkipsACPReplayWhenChildRunStateIsAlreadyCompleted(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
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
				"agent":            "gemini",
				"state":            "running",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if updater, ok := any(store).(session.StateUpdateStore); ok {
		if err := updater.UpdateState(context.Background(), child, func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			values["runtime.lifecycle"] = map[string]any{
				"status": string(runtime.RunLifecycleStatusCompleted),
				"phase":  "run",
			}
			return values, nil
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("expected state update store")
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
	var adapterCalls atomic.Int32
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, _ func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		adapterCalls.Add(1)
		return &blockingResumeClient{}, func() {}, nil
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
		rt:             rt,
	}

	if _, err := handleResume(console, []string{"resume-parent"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := adapterCalls.Load(); got != 0 {
		t.Fatalf("expected completed child run state to skip ACP replay, got %d adapter calls", got)
	}
}

func TestShouldReplayResumedSubagentTarget_UsesChildSessionStateForContinuation(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-1"}
	if _, err := store.GetOrCreate(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), child, runtime.LifecycleEvent(child, runtime.RunLifecycleStatusCompleted, "run", nil)); err != nil {
		t.Fatal(err)
	}

	console := &cliConsole{
		appName: "app",
		userID:  "u",
		rt:      rt,
	}
	if console.shouldReplayResumedSubagentTarget(context.Background(), resumedSubagentTarget{
		SpawnID:   "call-task-write-1",
		SessionID: "child-1",
	}) {
		t.Fatal("expected completed child continuation to skip ACP replay")
	}
}

func TestHandleResume_SkipsACPReplayForCompletedSpawn(t *testing.T) {
	store := inmemory.New()
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
				"state":            "completed",
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-child-1",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "child reply"),
		Meta: map[string]any{
			"parent_session_id":   parent.ID,
			"child_session_id":    child.ID,
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-spawn-1",
			"parent_tool_name":    "SPAWN",
		},
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
	var adapterCalls atomic.Int32
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, _ func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		adapterCalls.Add(1)
		return &blockingResumeClient{}, func() {}, nil
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
	time.Sleep(100 * time.Millisecond)
	if got := adapterCalls.Load(); got != 0 {
		t.Fatalf("expected completed spawn to skip ACP replay, got %d adapter calls", got)
	}
}

func TestHandleResume_SkipsACPReplayWhenTaskWaitAlreadyCompleted(t *testing.T) {
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
	var adapterCalls atomic.Int32
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, _ func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		adapterCalls.Add(1)
		return &blockingResumeClient{}, func() {}, nil
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
	time.Sleep(100 * time.Millisecond)
	if got := adapterCalls.Load(); got != 0 {
		t.Fatalf("expected completed TASK wait result to skip ACP replay, got %d adapter calls", got)
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

func TestResumedSubagentLoadTimeoutForAgent(t *testing.T) {
	if got := resumedSubagentLoadTimeoutForAgent("self"); got != resumedSubagentSelfLoadTimeout {
		t.Fatalf("expected self timeout %v, got %v", resumedSubagentSelfLoadTimeout, got)
	}
	if got := resumedSubagentLoadTimeoutForAgent("codex"); got != resumedSubagentACPLoadTimeout {
		t.Fatalf("expected ACP timeout %v, got %v", resumedSubagentACPLoadTimeout, got)
	}
	if resumedSubagentACPLoadTimeout <= 5*time.Second {
		t.Fatalf("expected ACP resume timeout to exceed cold-start window, got %v", resumedSubagentACPLoadTimeout)
	}
}

func TestRestoreResumedSubagentPanelFromACP_LoadTimeoutDoesNotEmitFailed(t *testing.T) {
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, _ func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		return &blockingResumeClient{loadDelay: 200 * time.Millisecond}, func() {}, nil
	}

	prev := resumedSubagentSelfLoadTimeout
	resumedSubagentSelfLoadTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		resumedSubagentSelfLoadTimeout = prev
	})

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      "parent",
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	done := make(chan struct{})
	go func() {
		console.restoreResumedSubagentPanelFromACP(context.Background(), "parent", resumedSubagentTarget{
			SpawnID:   "child-1",
			SessionID: "child-1",
			Agent:     "self",
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected replay attach to stop after local load timeout")
	}

	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.SubagentDoneMsg)
		if !ok {
			continue
		}
		if msg.SpawnID == "child-1" && msg.State == "failed" {
			t.Fatalf("expected resume load timeout to avoid terminal failed update, got %#v", msg)
		}
	}
}

func TestRestoreResumedSubagentPanelFromACP_LoadFailureEmitsFailed(t *testing.T) {
	prevStarter := startResumedACPClient
	t.Cleanup(func() {
		startResumedACPClient = prevStarter
	})
	startResumedACPClient = func(_ *cliConsole, _ context.Context, _ resumedSubagentTarget, _ func(acpclient.UpdateEnvelope)) (resumedACPClient, func(), error) {
		return &blockingResumeClient{loadErr: errors.New("load failed")}, func() {}, nil
	}

	sender := &testSender{}
	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      "parent",
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:      sender,
		spawnPreviewer: newSpawnPreviewProjector(),
	}

	console.restoreResumedSubagentPanelFromACP(context.Background(), "parent", resumedSubagentTarget{
		SpawnID:   "child-1",
		SessionID: "child-1",
		Agent:     "self",
	})

	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.SubagentDoneMsg)
		if !ok {
			continue
		}
		if msg.SpawnID == "child-1" && msg.State == "failed" {
			return
		}
	}
	t.Fatalf("expected failed terminal update after non-timeout load failure, got %#v", sender.Snapshot())
}

type resumeIndexStub struct {
	resolveID string
}

type blockingResumeClient struct {
	initDelay time.Duration
	loadDelay time.Duration
	loadErr   error
	updates   []acpclient.UpdateEnvelope
	onUpdate  func(acpclient.UpdateEnvelope)
}

func (c *blockingResumeClient) Initialize(ctx context.Context) (acpclient.InitializeResponse, error) {
	select {
	case <-ctx.Done():
		return acpclient.InitializeResponse{}, ctx.Err()
	case <-time.After(c.initDelay):
	}
	return acpclient.InitializeResponse{}, nil
}

func (c *blockingResumeClient) LoadSession(ctx context.Context, sessionID string, _ string, _ map[string]any) (acpclient.LoadSessionResponse, error) {
	select {
	case <-ctx.Done():
		return acpclient.LoadSessionResponse{}, ctx.Err()
	case <-time.After(c.loadDelay):
	}
	if c.loadErr != nil {
		return acpclient.LoadSessionResponse{}, c.loadErr
	}
	for _, update := range c.updates {
		if c.onUpdate != nil {
			update.SessionID = strings.TrimSpace(sessionID)
			c.onUpdate(update)
		}
	}
	return acpclient.LoadSessionResponse{}, nil
}

func resumedACPTextUpdate(sessionID string, text string) acpclient.UpdateEnvelope {
	return acpclient.UpdateEnvelope{
		SessionID: strings.TrimSpace(sessionID),
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalResumeTextChunk(text),
		},
	}
}

func mustMarshalResumeTextChunk(text string) json.RawMessage {
	data, err := json.Marshal(acpclient.TextContent{Type: "text", Text: text})
	if err != nil {
		panic(err)
	}
	return data
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
