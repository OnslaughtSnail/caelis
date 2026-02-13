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
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	// BashToolName is the conventional shell execution tool name.
	BashToolName       = "BASH"
	defaultBashTimeout = 90 * time.Second
	defaultBashIdle    = 45 * time.Second
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

func (t *BashTool) Capability() toolcap.Capability {
	return toolcap.Capability{
		Operations: []toolcap.Operation{toolcap.OperationExec},
		Risk:       toolcap.RiskHigh,
	}
}

func (t *BashTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "shell command"},
				"dir":     map[string]any{"type": "string", "description": "working directory"},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "optional timeout in milliseconds, overrides default tool timeout",
				},
				"idle_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "optional no-output timeout in milliseconds, overrides default idle timeout",
				},
				"sandbox_permissions": map[string]any{
					"type":        "string",
					"description": "sandbox permission mode: auto|require_escalated",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	command, err := argparse.String(args, "command", true)
	if err != nil {
		return nil, err
	}
	workingDir, err := argparse.String(args, "dir", false)
	if err != nil {
		return nil, err
	}
	sandboxPermissionArg, err := argparse.String(args, "sandbox_permissions", false)
	if err != nil {
		return nil, err
	}
	timeoutMS, err := argparse.Int(args, "timeout_ms", 0)
	if err != nil {
		return nil, err
	}
	if timeoutMS < 0 {
		return nil, fmt.Errorf("tool: arg %q must be >= 0", "timeout_ms")
	}
	idleTimeoutMS, err := argparse.Int(args, "idle_timeout_ms", 0)
	if err != nil {
		return nil, err
	}
	if idleTimeoutMS < 0 {
		return nil, fmt.Errorf("tool: arg %q must be >= 0", "idle_timeout_ms")
	}
	sandboxPermission, err := parseSandboxPermission(sandboxPermissionArg)
	if err != nil {
		return nil, err
	}
	timeout := t.cfg.Timeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	idleTimeout := t.cfg.IdleTimeout
	if idleTimeoutMS > 0 {
		idleTimeout = time.Duration(idleTimeoutMS) * time.Millisecond
	}

	if t.cfg.PreRun != nil {
		if err := t.cfg.PreRun(command, workingDir); err != nil {
			return nil, err
		}
	}

	decision, policyDecision, err := t.resolveCommandDecision(ctx, command, sandboxPermission)
	if err != nil {
		return nil, err
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
	result, err := runner.Run(ctx, toolexec.CommandRequest{
		Command:     command,
		Dir:         workingDir,
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
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
		})
	}
	if err != nil {
		return nil, fmt.Errorf("tool: BASH failed: %w", err)
	}
	return map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}, nil
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
				}
				return out, decision, nil
			}
		}
		if decision.Effect == policy.DecisionEffectRequireApproval {
			return toolexec.CommandDecision{
				Route: toolexec.ExecutionRouteHost,
				Escalation: &toolexec.EscalationReason{
					Message: strings.TrimSpace(decision.Reason),
				},
			}, decision, nil
		}
	}
	return t.runtime.DecideRoute(command, sandboxPermission), policy.Decision{}, nil
}

func parseSandboxPermission(raw string) (toolexec.SandboxPermission, error) {
	value := toolexec.SandboxPermission(strings.TrimSpace(strings.ToLower(raw)))
	switch value {
	case "", toolexec.SandboxPermissionAuto:
		return toolexec.SandboxPermissionAuto, nil
	case toolexec.SandboxPermissionRequireEscalated:
		return toolexec.SandboxPermissionRequireEscalated, nil
	default:
		return "", fmt.Errorf("tool: invalid sandbox_permissions %q, expected auto|require_escalated", raw)
	}
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
