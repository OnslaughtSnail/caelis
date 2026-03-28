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
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
	visiblemeta "github.com/OnslaughtSnail/caelis/kernel/tool/internal/outputmeta"
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
	return "Run a shell command. Use it for commands that are simpler in the shell than via file tools."
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
				"workdir": map[string]any{"type": "string", "description": "Optional working directory."},
				"require_escalated": map[string]any{
					"type":        "boolean",
					"description": "Request host execution only when sandbox limits block the task.",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "Optional wait before returning. Defaults to 5000; long runs return a task_id.",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional total command timeout in milliseconds. Defaults to 1800000.",
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
	if !yieldSpecified || explicitYieldMS <= 0 {
		yieldMS = int(defaultBashWait / time.Millisecond)
	}
	timeoutMS, err := argparse.Int(args, "timeout_ms", 0)
	if err != nil {
		return nil, err
	}

	timeout := t.cfg.Timeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
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
	asyncRunner, ok := runner.(toolexec.AsyncCommandRunner)
	if !ok || asyncRunner == nil {
		return nil, fmt.Errorf("tool: BASH failed (route=%s): async execution is not supported", decision.Route)
	}
	manager, ok := task.ManagerFromContext(ctx)
	if !ok || manager == nil {
		sessionID, err := asyncRunner.StartAsync(ctx, toolexec.CommandRequest{
			Command:     command,
			Dir:         workingDir,
			Timeout:     timeout,
			IdleTimeout: idleTimeout,
			OnOutput: func(chunk toolexec.CommandOutputChunk) {
				toolexec.EmitOutputChunk(ctx, chunk)
			},
		})
		if err != nil {
			return nil, fmt.Errorf("tool: BASH failed (route=%s): %w", decision.Route, err)
		}
		waited, waitErr := asyncRunner.WaitSession(ctx, sessionID, time.Duration(yieldMS)*time.Millisecond)
		if waitErr != nil {
			return nil, fmt.Errorf("tool: task manager is unavailable")
		}
		return ktoolSnapshotResult(task.Snapshot{
			Kind:  task.KindBash,
			State: task.StateCompleted,
			Output: task.Output{
				Stdout: waited.Stdout,
				Stderr: waited.Stderr,
			},
			Result: map[string]any{
				"exit_code": waited.ExitCode,
			},
		}, string(decision.Route)), nil
	}
	snapshot, err := manager.StartBash(ctx, task.BashStartRequest{
		Command:     command,
		Workdir:     workingDir,
		Yield:       time.Duration(yieldMS) * time.Millisecond,
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
		Route:       string(decision.Route),
	})
	if err != nil {
		return nil, fmt.Errorf("tool: BASH failed (route=%s): %w", decision.Route, err)
	}
	result := ktoolSnapshotResult(snapshot, string(decision.Route))
	return ktoolAppendTaskEvents(result, snapshot), nil
}

func (t *BashTool) WithRuntime(runtime toolexec.Runtime) (*BashTool, error) {
	cfg := t.cfg
	cfg.Runtime = runtime
	return NewBash(cfg)
}

func ktoolSnapshotResult(snapshot task.Snapshot, route string) map[string]any {
	_ = route
	return snapshotResultMap(snapshot)
}

func ktoolAppendTaskEvents(result map[string]any, snapshot task.Snapshot) map[string]any {
	return appendTaskSnapshotEvents(result, snapshot)
}

func appendTaskSnapshotEvents(result map[string]any, snapshot task.Snapshot) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	if snapshot.TaskID == "" {
		return result
	}
	state := "running"
	if !snapshot.Running {
		switch snapshot.State {
		case task.StateCompleted:
			state = "completed"
		case task.StateFailed:
			state = "failed"
		case task.StateInterrupted:
			state = "interrupted"
		case task.StateCancelled:
			state = "cancelled"
		case task.StateTerminated:
			state = "terminated"
		}
	}
	return taskstream.AppendResultEvent(result, taskstream.Event{
		Label:  "BASH",
		TaskID: snapshot.TaskID,
		State:  state,
		Final:  !snapshot.Running,
	})
}

func snapshotResultMap(snapshot task.Snapshot) map[string]any {
	result := map[string]any{
		"state": string(snapshot.State),
	}
	appendSnapshotMetaFields(result, snapshot)
	if snapshotIsActive(snapshot) {
		if id := strings.TrimSpace(snapshot.TaskID); id != "" {
			result["task_id"] = id
		}
		return result
	}
	appendTerminalOutputFields(result, snapshot)
	appendSnapshotTruncationMessage(result, snapshot)
	return result
}

