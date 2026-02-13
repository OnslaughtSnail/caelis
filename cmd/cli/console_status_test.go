package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state=completed phase=run") {
		t.Fatalf("expected completed run_state line, got: %s", text)
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
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state=waiting_approval phase=run") {
		t.Fatalf("expected waiting_approval run_state line, got: %s", text)
	}
	if !strings.Contains(text, "run_state_error_code=ERR_APPROVAL_REQUIRED") {
		t.Fatalf("expected run_state_error_code line, got: %s", text)
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
	}
	_, err = handleStatus(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "run_state=none") {
		t.Fatalf("expected run_state=none line, got: %s", text)
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
