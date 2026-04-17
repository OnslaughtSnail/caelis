package acpmeta

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	stateKeyControllerEpoch = "controllerEpoch"
	stateKeyRemoteSync      = "remoteSync"

	epochFieldID             = "epochId"
	epochFieldControllerKind = "controllerKind"
	epochFieldControllerID   = "controllerId"

	syncFieldControllerID       = "controllerId"
	syncFieldRemoteSessionID    = "remoteSessionId"
	syncFieldLastHandoffEventID = "lastHandoffEventId"
	syncFieldLastHandoffEpochID = "lastHandoffEpochId"
	syncFieldHandoffHash        = "handoffHash"

	// ControllerKindSelf means the local kernel (llmagent) is in charge.
	ControllerKindSelf = "self"
	// ControllerKindACP means an external ACP agent is the primary controller.
	ControllerKindACP = "acp"
)

// ControllerEpoch represents one contiguous period during which a single
// controller is driving the main session.
type ControllerEpoch struct {
	// EpochID is a monotonically increasing identifier unique within a session.
	EpochID string
	// ControllerKind is "self" or "acp".
	ControllerKind string
	// ControllerID is the agent ID for ACP, empty for self.
	ControllerID string
}

// RemoteSyncState tracks what has been handed off to a particular remote ACP
// controller session, so we can decide between full and incremental handoff
// and avoid duplicate injection.
type RemoteSyncState struct {
	ControllerID       string
	RemoteSessionID    string
	LastHandoffEventID string
	LastHandoffEpochID string
	HandoffHash        string
}

// ControllerEpochFromState extracts the current epoch from session state.
func ControllerEpochFromState(state map[string]any) ControllerEpoch {
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		return ControllerEpoch{}
	}
	raw, _ := acpState[stateKeyControllerEpoch].(map[string]any)
	if len(raw) == 0 {
		return ControllerEpoch{}
	}
	return ControllerEpoch{
		EpochID:        strings.TrimSpace(stringValue(raw[epochFieldID])),
		ControllerKind: strings.TrimSpace(stringValue(raw[epochFieldControllerKind])),
		ControllerID:   strings.TrimSpace(stringValue(raw[epochFieldControllerID])),
	}
}

// ControllerEpochFromStore loads the current epoch from the session store.
func ControllerEpochFromStore(ctx context.Context, store session.StateStore, sess *session.Session) (ControllerEpoch, error) {
	if store == nil || sess == nil {
		return ControllerEpoch{}, nil
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return ControllerEpoch{}, err
	}
	return ControllerEpochFromState(values), nil
}

// StoreControllerEpoch writes the epoch into a session state snapshot.
func StoreControllerEpoch(state map[string]any, epoch ControllerEpoch) map[string]any {
	if len(state) == 0 {
		state = map[string]any{}
	} else {
		state = maps.Clone(state)
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		acpState = map[string]any{}
	} else {
		acpState = maps.Clone(acpState)
	}
	acpState[stateKeyControllerEpoch] = map[string]any{
		epochFieldID:             epoch.EpochID,
		epochFieldControllerKind: epoch.ControllerKind,
		epochFieldControllerID:   epoch.ControllerID,
	}
	state[stateKeyACP] = acpState
	return state
}

// UpdateControllerEpoch atomically reads and updates the epoch in the store.
func UpdateControllerEpoch(ctx context.Context, store session.StateStore, sess *session.Session, update func(ControllerEpoch) ControllerEpoch) error {
	if store == nil || sess == nil || update == nil {
		return nil
	}
	apply := func(values map[string]any) map[string]any {
		return StoreControllerEpoch(values, update(ControllerEpochFromState(values)))
	}
	if updater, ok := store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sess, func(values map[string]any) (map[string]any, error) {
			return apply(values), nil
		})
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	return store.ReplaceState(ctx, sess, apply(values))
}

// RemoteSyncStateFromState extracts the remote sync state from session state.
func RemoteSyncStateFromState(state map[string]any) RemoteSyncState {
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		return RemoteSyncState{}
	}
	raw, _ := acpState[stateKeyRemoteSync].(map[string]any)
	if len(raw) == 0 {
		return RemoteSyncState{}
	}
	return RemoteSyncState{
		ControllerID:       strings.TrimSpace(stringValue(raw[syncFieldControllerID])),
		RemoteSessionID:    strings.TrimSpace(stringValue(raw[syncFieldRemoteSessionID])),
		LastHandoffEventID: strings.TrimSpace(stringValue(raw[syncFieldLastHandoffEventID])),
		LastHandoffEpochID: strings.TrimSpace(stringValue(raw[syncFieldLastHandoffEpochID])),
		HandoffHash:        strings.TrimSpace(stringValue(raw[syncFieldHandoffHash])),
	}
}

// RemoteSyncStateFromStore loads the sync state from the session store.
func RemoteSyncStateFromStore(ctx context.Context, store session.StateStore, sess *session.Session) (RemoteSyncState, error) {
	if store == nil || sess == nil {
		return RemoteSyncState{}, nil
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return RemoteSyncState{}, err
	}
	return RemoteSyncStateFromState(values), nil
}

// StoreRemoteSyncState writes the sync state into a session state snapshot.
func StoreRemoteSyncState(state map[string]any, sync RemoteSyncState) map[string]any {
	if len(state) == 0 {
		state = map[string]any{}
	} else {
		state = maps.Clone(state)
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		acpState = map[string]any{}
	} else {
		acpState = maps.Clone(acpState)
	}
	if sync.RemoteSessionID == "" && sync.ControllerID == "" {
		delete(acpState, stateKeyRemoteSync)
	} else {
		acpState[stateKeyRemoteSync] = map[string]any{
			syncFieldControllerID:       sync.ControllerID,
			syncFieldRemoteSessionID:    sync.RemoteSessionID,
			syncFieldLastHandoffEventID: sync.LastHandoffEventID,
			syncFieldLastHandoffEpochID: sync.LastHandoffEpochID,
			syncFieldHandoffHash:        sync.HandoffHash,
		}
	}
	if len(acpState) == 0 {
		delete(state, stateKeyACP)
		return state
	}
	state[stateKeyACP] = acpState
	return state
}

// UpdateRemoteSyncState atomically reads and updates the sync state.
func UpdateRemoteSyncState(ctx context.Context, store session.StateStore, sess *session.Session, update func(RemoteSyncState) RemoteSyncState) error {
	if store == nil || sess == nil || update == nil {
		return nil
	}
	apply := func(values map[string]any) map[string]any {
		return StoreRemoteSyncState(values, update(RemoteSyncStateFromState(values)))
	}
	if updater, ok := store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sess, func(values map[string]any) (map[string]any, error) {
			return apply(values), nil
		})
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	return store.ReplaceState(ctx, sess, apply(values))
}

// NextEpochID increments from the current epoch to produce the next ID.
func NextEpochID(current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return "1"
	}
	var n int
	if _, err := fmt.Sscanf(current, "%d", &n); err != nil || n < 0 {
		return "1"
	}
	return fmt.Sprintf("%d", n+1)
}
