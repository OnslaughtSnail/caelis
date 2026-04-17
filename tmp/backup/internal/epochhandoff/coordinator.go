package epochhandoff

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

// ────────────────────────────────────────────────────────────────────────────
// HandoffCoordinator — the primary façade of the Epoch Handoff Layer.
// ────────────────────────────────────────────────────────────────────────────

// HandoffCoordinator encapsulates all epoch handoff logic: closing epochs,
// generating checkpoints, assembling bundles, and managing sync waterlines.
// It is independent of kernel and ACP controller internals.
type HandoffCoordinator struct {
	store session.Store
}

// NewHandoffCoordinator creates a HandoffCoordinator backed by the given
// session store.
func NewHandoffCoordinator(store session.Store) *HandoffCoordinator {
	return &HandoffCoordinator{store: store}
}

// ────────────────────────────────────────────────────────────────────────────
// CloseEpochAndCheckpoint — close current epoch and generate checkpoint.
// ────────────────────────────────────────────────────────────────────────────

// CloseEpochAndCheckpoint closes the current epoch and generates its canonical
// checkpoint. If the epoch already has a checkpoint (idempotent), it returns
// the existing one. The checkpoint is persisted to the session as a system
// event. The summarize process itself is NOT persisted.
func (h *HandoffCoordinator) CloseEpochAndCheckpoint(ctx context.Context, sess *session.Session) (EpochCheckpoint, error) {
	if h.store == nil || sess == nil {
		return EpochCheckpoint{}, nil
	}

	epoch, err := coreacpmeta.ControllerEpochFromStore(ctx, h.store, sess)
	if err != nil {
		return EpochCheckpoint{}, err
	}
	if epoch.EpochID == "" {
		return EpochCheckpoint{}, nil
	}

	// Check if this epoch already has a checkpoint.
	existing, err := h.LoadCheckpointForEpoch(ctx, sess, epoch.EpochID)
	if err != nil {
		return EpochCheckpoint{}, err
	}
	if !existing.IsEmpty() {
		return existing, nil
	}

	events, err := h.store.ListEvents(ctx, sess)
	if err != nil {
		return EpochCheckpoint{}, err
	}

	cp := BuildCheckpoint(events, epoch, nil)
	if err := h.PersistCheckpoint(ctx, sess, cp); err != nil {
		return cp, err
	}
	return cp, nil
}

// ────────────────────────────────────────────────────────────────────────────
// BuildHandoffBundle — assemble the context package for controller switch.
// ────────────────────────────────────────────────────────────────────────────

// BuildHandoffBundle assembles a HandoffBundle for the target controller.
// It determines full vs incremental based on the sync state, collects
// relevant checkpoints, and optionally includes a transcript tail.
func (h *HandoffCoordinator) BuildHandoffBundle(
	ctx context.Context,
	sess *session.Session,
	_ string, // targetControllerID reserved for future routing
	syncState coreacpmeta.RemoteSyncState,
	transcriptTail string,
) (HandoffBundle, error) {
	events, err := h.store.ListEvents(ctx, sess)
	if err != nil {
		return HandoffBundle{}, err
	}

	mode, incrementalStart := computeHandoffMode(events, syncState)

	var relevantEvents []*session.Event
	if mode == HandoffBundleModeIncremental {
		if incrementalStart < len(events) {
			relevantEvents = events[incrementalStart:]
		}
	} else {
		relevantEvents = events
	}

	epoch, _ := coreacpmeta.ControllerEpochFromStore(ctx, h.store, sess)

	// Build checkpoint from relevant events.
	cp := BuildCheckpoint(relevantEvents, epoch, nil)
	if mode == HandoffBundleModeIncremental {
		cp.System.Mode = CheckpointModeIncremental
	}

	bundle := HandoffBundle{
		Mode:                 mode,
		Checkpoints:          []EpochCheckpoint{cp},
		RecentTranscriptTail: strings.TrimSpace(transcriptTail),
	}

	// Set watermark for subsequent incremental computation.
	if last := lastNonNilEvent(events); last != nil {
		bundle.SyncWatermarkEventID = eventIDOrTime(last)
	}

	return bundle, nil
}

// ComputeIncrementalRange determines whether a full or incremental handoff is
// needed and returns the starting index for incremental events.
func ComputeIncrementalRange(events []*session.Event, syncState coreacpmeta.RemoteSyncState) (HandoffBundleMode, int) {
	return computeHandoffMode(events, syncState)
}

