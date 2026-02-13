package execenv

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorCode is a stable machine-readable code for kernel runtime/execution errors.
type ErrorCode string

const (
	ErrorCodeSessionBusy          ErrorCode = "ERR_SESSION_BUSY"
	ErrorCodeApprovalRequired      ErrorCode = "ERR_APPROVAL_REQUIRED"
	ErrorCodeApprovalAborted       ErrorCode = "ERR_APPROVAL_ABORTED"
	ErrorCodeSandboxUnsupported    ErrorCode = "ERR_SANDBOX_UNSUPPORTED"
	ErrorCodeSandboxUnavailable    ErrorCode = "ERR_SANDBOX_UNAVAILABLE"
	ErrorCodeSandboxCommandTimeout ErrorCode = "ERR_SANDBOX_COMMAND_TIMEOUT"
	ErrorCodeSandboxIdleTimeout    ErrorCode = "ERR_SANDBOX_IDLE_TIMEOUT"
	ErrorCodeHostCommandTimeout    ErrorCode = "ERR_HOST_COMMAND_TIMEOUT"
	ErrorCodeHostIdleTimeout       ErrorCode = "ERR_HOST_IDLE_TIMEOUT"
)

// CodedError exposes a stable code for programmatic handling.
type CodedError interface {
	error
	Code() ErrorCode
}

type codedError struct {
	code    ErrorCode
	message string
	cause   error
}

func (e *codedError) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.message)
	if e.cause == nil {
		return msg
	}
	if msg == "" {
		return e.cause.Error()
	}
	return fmt.Sprintf("%s: %v", msg, e.cause)
}

func (e *codedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *codedError) Code() ErrorCode {
	if e == nil {
		return ""
	}
	return e.code
}

// NewCodedError creates a coded error with formatted message.
func NewCodedError(code ErrorCode, format string, args ...any) error {
	return &codedError{
		code:    code,
		message: fmt.Sprintf(format, args...),
	}
}

// WrapCodedError wraps an existing cause with a stable error code.
func WrapCodedError(code ErrorCode, cause error, format string, args ...any) error {
	if cause == nil {
		return NewCodedError(code, format, args...)
	}
	return &codedError{
		code:    code,
		message: fmt.Sprintf(format, args...),
		cause:   cause,
	}
}

// ErrorCodeOf extracts machine-readable error code, if present.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var coded CodedError
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}

// IsErrorCode reports whether err carries a specific machine-readable code.
func IsErrorCode(err error, code ErrorCode) bool {
	return ErrorCodeOf(err) == code
}
