package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

const (
	// BashToolName is the conventional shell execution tool name.
	BashToolName = "BASH"
)

// BashConfig configures the optional BASH tool.
type BashConfig struct {
	Timeout         time.Duration
	PreRun          func(command, workingDir string) error
	AllowedCommands []string
	Runtime         toolexec.Runtime
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

func (t *BashTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "shell command"},
				"dir":     map[string]any{"type": "string", "description": "working directory"},
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
	if t.cfg.PreRun != nil {
		if err := t.cfg.PreRun(command, workingDir); err != nil {
			return nil, err
		}
	}
	policy := t.runtime.BashPolicy()
	if len(t.cfg.AllowedCommands) > 0 {
		policy.Allowlist = append([]string(nil), t.cfg.AllowedCommands...)
	}
	if err := ensureAllowedCommand(ctx, command, policy); err != nil {
		return nil, err
	}

	result, err := t.runtime.Runner().Run(ctx, toolexec.CommandRequest{
		Command: command,
		Dir:     workingDir,
		Timeout: t.cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("tool: BASH failed: %w", err)
	}
	return map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}, nil
}

func ensureAllowedCommand(ctx context.Context, command string, policy toolexec.BashPolicy) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("tool: permission denied: empty command")
	}
	if policy.Strategy == toolexec.BashStrategyFullAccess {
		return nil
	}
	hasMeta := strings.ContainsAny(command, "|;&><`$\\")
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("tool: permission denied: empty command")
	}
	base := filepath.Base(fields[0])
	allowed := map[string]struct{}{}
	for _, one := range policy.Allowlist {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		allowed[one] = struct{}{}
	}
	isAllowed := false
	if len(allowed) > 0 {
		if _, ok := allowed[base]; ok {
			isAllowed = true
		}
	}

	switch policy.Strategy {
	case toolexec.BashStrategyStrict:
		needApproval := false
		reasons := make([]string, 0, 2)
		if policy.DenyMetaChars && hasMeta {
			needApproval = true
			reasons = append(reasons, "shell meta characters detected")
		}
		if !isAllowed {
			needApproval = true
			reasons = append(reasons, fmt.Sprintf("command %q is outside allowlist", base))
		}
		if !needApproval {
			return nil
		}
		return requestApproval(ctx, command, strings.Join(reasons, "; "))
	case toolexec.BashStrategyAgentDecide:
		needApproval := false
		reasons := make([]string, 0, 2)
		if hasMeta && policy.DenyMetaChars {
			needApproval = true
			reasons = append(reasons, "shell meta characters detected")
		}
		if !isAllowed {
			needApproval = true
			reasons = append(reasons, fmt.Sprintf("command %q is outside allowlist", base))
		}
		if !needApproval {
			return nil
		}
		reason := strings.Join(reasons, "; ")
		return requestApproval(ctx, command, reason)
	default:
		return fmt.Errorf("tool: permission denied: command %q is not in whitelist", base)
	}
}

func requestApproval(ctx context.Context, command string, reason string) error {
	approver, ok := toolexec.ApproverFromContext(ctx)
	if !ok {
		return &toolexec.ApprovalRequiredError{Reason: reason}
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
		return fmt.Errorf("tool: permission denied: approval denied")
	}
	return nil
}
