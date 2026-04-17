package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	seatbeltSandboxType = "seatbelt"
)

type seatbeltSandboxFactory struct{}

func (f seatbeltSandboxFactory) Type() string {
	return seatbeltSandboxType
}

func (f seatbeltSandboxFactory) Build(cfg Config) (CommandRunner, error) {
	return newSeatbeltRunner(deriveSandboxPolicy(PermissionModeDefault, cloneSandboxPolicy(cfg.SandboxPolicy))), nil
}

type seatbeltRunner struct {
	execCommand    func(context.Context, string, ...string) *exec.Cmd
	lookPath       func(string) (string, error)
	goos           string
	policy         SandboxPolicy
	sessionManager *SessionManager
	closed         atomic.Bool
}

func newSeatbeltRunner(policy SandboxPolicy) CommandRunner {
	return &seatbeltRunner{
		execCommand:    exec.CommandContext,
		lookPath:       exec.LookPath,
		goos:           stdruntime.GOOS,
		policy:         policy,
		sessionManager: NewSessionManager(DefaultSessionManagerConfig()),
	}
}

func (s *seatbeltRunner) Probe(ctx context.Context) error {
	if s.goos != "darwin" {
		return fmt.Errorf("seatbelt sandbox is only supported on darwin (current=%s)", s.goos)
	}
	if _, err := s.lookPath("sandbox-exec"); err != nil {
		return fmt.Errorf("seatbelt sandbox unavailable: sandbox-exec not found: %w", err)
	}
	cmd := s.execCommand(ctx, "sandbox-exec", "-p", "(version 1) (allow default)", "/bin/sh", "-lc", "echo seatbelt-probe")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("seatbelt sandbox probe failed: %w", err)
		}
		return fmt.Errorf("seatbelt sandbox probe failed: %w; stderr=%s", err, msg)
	}
	return nil
}

func (s *seatbeltRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	workDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve seatbelt workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(s.policy, req)
	profile := buildSeatbeltProfile(effectivePolicy, workDir)

	args := []string{"-p", profile, "bash", "-lc", req.Command}
	cmd := s.execCommand(runCtx, "sandbox-exec", args...)
	applyNonInteractiveCommandDefaults(cmd)
	if strings.TrimSpace(req.Dir) != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = mergeCommandEnv(req.EnvOverrides)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lastOutput := atomic.Int64{}
	lastOutput.Store(time.Now().UnixNano())
	cmd.Stdout = &activityWriter{buffer: &stdout, lastOutput: &lastOutput, stream: "stdout", onOutput: req.OnOutput}
	cmd.Stderr = &activityWriter{buffer: &stderr, lastOutput: &lastOutput, stream: "stderr", onOutput: req.OnOutput}

	if err := cmd.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("tool: seatbelt sandbox command start failed: %w", err)
	}
	waitErr := waitWithIdleTimeout(runCtx, cmd, req.IdleTimeout, &lastOutput)

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if waitErr == nil {
		return result, nil
	}
	result.ExitCode = resolveExitCode(waitErr)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(waitErr, context.DeadlineExceeded) {
		label := "context deadline"
		if req.Timeout > 0 {
			label = req.Timeout.String()
		}
		return result, WrapCodedError(
			ErrorCodeSandboxCommandTimeout,
			waitErr,
			"tool: seatbelt sandbox command timed out after %s; %s",
			label,
			commandOutputSummary(result),
		)
	}
	if errors.Is(waitErr, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, NewCodedError(
			ErrorCodeSandboxIdleTimeout,
			"tool: seatbelt sandbox command produced no output for %s and was terminated (likely interactive or long-running); %s",
			label,
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: seatbelt sandbox command failed: %w; %s", waitErr, commandOutputSummary(result))
}

func (s *seatbeltRunner) StartAsync(_ context.Context, req CommandRequest) (string, error) {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return "", err
	}
	if req.TTY {
		return "", fmt.Errorf("tool: seatbelt async tty is not supported")
	}
	workDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return "", fmt.Errorf("tool: resolve seatbelt workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(s.policy, req)
	session, err := manager.StartSession(AsyncSessionConfig{
		Command:         req.Command,
		Dir:             req.Dir,
		Env:             mergeCommandEnv(req.EnvOverrides),
		OutputBufferCap: 256 * 1024,
		Timeout:         req.Timeout,
		IdleTimeout:     req.IdleTimeout,
		BuildCommand: func(ctx context.Context, cfg AsyncSessionConfig) (*exec.Cmd, error) {
			profile := buildSeatbeltProfile(effectivePolicy, workDir)
			cmd := s.execCommand(ctx, "sandbox-exec", "-p", profile, "bash", "-lc", cfg.Command)
			if strings.TrimSpace(cfg.Dir) != "" {
				cmd.Dir = cfg.Dir
			}
			cmd.Env = append([]string(nil), cfg.Env...)
			return cmd, nil
		},
	})
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

func (s *seatbeltRunner) WriteInput(sessionID string, input []byte) error {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.WriteInput(sessionID, input)
}

func (s *seatbeltRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return manager.ReadOutput(sessionID, stdoutMarker, stderrMarker)
}

func (s *seatbeltRunner) GetSessionStatus(sessionID string) (SessionStatus, error) {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return SessionStatus{}, err
	}
	return manager.GetSessionStatus(sessionID)
}

func (s *seatbeltRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (CommandResult, error) {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return CommandResult{}, err
	}
	waitCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	exitCode, err := manager.WaitSession(waitCtx, sessionID)
	if err != nil {
		return CommandResult{}, err
	}
	result, err := manager.GetResult(sessionID)
	if err != nil {
		return CommandResult{ExitCode: exitCode}, nil
	}
	return result, nil
}

func (s *seatbeltRunner) TerminateSession(sessionID string) error {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.TerminateSession(sessionID)
}

func (s *seatbeltRunner) ListSessions() []SessionInfo {
	manager, err := s.asyncSessionManager()
	if err != nil {
		return nil
	}
	return manager.ListSessions()
}

func (s *seatbeltRunner) Close() error {
	s.closed.Store(true)
	if s.sessionManager != nil {
		return s.sessionManager.Close()
	}
	return nil
}

func (s *seatbeltRunner) asyncSessionManager() (*SessionManager, error) {
	if s == nil || s.closed.Load() || s.sessionManager == nil {
		return nil, fmt.Errorf("execenv: seatbelt runner is closed")
	}
	return s.sessionManager, nil
}

func buildSeatbeltProfile(policy SandboxPolicy, workDir string) string {
	var b strings.Builder

	// Base rules — start with deny-all, import system baseline, then add
	// broad process/file-read/sysctl permissions.
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(import \"system.sb\")\n")
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow signal (target same-sandbox))\n")
	b.WriteString("(allow sysctl-read)\n")
	if roots := shellReadableRoots(policy, workDir); len(roots) > 0 {
		for _, root := range roots {
			fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", sbplString(root))
			fmt.Fprintf(&b, "(allow file-read-metadata file-test-existence (subpath %s))\n", sbplString(root))
		}
	} else {
		b.WriteString("(allow file-read*)\n")
	}

	// Core extensions: PTY, IPC, IOKit, system calls.
	b.WriteString(seatbeltCoreExtensions)

	// Mach services for system libraries, logging, crash reporting, etc.
	b.WriteString(seatbeltMachServices)

	// Device files and framework mapping.
	b.WriteString(seatbeltDeviceAndFramework)

	// Network
	if policy.NetworkAccess {
		b.WriteString("(allow network*)\n")
		b.WriteString(seatbeltNetworkExtensions)
	}

	// Writable roots
	for _, root := range seatbeltWritableRoots(policy, workDir) {
		fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", sbplString(root))
	}

	// Read-only subpaths (deny after allow so they take precedence)
	for _, sub := range seatbeltReadOnlySubpaths(policy, workDir) {
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", sbplString(sub))
	}

	return b.String()
}

