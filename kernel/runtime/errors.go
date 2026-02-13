package runtime

import (
	"errors"
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

// SessionBusyError indicates one session already has an in-flight run.
type SessionBusyError struct {
	AppName   string
	UserID    string
	SessionID string
}

func (e *SessionBusyError) Error() string {
	if e == nil {
		return "runtime: session is busy"
	}
	return fmt.Sprintf("runtime: session %q is busy for app=%q user=%q", e.SessionID, e.AppName, e.UserID)
}

func (e *SessionBusyError) Code() toolexec.ErrorCode {
	return toolexec.ErrorCodeSessionBusy
}

func IsSessionBusy(err error) bool {
	var target *SessionBusyError
	return errors.As(err, &target)
}
