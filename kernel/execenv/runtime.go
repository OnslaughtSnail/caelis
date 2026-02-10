package execenv

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"time"
)

// PermissionMode describes top-level execution authorization strategy.
type PermissionMode string

const (
	PermissionModeDefault     PermissionMode = "default"
	PermissionModeFullControl PermissionMode = "full_control"
)

// SandboxPolicyType describes high-level sandbox data boundary semantics.
type SandboxPolicyType string

const (
	SandboxPolicyReadOnly       SandboxPolicyType = "read_only"
	SandboxPolicyWorkspaceWrite SandboxPolicyType = "workspace_write"
	SandboxPolicyDangerFull     SandboxPolicyType = "danger_full_access"
	SandboxPolicyExternal       SandboxPolicyType = "external_sandbox"
)

// SandboxPolicy is a backend-agnostic sandbox policy summary.
type SandboxPolicy struct {
	Type             SandboxPolicyType
	NetworkAccess    bool
	WritableRoots    []string
	ReadOnlySubpaths []string
}

// ExecutionRoute indicates where one command should run.
type ExecutionRoute string

const (
	ExecutionRouteSandbox ExecutionRoute = "sandbox"
	ExecutionRouteHost    ExecutionRoute = "host"
)

// SandboxPermission allows tools to request host escalation.
type SandboxPermission string

const (
	SandboxPermissionAuto             SandboxPermission = "auto"
	SandboxPermissionRequireEscalated SandboxPermission = "require_escalated"
)

// EscalationReason explains why command should leave sandbox path.
type EscalationReason struct {
	Message string
}

// CommandDecision is runtime routing result for one command request.
type CommandDecision struct {
	Route      ExecutionRoute
	Escalation *EscalationReason
}

// Config builds an execution runtime.
type Config struct {
	PermissionMode PermissionMode
	SandboxType    string
	SafeCommands   []string
	SandboxPolicy  SandboxPolicy

	FileSystem    FileSystem
	HostRunner    CommandRunner
	SandboxRunner CommandRunner
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
	Command     string
	Dir         string
	Timeout     time.Duration
	IdleTimeout time.Duration
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
	PermissionMode() PermissionMode
	SandboxType() string
	SandboxPolicy() SandboxPolicy
	FallbackToHost() bool
	FallbackReason() string
	FileSystem() FileSystem
	HostRunner() CommandRunner
	SandboxRunner() CommandRunner
	SafeCommands() []string
	DenyMetaChars() bool
	DecideRoute(command string, sandboxPermission SandboxPermission) CommandDecision
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
	permissionMode PermissionMode
	sandboxType    string
	fallbackToHost bool
	fallbackReason string
	sandboxPolicy  SandboxPolicy
	fs             FileSystem
	hostRunner     CommandRunner
	sandboxRunner  CommandRunner
	safeCommands   []string
	denyMetaChars  bool
	closers        []runtimeCloser
	closeOnce      sync.Once
	closeErr       error
}

func (r *runtimeImpl) PermissionMode() PermissionMode {
	return r.permissionMode
}

func (r *runtimeImpl) SandboxType() string {
	return r.sandboxType
}

func (r *runtimeImpl) SandboxPolicy() SandboxPolicy {
	policy := r.sandboxPolicy
	policy.WritableRoots = append([]string(nil), policy.WritableRoots...)
	policy.ReadOnlySubpaths = append([]string(nil), policy.ReadOnlySubpaths...)
	return policy
}

func (r *runtimeImpl) FallbackToHost() bool {
	return r.fallbackToHost
}

func (r *runtimeImpl) FallbackReason() string {
	return r.fallbackReason
}

func (r *runtimeImpl) FileSystem() FileSystem {
	return r.fs
}

func (r *runtimeImpl) HostRunner() CommandRunner {
	return r.hostRunner
}

func (r *runtimeImpl) SandboxRunner() CommandRunner {
	return r.sandboxRunner
}

func (r *runtimeImpl) SafeCommands() []string {
	return append([]string(nil), r.safeCommands...)
}

func (r *runtimeImpl) DenyMetaChars() bool {
	return r.denyMetaChars
}

