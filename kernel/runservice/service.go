package runservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
)

type ServiceConfig struct {
	Runtime               *runtime.Runtime
	AppName               string
	UserID                string
	DefaultAgent          string
	WorkspaceRoot         string
	WorkspaceCWD          string
	Execution             toolexec.Runtime
	Tools                 []tool.Tool
	Policies              []policy.Hook
	TaskRegistry          *task.Registry
	EnablePlan            bool
	EnableSelfSpawn       bool
	SubagentRunnerFactory runtime.SubagentRunnerFactory
}

type Service struct {
	runtime               *runtime.Runtime
	appName               string
	userID                string
	defaultAgent          string
	workspaceRoot         string
	workspaceCWD          string
	execution             toolexec.Runtime
	tools                 []tool.Tool
	policies              []policy.Hook
	taskRegistry          *task.Registry
	enablePlan            bool
	enableSelfSpawn       bool
	subagentRunnerFactory runtime.SubagentRunnerFactory
}

type RunTurnRequest struct {
	SessionID           string
	Input               string
	ContentParts        []model.ContentPart
	Agent               agent.Agent
	Model               model.LLM
	ContextWindowTokens int
}

type RunTurnResult struct {
	SessionID string
	Runner    runtime.Runner
}

func New(cfg ServiceConfig) (*Service, error) {
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("runservice: runtime is required")
	}
	if strings.TrimSpace(cfg.AppName) == "" || strings.TrimSpace(cfg.UserID) == "" {
		return nil, fmt.Errorf("runservice: app_name and user_id are required")
	}
	return &Service{
		runtime:               cfg.Runtime,
		appName:               strings.TrimSpace(cfg.AppName),
		userID:                strings.TrimSpace(cfg.UserID),
		defaultAgent:          strings.TrimSpace(cfg.DefaultAgent),
		workspaceRoot:         strings.TrimSpace(cfg.WorkspaceRoot),
		workspaceCWD:          strings.TrimSpace(cfg.WorkspaceCWD),
		execution:             cfg.Execution,
		tools:                 append([]tool.Tool(nil), cfg.Tools...),
		policies:              append([]policy.Hook(nil), cfg.Policies...),
		taskRegistry:          cfg.TaskRegistry,
		enablePlan:            cfg.EnablePlan,
		enableSelfSpawn:       cfg.EnableSelfSpawn,
		subagentRunnerFactory: cfg.SubagentRunnerFactory,
	}, nil
}

func (s *Service) AssembleTools() ([]tool.Tool, error) {
	if s == nil {
		return nil, fmt.Errorf("runservice: service is nil")
	}
	out := append([]tool.Tool(nil), s.tools...)
	if s.enablePlan && !hasToolNamed(out, tool.PlanToolName) {
		planTool, err := tool.NewPlanTool()
		if err != nil {
			return nil, err
		}
		out = append(out, planTool)
	}
	if s.enableSelfSpawn && !hasToolNamed(out, tool.SpawnToolName) {
		spawnTool, err := NewSelfSpawnTool(s.defaultAgent)
		if err != nil {
			return nil, err
		}
		out = append(out, spawnTool)
	}
	return out, nil
}

func (s *Service) VisibleTools() ([]tool.Tool, error) {
	tools, err := s.AssembleTools()
	if err != nil {
		return nil, err
	}
	builtins, err := tool.BuildCoreTools(tool.CoreToolsConfig{
		Runtime:      s.execution,
		TaskRegistry: s.taskRegistry,
	})
	if err != nil {
		return nil, err
	}
	return tool.EnsureCoreTools(tools, builtins)
}

func (s *Service) RunTurn(ctx context.Context, req RunTurnRequest) (RunTurnResult, error) {
	if s == nil {
		return RunTurnResult{}, fmt.Errorf("runservice: service is nil")
	}
	tools, err := s.AssembleTools()
	if err != nil {
		return RunTurnResult{}, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = idutil.NewSessionID()
	}
	runner, err := s.runtime.Run(ctx, runtime.RunRequest{
		AppName:               s.appName,
		UserID:                s.userID,
		SessionID:             sessionID,
		Input:                 req.Input,
		ContentParts:          append([]model.ContentPart(nil), req.ContentParts...),
		Agent:                 req.Agent,
		Model:                 req.Model,
		Tools:                 tools,
		CoreTools:             tool.CoreToolsConfig{Runtime: s.execution, TaskRegistry: s.taskRegistry},
		Policies:              append([]policy.Hook(nil), s.policies...),
		ContextWindowTokens:   req.ContextWindowTokens,
		SubagentRunnerFactory: s.subagentRunnerFactory,
	})
	if err != nil {
		return RunTurnResult{}, err
	}
	return RunTurnResult{
		SessionID: sessionID,
		Runner:    runner,
	}, nil
}

func hasToolNamed(tools []tool.Tool, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, one := range tools {
		if one == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(one.Name()), name) {
			return true
		}
	}
	return false
}
