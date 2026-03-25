package runtime

import "context"

type subagentContinuationContextKey struct{}

const SubagentContinuationAnchorTool = "TASK WRITE"

func withSubagentContinuation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, subagentContinuationContextKey{}, true)
}

func isSubagentContinuation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(subagentContinuationContextKey{}).(bool)
	return active
}

func WithSubagentContinuation(ctx context.Context) context.Context {
	return withSubagentContinuation(ctx)
}

func IsSubagentContinuation(ctx context.Context) bool {
	return isSubagentContinuation(ctx)
}
