package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestStoreAppendAndPersistCanonicalEvents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := NewStore(Config{
		RootDir:            root,
		SessionIDGenerator: func() string { return "sess-1" },
		EventIDGenerator:   func() string { return "evt-1" },
	})
	ctx := context.Background()

	session, err := store.GetOrCreate(ctx, sdksession.StartSessionRequest{
		AppName: "caelis",
		UserID:  "user-1",
		Workspace: sdksession.WorkspaceRef{
			Key: "ws-1",
			CWD: "/tmp/ws",
		},
	})
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	if _, err := store.AppendEvent(ctx, session.SessionRef, &sdksession.Event{
		Message: ptrMessage(sdkmodel.NewTextMessage(sdkmodel.RoleAssistant, "hello")),
		Text:    "hello",
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if _, err := store.AppendEvent(ctx, session.SessionRef, sdksession.MarkNotice(&sdksession.Event{
		Message: ptrMessage(sdkmodel.NewTextMessage(sdkmodel.RoleSystem, "warn: retrying")),
	}, "warn", "retrying")); err != nil {
		t.Fatalf("AppendEvent(notice) error = %v", err)
	}

	events, err := store.Events(ctx, sdksession.EventsRequest{SessionRef: session.SessionRef})
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if got, want := len(events), 1; got != want {
		t.Fatalf("len(events) = %d, want %d", got, want)
	}

	data, err := os.ReadFile(filepath.Join(root, "sess-1.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "\"hello\"") {
		t.Fatal("persisted file must contain canonical event text")
	}
	if strings.Contains(text, "retrying") {
		t.Fatal("persisted file must not contain transient notice text")
	}
}

func TestStoreUpdateStateAndParticipantAnchor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(NewStore(Config{
		RootDir:            root,
		SessionIDGenerator: func() string { return "sess-1" },
	}))
	ctx := context.Background()

	session, err := service.StartSession(ctx, sdksession.StartSessionRequest{
		AppName: "caelis",
		UserID:  "user-1",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}

	if err := service.UpdateState(ctx, session.SessionRef, func(state map[string]any) (map[string]any, error) {
		state["mode"] = "default"
		return state, nil
	}); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}

	session, err = service.PutParticipant(ctx, sdksession.PutParticipantRequest{
		SessionRef: session.SessionRef,
		Binding: sdksession.ParticipantBinding{
			ID:            "part-1",
			Kind:          sdksession.ParticipantKindSubagent,
			Role:          sdksession.ParticipantRoleDelegated,
			Label:         "spark-explorer",
			SessionID:     "child-1",
			Source:        "spawn",
			DelegationID:  "dlg-1",
			ControllerRef: "ep-1",
		},
	})
	if err != nil {
		t.Fatalf("PutParticipant() error = %v", err)
	}

	if got, want := len(session.Participants), 1; got != want {
		t.Fatalf("len(participants) = %d, want %d", got, want)
	}
	if got := session.Participants[0].SessionID; got != "child-1" {
		t.Fatalf("participant session_id = %q, want %q", got, "child-1")
	}

	state, err := service.SnapshotState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SnapshotState() error = %v", err)
	}
	if got := state["mode"]; got != "default" {
		t.Fatalf("state[mode] = %v, want %q", got, "default")
	}

	data, err := os.ReadFile(filepath.Join(root, "sess-1.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "\"session_id\": \"child-1\"") {
		t.Fatal("persisted participant anchor must include child session id")
	}
}

func ptrMessage(message sdkmodel.Message) *sdkmodel.Message {
	return &message
}