func computeHandoffMode(events []*session.Event, syncState coreacpmeta.RemoteSyncState) (HandoffBundleMode, int) {
	if syncState.LastHandoffEventID == "" {
		return HandoffBundleModeFull, 0
	}
	for i, ev := range events {
		if ev != nil && ev.ID == syncState.LastHandoffEventID {
			if i+1 >= len(events) {
				// No new events since last handoff.
				return HandoffBundleModeIncremental, len(events)
			}
			return HandoffBundleModeIncremental, i + 1
		}
	}
	// Waterline event not found — fall back to full.
	return HandoffBundleModeFull, 0
}

// ────────────────────────────────────────────────────────────────────────────
// Checkpoint persistence — stored as session events.
// ────────────────────────────────────────────────────────────────────────────

const (
	checkpointEventType = "epoch_checkpoint"
	checkpointMetaKey   = "epoch_checkpoint"
)

// PersistCheckpoint stores the checkpoint as a session event. Only the
// checkpoint result is persisted; the summarize process is ephemeral.
func (h *HandoffCoordinator) PersistCheckpoint(ctx context.Context, sess *session.Session, cp EpochCheckpoint) error {
	if h.store == nil || sess == nil {
		return nil
	}

	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}

	ev := &session.Event{
		Time: time.Now(),
		Meta: map[string]any{
			"event_type":      checkpointEventType,
			checkpointMetaKey: true,
			"epoch_id":        cp.System.EpochID,
			"checkpoint_id":   cp.System.CheckpointID,
		},
		// Store the full checkpoint JSON in the message text for durability.
		// This event is not a canonical history event; it's a system artifact.
		Message: systemCheckpointMessage(string(raw)),
	}
	ev = session.MarkMirror(ev)
	// MarkMirror normalizes event_type to a built-in session type. Restore the
	// checkpoint discriminator so the handoff layer can load persisted
	// checkpoints back from session history.
	ev.Meta["event_type"] = checkpointEventType
	ev.Meta[checkpointMetaKey] = true
	return h.store.AppendEvent(ctx, sess, ev)
}

// LoadCheckpointForEpoch scans session events for the canonical checkpoint of
// the given epoch.
func (h *HandoffCoordinator) LoadCheckpointForEpoch(ctx context.Context, sess *session.Session, epochID string) (EpochCheckpoint, error) {
	events, err := h.store.ListEvents(ctx, sess)
	if err != nil {
		return EpochCheckpoint{}, err
	}
	return findCheckpointForEpoch(events, epochID), nil
}

// LoadCheckpointState loads all persisted checkpoints from the session.
func (h *HandoffCoordinator) LoadCheckpointState(ctx context.Context, sess *session.Session) ([]EpochCheckpoint, error) {
	events, err := h.store.ListEvents(ctx, sess)
	if err != nil {
		return nil, err
	}
	var checkpoints []EpochCheckpoint
	for _, ev := range events {
		if cp, ok := parseCheckpointEvent(ev); ok {
			checkpoints = append(checkpoints, cp)
		}
	}
	return checkpoints, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Sync waterline management
// ────────────────────────────────────────────────────────────────────────────

// UpdateSyncWaterline updates the RemoteSyncState after a successful handoff.
func (h *HandoffCoordinator) UpdateSyncWaterline(
	ctx context.Context,
	sess *session.Session,
	controllerID string,
	remoteSessionID string,
	epochID string,
	watermarkEventID string,
	handoffHash string,
) error {
	return coreacpmeta.UpdateRemoteSyncState(ctx, h.store, sess, func(_ coreacpmeta.RemoteSyncState) coreacpmeta.RemoteSyncState {
		return coreacpmeta.RemoteSyncState{
			ControllerID:       controllerID,
			RemoteSessionID:    remoteSessionID,
			LastHandoffEventID: watermarkEventID,
			LastHandoffEpochID: epochID,
			HandoffHash:        handoffHash,
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────────────

func findCheckpointForEpoch(events []*session.Event, epochID string) EpochCheckpoint {
	for _, ev := range events {
		cp, ok := parseCheckpointEvent(ev)
		if ok && cp.System.EpochID == epochID {
			return cp
		}
	}
	return EpochCheckpoint{}
}

func parseCheckpointEvent(ev *session.Event) (EpochCheckpoint, bool) {
	if ev == nil {
		return EpochCheckpoint{}, false
	}
	t, _ := ev.Meta["event_type"].(string)
	if strings.TrimSpace(t) != checkpointEventType {
		return EpochCheckpoint{}, false
	}
	text := ev.Message.TextContent()
	if text == "" {
		return EpochCheckpoint{}, false
	}
	var cp EpochCheckpoint
	if err := json.Unmarshal([]byte(text), &cp); err != nil {
		return EpochCheckpoint{}, false
	}
	return cp, true
}
