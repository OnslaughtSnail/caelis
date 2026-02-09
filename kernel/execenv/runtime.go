package execenv

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

// Mode describes the execution isolation mode.
type Mode string

const (
	ModeNoSandbox Mode = "no_sandbox"
	ModeSandbox   Mode = "sandbox"
)

// BashStrategy controls command risk handling.
type BashStrategy string

const (
	BashStrategyAuto        BashStrategy = "auto"
	BashStrategyFullAccess  BashStrategy = "full_access"
	BashStrategyAgentDecide BashStrategy = "agent_decided"
	BashStrategyStrict      BashStrategy = "strict"
)

// BashPolicy is derived by execution runtime and consumed by tools.
type BashPolicy struct {
	Strategy      BashStrategy
	Allowlist     []string
	DenyMetaChars bool
}

// Config builds an execution runtime.
type Config struct {
	Mode        Mode
	SandboxType string
	BashPolicy  BashPolicy

	FileSystem FileSystem
	Runner     CommandRunner
}

// FileSystem defines file operations for tools. Implementations can target
// host filesystem or isolated sandboxes.
type FileSystem interface {
	Getwd() (string, error)
	UserHomeDir() (string, error)
	Open(path string) (*os.File, error)
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (os.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Glob(pattern string) ([]string, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// CommandRequest is one command execution request.
type CommandRequest struct {
	Command string
	Dir     string
	Timeout time.Duration
}

// CommandResult is one command execution result.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner executes shell commands for tools.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}

// Runtime exposes execution primitives and derived security policies.
type Runtime interface {
	Mode() Mode
	SandboxType() string
	FileSystem() FileSystem
	Runner() CommandRunner
	BashPolicy() BashPolicy
}

// ApprovalRequiredError indicates that the call should be reviewed by upper
// application layer. Kernel tool layer does not handle approval workflow.
type ApprovalRequiredError struct {
	Reason string
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("tool: approval required: %s", e.Reason)
}

type runtimeImpl struct {
	mode        Mode
	sandboxType string
	fs          FileSystem
	runner      CommandRunner
	bashPolicy  BashPolicy
}

func (r *runtimeImpl) Mode() Mode {
	return r.mode
}

func (r *runtimeImpl) SandboxType() string {
	return r.sandboxType
}

func (r *runtimeImpl) FileSystem() FileSystem {
	return r.fs
}

func (r *runtimeImpl) Runner() CommandRunner {
	return r.runner
}

func (r *runtimeImpl) BashPolicy() BashPolicy {
	return r.bashPolicy
}

// SandboxFactory builds a sandbox runtime backend by type.
type SandboxFactory interface {
	Type() string
	Build(Config) (Runtime, error)
}

var (
	sandboxFactoriesMu sync.RWMutex
	sandboxFactories   = map[string]SandboxFactory{}
)

// RegisterSandboxFactory registers one sandbox backend factory.
func RegisterSandboxFactory(factory SandboxFactory) error {
	if factory == nil || factory.Type() == "" {
		return fmt.Errorf("execenv: invalid sandbox factory")
	}
	sandboxFactoriesMu.Lock()
	defer sandboxFactoriesMu.Unlock()
	if _, exists := sandboxFactories[factory.Type()]; exists {
		return fmt.Errorf("execenv: duplicated sandbox factory %q", factory.Type())
	}
	sandboxFactories[factory.Type()] = factory
	return nil
}

// New builds runtime based on isolation mode and optional sandbox type.
func New(cfg Config) (Runtime, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeNoSandbox
	}

	if mode == ModeSandbox && cfg.SandboxType != "" {
		sandboxFactoriesMu.RLock()
		factory, ok := sandboxFactories[cfg.SandboxType]
		sandboxFactoriesMu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("execenv: unknown sandbox type %q", cfg.SandboxType)
		}
		return factory.Build(cfg)
	}

	filesystem := cfg.FileSystem
	if filesystem == nil {
		filesystem = newHostFileSystem()
	}
	runner := cfg.Runner
	if runner == nil {
		runner = newHostRunner()
	}
	policy := deriveBashPolicy(mode, cfg.BashPolicy)

	return &runtimeImpl{
		mode:        mode,
		sandboxType: cfg.SandboxType,
		fs:          filesystem,
		runner:      runner,
		bashPolicy:  policy,
	}, nil
}

func deriveBashPolicy(mode Mode, in BashPolicy) BashPolicy {
	out := in
	if out.Strategy == "" {
		out.Strategy = BashStrategyAuto
	}
	if out.Strategy == BashStrategyAuto {
		if mode == ModeSandbox {
			out.Strategy = BashStrategyAgentDecide
		} else {
			out.Strategy = BashStrategyStrict
		}
	}
	if len(out.Allowlist) == 0 {
		out.Allowlist = defaultAllowedCommands()
	}
	if !out.DenyMetaChars && out.Strategy == BashStrategyStrict {
		out.DenyMetaChars = true
	}
	return out
}

func defaultAllowedCommands() []string {
	return []string{
		"pwd", "ls", "find", "cat", "head", "tail", "wc", "echo", "grep", "sed", "awk", "rg",
	}
}
