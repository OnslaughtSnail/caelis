package policy

import (
	"context"
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

const defaultCommandExecutionToolName = "BASH"

type CommandExecutionConfig struct {
	Runtime  toolexec.Runtime
	ToolName string
}

type commandExecutionHook struct {
	name    string
	runtime toolexec.Runtime
	tool    string
}

func RouteCommandExecution(cfg CommandExecutionConfig) Hook {
	name := "route_command_execution"
	toolName := strings.TrimSpace(cfg.ToolName)
	if toolName == "" {
		toolName = defaultCommandExecutionToolName
	}
	return commandExecutionHook{
		name:    name,
		runtime: cfg.Runtime,
		tool:    toolName,
	}
}

func (h commandExecutionHook) Name() string {
	return h.name
}

func (h commandExecutionHook) BeforeModel(ctx context.Context, in ModelInput) (ModelInput, error) {
	_ = ctx
	return in, nil
}

func (h commandExecutionHook) BeforeTool(ctx context.Context, in ToolInput) (ToolInput, error) {
	_ = ctx
	if h.runtime == nil {
		return in, nil
	}
	if strings.TrimSpace(in.Call.Name) != h.tool {
		return in, nil
	}
	commandRaw, _ := in.Call.Args["command"].(string)
	command := strings.TrimSpace(commandRaw)
	if command == "" {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: "command is required",
		}
		return in, nil
	}
	permission, err := parseSandboxPermissionArg(in.Call.Args["sandbox_permissions"])
	if err != nil {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: err.Error(),
		}
		return in, nil
	}

	routeDecision := h.runtime.DecideRoute(command, permission)
	switch routeDecision.Route {
	case toolexec.ExecutionRouteSandbox:
		decision := DecisionWithRoute(Decision{
			Effect: DecisionEffectAllow,
		}, DecisionRouteSandbox)
		if decision.Metadata == nil {
			decision.Metadata = map[string]any{}
		}
		decision.Metadata[DecisionMetaFallbackOnCommandNotFound] = true
		in.Decision = decision
	case toolexec.ExecutionRouteHost:
		if routeDecision.Escalation != nil {
			in.Decision = DecisionWithRoute(Decision{
				Effect: DecisionEffectRequireApproval,
				Reason: strings.TrimSpace(routeDecision.Escalation.Message),
			}, DecisionRouteHost)
		} else {
			in.Decision = DecisionWithRoute(Decision{
				Effect: DecisionEffectAllow,
			}, DecisionRouteHost)
		}
	default:
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: fmt.Sprintf("unsupported execution route %q", routeDecision.Route),
		}
	}
	return in, nil
}

func (h commandExecutionHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	_ = ctx
	return out, nil
}

func (h commandExecutionHook) BeforeOutput(ctx context.Context, out Output) (Output, error) {
	_ = ctx
	return out, nil
}

func parseSandboxPermissionArg(raw any) (toolexec.SandboxPermission, error) {
	value, _ := raw.(string)
	switch toolexec.SandboxPermission(strings.TrimSpace(strings.ToLower(value))) {
	case "", toolexec.SandboxPermissionAuto:
		return toolexec.SandboxPermissionAuto, nil
	case toolexec.SandboxPermissionRequireEscalated:
		return toolexec.SandboxPermissionRequireEscalated, nil
	default:
		return "", fmt.Errorf("invalid sandbox_permissions %q", value)
	}
}