func seatbeltWritableRoots(policy SandboxPolicy, workDir string) []string {
	if policy.Type == SandboxPolicyReadOnly {
		return nil
	}
	roots := make([]string, 0, len(policy.WritableRoots)+8)

	// User-declared writable roots.
	for _, one := range policy.WritableRoots {
		resolved := resolveSandboxPath(workDir, one)
		if resolved != "" {
			roots = append(roots, sandboxPathVariants(resolved)...)
		}
	}

	// Temp directories — always writable.
	tmp := strings.TrimSpace(os.TempDir())
	if tmp != "" {
		roots = append(roots, sandboxPathVariants(tmp)...)
	}
	// On macOS /tmp is a symlink to /private/tmp; os.TempDir() returns
	// $TMPDIR (e.g. /var/folders/...) which does NOT cover /tmp.
	roots = append(roots, sandboxPathVariants("/tmp")...)
	// /var/tmp is another common temp location used by system tools.
	roots = append(roots, sandboxPathVariants("/var/tmp")...)

	// Cache directories — low-risk, regenerable data that many dev tools
	// (pip, npm, go build, homebrew, playwright) require for normal
	// operation.  We intentionally do NOT open ~/Library/Application Support
	// or ~/.local because those contain persistent app state.
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, sandboxPathVariants(filepath.Join(home, "Library", "Caches"))...)
		roots = append(roots, sandboxPathVariants(filepath.Join(home, ".cache"))...)
	}

	// macOS per-user cache dir (/var/folders/xx/xxx/C/) — needed for TLS
	// certificate caching when network is enabled.  Matches codex's
	// DARWIN_USER_CACHE_DIR behaviour.
	if policy.NetworkAccess {
		if cacheDir := darwinUserCacheDir(); cacheDir != "" {
			roots = append(roots, sandboxPathVariants(cacheDir)...)
		}
	}

	return normalizeStringList(roots)
}

// darwinUserCacheDir returns the macOS per-user cache directory
// (/var/folders/xx/xxx/C/), derived from $TMPDIR.  On macOS $TMPDIR
// is always /var/folders/XX/XXXXXXXXX/T/ and the cache dir is the
// sibling /var/folders/XX/XXXXXXXXX/C/.
func darwinUserCacheDir() string {
	tmpDir := strings.TrimRight(os.TempDir(), string(filepath.Separator))
	parent := filepath.Dir(tmpDir)
	if parent == "" || !strings.Contains(parent, string(filepath.Separator)+"var"+string(filepath.Separator)+"folders") {
		return ""
	}
	cacheDir := filepath.Join(parent, "C")
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		return cacheDir
	}
	return ""
}

func seatbeltReadOnlySubpaths(policy SandboxPolicy, workDir string) []string {
	values := make([]string, 0, len(policy.ReadOnlySubpaths))
	for _, one := range policy.ReadOnlySubpaths {
		resolved := resolveSandboxPath(workDir, one)
		if resolved != "" {
			values = append(values, sandboxPathVariants(resolved)...)
		}
	}
	return normalizeStringList(values)
}

func sbplString(v string) string {
	return strconv.Quote(v)
}

func init() {
	if err := RegisterSandboxFactory(seatbeltSandboxFactory{}); err != nil {
		panic(err)
	}
}
