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

func TestHandleResumeEmitsSessionHintInTUI(t *testing.T) {
	store := inmemory.New()
	target := &session.Session{AppName: "app", UserID: "u", ID: "resume-me"}
	if _, err := store.GetOrCreate(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), target, &session.Event{
		ID:        "ev-1",
		SessionID: target.ID,
		Message:   model.Message{Role: model.RoleAssistant, Text: "hello again"},
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

type resumeIndexStub struct {
	resolveID string
}

func (s resumeIndexStub) ResolveWorkspaceSessionID(workspaceKey, prefix string) (string, bool, error) {
	if strings.TrimSpace(prefix) == strings.TrimSpace(s.resolveID) {
		return s.resolveID, true, nil
	}
	return "", false, nil
}

func (s resumeIndexStub) MostRecentWorkspaceSessionID(workspaceKey, excludeSessionID string) (string, bool, error) {
	return "", false, nil
}

func (s resumeIndexStub) ListWorkspaceSessionsPage(workspaceKey string, page int, pageSize int) ([]sessionsvc.SessionSummary, error) {
	return nil, nil
}
