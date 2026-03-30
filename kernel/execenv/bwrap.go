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
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	bwrapSandboxType  = "bwrap"
	bubblewrapDocsURL = "https://github.com/containers/bubblewrap"
)

type bwrapSandboxFactory struct{}

func (f bwrapSandboxFactory) Type() string {
	return bwrapSandboxType
}

func (f bwrapSandboxFactory) Build(cfg Config) (CommandRunner, error) {
	return newBwrapRunner(deriveSandboxPolicy(PermissionModeDefault, cloneSandboxPolicy(cfg.SandboxPolicy))), nil
}

type bwrapRunner struct {
	execCommand    func(context.Context, string, ...string) *exec.Cmd
	lookPath       func(string) (string, error)
	readFile       func(string) ([]byte, error)
	stat           func(string) (os.FileInfo, error)
	goos           string
	policy         SandboxPolicy
	sessionManager *SessionManager
	closed         atomic.Bool
}

func newBwrapRunner(policy SandboxPolicy) CommandRunner {
	return &bwrapRunner{
		execCommand:    exec.CommandContext,
		lookPath:       exec.LookPath,
		readFile:       os.ReadFile,
		stat:           os.Stat,
		goos:           stdruntime.GOOS,
		policy:         policy,
		sessionManager: NewSessionManager(DefaultSessionManagerConfig()),
	}
}

func (b *bwrapRunner) Probe(ctx context.Context) error {
	if b.goos != "linux" {
		return fmt.Errorf("bwrap sandbox is only supported on linux (current=%s)", b.goos)
	}
	bwrapPath, err := b.lookPath("bwrap")
	if err != nil {
		return fmt.Errorf("bwrap sandbox unavailable: bwrap not found: %w; %s", err, bubblewrapInstallHint(b.readFile))
	}
	if _, err := b.lookPath("bash"); err != nil {
		return fmt.Errorf("bwrap sandbox unavailable: bash not found: %w", err)
	}
	// Exercise the same namespace flags the runtime will actually use for
	// this policy so we don't reject machines that can run the default path.
	probeArgs := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--new-session",
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
	}
	if !b.policy.NetworkAccess {
		probeArgs = append(probeArgs, "--unshare-net")
	}
	probeArgs = append(probeArgs, "--", "bash", "-lc", "echo bwrap-probe")
	cmd := b.execCommand(ctx, "bwrap", probeArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("bwrap sandbox probe failed: %w", err)
		}
		if detail := bwrapProbeFailureDetail(bwrapPath, msg, b.stat, b.readFile); detail != "" {
			return fmt.Errorf("bwrap sandbox probe failed: %w; stderr=%s; %s", err, msg, detail)
		}
		return fmt.Errorf("bwrap sandbox probe failed: %w; stderr=%s", err, msg)
	}
	return nil
}

func bwrapProbeFailureDetail(
	bwrapPath string,
	stderr string,
	statFn func(string) (os.FileInfo, error),
	readFileFn func(string) ([]byte, error),
) string {
	lower := strings.ToLower(strings.TrimSpace(stderr))
	if lower == "" {
		return ""
	}
	if !strings.Contains(lower, "uid map") &&
		!strings.Contains(lower, "new namespace") &&
		!strings.Contains(lower, "namespace failed") &&
		!strings.Contains(lower, "operation not permitted") &&
		!strings.Contains(lower, "permission denied") {
		return ""
	}

	parts := []string{
		"bubblewrap needs a working unprivileged user-namespace setup or a setuid-root bwrap binary on linux",
	}
	if statFn != nil && strings.TrimSpace(bwrapPath) != "" {
		if info, err := statFn(bwrapPath); err == nil && info.Mode()&os.ModeSetuid == 0 {
			parts = append(parts, fmt.Sprintf("%s is not setuid", bwrapPath))
		}
	}
	if readFileFn != nil {
		if value, ok := readFirstLineInt(readFileFn, "/proc/sys/kernel/unprivileged_userns_clone"); ok && value == 0 {
			parts = append(parts, "kernel.unprivileged_userns_clone=0")
		}
		if value, ok := readFirstLineInt(readFileFn, "/proc/sys/user/max_user_namespaces"); ok && value == 0 {
			parts = append(parts, "user.max_user_namespaces=0")
		}
	}
	parts = append(parts, "docs="+bubblewrapDocsURL)
	return strings.Join(parts, "; ")
}

func bubblewrapInstallHint(readFileFn func(string) ([]byte, error)) string {
	if cmd := bubblewrapInstallCommand(readFileFn); cmd != "" {
		return fmt.Sprintf("install bubblewrap (for example: %s); docs=%s", cmd, bubblewrapDocsURL)
	}
	return fmt.Sprintf("install bubblewrap from your distro packages; docs=%s", bubblewrapDocsURL)
}

