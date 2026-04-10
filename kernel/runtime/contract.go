package runtime

import (
	"github.com/OnslaughtSnail/caelis/kernel/runstatus"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	ContractVersionV1   = runstatus.ContractVersionV1
	MetaContractVersion = runstatus.MetaContractVersion
	MetaLifecycle       = runstatus.MetaLifecycle
)

const (
	metaKindLifecycle = "lifecycle"
)

// RunLifecycleStatus is a machine-readable runtime run status.
type RunLifecycleStatus = runstatus.Status

const (
	RunLifecycleStatusRunning         RunLifecycleStatus = runstatus.StatusRunning
	RunLifecycleStatusWaitingApproval RunLifecycleStatus = runstatus.StatusWaitingApproval
	RunLifecycleStatusInterrupted     RunLifecycleStatus = runstatus.StatusInterrupted
	RunLifecycleStatusFailed          RunLifecycleStatus = runstatus.StatusFailed
	RunLifecycleStatusCompleted       RunLifecycleStatus = runstatus.StatusCompleted
)

func lifecycleStatusForError(err error) RunLifecycleStatus {
	return runstatus.StatusForError(err)
}

func lifecycleEvent(sess *session.Session, status RunLifecycleStatus, phase string, cause error) *session.Event {
	ev := runstatus.Event(sess, status, phase, cause)
	ev.ID = eventID()
	return ev
}

func LifecycleEvent(sess *session.Session, status RunLifecycleStatus, phase string, cause error) *session.Event {
	return lifecycleEvent(sess, status, phase, cause)
}

func isLifecycleEvent(ev *session.Event) bool {
	return runstatus.IsEvent(ev)
}

// LifecycleInfo is parsed lifecycle state from one lifecycle event.
type LifecycleInfo = runstatus.Info

// LifecycleFromEvent extracts lifecycle info from one runtime lifecycle event.
func LifecycleFromEvent(ev *session.Event) (LifecycleInfo, bool) {
	return runstatus.FromEvent(ev)
}
