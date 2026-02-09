package bootstrap

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// AssembleSpec describes plugin-level assembly settings.
type AssembleSpec struct {
	ToolProviders   []string
	PolicyProviders []string
}

// ResolvedSpec is the assembled runtime capability set.
type ResolvedSpec struct {
	Tools    []tool.Tool
	Policies []policy.Hook
}

// Assemble resolves runtime capabilities from plugin providers.
func Assemble(ctx context.Context, spec AssembleSpec) (*ResolvedSpec, error) {
	preg := plugin.NewRegistry()
	if err := pluginbuiltin.RegisterAll(preg); err != nil {
		return nil, err
	}
	tools, err := preg.ResolveTools(ctx, spec.ToolProviders)
	if err != nil {
		return nil, err
	}
	policies, err := preg.ResolvePolicies(ctx, spec.PolicyProviders)
	if err != nil {
		return nil, err
	}
	resolved := &ResolvedSpec{
		Tools:    tools,
		Policies: policies,
	}
	return resolved, nil
}