func (r *runtimeImpl) DecideRoute(_ string, sandboxPermission SandboxPermission) CommandDecision {
	if r.permissionMode == PermissionModeFullControl {
		return CommandDecision{Route: ExecutionRouteHost}
	}

	if r.fallbackToHost {
		message := "sandbox unavailable, host execution requires approval"
		if strings.TrimSpace(r.fallbackReason) != "" {
			message = message + ": " + strings.TrimSpace(r.fallbackReason)
		}
		return CommandDecision{
			Route: ExecutionRouteHost,
			Escalation: &EscalationReason{
				Message: message,
			},
		}
	}

	if sandboxPermission == SandboxPermissionRequireEscalated {
		return CommandDecision{
			Route:      ExecutionRouteHost,
			Escalation: &EscalationReason{Message: "sandbox_permissions=require_escalated requested"},
		}
	}
	return CommandDecision{Route: ExecutionRouteSandbox}
}

func (r *runtimeImpl) Close() error {
	r.closeOnce.Do(func() {
		for _, closer := range r.closers {
			if closer == nil {
				continue
			}
			if err := closer.Close(); err != nil && r.closeErr == nil {
				r.closeErr = err
			}
		}
	})
	return r.closeErr
}

// SandboxFactory builds one sandbox command runner by type.
type SandboxFactory interface {
	Type() string
	Build(Config) (CommandRunner, error)
}

type sandboxProber interface {
	Probe(context.Context) error
}

type runtimeCloser interface {
	Close() error
}

// Close releases optional runtime resources (for example persistent sandbox
// sessions). Runtimes without cleanup hooks are no-op.
func Close(rt Runtime) error {
	if rt == nil {
		return nil
	}
	closer, ok := rt.(runtimeCloser)
	if !ok {
		return nil
	}
	return closer.Close()
}

var (
	sandboxFactoriesMu sync.RWMutex
	sandboxFactories   = map[string]SandboxFactory{}
	runtimeGOOS        = stdruntime.GOOS
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

// New builds runtime based on permission mode and optional sandbox type.
func New(cfg Config) (Runtime, error) {
	mode := cfg.PermissionMode
	if mode == "" {
		mode = PermissionModeDefault
	}
	if mode != PermissionModeDefault && mode != PermissionModeFullControl {
		return nil, fmt.Errorf("execenv: invalid permission mode %q", mode)
	}

	filesystem := cfg.FileSystem
	if filesystem == nil {
		filesystem = newHostFileSystem()
	}
	hostRunner := cfg.HostRunner
	if hostRunner == nil {
		hostRunner = newHostRunner()
	}

	safeCommands := append([]string(nil), cfg.SafeCommands...)
	if len(safeCommands) == 0 {
		safeCommands = defaultSafeCommands()
	}
	denyMetaChars := true
	resolvedPolicy := deriveSandboxPolicy(mode, cfg.SandboxPolicy)

	runtime := &runtimeImpl{
		permissionMode: mode,
		sandboxPolicy:  resolvedPolicy,
		fs:             filesystem,
		hostRunner:     hostRunner,
		safeCommands:   safeCommands,
		denyMetaChars:  denyMetaChars,
	}

	if mode == PermissionModeFullControl {
		return runtime, nil
	}

	requestedSandboxType := strings.TrimSpace(strings.ToLower(cfg.SandboxType))
	candidates := sandboxTypeCandidates(requestedSandboxType)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("execenv: no sandbox backend candidates")
	}
	runtime.sandboxType = candidates[0]

	sandboxRunner := cfg.SandboxRunner
	if sandboxRunner == nil {
		failures := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			sandboxFactoriesMu.RLock()
			factory, ok := sandboxFactories[candidate]
			sandboxFactoriesMu.RUnlock()
			if !ok {
				if requestedSandboxType == candidate {
					return nil, fmt.Errorf("execenv: unknown sandbox type %q", candidate)
				}
				failures = append(failures, fmt.Sprintf("%s: unknown sandbox type", candidate))
				continue
			}
			buildCfg := cfg
			buildCfg.PermissionMode = mode
			buildCfg.SandboxType = candidate
			buildCfg.SandboxPolicy = resolvedPolicy
			builtRunner, err := factory.Build(buildCfg)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: init failed: %v", candidate, err))
				continue
			}
			if prober, ok := builtRunner.(sandboxProber); ok {
				probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				probeErr := prober.Probe(probeCtx)
				cancel()
				if probeErr != nil {
					failures = append(failures, fmt.Sprintf("%s: unavailable: %v", candidate, probeErr))
					continue
				}
			}
			runtime.sandboxType = candidate
			sandboxRunner = builtRunner
			break
		}
		if sandboxRunner == nil {
			runtime.fallbackToHost = true
			runtime.fallbackReason = strings.Join(failures, "; ")
			return runtime, nil
		}
	} else {
		if prober, ok := sandboxRunner.(sandboxProber); ok {
			probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			probeErr := prober.Probe(probeCtx)
			cancel()
			if probeErr != nil {
				runtime.fallbackToHost = true
				runtime.fallbackReason = fmt.Sprintf("sandbox backend %q unavailable: %v", runtime.sandboxType, probeErr)
				return runtime, nil
			}
		}
	}

	runtime.sandboxRunner = sandboxRunner
	if closer, ok := sandboxRunner.(runtimeCloser); ok {
		runtime.closers = append(runtime.closers, closer)
	}
	return runtime, nil
}

