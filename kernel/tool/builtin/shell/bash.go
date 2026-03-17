package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	ktool "github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	// BashToolName is the conventional shell execution tool name.
	BashToolName       = "BASH"
	defaultBashTimeout = 30 * time.Minute
	defaultBashIdle    = 0
	defaultBashWait    = 5 * time.Second
)

// BashConfig configures the optional BASH tool.
type BashConfig struct {
	Timeout     time.Duration
	IdleTimeout time.Duration
	PreRun      func(command, workingDir string) error
	Runtime     toolexec.Runtime
}

// BashTool executes shell commands.
type BashTool struct {
	cfg     BashConfig
	runtime toolexec.Runtime
}

// NewBash creates an optional shell execution tool.
func NewBash(cfg BashConfig) (*BashTool, error) {
	resolvedRuntime, err := runtimeOrDefault(cfg.Runtime)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultBashTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultBashIdle
	}
	return &BashTool{
		cfg:     cfg,
		runtime: resolvedRuntime,
	}, nil
}

func (t *BashTool) Name() string {
	return BashToolName
}

func (t *BashTool) Description() string {
	return "Execute a shell command and return stdout/stderr."
}

func (t *BashTool) Capability() capability.Capability {
	return capability.Capability{
		Operations: []capability.Operation{capability.OperationExec},
		Risk:       capability.RiskHigh,
	}
}

func (t *BashTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "shell command to execute"},
				"workdir": map[string]any{"type": "string", "description": "working directory"},
				"require_escalated": map[string]any{
					"type":        "boolean",
					"description": "request host execution only when sandbox limits are blocking the task",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "optional wait time before yielding control. Values greater than 0 wait that many milliseconds. If omitted or set to 0 or a negative value, BASH waits 5 seconds and returns a task_id if still running.",
				},
				"tty": map[string]any{
					"type":        "boolean",
					"description": "allocate a pseudo-terminal for interactive commands.",
				},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
}

