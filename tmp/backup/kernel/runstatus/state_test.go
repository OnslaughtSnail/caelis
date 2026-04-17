package runstatus

import (
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestFromEventParsesLifecycle(t *testing.T) {
	ev := &session.Event{
		ID:   "ev_lifecycle",
		Time: time.Now(),
		Meta: map[string]any{
			"kind":              "lifecycle",
			MetaContractVersion: ContractVersionV1,
			MetaLifecycle: map[string]any{
				"status":     string(StatusWaitingApproval),
				"phase":      "run",
				"error":      "approval required",
				"error_code": string(toolexec.ErrorCodeApprovalRequired),
			},
		},
	}
	info, ok := FromEvent(ev)
	if !ok {
		t.Fatal("expected lifecycle event parsed")
	}
	if info.Status != StatusWaitingApproval {
		t.Fatalf("unexpected status: %q", info.Status)
	}
	if info.ErrorCode != toolexec.ErrorCodeApprovalRequired {
		t.Fatalf("unexpected error code: %q", info.ErrorCode)
	}
}

func TestStateSnapshotRoundTrip(t *testing.T) {
	now := time.Now().UTC().Round(0)
	state := State{
		HasLifecycle: true,
		Status:       StatusCompleted,
		Phase:        "run",
		ErrorCode:    toolexec.ErrorCodeApprovalRequired,
		EventID:      "ev_done",
		UpdatedAt:    now,
	}

	got, ok := StateFromSnapshot(StateSnapshot(state))
	if !ok {
		t.Fatal("expected snapshot round-trip")
	}
	if got.Status != state.Status {
		t.Fatalf("Status=%q, want %q", got.Status, state.Status)
	}
	if got.EventID != state.EventID {
		t.Fatalf("EventID=%q, want %q", got.EventID, state.EventID)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt=%s, want %s", got.UpdatedAt, now)
	}
}
