package policy

import (
	"context"
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/pkg/cmdsafety"
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
	args := resolveToolInputArgs(in)
	commandRaw, _ := args["command"].(string)
	command := strings.TrimSpace(commandRaw)
	if command == "" {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: "command is required",
		}
		return in, nil
	}
	permission, err := parseSandboxPermissionArgs(args)
	if err != nil {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: err.Error(),
		}
		return in, nil
	}
	if _, reason := detectDestructiveCommand(command); reason != "" {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: reason,
		}
		return in, nil
	}

	routeDecision := h.runtime.DecideRoute(command, permission)
	switch routeDecision.Route {
	case toolexec.ExecutionRouteSandbox:
		decision := DecisionWithRoute(Decision{
			Effect: DecisionEffectAllow,
		}, DecisionRouteSandbox)
		decision = DecisionWithAnnotation(decision, DecisionAnnotationFallbackOnCommandNotFound)
		in.Decision = decision
	case toolexec.ExecutionRouteHost:
		if routeDecision.Escalation != nil {
			decision := DecisionWithRoute(Decision{
				Effect: DecisionEffectRequireApproval,
				Reason: strings.TrimSpace(routeDecision.Escalation.Message),
			}, DecisionRouteHost)
			in.Decision = DecisionWithAnnotation(decision, DecisionAnnotationHostExecutionRequiresApproval)
		} else {
			decision := DecisionWithRoute(Decision{
				Effect: DecisionEffectAllow,
			}, DecisionRouteHost)
			in.Decision = DecisionWithAnnotation(decision, DecisionAnnotationHostExecutionWithoutApproval)
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

func parseSandboxPermissionArgs(args map[string]any) (toolexec.SandboxPermission, error) {
	if raw, ok := args["require_escalated"]; ok && raw != nil {
		value, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("invalid require_escalated %v", raw)
		}
		if value {
			return toolexec.SandboxPermissionRequireEscalated, nil
		}
		return toolexec.SandboxPermissionAuto, nil
	}
	return toolexec.SandboxPermissionAuto, nil
}

// detectDestructiveCommand checks whether the given shell command contains a
// known destructive operation. Returns (baseName, reason) when detected, or
// ("", "") when safe. The heuristic is intentionally conservative: it only
// matches clear-cut data-deletion commands so that false positives are
// avoided for legitimate operations like "grep -r ... | rm".
func detectDestructiveCommand(command string) (string, string) {
	return cmdsafety.DetectBlockedCommand(command)
}
