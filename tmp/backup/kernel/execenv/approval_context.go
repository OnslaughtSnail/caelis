package execenv

import "context"

type interactiveApprovalContextKey struct{}

func WithInteractiveApprovalRequired(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, interactiveApprovalContextKey{}, true)
}

func InteractiveApprovalRequired(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	required, _ := ctx.Value(interactiveApprovalContextKey{}).(bool)
	return required
}
