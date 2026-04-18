package core

import "errors"

type ErrorKind string

const (
	KindValidation  ErrorKind = "validation"
	KindConflict    ErrorKind = "conflict"
	KindNotFound    ErrorKind = "not_found"
	KindInternal    ErrorKind = "internal"
	KindApproval    ErrorKind = "approval"
	KindUnsupported ErrorKind = "unsupported"
)

const (
	CodeNotImplemented          = "not_implemented"
	CodeActiveRunConflict       = "active_run_conflict"
	CodeInvalidRequest          = "invalid_request"
	CodeSubmissionUnsupported   = "submission_unsupported"
	CodeApprovalNotPending      = "approval_not_pending"
	CodeSessionNotFound         = "session_not_found"
	CodeSessionAmbiguous        = "session_ambiguous"
	CodeBindingNotFound         = "binding_not_found"
	CodeNoResumableSession      = "no_resumable_session"
	CodeNoActiveRun             = "no_active_run"
	CodeModeNotFound            = "mode_not_found"
	CodeControlPlaneUnsupported = "control_plane_unsupported"
)

type Error struct {
	Kind        ErrorKind
	Code        string
	Retryable   bool
	UserVisible bool
	Message     string
	Detail      string
	Cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return string(e.Kind) + ":" + e.Code
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Cause }

func As(err error, target any) bool { return errors.As(err, target) }
