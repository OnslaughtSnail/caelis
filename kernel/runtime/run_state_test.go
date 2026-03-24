package runtime

import (
	"context"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestLifecycleFromEvent_Parses(t *testing.T) {
	ev := &session.Event{
		ID:   "ev_lifecycle",
		Time: time.Now(),
		Meta: map[string]any{
			metaKind:            metaKindLifecycle,
			MetaContractVersion: ContractVersionV1,
			MetaLifecycle: map[string]any{
				"status":     string(RunLifecycleStatusWaitingApproval),
				"phase":      "run",
				"error":      "approval required",
				"error_code": string(toolexec.ErrorCodeApprovalRequired),
			},
		},
	}
	info, ok := LifecycleFromEvent(ev)
	if !ok {
		t.Fatal("expected lifecycle event parsed")
	}
	if info.Status != RunLifecycleStatusWaitingApproval {
		t.Fatalf("unexpected status: %q", info.Status)
	}
	if info.ErrorCode != toolexec.ErrorCodeApprovalRequired {
		t.Fatalf("unexpected error code: %q", info.ErrorCode)
	}
}

func TestRuntime_RunState_Completed(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-completed",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle {
		t.Fatal("expected lifecycle state")
	}
	if state.Status != RunLifecycleStatusCompleted {
		t.Fatalf("expected completed status, got %q", state.Status)
	}
}

func TestRuntime_RunState_WaitingApproval(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var gotErr error
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-approval",
		Input:     "hello",
		Agent:     approvalRequiredAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected approval required error")
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle {
		t.Fatal("expected lifecycle state")
	}
	if state.Status != RunLifecycleStatusWaitingApproval {
		t.Fatalf("expected waiting_approval status, got %q", state.Status)
	}
	if state.ErrorCode != toolexec.ErrorCodeApprovalRequired {
		t.Fatalf("expected approval-required error code, got %q", state.ErrorCode)
	}
}

func TestRuntime_RunState_NoLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-run-state-no-lifecycle"}
	_, err = store.GetOrCreate(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "ev_user",
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleUser, "hello"),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-no-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.HasLifecycle {
		t.Fatalf("expected no lifecycle state, got %+v", state)
	}
}

func TestRuntime_RunState_FromSnapshot(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-run-state-snapshot"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(0)
	if err := store.ReplaceState(context.Background(), sess, map[string]any{
		runtimeLifecycleStateKey: map[string]any{
			"status":     string(RunLifecycleStatusCompleted),
			"phase":      "run",
			"error":      "",
			"error_code": "",
			"event_id":   "ev_done",
			"updated_at": now.Format(time.RFC3339Nano),
		},
	}); err != nil {
		t.Fatal(err)
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle {
		t.Fatal("expected lifecycle state from snapshot")
	}
	if state.Status != RunLifecycleStatusCompleted {
		t.Fatalf("unexpected status %q", state.Status)
	}
	if state.EventID != "ev_done" {
		t.Fatalf("unexpected event id %q", state.EventID)
	}
	if !state.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected updated_at %s", state.UpdatedAt)
	}
}
