package execenv

import (
	"context"
	"errors"
	"fmt"
)

type approvalContextKey struct{}

// ApprovalRequest describes one approval request raised by tools.
type ApprovalRequest struct {
	ToolName string
	Action   string
	Reason   string
	Command  string
}

// Approver handles interactive approval decision in upper application layer.
type Approver interface {
	Approve(context.Context, ApprovalRequest) (bool, error)
}

// ApprovalAbortedError indicates user explicitly canceled an approval request.
type ApprovalAbortedError struct {
	Reason string
}

func (e *ApprovalAbortedError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "approval canceled by user"
	}
	return fmt.Sprintf("tool: approval canceled: %s", reason)
}

// IsApprovalAborted reports whether err indicates user canceled approval.
func IsApprovalAborted(err error) bool {
	if err == nil {
		return false
	}
	var target *ApprovalAbortedError
	return errors.As(err, &target)
}

// WithApprover injects one approver into context.
func WithApprover(ctx context.Context, approver Approver) context.Context {
	if ctx == nil || approver == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalContextKey{}, approver)
}

// ApproverFromContext returns approver from context.
func ApproverFromContext(ctx context.Context) (Approver, bool) {
	if ctx == nil {
		return nil, false
	}
	approver, ok := ctx.Value(approvalContextKey{}).(Approver)
	return approver, ok
}
