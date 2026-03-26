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
	Type             SandboxPolicyType `json:"type"`
	NetworkAccess    bool              `json:"network_access"`
	WritableRoots    []string          `json:"writable_roots"`
	ReadOnlySubpaths []string          `json:"read_only_subpaths"`
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
	Route        ExecutionRoute
	Escalation   *EscalationReason
	NeedApproval bool
}

// Config builds an execution runtime.
type Config struct {
	PermissionMode    PermissionMode
	SandboxType       string
	SandboxPolicy     SandboxPolicy
	SandboxHelperPath string

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
	Command               string
	Dir                   string
	Timeout               time.Duration
	IdleTimeout           time.Duration
	TTY                   bool
	EnvOverrides          map[string]string
	SandboxPolicyOverride *SandboxPolicy
	OnOutput              func(CommandOutputChunk)
}

type CommandOutputChunk struct {
	Stream string
	Text   string
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
	DecideRoute(command string, sandboxPermission SandboxPermission) CommandDecision
}

// PermissionModeSetter allows callers to update the active permission mode
// without rebuilding underlying runtime resources.
type PermissionModeSetter interface {
	SetPermissionMode(mode PermissionMode) error
}

// ApprovalRequiredError indicates that the call should be reviewed by upper
// application layer. Kernel tool layer does not handle approval workflow.
type ApprovalRequiredError struct {
	Reason string
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("tool: approval required: %s", e.Reason)
}

func (e *ApprovalRequiredError) Code() ErrorCode {
	return ErrorCodeApprovalRequired
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
	closers        []runtimeCloser
	closeOnce      sync.Once
	closeErr       error
}

type modeSwitchableRuntime struct {
	config         Config
	hostRuntime    Runtime
	defaultRuntime Runtime
	mu             sync.RWMutex
	permissionMode PermissionMode
}

func cloneSandboxPolicy(policy SandboxPolicy) SandboxPolicy {
	policy.WritableRoots = append([]string(nil), policy.WritableRoots...)
	policy.ReadOnlySubpaths = append([]string(nil), policy.ReadOnlySubpaths...)
	return policy
}

func sandboxPolicyForCommand(base SandboxPolicy, req CommandRequest) SandboxPolicy {
	if req.SandboxPolicyOverride == nil {
		return cloneSandboxPolicy(base)
	}
	return deriveSandboxPolicy(PermissionModeDefault, cloneSandboxPolicy(*req.SandboxPolicyOverride))
}

func mergeCommandEnv(overrides map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	env = append(env, defaultCommandEnvVars...)
	if len(overrides) == 0 {
		return env
	}
	index := make(map[string]int, len(env))
	for i, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok {
			index[key] = i
		}
	}
	for key, value := range overrides {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		entry := trimmedKey + "=" + value
		if at, ok := index[trimmedKey]; ok {
			env[at] = entry
			continue
		}
		index[trimmedKey] = len(env)
		env = append(env, entry)
	}
	return env
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
	if r.permissionMode == PermissionModeFullControl {
		return r.hostRunner
	}
	return r.sandboxRunner
}

func (r *runtimeImpl) DecideRoute(command string, sandboxPermission SandboxPermission) CommandDecision {
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
			NeedApproval: true,
		}
	}

	if sandboxPermission == SandboxPermissionRequireEscalated {
		if commandIsApprovalWhitelisted(command) {
			return CommandDecision{Route: ExecutionRouteHost}
		}
		return CommandDecision{
			Route:        ExecutionRouteHost,
			Escalation:   &EscalationReason{Message: "require_escalated requested"},
			NeedApproval: true,
		}
	}
	return CommandDecision{Route: ExecutionRouteSandbox}
}

