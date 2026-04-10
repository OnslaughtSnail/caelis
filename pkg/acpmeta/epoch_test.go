package acpmeta

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessioninmemory "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestControllerEpochRoundTrip(t *testing.T) {
	t.Parallel()
	epoch := ControllerEpoch{
		EpochID:        "3",
		ControllerKind: ControllerKindACP,
		ControllerID:   "copilot",
	}
	state := StoreControllerEpoch(nil, epoch)
	got := ControllerEpochFromState(state)
	if got != epoch {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, epoch)
	}
}

func TestControllerEpochFromEmptyState(t *testing.T) {
	t.Parallel()
	got := ControllerEpochFromState(nil)
	if got != (ControllerEpoch{}) {
		t.Fatalf("expected zero value from nil state, got %+v", got)
	}
}

func TestRemoteSyncStateRoundTrip(t *testing.T) {
	t.Parallel()
	sync := RemoteSyncState{
		ControllerID:       "copilot",
		RemoteSessionID:    "rs-123",
		LastHandoffEventID: "ev-42",
		LastHandoffEpochID: "3",
		HandoffHash:        "abc123",
	}
	state := StoreRemoteSyncState(nil, sync)
	got := RemoteSyncStateFromState(state)
	if got != sync {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, sync)
	}
}

func TestRemoteSyncStateEmptyClears(t *testing.T) {
	t.Parallel()
	sync := RemoteSyncState{
		ControllerID:    "copilot",
		RemoteSessionID: "rs-1",
	}
	state := StoreRemoteSyncState(nil, sync)
	// Clear by storing empty.
	state = StoreRemoteSyncState(state, RemoteSyncState{})
	got := RemoteSyncStateFromState(state)
	if got != (RemoteSyncState{}) {
		t.Fatalf("expected empty sync state after clearing, got %+v", got)
	}
}

func TestUpdateControllerEpochPersistsThroughStore(t *testing.T) {
	t.Parallel()
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "epoch-test"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}
	err := UpdateControllerEpoch(ctx, store, sess, func(_ ControllerEpoch) ControllerEpoch {
		return ControllerEpoch{
			EpochID:        "2",
			ControllerKind: ControllerKindACP,
			ControllerID:   "codex",
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ControllerEpochFromStore(ctx, store, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got.EpochID != "2" || got.ControllerKind != ControllerKindACP || got.ControllerID != "codex" {
		t.Fatalf("unexpected epoch: %+v", got)
	}
}

func TestUpdateRemoteSyncStatePersistsThroughStore(t *testing.T) {
	t.Parallel()
	store := sessioninmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "sync-test"}
	ctx := t.Context()
	if _, err := store.GetOrCreate(ctx, sess); err != nil {
		t.Fatal(err)
	}
	err := UpdateRemoteSyncState(ctx, store, sess, func(_ RemoteSyncState) RemoteSyncState {
		return RemoteSyncState{
			ControllerID:       "codex",
			RemoteSessionID:    "rs-99",
			LastHandoffEventID: "ev-last",
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := RemoteSyncStateFromStore(ctx, store, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got.ControllerID != "codex" || got.RemoteSessionID != "rs-99" || got.LastHandoffEventID != "ev-last" {
		t.Fatalf("unexpected sync state: %+v", got)
	}
}

func TestNextEpochID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"", "1"},
		{"1", "2"},
		{"10", "11"},
		{"abc", "1"},
	}
	for _, tc := range cases {
		got := NextEpochID(tc.input)
		if got != tc.want {
			t.Errorf("NextEpochID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestEpochAndSyncCoexistInState(t *testing.T) {
	t.Parallel()
	state := StoreControllerEpoch(nil, ControllerEpoch{
		EpochID: "1", ControllerKind: ControllerKindSelf,
	})
	state = StoreRemoteSyncState(state, RemoteSyncState{
		ControllerID: "copilot", RemoteSessionID: "rs-1",
	})
	epoch := ControllerEpochFromState(state)
	sync := RemoteSyncStateFromState(state)
	if epoch.EpochID != "1" || epoch.ControllerKind != ControllerKindSelf {
		t.Fatalf("epoch lost after storing sync: %+v", epoch)
	}
	if sync.ControllerID != "copilot" || sync.RemoteSessionID != "rs-1" {
		t.Fatalf("sync lost after storing epoch: %+v", sync)
	}
}