func defaultSandboxTypeForPlatform() string {
	if runtimeGOOS == "darwin" {
		return seatbeltSandboxType
	}
	return dockerSandboxType
}

func sandboxTypeCandidates(requested string) []string {
	return sandboxTypeCandidatesForPlatform(requested, runtimeGOOS)
}

func sandboxTypeCandidatesForPlatform(requested string, goos string) []string {
	value := strings.TrimSpace(strings.ToLower(requested))
	if value == "" {
		if strings.TrimSpace(strings.ToLower(goos)) == "darwin" {
			return []string{seatbeltSandboxType, dockerSandboxType}
		}
		return []string{dockerSandboxType}
	}
	if strings.TrimSpace(strings.ToLower(goos)) == "darwin" && value == seatbeltSandboxType {
		return []string{seatbeltSandboxType, dockerSandboxType}
	}
	return []string{value}
}

func hasShellMeta(command string) bool {
	return strings.ContainsAny(command, "|;&><`$\\")
}

func baseCommand(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func isAllowedCommand(base string, allowlist []string) bool {
	if base == "" {
		return false
	}
	if len(allowlist) == 0 {
		return false
	}
	for _, one := range allowlist {
		if strings.TrimSpace(one) == base {
			return true
		}
	}
	return false
}

func defaultSafeCommands() []string {
	return []string{
		"pwd", "ls", "find", "cat", "head", "tail", "wc", "echo", "grep", "sed", "awk", "rg",
	}
}

func deriveSandboxPolicy(mode PermissionMode, policy SandboxPolicy) SandboxPolicy {
	switch normalizeSandboxPolicyType(policy.Type, mode) {
	case SandboxPolicyReadOnly:
		policy.Type = SandboxPolicyReadOnly
		policy.NetworkAccess = false
		policy.WritableRoots = nil
		policy.ReadOnlySubpaths = nil
	case SandboxPolicyWorkspaceWrite:
		policy.Type = SandboxPolicyWorkspaceWrite
		if len(policy.ReadOnlySubpaths) == 0 {
			policy.ReadOnlySubpaths = []string{".git", ".codex"}
		}
		if len(policy.WritableRoots) == 0 {
			policy.WritableRoots = []string{"."}
		}
		if !policy.NetworkAccess {
			policy.NetworkAccess = true
		}
	case SandboxPolicyExternal:
		policy.Type = SandboxPolicyExternal
	case SandboxPolicyDangerFull:
		fallthrough
	default:
		policy.Type = SandboxPolicyDangerFull
		policy.NetworkAccess = true
		policy.WritableRoots = nil
		policy.ReadOnlySubpaths = nil
	}
	policy.WritableRoots = normalizeStringList(policy.WritableRoots)
	policy.ReadOnlySubpaths = normalizeStringList(policy.ReadOnlySubpaths)
	return policy
}

func normalizeSandboxPolicyType(policyType SandboxPolicyType, mode PermissionMode) SandboxPolicyType {
	switch policyType {
	case SandboxPolicyReadOnly, SandboxPolicyWorkspaceWrite, SandboxPolicyDangerFull, SandboxPolicyExternal:
		return policyType
	}
	if mode == PermissionModeFullControl {
		return SandboxPolicyDangerFull
	}
	return SandboxPolicyWorkspaceWrite
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
