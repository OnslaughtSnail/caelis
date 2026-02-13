package builtin

import (
	"context"
	"fmt"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toollsp "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/lsp"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

const (
	ProviderLocalTools     = "local_tools"
	ProviderWorkspaceTools = "workspace_tools"
	ProviderShellTools     = "shell_tools"
	ProviderLSPActivation  = "lsp_activation"
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
	if err := r.RegisterToolProvider(localToolProvider{}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(workspaceToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(shellToolProvider{runtime: options.ExecutionRuntime}); err != nil {
		return err
	}
	if err := r.RegisterToolProvider(lspActivationToolProvider{}); err != nil {
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

type localToolProvider struct{}

func (p localToolProvider) Name() string {
	return ProviderLocalTools
}

func (p localToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	echoTool, err := tool.NewFunction[struct {
		Text string `json:"text"`
	}, struct {
		Echo string `json:"echo"`
	}](
		"echo",
		"Echo input text.",
		func(ctx context.Context, args struct {
			Text string `json:"text"`
		}) (struct {
			Echo string `json:"echo"`
		}, error) {
			_ = ctx
			return struct {
				Echo string `json:"echo"`
			}{Echo: args.Text}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	nowTool, err := tool.NewFunction[struct{}, struct {
		Now string `json:"now"`
	}](
		"now",
		"Return current UTC time in RFC3339 format.",
		func(ctx context.Context, args struct{}) (struct {
			Now string `json:"now"`
		}, error) {
			_ = ctx
			_ = args
			return struct {
				Now string `json:"now"`
			}{Now: time.Now().UTC().Format(time.RFC3339)}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{echoTool, nowTool}, nil
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
	statTool, err := toolfs.NewStatWithRuntime(p.runtime)
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
		statTool,
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
		policy.DefaultAllow(),
	}
	if p.runtime != nil {
		hooks = append(hooks, policy.RouteCommandExecution(policy.CommandExecutionConfig{
			Runtime:  p.runtime,
			ToolName: toolshell.BashToolName,
		}))
	}
	hooks = append(hooks, policy.RequireReadBeforeWrite(policy.ReadBeforeWriteConfig{}))
	return hooks, nil
}

type lspActivationToolProvider struct{}

func (p lspActivationToolProvider) Name() string {
	return ProviderLSPActivation
}

func (p lspActivationToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	activateTool, err := toollsp.NewActivate()
	if err != nil {
		return nil, err
	}
	return []tool.Tool{activateTool}, nil
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
