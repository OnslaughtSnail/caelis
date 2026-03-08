package policy

import (
	"context"
	"fmt"
	"path/filepath"
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
		// Destructive commands (rm, shred, dd with output) still default to the
		// sandbox route unless the caller explicitly requests escalation. This
		// keeps the model-visible semantics sandbox-first while still surfacing an
		// approval checkpoint before deletion happens.
		if base, reason := detectDestructiveCommand(command); base != "" {
			decision = DecisionWithRoute(Decision{
				Effect: DecisionEffectRequireApproval,
				Reason: reason,
			}, DecisionRouteSandbox)
			decision.Metadata[DecisionMetaFallbackOnCommandNotFound] = true
			_ = base
		}
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
// known destructive operation.  Returns (baseName, reason) when detected, or
// ("", "") when safe.  The heuristic is intentionally conservative: it only
// matches clear-cut data-deletion commands so that false positives are
// avoided for legitimate operations like "grep -r ... | rm".
func detectDestructiveCommand(command string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return "", ""
	}
	// Scan all tokens so compound commands like "echo hi && rm -rf dir/"
	// are also detected. We skip shell operators themselves.
	for _, token := range fields {
		base := filepath.Base(token)
		switch base {
		case "rm", "rmdir":
			return base, fmt.Sprintf("%q deletes files and requires approval", base)
		case "shred":
			return base, "shred permanently destroys file contents and requires approval"
		case "dd":
			// Only flag dd when it has an output file (of=...) argument.
			for _, f := range fields {
				if strings.HasPrefix(f, "of=") {
					return base, "dd with output file requires approval"
				}
			}
		case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.fat", "mke2fs":
			return base, fmt.Sprintf("%q formats a filesystem and requires approval", base)
		}
	}
	return "", ""
}
