package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestHandleStatus_UsesRunStateCompleted(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	app := "app"
	user := "u"
	sid := "s1"
	if err := appendLifecycleState(store, app, user, sid, runtime.RunLifecycleStatusCompleted, "run", "", ""); err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(execRT) }()
	var out bytes.Buffer
	console := &cliConsole{
		baseCtx:     context.Background(),
		rt:          rt,
		appName:     app,
		userID:      user,
		sessionID:   sid,
		workspace:   workspaceContext{CWD: "/tmp/ws"},
		execRuntime: execRT,
		sandboxType: execRT.SandboxType(),
		modelAlias:  "fake",
		out:         &out,
		ui:          newUI(&out, true, false),
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state") || !strings.Contains(text, "completed") {
		t.Fatalf("expected run_state completed, got: %s", text)
	}
	if !strings.Contains(text, "phase") || !strings.Contains(text, "run") {
		t.Fatalf("expected phase run, got: %s", text)
	}
}

func TestHandleStatus_UsesRunStateWaitingApprovalWithCode(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	app := "app"
	user := "u"
	sid := "s2"
	if err := appendLifecycleState(store, app, user, sid, runtime.RunLifecycleStatusWaitingApproval, "run", "approval required", string(toolexec.ErrorCodeApprovalRequired)); err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(execRT) }()
	var out bytes.Buffer
	console := &cliConsole{
		baseCtx:     context.Background(),
		rt:          rt,
		appName:     app,
		userID:      user,
		sessionID:   sid,
		workspace:   workspaceContext{CWD: "/tmp/ws"},
		execRuntime: execRT,
		sandboxType: execRT.SandboxType(),
		modelAlias:  "fake",
		out:         &out,
		ui:          newUI(&out, true, false),
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state") || !strings.Contains(text, "waiting_approval") {
		t.Fatalf("expected run_state waiting_approval, got: %s", text)
	}
	if !strings.Contains(text, "error_code") || !strings.Contains(text, "ERR_APPROVAL_REQUIRED") {
		t.Fatalf("expected error_code ERR_APPROVAL_REQUIRED, got: %s", text)
	}
}

func TestHandleStatus_UsesRunStateNoneWhenMissing(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	app := "app"
	user := "u"
	sid := "s3"
	sess := &session.Session{AppName: app, UserID: user, ID: sid}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(execRT) }()
	var out bytes.Buffer
	console := &cliConsole{
		baseCtx:     context.Background(),
		rt:          rt,
		appName:     app,
		userID:      user,
		sessionID:   sid,
		workspace:   workspaceContext{CWD: "/tmp/ws"},
		execRuntime: execRT,
		sandboxType: execRT.SandboxType(),
		modelAlias:  "fake",
		out:         &out,
		ui:          newUI(&out, true, false),
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state") || !strings.Contains(text, "none") {
		t.Fatalf("expected run_state none, got: %s", text)
	}
}

func appendLifecycleState(
	store *inmemory.Store,
	appName string,
	userID string,
	sessionID string,
	status runtime.RunLifecycleStatus,
	phase string,
	errText string,
	errCode string,
) error {
	sess := &session.Session{AppName: appName, UserID: userID, ID: sessionID}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		return err
	}
	meta := map[string]any{
		"kind":                      "lifecycle",
		runtime.MetaContractVersion: runtime.ContractVersionV1,
		runtime.MetaLifecycle: map[string]any{
			"status": string(status),
			"phase":  phase,
		},
	}
	payload := meta[runtime.MetaLifecycle].(map[string]any)
	if strings.TrimSpace(errText) != "" {
		payload["error"] = errText
	}
	if strings.TrimSpace(errCode) != "" {
		payload["error_code"] = errCode
	}
	return store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "ev_" + sessionID + "_" + string(status),
		SessionID: sessionID,
		Time:      time.Now(),
		Meta:      meta,
	})
}

func TestRefreshContextUsageFromEvent_FallsBackToRuntimeEstimateWithoutUsage(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	app := "app"
	user := "u"
	sid := "s-usage"
	sess := &session.Session{AppName: app, UserID: user, ID: sid}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "ev-user",
		SessionID: sid,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleUser, "please summarize the current repo state and pending edits"),
	}); err != nil {
		t.Fatal(err)
	}
	assistantEvent := &session.Event{
		ID:        "ev-assistant",
		SessionID: sid,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleAssistant, "I reviewed the repo and found two pending edit areas."),
	}
	if err := store.AppendEvent(context.Background(), sess, assistantEvent); err != nil {
		t.Fatal(err)
	}

	console := &cliConsole{
		baseCtx:          context.Background(),
		rt:               rt,
		appName:          app,
		userID:           user,
		sessionID:        sid,
		contextWindow:    128000,
		lastPromptTokens: 0,
	}
	console.refreshContextUsageFromEvent(assistantEvent)
	if console.lastPromptTokens <= 0 {
		t.Fatalf("expected runtime usage fallback to populate status tokens, got %d", console.lastPromptTokens)
	}
}
