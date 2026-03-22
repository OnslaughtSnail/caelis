package main

import (
	"context"
	"strings"
	"testing"

	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestHandleAttachAndBack(t *testing.T) {
	store := inmemory.New()
	for _, sess := range []*session.Session{
		{AppName: "app", UserID: "u", ID: "parent"},
		{AppName: "app", UserID: "u", ID: "child"},
	} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "parent"}, &session.Event{
		ID:        "ev-parent-1",
		SessionID: "parent",
		Message:   model.Message{Role: model.RoleAssistant, Text: "spawned"},
		Meta: map[string]any{
			"parent_session_id":   "parent",
			"child_session_id":    "child",
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-1",
			"parent_tool_name":    "SPAWN",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "child"}, &session.Event{
		ID:        "ev-child-1",
		SessionID: "child",
		Message:   model.Message{Role: model.RoleAssistant, Text: "child reply"},
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "parent",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
	}
	if _, err := gw.StartSession(context.Background(), appgateway.StartSessionRequest{
		Channel:            console.gatewayChannel(),
		PreferredSessionID: "parent",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := handleAttach(console, []string{"child"}); err != nil {
		t.Fatal(err)
	}
	if console.sessionID != "child" {
		t.Fatalf("expected attached child session, got %q", console.sessionID)
	}

	if _, err := handleBack(console, nil); err != nil {
		t.Fatal(err)
	}
	if console.sessionID != "parent" {
		t.Fatalf("expected parent session restored, got %q", console.sessionID)
	}
}

func TestHandleAttachAndBackEmitSessionHintsInTUI(t *testing.T) {
	store := inmemory.New()
	for _, sess := range []*session.Session{
		{AppName: "app", UserID: "u", ID: "parent"},
		{AppName: "app", UserID: "u", ID: "child"},
	} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "parent"}, &session.Event{
		ID:        "ev-parent-1",
		SessionID: "parent",
		Message:   model.Message{Role: model.RoleAssistant, Text: "spawned"},
		Meta: map[string]any{
			"parent_session_id":   "parent",
			"child_session_id":    "child",
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-1",
			"parent_tool_name":    "SPAWN",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "child"}, &session.Event{
		ID:        "ev-child-1",
		SessionID: "child",
		Message:   model.Message{Role: model.RoleAssistant, Text: "child reply"},
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
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
		sessionID:    "parent",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
		tuiSender:    sender,
	}
	if _, err := gw.StartSession(context.Background(), appgateway.StartSessionRequest{
		Channel:            console.gatewayChannel(),
		PreferredSessionID: "parent",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := handleAttach(console, []string{"child"}); err != nil {
		t.Fatal(err)
	}
	if got := lastHint(sender.msgs); !strings.Contains(got, "attached child session: child") {
		t.Fatalf("expected attach hint with session id, got %q", got)
	}

	if _, err := handleBack(console, nil); err != nil {
		t.Fatal(err)
	}
	if got := lastHint(sender.msgs); !strings.Contains(got, "returned to parent session: parent") {
		t.Fatalf("expected back hint with session id, got %q", got)
	}
}

func TestHandleAttachWithDelegationID(t *testing.T) {
	store := inmemory.New()
	for _, sess := range []*session.Session{
		{AppName: "app", UserID: "u", ID: "parent"},
		{AppName: "app", UserID: "u", ID: "child"},
	} {
		if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "parent"}, &session.Event{
		ID:        "ev-parent-1",
		SessionID: "parent",
		Message:   model.Message{Role: model.RoleAssistant, Text: "spawned"},
		Meta: map[string]any{
			"parent_session_id":   "parent",
			"child_session_id":    "child",
			"delegation_id":       "dlg-1",
			"parent_tool_call_id": "call-1",
			"parent_tool_name":    "SPAWN",
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Store:   store,
		AppName: "app",
		UserID:  "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    "parent",
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore: store,
		gateway:      gw,
	}
	if _, err := gw.StartSession(context.Background(), appgateway.StartSessionRequest{
		Channel:            console.gatewayChannel(),
		PreferredSessionID: "parent",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := handleAttach(console, []string{"dlg-1"}); err != nil {
		t.Fatal(err)
	}
	if console.sessionID != "child" {
		t.Fatalf("expected child session selected by delegation id, got %q", console.sessionID)
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
