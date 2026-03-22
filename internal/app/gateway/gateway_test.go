package gateway

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestGatewayAttachAndBackToParent(t *testing.T) {
	svc := newGatewayTestService(t)
	gw, err := New(svc)
	if err != nil {
		t.Fatal(err)
	}
	channel := ChannelRef{ID: "ch-1", AppName: "app", UserID: "u", WorkspaceKey: "wk", WorkspaceCWD: "/workspace"}

	if _, err := gw.StartSession(context.Background(), StartSessionRequest{
		Channel:            channel,
		PreferredSessionID: "parent",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := gw.AttachSession(context.Background(), AttachSessionRequest{
		Channel:        channel,
		ChildSessionID: "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID != "child" {
		t.Fatalf("expected child session attached, got %q", loaded.SessionID)
	}
	current, ok := gw.CurrentSession(channel.ID)
	if !ok || current.SessionID != "child" {
		t.Fatalf("expected current session child, got %+v ok=%v", current, ok)
	}

	loaded, err = gw.BackToParent(context.Background(), channel)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID != "parent" {
		t.Fatalf("expected parent session restored, got %q", loaded.SessionID)
	}
	current, ok = gw.CurrentSession(channel.ID)
	if !ok || current.SessionID != "parent" {
		t.Fatalf("expected current session parent, got %+v ok=%v", current, ok)
	}
}

func TestGatewayChannelStacksDoNotLeak(t *testing.T) {
	svc := newGatewayTestService(t)
	gw, err := New(svc)
	if err != nil {
		t.Fatal(err)
	}
	channelA := ChannelRef{ID: "ch-a", AppName: "app", UserID: "u", WorkspaceKey: "wk", WorkspaceCWD: "/workspace"}
	channelB := ChannelRef{ID: "ch-b", AppName: "app", UserID: "u", WorkspaceKey: "wk", WorkspaceCWD: "/workspace"}

	if _, err := gw.StartSession(context.Background(), StartSessionRequest{Channel: channelA, PreferredSessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.StartSession(context.Background(), StartSessionRequest{Channel: channelB, PreferredSessionID: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.AttachSession(context.Background(), AttachSessionRequest{Channel: channelA, ChildSessionID: "child"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gw.BackToParent(context.Background(), channelB); err == nil {
		t.Fatal("expected channel B to have no parent stack")
	}
	currentA, _ := gw.CurrentSession(channelA.ID)
	currentB, _ := gw.CurrentSession(channelB.ID)
	if currentA.SessionID != "child" || currentB.SessionID != "other" {
		t.Fatalf("unexpected channel sessions: A=%+v B=%+v", currentA, currentB)
	}
}

func newGatewayTestService(t *testing.T) *sessionsvc.Service {
	t.Helper()
	store := inmemory.New()
	for _, sess := range []*session.Session{
		{AppName: "app", UserID: "u", ID: "parent"},
		{AppName: "app", UserID: "u", ID: "child"},
		{AppName: "app", UserID: "u", ID: "other"},
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
		Message:   model.Message{Role: model.RoleAssistant, Text: "child"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "other"}, &session.Event{
		ID:        "ev-other-1",
		SessionID: "other",
		Message:   model.Message{Role: model.RoleAssistant, Text: "other"},
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
	return svc
}