func snapshotIsActive(snapshot task.Snapshot) bool {
	if snapshot.Running {
		return true
	}
	switch snapshot.State {
	case task.StateRunning, task.StateWaitingApproval, task.StateWaitingInput:
		return true
	default:
		return false
	}
}

func compactSnippet(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	rs := []rune(text)
	if len(rs) > 240 {
		return string(rs[:237]) + "..."
	}
	return text
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func snapshotYieldMessage(snapshot task.Snapshot) string {
	id := strings.TrimSpace(snapshot.TaskID)
	snippet := compactSnippet(firstNonEmptyText(snapshot.Output.Stdout, snapshot.Output.Stderr, snapshot.Output.Log))
	base := "task yielded before completion"
	if id != "" {
		base += "; use TASK with task_id " + id
	}
	if snippet == "" {
		return base
	}
	return base + "\n" + snippet
}

func snapshotStatusMessage(snapshot task.Snapshot) string {
	id := strings.TrimSpace(snapshot.TaskID)
	switch snapshot.State {
	case task.StateCompleted:
		return "task success"
	case task.StateFailed:
		return "task failed"
	case task.StateCancelled:
		return "cancelled"
	case task.StateInterrupted:
		return "interrupted"
	case task.StateTerminated:
		return "terminated"
	case task.StateWaitingInput:
		if id != "" {
			return "waiting for input; use TASK write with task_id " + id
		}
		return "waiting for input"
	case task.StateWaitingApproval:
		if id != "" {
			return "waiting for approval; use TASK with task_id " + id
		}
		return "waiting for approval"
	default:
		if id != "" {
			return "task yielded before completion; use TASK with task_id " + id
		}
		return "task yielded before completion"
	}
}

func snapshotProgressValue(snapshot task.Snapshot) string {
	if preview := strings.TrimSpace(fmt.Sprint(snapshot.Result["latest_output"])); preview != "" && preview != "<nil>" {
		return preview
	}
	return compactSnippet(firstNonEmptyText(snapshot.Output.Stdout, snapshot.Output.Stderr, snapshot.Output.Log))
}

func snapshotTerminalOutput(snapshot task.Snapshot) string {
	return compactSnippet(firstNonEmptyText(snapshot.Output.Stdout, snapshot.Output.Stderr, snapshot.Output.Log))
}

func appendTerminalOutputFields(result map[string]any, snapshot task.Snapshot) {
	if result == nil {
		return
	}
	if stdout := strings.TrimSpace(snapshot.Output.Stdout); stdout != "" {
		result["stdout"] = snapshot.Output.Stdout
	}
	if stderr := strings.TrimSpace(snapshot.Output.Stderr); stderr != "" {
		result["stderr"] = snapshot.Output.Stderr
	}
	if output := strings.TrimSpace(snapshot.Output.Log); output != "" {
		result["output"] = snapshot.Output.Log
	}
}

func appendSnapshotTruncationMessage(result map[string]any, snapshot task.Snapshot) {
	if result == nil || len(snapshot.Result) == 0 {
		return
	}
	raw, ok := snapshot.Result["output_meta"]
	if !ok {
		return
	}
	meta, ok := raw.(map[string]any)
	if !ok || len(meta) == 0 {
		return
	}
	if compacted := visiblemeta.CompactVisible(meta); len(compacted) > 0 {
		result["msg"] = "output truncated"
	}
}

func appendSnapshotMetaFields(result map[string]any, snapshot task.Snapshot) {
	if result == nil || len(snapshot.Result) == 0 {
		return
	}
	if value, ok := snapshot.Result["exit_code"]; ok && value != nil {
		result["exit_code"] = value
	}
	for _, key := range []string{"_ui_exec_session_id", "_ui_route"} {
		if value, ok := snapshot.Result[key]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			result[key] = value
		}
	}
}

func syncOutputMeta(result toolexec.CommandResult, tty bool) map[string]any {
	return map[string]any{
		"streamed":              false,
		"tty":                   tty,
		"capture_cap_bytes":     0,
		"stdout_captured_bytes": len(result.Stdout),
		"stderr_captured_bytes": len(result.Stderr),
		"stdout_retained_bytes": len(result.Stdout),
		"stderr_retained_bytes": len(result.Stderr),
		"stdout_cap_reached":    false,
		"stderr_cap_reached":    false,
		"stdout_dropped_bytes":  0,
		"stderr_dropped_bytes":  0,
		"capture_truncated":     false,
		"model_truncated":       false,
	}
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
