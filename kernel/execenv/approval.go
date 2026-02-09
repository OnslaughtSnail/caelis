package execenv

import "context"

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
