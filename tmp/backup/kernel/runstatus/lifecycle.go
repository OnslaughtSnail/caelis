package runstatus

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
	ContractVersionV1   = "v1"
	MetaContractVersion = "contract_version"
	MetaLifecycle       = "lifecycle"

	metaKindKey       = "kind"
	metaKindLifecycle = "lifecycle"
)

type Status string

const (
	StatusRunning         Status = "running"
	StatusWaitingApproval Status = "waiting_approval"
	StatusInterrupted     Status = "interrupted"
	StatusFailed          Status = "failed"
	StatusCompleted       Status = "completed"
)

type Info struct {
	Status    Status
	Phase     string
	Error     string
	ErrorCode toolexec.ErrorCode
}

func StatusForError(err error) Status {
	if err == nil {
		return StatusCompleted
	}
	if toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalRequired) {
		return StatusWaitingApproval
	}
	if toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalAborted) || errors.Is(err, context.Canceled) {
		return StatusInterrupted
	}
	return StatusFailed
}

func Event(sess *session.Session, status Status, phase string, cause error) *session.Event {
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	meta := map[string]any{
		metaKindKey:         metaKindLifecycle,
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
		SessionID: sessionID,
		Time:      time.Now(),
		Message:   model.Message{Role: model.RoleSystem},
		Meta:      meta,
	}
}

func IsEvent(ev *session.Event) bool {
	return session.IsLifecycle(ev)
}

func FromEvent(ev *session.Event) (Info, bool) {
	if !IsEvent(ev) {
		return Info{}, false
	}
	payload, ok := ev.Meta[MetaLifecycle].(map[string]any)
	if !ok {
		return Info{}, false
	}
	status := Status(strings.TrimSpace(fmt.Sprint(payload["status"])))
	if status == "" {
		return Info{}, false
	}
	info := Info{
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