func commandIsApprovalWhitelisted(command string) bool {
	segments := shellCommandSegments(command)
	if len(segments) == 0 {
		return false
	}
	sawCommand := false
	for _, segment := range segments {
		tokens := shellSegmentTokens(segment)
		if len(tokens) == 0 {
			continue
		}
		base := strings.ToLower(filepath.Base(tokens[0]))
		if base == "" {
			continue
		}
		sawCommand = true
		if !isApprovalWhitelistedBase(base) {
			return false
		}
	}
	return sawCommand
}

func isApprovalWhitelistedBase(base string) bool {
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "cd", "pwd", "ls", "stat", "file", "head", "tail", "cat", "grep", "egrep", "fgrep", "find", "which", "whereis", "env", "printenv", "uname", "id", "whoami":
		return true
	default:
		return false
	}
}

func shellCommandSegments(command string) []string {
	var (
		segments []string
		buf      strings.Builder
		squote   bool
		dquote   bool
		escape   bool
	)
	flush := func() {
		part := strings.TrimSpace(buf.String())
		if part != "" {
			segments = append(segments, part)
		}
		buf.Reset()
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
			buf.WriteRune(r)
		case '\'':
			if !dquote {
				squote = !squote
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
			}
			buf.WriteRune(r)
		case ';':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		case '&':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			buf.WriteRune(r)
		case '|':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellSegmentTokens(segment string) []string {
	var (
		tokens []string
		buf    strings.Builder
		squote bool
		dquote bool
		escape bool
	)
	flush := func() {
		token := strings.TrimSpace(buf.String())
		if token == "" {
			buf.Reset()
			return
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") && len(tokens) == 0 {
			buf.Reset()
			return
		}
		tokens = append(tokens, token)
		buf.Reset()
	}
	for _, r := range segment {
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
		case '\'':
			if !dquote {
				squote = !squote
				continue
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
				continue
			}
			buf.WriteRune(r)
		case ' ', '\t', '\n':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return tokens
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

func normalizePermissionMode(mode PermissionMode) (PermissionMode, error) {
	if mode == "" {
		mode = PermissionModeDefault
	}
	if mode != PermissionModeDefault && mode != PermissionModeFullControl {
		return "", fmt.Errorf("execenv: invalid permission mode %q", mode)
	}
	return mode, nil
}

// NewModeSwitchable builds one runtime whose underlying resources remain
// stable while callers switch between permission modes.
func NewModeSwitchable(cfg Config) (Runtime, error) {
	mode, err := normalizePermissionMode(cfg.PermissionMode)
	if err != nil {
		return nil, err
	}
	sharedCfg := cfg
	if sharedCfg.FileSystem == nil {
		sharedCfg.FileSystem = newHostFileSystem()
	}
	if sharedCfg.HostRunner == nil {
		sharedCfg.HostRunner = newHostRunner()
	}
	hostCfg := sharedCfg
	hostCfg.PermissionMode = PermissionModeFullControl
	hostRuntime, err := New(hostCfg)
	if err != nil {
		return nil, err
	}
	runtime := &modeSwitchableRuntime{
		config:         sharedCfg,
		hostRuntime:    hostRuntime,
		permissionMode: mode,
	}
	if mode != PermissionModeFullControl {
		if err := runtime.initDefaultRuntimeLocked(); err != nil {
			_ = Close(hostRuntime)
			return nil, err
		}
	}
	return runtime, nil
}

func (r *modeSwitchableRuntime) initDefaultRuntimeLocked() error {
	if r == nil || r.defaultRuntime != nil {
		return nil
	}
	defaultCfg := r.config
	defaultCfg.PermissionMode = PermissionModeDefault
	defaultRuntime, err := New(defaultCfg)
	if err != nil {
		return err
	}
	r.defaultRuntime = defaultRuntime
	return nil
}

func (r *modeSwitchableRuntime) SetPermissionMode(mode PermissionMode) error {
	normalized, err := normalizePermissionMode(mode)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if normalized != PermissionModeFullControl {
		if err := r.initDefaultRuntimeLocked(); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	r.permissionMode = normalized
	r.mu.Unlock()
	return nil
}

func (r *modeSwitchableRuntime) currentPermissionMode() PermissionMode {
	if r == nil {
		return PermissionModeDefault
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.permissionMode == "" {
		return PermissionModeDefault
	}
	return r.permissionMode
}

func (r *modeSwitchableRuntime) PermissionMode() PermissionMode {
	return r.currentPermissionMode()
}

func (r *modeSwitchableRuntime) SandboxType() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime != nil {
		return defaultRuntime.SandboxType()
	}
	return strings.TrimSpace(strings.ToLower(r.config.SandboxType))
}

func (r *modeSwitchableRuntime) SandboxPolicy() SandboxPolicy {
	if r == nil {
		return SandboxPolicy{}
	}
	if r.currentPermissionMode() == PermissionModeFullControl {
		return deriveSandboxPolicy(PermissionModeFullControl, SandboxPolicy{})
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime == nil {
		return SandboxPolicy{}
	}
	return defaultRuntime.SandboxPolicy()
}

func (r *modeSwitchableRuntime) FallbackToHost() bool {
	if r == nil {
		return false
	}
	if r.currentPermissionMode() == PermissionModeFullControl {
		return false
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime == nil {
		return false
	}
	return defaultRuntime.FallbackToHost()
}

func (r *modeSwitchableRuntime) FallbackReason() string {
	if r == nil {
		return ""
	}
	if r.currentPermissionMode() == PermissionModeFullControl {
		return ""
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime == nil {
		return ""
	}
	return defaultRuntime.FallbackReason()
}

func (r *modeSwitchableRuntime) FileSystem() FileSystem {
	if r == nil {
		return nil
	}
	if r.currentPermissionMode() != PermissionModeFullControl {
		r.mu.RLock()
		defaultRuntime := r.defaultRuntime
		r.mu.RUnlock()
		if defaultRuntime != nil {
			return defaultRuntime.FileSystem()
		}
	}
	if r.hostRuntime == nil {
		return nil
	}
	return r.hostRuntime.FileSystem()
}

func (r *modeSwitchableRuntime) HostRunner() CommandRunner {
	if r == nil {
		return nil
	}
	if r.currentPermissionMode() != PermissionModeFullControl {
		r.mu.RLock()
		defaultRuntime := r.defaultRuntime
		r.mu.RUnlock()
		if defaultRuntime != nil {
			return defaultRuntime.HostRunner()
		}
	}
	if r.hostRuntime == nil {
		return nil
	}
	return r.hostRuntime.HostRunner()
}

func (r *modeSwitchableRuntime) SandboxRunner() CommandRunner {
	if r == nil {
		return nil
	}
	if r.currentPermissionMode() == PermissionModeFullControl {
		if r.hostRuntime == nil {
			return nil
		}
		return r.hostRuntime.HostRunner()
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime == nil {
		return nil
	}
	return defaultRuntime.SandboxRunner()
}

func (r *modeSwitchableRuntime) DecideRoute(command string, sandboxPermission SandboxPermission) CommandDecision {
	mode := r.currentPermissionMode()
	if mode == PermissionModeFullControl {
		return CommandDecision{Route: ExecutionRouteHost}
	}
	if r == nil {
		return CommandDecision{}
	}
	r.mu.RLock()
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	if defaultRuntime == nil {
		return CommandDecision{}
	}
	if defaultRuntime.FallbackToHost() {
		message := "sandbox unavailable, host execution requires approval"
		if strings.TrimSpace(defaultRuntime.FallbackReason()) != "" {
			message = message + ": " + strings.TrimSpace(defaultRuntime.FallbackReason())
		}
		return CommandDecision{
			Route: ExecutionRouteHost,
			Escalation: &EscalationReason{
				Message: message,
			},
			NeedApproval: true,
		}
	}
	if sandboxPermission == SandboxPermissionRequireEscalated {
		if commandIsApprovalWhitelisted(command) {
			return CommandDecision{Route: ExecutionRouteHost}
		}
		return CommandDecision{
			Route:        ExecutionRouteHost,
			Escalation:   &EscalationReason{Message: "require_escalated requested"},
			NeedApproval: true,
		}
	}
	return CommandDecision{Route: ExecutionRouteSandbox}
}

func (r *modeSwitchableRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	hostRuntime := r.hostRuntime
	defaultRuntime := r.defaultRuntime
	r.mu.RUnlock()
	var firstErr error
	if defaultRuntime != nil {
		closeErr := Close(defaultRuntime)
		if closeErr != nil {
			firstErr = closeErr
		}
	}
	if hostRuntime != nil {
		closeErr := Close(hostRuntime)
		if closeErr != nil && firstErr == nil {
			firstErr = closeErr
		}
	}
	return firstErr
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
	mode, err := normalizePermissionMode(cfg.PermissionMode)
	if err != nil {
		return nil, err
	}

	filesystem := cfg.FileSystem
	if filesystem == nil {
		filesystem = newHostFileSystem()
	}
	hostRunner := cfg.HostRunner
	if hostRunner == nil {
		hostRunner = newHostRunner()
	}

	resolvedPolicy := deriveSandboxPolicy(mode, cfg.SandboxPolicy)

	runtime := &runtimeImpl{
		permissionMode: mode,
		sandboxPolicy:  resolvedPolicy,
		fs:             filesystem,
		hostRunner:     hostRunner,
	}

	// Register host runner for cleanup if it implements runtimeCloser
	// (e.g. to terminate async sessions on shutdown).
	if closer, ok := hostRunner.(runtimeCloser); ok {
		runtime.closers = append(runtime.closers, closer)
	}

	if mode == PermissionModeFullControl {
		return runtime, nil
	}

	requestedSandboxType := strings.TrimSpace(strings.ToLower(cfg.SandboxType))
	if strings.EqualFold(runtimeGOOS, "darwin") && requestedSandboxType != "" && requestedSandboxType != seatbeltSandboxType {
		return nil, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: sandbox type %q is unsupported on darwin, expected %q", requestedSandboxType, seatbeltSandboxType)
	}
	if strings.EqualFold(runtimeGOOS, "linux") &&
		requestedSandboxType != "" &&
		requestedSandboxType != landlockSandboxType &&
		requestedSandboxType != bwrapSandboxType {
		return nil, NewCodedError(
			ErrorCodeSandboxUnsupported,
			"execenv: sandbox type %q is unsupported on linux, expected %q or %q",
			requestedSandboxType,
			landlockSandboxType,
			bwrapSandboxType,
		)
	}
	candidates := sandboxTypeCandidates(requestedSandboxType)
	if len(candidates) == 0 {
		return nil, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: no sandbox backend candidates")
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
					return nil, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: unknown sandbox type %q", candidate)
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

func sandboxTypeCandidates(requested string) []string {
	return sandboxTypeCandidatesForPlatform(requested, runtimeGOOS)
}

func sandboxTypeCandidatesForPlatform(requested string, goos string) []string {
	value := strings.TrimSpace(strings.ToLower(requested))
	normalizedGoos := strings.TrimSpace(strings.ToLower(goos))
	if normalizedGoos == "darwin" {
		if value == "" || value == seatbeltSandboxType {
			return []string{seatbeltSandboxType}
		}
		return nil
	}
	if normalizedGoos == "linux" {
		if value == "" {
			return []string{bwrapSandboxType, landlockSandboxType}
		}
		if value == landlockSandboxType {
			return []string{landlockSandboxType}
		}
		if value == bwrapSandboxType {
			return []string{bwrapSandboxType}
		}
		return nil
	}
	if value == "" {
		return []string{bwrapSandboxType}
	}
	return []string{value}
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
			policy.ReadOnlySubpaths = []string{".git"}
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