func (t *BashTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	command, err := argparse.String(args, "command", true)
	if err != nil {
		return nil, err
	}
	workingDir, err := argparse.String(args, "workdir", false)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(workingDir) == "" {
		workingDir, _ = argparse.String(args, "dir", false)
	}
	if strings.TrimSpace(workingDir) == "" && t.runtime != nil && t.runtime.FileSystem() != nil {
		workingDir, _ = t.runtime.FileSystem().Getwd()
	}
	sandboxPermission, err := parseSandboxPermissionArgs(args)
	if err != nil {
		return nil, err
	}
	rawYield, yieldSpecified := args["yield_time_ms"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldMS, yErr := argparse.Int(args, "yield_time_ms", 0)
	if yErr != nil {
		return nil, yErr
	}
	explicitYieldMS := yieldMS
	asyncYieldRequested := yieldSpecified && explicitYieldMS > 0
	tty, ttyErr := argparse.Bool(args, "tty", false)
	if ttyErr != nil {
		return nil, ttyErr
	}
	if !yieldSpecified || explicitYieldMS <= 0 {
		yieldMS = int(defaultBashWait / time.Millisecond)
	}

	timeout := t.cfg.Timeout
	idleTimeout := t.cfg.IdleTimeout
	if t.cfg.PreRun != nil {
		if err := t.cfg.PreRun(command, workingDir); err != nil {
			return nil, err
		}
	}
	// Policy evaluation — all state-mutating actions go through this.
	decision, policyDecision, err := t.resolveCommandDecision(ctx, command, sandboxPermission)
	if err != nil {
		return nil, err
	}
	if isACPXCommand(command) && decision.Route == toolexec.ExecutionRouteSandbox {
		return nil, &toolexec.ApprovalRequiredError{
			Reason: "acpx must run outside the Caelis sandbox: operation not permitted; rerun this command with require_escalated=true",
		}
	}

	// Interactive async commands still require host execution because sandbox
	// backends currently do not provide PTY sessions.
	if tty && decision.Route != toolexec.ExecutionRouteHost {
		if decision.Route != toolexec.ExecutionRouteHost {
			decision.NeedApproval = true
		}
		decision.Route = toolexec.ExecutionRouteHost
	}

	if policyDecision.Effect == policy.DecisionEffectRequireApproval && decision.Route == toolexec.ExecutionRouteSandbox {
		approvalReason := strings.TrimSpace(policyDecision.Reason)
		if approvalReason == "" {
			approvalReason = "command requires approval before execution"
		}
		if err := requestApproval(ctx, command, approvalReason); err != nil {
			return nil, err
		}
	}
	runner, needApproval, reason, err := t.resolveRunner(decision)
	if err != nil {
		return nil, err
	}
	if needApproval {
		if err := requestApproval(ctx, command, reason); err != nil {
			return nil, err
		}
	}

	if _, ok := runner.(toolexec.AsyncCommandRunner); !ok {
		if asyncYieldRequested {
			return nil, fmt.Errorf("tool: BASH failed (route=%s): async execution is not supported", decision.Route)
		}
	} else {
		manager, ok := task.ManagerFromContext(ctx)
		if !ok || manager == nil {
			if asyncYieldRequested {
				return nil, fmt.Errorf("tool: task manager is unavailable")
			}
		} else {
			snapshot, err := manager.StartBash(ctx, task.BashStartRequest{
				Command:     command,
				Workdir:     workingDir,
				Yield:       time.Duration(yieldMS) * time.Millisecond,
				Timeout:     timeout,
				IdleTimeout: idleTimeout,
				TTY:         tty,
				Route:       string(decision.Route),
			})
			if err != nil {
				return nil, fmt.Errorf("tool: BASH failed (route=%s): %w", decision.Route, err)
			}
			result := ktoolSnapshotResult(snapshot, string(decision.Route))
			return ktoolAppendTaskEvents(result, snapshot), nil
		}
	}

	result, err := runner.Run(ctx, toolexec.CommandRequest{
		Command:     command,
		Dir:         workingDir,
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
		TTY:         tty,
		OnOutput: func(chunk toolexec.CommandOutputChunk) {
			toolexec.EmitOutputChunk(ctx, chunk)
		},
	})
	if err != nil && shouldEscalateWhenSandboxUnavailable(decision, command, result, policyDecision) {
		hostRunner := t.runtime.HostRunner()
		if hostRunner == nil {
			return nil, fmt.Errorf("tool: host runner is unavailable")
		}
		base := commandBaseName(command)
		reason := fmt.Sprintf("sandbox image lacks command %q; approve host execution", base)
		if reqErr := requestApproval(ctx, command, reason); reqErr != nil {
			return nil, reqErr
		}
		result, err = hostRunner.Run(ctx, toolexec.CommandRequest{
			Command:     command,
			Dir:         workingDir,
			Timeout:     timeout,
			IdleTimeout: idleTimeout,
			TTY:         tty,
			OnOutput: func(chunk toolexec.CommandOutputChunk) {
				toolexec.EmitOutputChunk(ctx, chunk)
			},
		})
	}
	if err != nil {
		return nil, fmt.Errorf("tool: BASH failed (route=%s): %w", decision.Route, err)
	}
	return ktoolSnapshotResult(task.Snapshot{
		Kind:           task.KindBash,
		State:          task.StateCompleted,
		Running:        false,
		SupportsInput:  false,
		SupportsCancel: false,
		Output:         task.Output{Stdout: result.Stdout, Stderr: result.Stderr},
		Result: map[string]any{
			"exit_code": result.ExitCode,
		},
	}, string(decision.Route)), nil
}

func ktoolSnapshotResult(snapshot task.Snapshot, route string) map[string]any {
	result := ktool.SnapshotResultMap(snapshot)
	if strings.TrimSpace(route) != "" {
		result["route"] = strings.TrimSpace(route)
	}
	return result
}

func ktoolAppendTaskEvents(result map[string]any, snapshot task.Snapshot) map[string]any {
	return ktool.AppendTaskSnapshotEvents(result, snapshot)
}

func shouldEscalateWhenSandboxUnavailable(
	decision toolexec.CommandDecision,
	command string,
	result toolexec.CommandResult,
	policyDecision policy.Decision,
) bool {
	if decision.Route != toolexec.ExecutionRouteSandbox {
		return false
	}
	if !fallbackOnCommandNotFoundEnabled(policyDecision) {
		return false
	}
	base := commandBaseName(command)
	if base == "" {
		return false
	}
	if result.ExitCode != 127 {
		return false
	}
	lowerErr := strings.ToLower(strings.TrimSpace(result.Stderr))
	if lowerErr == "" {
		return false
	}
	// Common shell errors: "go: not found", "sh: go: command not found", etc.
	if strings.Contains(lowerErr, "not found") || strings.Contains(lowerErr, "command not found") {
		return true
	}
	return false
}

func fallbackOnCommandNotFoundEnabled(decision policy.Decision) bool {
	if decision.Metadata == nil {
		return false
	}
	raw, ok := decision.Metadata[policy.DecisionMetaFallbackOnCommandNotFound]
	if !ok {
		return false
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		value := strings.TrimSpace(strings.ToLower(typed))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	default:
		return false
	}
}

func commandBaseName(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func (t *BashTool) resolveCommandDecision(
	ctx context.Context,
	command string,
	sandboxPermission toolexec.SandboxPermission,
) (toolexec.CommandDecision, policy.Decision, error) {
	if decision, ok := policy.ToolDecisionFromContext(ctx); ok {
		decision = policy.NormalizeDecision(decision)
		if decision.Effect == policy.DecisionEffectDeny {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "command denied by policy"
			}
			return toolexec.CommandDecision{}, decision, fmt.Errorf("tool: command denied by policy: %s", reason)
		}
		if route, ok := policy.DecisionRouteFromMetadata(decision); ok {
			switch route {
			case policy.DecisionRouteSandbox:
				return toolexec.CommandDecision{Route: toolexec.ExecutionRouteSandbox}, decision, nil
			case policy.DecisionRouteHost:
				out := toolexec.CommandDecision{Route: toolexec.ExecutionRouteHost}
				if decision.Effect == policy.DecisionEffectRequireApproval {
					out.Escalation = &toolexec.EscalationReason{
						Message: strings.TrimSpace(decision.Reason),
					}
					out.NeedApproval = true
				}
				return out, decision, nil
			}
		}
		if decision.Effect == policy.DecisionEffectRequireApproval {
			return toolexec.CommandDecision{
				Route:        toolexec.ExecutionRouteHost,
				NeedApproval: true,
				Escalation: &toolexec.EscalationReason{
					Message: strings.TrimSpace(decision.Reason),
				},
			}, decision, nil
		}
	}
	return t.runtime.DecideRoute(command, sandboxPermission), policy.Decision{}, nil
}

func parseSandboxPermissionArgs(args map[string]any) (toolexec.SandboxPermission, error) {
	requireEscalated, err := argparse.Bool(args, "require_escalated", false)
	if err != nil {
		return "", err
	}
	if requireEscalated {
		return toolexec.SandboxPermissionRequireEscalated, nil
	}
	return toolexec.SandboxPermissionAuto, nil
}

func (t *BashTool) resolveRunner(decision toolexec.CommandDecision) (toolexec.CommandRunner, bool, string, error) {
	reason := ""
	if decision.Escalation != nil {
		reason = strings.TrimSpace(decision.Escalation.Message)
	}

	switch decision.Route {
	case toolexec.ExecutionRouteSandbox:
		runner := t.runtime.SandboxRunner()
		if runner == nil {
			return nil, false, "", fmt.Errorf("tool: sandbox runner is unavailable")
		}
		return runner, false, "", nil
	case toolexec.ExecutionRouteHost:
		runner := t.runtime.HostRunner()
		if runner == nil {
			return nil, false, "", fmt.Errorf("tool: host runner is unavailable")
		}
		if t.runtime.PermissionMode() == toolexec.PermissionModeFullControl {
			return runner, false, "", nil
		}
		if !decision.NeedApproval {
			return runner, false, "", nil
		}
		if reason == "" {
			reason = "host execution requires approval in default permission mode"
		}
		return runner, true, reason, nil
	default:
		return nil, false, "", fmt.Errorf("tool: unsupported execution route %q", decision.Route)
	}
}

func requestApproval(ctx context.Context, command string, reason string) error {
	approver, ok := toolexec.ApproverFromContext(ctx)
	if !ok {
		suggestion := "approve in interactive mode or run with a host-permissive execution policy"
		if strings.TrimSpace(reason) == "" {
			return &toolexec.ApprovalRequiredError{Reason: suggestion}
		}
		return &toolexec.ApprovalRequiredError{Reason: reason + "; " + suggestion}
	}
	allowed, err := approver.Approve(ctx, toolexec.ApprovalRequest{
		ToolName: BashToolName,
		Action:   "execute_command",
		Reason:   reason,
		Command:  command,
	})
	if err != nil {
		return err
	}
	if !allowed {
		return &toolexec.ApprovalAbortedError{Reason: "approval denied"}
	}
	return nil
}
