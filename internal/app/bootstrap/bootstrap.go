package bootstrap

import (
	"context"
	"fmt"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/app/acpadapter"
	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type ACPResourceFactory func(context.Context, *internalacp.Conn, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error)

type ACPConfig struct {
	WorkspaceRoot       string
	SessionModes        []internalacp.SessionMode
	DefaultModeID       string
	SessionConfig       []internalacp.SessionConfigOptionTemplate
	BuildSystemPrompt   internalacp.PromptFactory
	SessionConfigState  internalacp.SessionConfigStateFactory
	NormalizeConfig     internalacp.SessionConfigNormalizer
	NewModel            internalacp.ModelFactory
	NewAgent            internalacp.AgentFactory
	ListSessions        internalacp.SessionListFactory
	AvailableCommands   internalacp.AvailableCommandsFactory
	SupportsPromptImage func(internalacp.AgentSessionConfig) bool
	PromptImageEnabled  func() bool
	TaskRegistry        *task.Registry
	NewSessionResources ACPResourceFactory
}

type Config struct {
	Runtime               *runtime.Runtime
	Store                 session.Store
	ACPRuntime            *runtime.Runtime
	ACPStore              session.Store
	AppName               string
	UserID                string
	DefaultAgent          string
	WorkspaceCWD          string
	Execution             toolexec.Runtime
	Tools                 []tool.Tool
	Policies              []policy.Hook
	Resolved              *appassembly.ResolvedSpec
	TaskRegistry          *task.Registry
	EnablePlan            bool
	EnableSelfSpawn       bool
	Index                 sessionsvc.WorkspaceSessionIndex
	SubagentRunnerFactory runtime.SubagentRunnerFactory
	ACP                   *ACPConfig
}

type ACPAdapterFactory func(conn *internalacp.Conn) (internalacp.Adapter, error)

type ServiceSet struct {
	SessionService *sessionsvc.Service
	Gateway        *appgateway.Gateway
	Resolved       *appassembly.ResolvedSpec
	NewACPAdapter  ACPAdapterFactory
	closeFn        func(context.Context) error
}

func (s ServiceSet) Close(ctx context.Context) error {
	if s.closeFn == nil {
		return nil
	}
	return s.closeFn(ctx)
}

func Build(cfg Config) (ServiceSet, error) {
	if cfg.Store == nil {
		return ServiceSet{}, fmt.Errorf("bootstrap: store is required")
	}
	if cfg.Runtime == nil {
		return ServiceSet{}, fmt.Errorf("bootstrap: runtime is required")
	}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:               cfg.Runtime,
		Store:                 cfg.Store,
		AppName:               cfg.AppName,
		UserID:                cfg.UserID,
		DefaultAgent:          cfg.DefaultAgent,
		WorkspaceCWD:          cfg.WorkspaceCWD,
		Execution:             cfg.Execution,
		Tools:                 append([]tool.Tool(nil), cfg.Tools...),
		Policies:              append([]policy.Hook(nil), cfg.Policies...),
		TaskRegistry:          cfg.TaskRegistry,
		EnablePlan:            cfg.EnablePlan,
		EnableSelfSpawn:       cfg.EnableSelfSpawn,
		Index:                 cfg.Index,
		SubagentRunnerFactory: cfg.SubagentRunnerFactory,
	})
	if err != nil {
		return ServiceSet{}, err
	}
	gw, err := appgateway.New(svc)
	if err != nil {
		return ServiceSet{}, err
	}
	set := ServiceSet{
		SessionService: svc,
		Gateway:        gw,
		Resolved:       cfg.Resolved,
		closeFn: func(ctx context.Context) error {
			if cfg.Resolved == nil {
				return nil
			}
			return cfg.Resolved.Close(ctx)
		},
	}
	if cfg.ACP != nil {
		set.NewACPAdapter = func(conn *internalacp.Conn) (internalacp.Adapter, error) {
			if conn == nil {
				return nil, fmt.Errorf("bootstrap: acp conn is required")
			}
			if cfg.ACP.NewSessionResources == nil {
				return nil, fmt.Errorf("bootstrap: acp session resource factory is required")
			}
			return acpadapter.New(acpadapter.Config{
				Runtime:               firstNonNilRuntime(cfg.ACPRuntime, cfg.Runtime),
				Store:                 firstNonNilStore(cfg.ACPStore, cfg.Store),
				AppName:               cfg.AppName,
				UserID:                cfg.UserID,
				DefaultAgent:          cfg.DefaultAgent,
				WorkspaceRoot:         cfg.ACP.WorkspaceRoot,
				SessionModes:          append([]internalacp.SessionMode(nil), cfg.ACP.SessionModes...),
				DefaultModeID:         cfg.ACP.DefaultModeID,
				SessionConfig:         append([]internalacp.SessionConfigOptionTemplate(nil), cfg.ACP.SessionConfig...),
				BuildSystemPrompt:     cfg.ACP.BuildSystemPrompt,
				SessionConfigState:    cfg.ACP.SessionConfigState,
				NormalizeConfig:       cfg.ACP.NormalizeConfig,
				NewModel:              cfg.ACP.NewModel,
				NewAgent:              cfg.ACP.NewAgent,
				ListSessions:          cfg.ACP.ListSessions,
				AvailableCommands:     cfg.ACP.AvailableCommands,
				SupportsPromptImage:   cfg.ACP.SupportsPromptImage,
				PromptImageEnabled:    cfg.ACP.PromptImageEnabled,
				TaskRegistry:          cfg.ACP.TaskRegistry,
				EnablePlan:            cfg.EnablePlan,
				EnableSelfSpawn:       cfg.EnableSelfSpawn,
				SubagentRunnerFactory: cfg.SubagentRunnerFactory,
				NewSessionResources: func(ctx context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
					return cfg.ACP.NewSessionResources(ctx, conn, sessionID, sessionCWD, caps, modeResolver)
				},
			})
		}
	}
	return set, nil
}

func firstNonNilRuntime(values ...*runtime.Runtime) *runtime.Runtime {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonNilStore(values ...session.Store) session.Store {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
