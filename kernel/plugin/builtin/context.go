package builtin

import (
	"context"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

type runtimeContextKey struct{}
type mcpManagerContextKey struct{}

// WithExecutionRuntime injects one execution runtime for builtin tool providers.
func WithExecutionRuntime(ctx context.Context, runtime toolexec.Runtime) context.Context {
	if ctx == nil || runtime == nil {
		return ctx
	}
	return context.WithValue(ctx, runtimeContextKey{}, runtime)
}

func executionRuntimeFromContext(ctx context.Context) toolexec.Runtime {
	if ctx == nil {
		return nil
	}
	runtime, ok := ctx.Value(runtimeContextKey{}).(toolexec.Runtime)
	if !ok {
		return nil
	}
	return runtime
}

// WithMCPToolManager injects MCP tool manager for builtin mcp tool provider.
func WithMCPToolManager(ctx context.Context, manager *toolmcp.Manager) context.Context {
	if ctx == nil || manager == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpManagerContextKey{}, manager)
}

func mcpManagerFromContext(ctx context.Context) *toolmcp.Manager {
	if ctx == nil {
		return nil
	}
	manager, ok := ctx.Value(mcpManagerContextKey{}).(*toolmcp.Manager)
	if !ok {
		return nil
	}
	return manager
}