func bubblewrapInstallCommand(readFileFn func(string) ([]byte, error)) string {
	ids := linuxDistributionIDs(readFileFn)
	switch {
	case containsAnyString(ids, "debian", "ubuntu", "linuxmint", "pop", "elementary", "neon", "raspbian", "kali"):
		return "sudo apt install bubblewrap"
	case containsAnyString(ids, "fedora", "rhel", "centos", "rocky", "almalinux", "ol"):
		return "sudo dnf install bubblewrap"
	case containsAnyString(ids, "arch", "manjaro", "endeavouros", "artix"):
		return "sudo pacman -S bubblewrap"
	case containsAnyString(ids, "opensuse", "opensuse-leap", "opensuse-tumbleweed", "suse", "sles"):
		return "sudo zypper install bubblewrap"
	case containsAnyString(ids, "alpine"):
		return "sudo apk add bubblewrap"
	case containsAnyString(ids, "void"):
		return "sudo xbps-install -S bubblewrap"
	case containsAnyString(ids, "gentoo"):
		return "sudo emerge bubblewrap"
	default:
		return ""
	}
}

func linuxDistributionIDs(readFileFn func(string) ([]byte, error)) []string {
	if readFileFn == nil {
		return nil
	}
	data, err := readFileFn("/etc/os-release")
	if err != nil {
		return nil
	}
	values := make([]string, 0, 4)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToUpper(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "ID", "ID_LIKE":
			values = append(values, strings.Fields(strings.ToLower(value))...)
		}
	}
	return normalizeStringList(values)
}

func containsAnyString(values []string, needles ...string) bool {
	for _, value := range values {
		for _, needle := range needles {
			if value == needle {
				return true
			}
		}
	}
	return false
}

