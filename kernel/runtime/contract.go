package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	// ContractVersionV1 is the first stable runtime event contract version.
	ContractVersionV1 = "v1"

	// MetaContractVersion marks runtime event contract version in event meta.
	MetaContractVersion = "contract_version"
	// MetaLifecycle is the payload key for lifecycle details.
	MetaLifecycle = "lifecycle"
)

const (
	metaKindLifecycle = "lifecycle"
)

// RunLifecycleStatus is a machine-readable runtime run status.
type RunLifecycleStatus string

const (
	RunLifecycleStatusRunning         RunLifecycleStatus = "running"
	RunLifecycleStatusWaitingApproval RunLifecycleStatus = "waiting_approval"
	RunLifecycleStatusInterrupted     RunLifecycleStatus = "interrupted"
	RunLifecycleStatusFailed          RunLifecycleStatus = "failed"
	RunLifecycleStatusCompleted       RunLifecycleStatus = "completed"
)

func lifecycleStatusForError(err error) RunLifecycleStatus {
	if err == nil {
		return RunLifecycleStatusCompleted
	}
	if toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalRequired) {
		return RunLifecycleStatusWaitingApproval
	}
	if toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalAborted) || errors.Is(err, context.Canceled) {
		return RunLifecycleStatusInterrupted
	}
	return RunLifecycleStatusFailed
}

func lifecycleEvent(sess *session.Session, status RunLifecycleStatus, phase string, cause error) *session.Event {
	meta := map[string]any{
		metaKind:            metaKindLifecycle,
		MetaContractVersion: ContractVersionV1,
		MetaLifecycle: map[string]any{
			"status": string(status),
			"phase":  phase,
		},
	}
	lifecycle, _ := meta[MetaLifecycle].(map[string]any)
	if cause != nil {
		lifecycle["error"] = cause.Error()
		if code := toolexec.ErrorCodeOf(cause); code != "" {
			lifecycle["error_code"] = string(code)
		}
	}
	return &session.Event{
		ID:        eventID(),
		SessionID: sess.ID,
		Time:      time.Now(),
		Message: model.Message{
			Role: model.RoleSystem,
			Text: "",
		},
		Meta: meta,
	}
}

func isLifecycleEvent(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	kind, _ := ev.Meta[metaKind].(string)
	return kind == metaKindLifecycle
}

func agentHistoryEvents(events []*session.Event) []*session.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil || isLifecycleEvent(ev) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// LifecycleInfo is parsed lifecycle state from one lifecycle event.
type LifecycleInfo struct {
	Status    RunLifecycleStatus
	Phase     string
	Error     string
	ErrorCode toolexec.ErrorCode
}

// LifecycleFromEvent extracts lifecycle info from one runtime lifecycle event.
func LifecycleFromEvent(ev *session.Event) (LifecycleInfo, bool) {
	if !isLifecycleEvent(ev) {
		return LifecycleInfo{}, false
	}
	payload, ok := ev.Meta[MetaLifecycle].(map[string]any)
	if !ok {
		return LifecycleInfo{}, false
	}
	status := RunLifecycleStatus(strings.TrimSpace(fmt.Sprint(payload["status"])))
	if status == "" {
		return LifecycleInfo{}, false
	}
	info := LifecycleInfo{
		Status: status,
		Phase:  strings.TrimSpace(fmt.Sprint(payload["phase"])),
	}
	if rawErr, exists := payload["error"]; exists {
		info.Error = strings.TrimSpace(fmt.Sprint(rawErr))
	}
	if rawCode, exists := payload["error_code"]; exists {
		info.ErrorCode = toolexec.ErrorCode(strings.TrimSpace(fmt.Sprint(rawCode)))
	}
	return info, true
}
