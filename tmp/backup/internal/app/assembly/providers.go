package assembly

import (
	"context"
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

const (
	ProviderWorkspaceTools = "workspace_tools"
	ProviderShellTools     = "shell_tools"
	ProviderDefaultPolicy  = "default_allow"
)

type RegisterOptions struct {
	ExecutionRuntime toolexec.Runtime
}

func RegisterBuiltinProviders(r *plugin.Registry, options RegisterOptions) error {
	if r == nil {
		return fmt.Errorf("assembly: registry is nil")
	}
	if err := r.RegisterToolProvider(workspaceToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(shellToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterPolicyProvider(defaultPolicyProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	return nil
}

type shellToolProvider struct {
	runtime toolexec.Runtime
}

func (p shellToolProvider) Name() string {
	return ProviderShellTools
}

func (p shellToolProvider) Tools(context.Context) ([]tool.Tool, error) {
	_ = p.runtime
	return nil, nil
}

type workspaceToolProvider struct {
	runtime toolexec.Runtime
}

func (p workspaceToolProvider) Name() string {
	return ProviderWorkspaceTools
}

func (p workspaceToolProvider) Tools(context.Context) ([]tool.Tool, error) {
	listTool, err := toolfs.NewListWithRuntime(p.runtime)
	if err != nil {
		return nil, err
	}
	globTool, err := toolfs.NewGlobWithRuntime(p.runtime)
	if err != nil {
		return nil, err
	}
	searchTool, err := toolfs.NewSearchWithRuntime(p.runtime)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{listTool, globTool, searchTool}, nil
}

type defaultPolicyProvider struct {
	runtime toolexec.Runtime
}

func (p defaultPolicyProvider) Name() string {
	return ProviderDefaultPolicy
}

func (p defaultPolicyProvider) Policies(context.Context) ([]policy.Hook, error) {
	hooks := []policy.Hook{policy.DefaultSecurityBaseline()}
	if p.runtime != nil {
		hooks = append(hooks, policy.RouteCommandExecution(policy.CommandExecutionConfig{
			Runtime:  p.runtime,
			ToolName: toolshell.BashToolName,
		}))
		hooks = append(hooks, policy.WorkspaceBoundary(policy.WorkspaceBoundaryConfig{
			Runtime: p.runtime,
		}))
	}
	hooks = append(hooks, policy.RequireReadBeforeWrite(policy.ReadBeforeWriteConfig{}))
	return hooks, nil
}
