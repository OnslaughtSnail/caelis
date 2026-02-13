package runtime

import (
	"context"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
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
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-completed",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
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
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-run-state-approval",
		Input:     "hello",
		Agent:     approvalRequiredAgent{},
		Model:     llm,
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
		ID:   "ev_user",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleUser,
			Text: "hello",
		},
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
