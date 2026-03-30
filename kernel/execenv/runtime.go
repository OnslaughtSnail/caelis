package execenv

import (
	"cmp"
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
	ReadableRoots    []string          `json:"readable_roots"`
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
	Backend      string
	Escalation   *EscalationReason
	NeedApproval bool
}

// SandboxDiagnostics captures backend selection attempts and fallback state.
type SandboxDiagnostics struct {
	RequestedType  string
	ResolvedType   string
	Candidates     []string
	Failures       []string
	FallbackToHost bool
	FallbackReason string
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
	SandboxPermission     SandboxPermission
	RouteHint             ExecutionRoute
	BackendName           string
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

type BackendKind string

const (
	BackendKindHost    BackendKind = "host"
	BackendKindSandbox BackendKind = "sandbox"
)

type BackendCapabilities struct {
	Async bool
}

type BackendHealth struct {
	Ready   bool
	Message string
}

type BackendSnapshot struct {
	Name         string
	Kind         BackendKind
	Capabilities BackendCapabilities
	Health       BackendHealth
}

type SandboxStatus string

const (
	SandboxStatusReady       SandboxStatus = "ready"
	SandboxStatusFallback    SandboxStatus = "fallback"
	SandboxStatusUnavailable SandboxStatus = "unavailable"
)

type RouterState struct {
	Diagnostics SandboxDiagnostics
}

type RuntimeState struct {
	Mode             PermissionMode
	RequestedSandbox string
	ResolvedSandbox  string
	SandboxStatus    SandboxStatus
	FallbackReason   string
	Backends         []BackendSnapshot
	RouterState      RouterState
}

type RouteRequest struct {
	Command           string
	SandboxPermission SandboxPermission
}

type CommandSessionRef struct {
	Backend   string
	SessionID string
}

type Session interface {
	Ref() CommandSessionRef
	WriteInput(input []byte) error
	ReadOutput(stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error)
	Status() (SessionStatus, error)
	Wait(ctx context.Context, timeout time.Duration) (CommandResult, error)
	Terminate() error
}

// CommandRunner executes shell commands for tools.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}

type Backend interface {
	Name() string
	Kind() BackendKind
	Capabilities() BackendCapabilities
	Health(context.Context) BackendHealth
	Execute(context.Context, CommandRequest) (CommandResult, error)
	Start(context.Context, CommandRequest) (Session, error)
	OpenSession(sessionID string) (Session, error)
}

type BackendSet interface {
	Backend(name string) (Backend, bool)
	DefaultHost() Backend
	DefaultSandbox() (Backend, bool)
	Snapshot() []BackendSnapshot
	Close() error
}

type Router interface {
	Decide(context.Context, RouteRequest) (CommandDecision, error)
}

// Runtime exposes execution primitives and derived security policies.
type Runtime interface {
	PermissionMode() PermissionMode
	SandboxType() string
	SandboxPolicy() SandboxPolicy
	FallbackToHost() bool
	FallbackReason() string
	Diagnostics() SandboxDiagnostics
	FileSystem() FileSystem
	Execute(context.Context, CommandRequest) (CommandResult, error)
	Start(context.Context, CommandRequest) (Session, error)
	OpenSession(CommandSessionRef) (Session, error)
	State() RuntimeState
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

func cloneSandboxPolicy(policy SandboxPolicy) SandboxPolicy {
	policy.ReadableRoots = append([]string(nil), policy.ReadableRoots...)
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

func normalizePermissionMode(mode PermissionMode) (PermissionMode, error) {
	if mode == "" {
		mode = PermissionModeDefault
	}
	if mode != PermissionModeDefault && mode != PermissionModeFullControl {
		return "", fmt.Errorf("execenv: invalid permission mode %q", mode)
	}
	return mode, nil
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

func cloneSandboxDiagnostics(diag SandboxDiagnostics) SandboxDiagnostics {
	diag.Candidates = append([]string(nil), diag.Candidates...)
	diag.Failures = append([]string(nil), diag.Failures...)
	return diag
}

func fallbackApprovalMessage(reason string) string {
	message := "sandbox unavailable, host execution requires approval"
	if strings.TrimSpace(reason) != "" {
		message = message + ": " + strings.TrimSpace(reason)
	}
	return message
}

func decideRoute(mode PermissionMode, diagnostics SandboxDiagnostics, hostBackend string, sandboxBackend string, command string, sandboxPermission SandboxPermission) CommandDecision {
	hostBackend = cmp.Or(strings.TrimSpace(hostBackend), hostBackendName)
	sandboxBackend = cmp.Or(strings.TrimSpace(sandboxBackend), "sandbox")
	if mode == PermissionModeFullControl {
		return CommandDecision{Route: ExecutionRouteHost, Backend: hostBackend}
	}
	if diagnostics.FallbackToHost {
		return CommandDecision{
			Route:   ExecutionRouteHost,
			Backend: hostBackend,
			Escalation: &EscalationReason{
				Message: fallbackApprovalMessage(diagnostics.FallbackReason),
			},
			NeedApproval: true,
		}
	}
	if sandboxPermission == SandboxPermissionRequireEscalated {
		if commandIsApprovalWhitelisted(command) {
			return CommandDecision{Route: ExecutionRouteHost, Backend: hostBackend}
		}
		return CommandDecision{
			Route:        ExecutionRouteHost,
			Backend:      hostBackend,
			Escalation:   &EscalationReason{Message: "require_escalated requested"},
			NeedApproval: true,
		}
	}
	return CommandDecision{Route: ExecutionRouteSandbox, Backend: sandboxBackend}
}

var (
	sandboxFactoriesMu sync.RWMutex
	sandboxFactories   = map[string]SandboxFactory{}
	runtimeGOOS        = stdruntime.GOOS
)

// SelectSandbox resolves one sandbox backend and returns structured selection
// diagnostics. A nil runner with FallbackToHost=true indicates successful
// fallback to host mode in default permission mode.
func SelectSandbox(cfg Config) (CommandRunner, SandboxDiagnostics, error) {
	requestedSandboxType := strings.TrimSpace(strings.ToLower(cfg.SandboxType))
	if strings.EqualFold(runtimeGOOS, "darwin") && requestedSandboxType != "" && requestedSandboxType != seatbeltSandboxType {
		return nil, SandboxDiagnostics{}, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: sandbox type %q is unsupported on darwin, expected %q", requestedSandboxType, seatbeltSandboxType)
	}
	if strings.EqualFold(runtimeGOOS, "linux") &&
		requestedSandboxType != "" &&
		requestedSandboxType != landlockSandboxType &&
		requestedSandboxType != bwrapSandboxType {
		return nil, SandboxDiagnostics{}, NewCodedError(
			ErrorCodeSandboxUnsupported,
			"execenv: sandbox type %q is unsupported on linux, expected %q or %q",
			requestedSandboxType,
			landlockSandboxType,
			bwrapSandboxType,
		)
	}

	diagnostics := SandboxDiagnostics{
		RequestedType: requestedSandboxType,
		Candidates:    selectSandboxCandidates(requestedSandboxType),
	}
	if len(diagnostics.Candidates) == 0 {
		return nil, diagnostics, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: no sandbox backend candidates")
	}
	diagnostics.ResolvedType = diagnostics.Candidates[0]

	if cfg.SandboxRunner != nil {
		if prober, ok := cfg.SandboxRunner.(sandboxProber); ok {
			probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			probeErr := prober.Probe(probeCtx)
			cancel()
			if probeErr != nil {
				diagnostics.Failures = append(diagnostics.Failures, fmt.Sprintf("%s: unavailable: %v", diagnostics.ResolvedType, probeErr))
				diagnostics.FallbackToHost = true
				diagnostics.FallbackReason = fmt.Sprintf("sandbox backend %q unavailable: %v", diagnostics.ResolvedType, probeErr)
				return nil, diagnostics, nil
			}
		}
		return cfg.SandboxRunner, diagnostics, nil
	}

	for _, candidate := range diagnostics.Candidates {
		sandboxFactoriesMu.RLock()
		factory, ok := sandboxFactories[candidate]
		sandboxFactoriesMu.RUnlock()
		if !ok {
			if requestedSandboxType == candidate {
				return nil, diagnostics, NewCodedError(ErrorCodeSandboxUnsupported, "execenv: unknown sandbox type %q", candidate)
			}
			diagnostics.Failures = append(diagnostics.Failures, fmt.Sprintf("%s: unknown sandbox type", candidate))
			continue
		}
		buildCfg := cfg
		buildCfg.SandboxType = candidate
		builtRunner, err := factory.Build(buildCfg)
		if err != nil {
			diagnostics.Failures = append(diagnostics.Failures, fmt.Sprintf("%s: init failed: %v", candidate, err))
			continue
		}
		if prober, ok := builtRunner.(sandboxProber); ok {
			probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			probeErr := prober.Probe(probeCtx)
			cancel()
			if probeErr != nil {
				diagnostics.Failures = append(diagnostics.Failures, fmt.Sprintf("%s: unavailable: %v", candidate, probeErr))
				if closer, ok := builtRunner.(runtimeCloser); ok {
					_ = closer.Close()
				}
				continue
			}
		}
		diagnostics.ResolvedType = candidate
		return builtRunner, diagnostics, nil
	}

	diagnostics.FallbackToHost = true
	diagnostics.FallbackReason = strings.Join(diagnostics.Failures, "; ")
	return nil, diagnostics, nil
}

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
	return newRuntimeView(cfg)
}

func sandboxTypeCandidates(requested string) []string {
	return sandboxTypeCandidatesForPlatform(requested, runtimeGOOS)
}

func selectSandboxCandidates(requested string) []string {
	candidates := sandboxTypeCandidatesForPlatform(requested, runtimeGOOS)
	if strings.TrimSpace(strings.ToLower(runtimeGOOS)) != "linux" {
		return candidates
	}
	if strings.TrimSpace(requested) != "" {
		return candidates
	}
	// Keep the platform default order here: bwrap can enforce read-only
	// subpaths inside writable roots, while the current landlock backend cannot.
	return candidates
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
		policy.ReadableRoots = nil
		policy.WritableRoots = nil
		policy.ReadOnlySubpaths = nil
	}
	policy.ReadableRoots = normalizeStringList(policy.ReadableRoots)
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
