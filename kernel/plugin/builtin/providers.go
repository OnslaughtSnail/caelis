package builtin

import (
	"context"
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

const (
	ProviderWorkspaceTools = "workspace_tools"
	ProviderShellTools     = "shell_tools"
	ProviderMCPTools       = "mcp_tools"
	ProviderDefaultPolicy  = "default_allow"
)

// RegisterOptions carries explicit dependencies for builtin providers.
type RegisterOptions struct {
	ExecutionRuntime toolexec.Runtime
	MCPToolManager   *toolmcp.Manager
}

// RegisterAll registers built-in providers into a plugin registry.
func RegisterAll(r *plugin.Registry, options RegisterOptions) error {
	if r == nil {
		return fmt.Errorf("builtin: registry is nil")
	}
	if err := r.RegisterToolProvider(workspaceToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(shellToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(mcpToolProvider{manager: options.MCPToolManager}); err != nil {
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

func (p shellToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	bashTool, err := toolshell.NewBash(toolshell.BashConfig{
		Runtime: p.runtime,
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{bashTool}, nil
}

type workspaceToolProvider struct {
	runtime toolexec.Runtime
}

func (p workspaceToolProvider) Name() string {
	return ProviderWorkspaceTools
}

func (p workspaceToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
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
	patchTool, err := toolfs.NewPatchWithRuntime(p.runtime)
	if err != nil {
		return nil, err
	}
	writeTool, err := toolfs.NewWriteWithRuntime(p.runtime)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{
		listTool,
		globTool,
		searchTool,
		patchTool,
		writeTool,
	}, nil
}

type defaultPolicyProvider struct {
	runtime toolexec.Runtime
}

func (p defaultPolicyProvider) Name() string {
	return ProviderDefaultPolicy
}

func (p defaultPolicyProvider) Policies(ctx context.Context) ([]policy.Hook, error) {
	_ = ctx
	hooks := []policy.Hook{
		policy.DefaultSecurityBaseline(),
	}
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

type mcpToolProvider struct {
	manager *toolmcp.Manager
}

func (p mcpToolProvider) Name() string {
	return ProviderMCPTools
}

func (p mcpToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	if p.manager == nil {
		return nil, nil
	}
	return p.manager.Tools(ctx)
}