func readFirstLineInt(readFileFn func(string) ([]byte, error), path string) (int, bool) {
	if readFileFn == nil {
		return 0, false
	}
	data, err := readFileFn(path)
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func (b *bwrapRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	workDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve bwrap workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(b.policy, req)
	bwrapArgs := buildBwrapArgs(effectivePolicy, workDir)

	bwrapArgs = append(bwrapArgs, "--", "bash", "-lc", req.Command)
	args := bwrapArgs
	cmd := b.execCommand(runCtx, "bwrap", args...)
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
		return CommandResult{}, fmt.Errorf("tool: bwrap sandbox command start failed: %w", err)
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
			"tool: bwrap sandbox command timed out after %s; %s",
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
			"tool: bwrap sandbox command produced no output for %s and was terminated (likely interactive or long-running); %s",
			label,
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: bwrap sandbox command failed: %w; %s", waitErr, commandOutputSummary(result))
}

func (b *bwrapRunner) StartAsync(_ context.Context, req CommandRequest) (string, error) {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return "", err
	}
	if req.TTY {
		return "", fmt.Errorf("tool: bwrap async tty is not supported")
	}
	workDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return "", fmt.Errorf("tool: resolve bwrap workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(b.policy, req)
	session, err := manager.StartSession(AsyncSessionConfig{
		Command:         req.Command,
		Dir:             req.Dir,
		Env:             mergeCommandEnv(req.EnvOverrides),
		OutputBufferCap: 256 * 1024,
		Timeout:         req.Timeout,
		IdleTimeout:     req.IdleTimeout,
		BuildCommand: func(ctx context.Context, cfg AsyncSessionConfig) (*exec.Cmd, error) {
			bwrapArgs := buildBwrapArgs(effectivePolicy, workDir)
			bwrapArgs = append(bwrapArgs, "--", "bash", "-lc", cfg.Command)
			args := bwrapArgs
			cmd := b.execCommand(ctx, "bwrap", args...)
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

func (b *bwrapRunner) WriteInput(sessionID string, input []byte) error {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.WriteInput(sessionID, input)
}

func (b *bwrapRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return manager.ReadOutput(sessionID, stdoutMarker, stderrMarker)
}

func (b *bwrapRunner) GetSessionStatus(sessionID string) (SessionStatus, error) {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return SessionStatus{}, err
	}
	return manager.GetSessionStatus(sessionID)
}

func (b *bwrapRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (CommandResult, error) {
	manager, err := b.asyncSessionManager()
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

func (b *bwrapRunner) TerminateSession(sessionID string) error {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.TerminateSession(sessionID)
}

func (b *bwrapRunner) ListSessions() []SessionInfo {
	manager, err := b.asyncSessionManager()
	if err != nil {
		return nil
	}
	return manager.ListSessions()
}

func (b *bwrapRunner) Close() error {
	b.closed.Store(true)
	if b.sessionManager != nil {
		return b.sessionManager.Close()
	}
	return nil
}

func (b *bwrapRunner) asyncSessionManager() (*SessionManager, error) {
	if b == nil || b.closed.Load() || b.sessionManager == nil {
		return nil, fmt.Errorf("execenv: bwrap runner is closed")
	}
	return b.sessionManager, nil
}

// buildBwrapArgs constructs bubblewrap arguments from the sandbox policy.
// All policy types are always wrapped in bwrap for consistent sandboxing.
// DangerFull uses the same --ro-bind / / + scoped writable roots model
// as other policies, matching seatbelt's behavior on macOS.
func buildBwrapArgs(policy SandboxPolicy, workDir string) []string {
	args := []string{
		"--new-session",
		"--die-with-parent",
		"--unshare-user",
		"--unshare-pid",
	}

	if !policy.NetworkAccess {
		args = append(args, "--unshare-net")
	}

	if hasExplicitReadableRoots(policy) {
		args = append(args, buildScopedBwrapRootArgs(policy, workDir)...)
	} else {
		// Always read-only root; writable access is granted via scoped binds.
		args = append(args, "--ro-bind", "/", "/")

		// /dev and /proc
		args = append(args, "--dev", "/dev")
		args = append(args, "--proc", "/proc")
	}

	if policy.Type != SandboxPolicyReadOnly {
		// Writable roots (scoped)
		for _, root := range bwrapWritableRoots(policy, workDir) {
			args = append(args, "--bind", root, root)
		}
	}

	// Read-only subpath overrides (applied after writable binds)
	for _, sub := range bwrapReadOnlySubpaths(policy, workDir) {
		args = append(args, "--ro-bind", sub, sub)
	}

	return args
}

func buildScopedBwrapRootArgs(policy SandboxPolicy, workDir string) []string {
	readableRoots := shellReadableRoots(policy, workDir)
	writableRoots := bwrapWritableRoots(policy, workDir)
	readOnlySubpaths := bwrapReadOnlySubpaths(policy, workDir)

	destParents := bwrapMountParentDirs(readableRoots, writableRoots, readOnlySubpaths)

	args := []string{"--tmpfs", "/"}
	for _, dir := range destParents {
		args = append(args, "--dir", dir)
	}
	args = append(args, "--dev", "/dev")
	args = append(args, "--proc", "/proc")
	for _, root := range readableRoots {
		args = append(args, "--ro-bind", root, root)
	}
	return args
}

// bwrapWritableRoots returns the set of directories that should be bind-
// mounted writable inside the bubblewrap sandbox.
// Nonexistent paths are silently skipped because bwrap requires bind
// sources to exist on the host.
func bwrapWritableRoots(policy SandboxPolicy, workDir string) []string {
	if policy.Type == SandboxPolicyReadOnly {
		return nil
	}
	roots := make([]string, 0, len(policy.WritableRoots)+8)

	for _, one := range policy.WritableRoots {
		resolved := resolveBwrapPath(workDir, one)
		if resolved != "" {
			roots = append(roots, resolved)
		}
	}

	// Temp directories — always writable
	roots = append(roots, "/tmp")
	roots = append(roots, "/var/tmp")

	// Cache directory
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".cache"))
	}

	return filterExistingPaths(normalizeStringList(roots))
}

// bwrapReadOnlySubpaths returns directories that should be mounted read-only
// even within otherwise writable parent mounts.
// Nonexistent paths are silently skipped because bwrap requires bind
// sources to exist on the host.
func bwrapReadOnlySubpaths(policy SandboxPolicy, workDir string) []string {
	values := make([]string, 0, len(policy.ReadOnlySubpaths))
	for _, one := range policy.ReadOnlySubpaths {
		resolved := resolveBwrapPath(workDir, one)
		if resolved != "" {
			values = append(values, resolved)
		}
	}
	return filterExistingPaths(normalizeStringList(values))
}

func bwrapMountParentDirs(pathGroups ...[]string) []string {
	dirs := make([]string, 0, 32)
	seen := map[string]struct{}{}
	for _, paths := range pathGroups {
		for _, target := range paths {
			current := filepath.Dir(filepath.Clean(strings.TrimSpace(target)))
			for current != "" && current != "." && current != string(filepath.Separator) {
				if _, ok := seen[current]; !ok {
					seen[current] = struct{}{}
					dirs = append(dirs, current)
				}
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		leftDepth := strings.Count(dirs[i], string(filepath.Separator))
		rightDepth := strings.Count(dirs[j], string(filepath.Separator))
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return dirs[i] < dirs[j]
	})
	return dirs
}

// filterExistingPaths returns only the paths that exist on the host.
// bwrap bind-mounts require the source path to exist; absent paths
// cause startup failures.
func filterExistingPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			result = append(result, p)
		}
	}
	return result
}

func resolveBwrapPath(baseDir, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(baseDir, trimmed))
}

func init() {
	if err := RegisterSandboxFactory(bwrapSandboxFactory{}); err != nil {
		panic(err)
	}
}
