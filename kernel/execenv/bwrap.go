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
	"strings"
	"sync/atomic"
	"time"
)

const (
	bwrapSandboxType = "bwrap"
)

type bwrapSandboxFactory struct{}

func (f bwrapSandboxFactory) Type() string {
	return bwrapSandboxType
}

func (f bwrapSandboxFactory) Build(cfg Config) (CommandRunner, error) {
	return newBwrapRunner(cfg.SandboxPolicy), nil
}

type bwrapRunner struct {
	execCommand func(context.Context, string, ...string) *exec.Cmd
	lookPath    func(string) (string, error)
	goos        string
	policy      SandboxPolicy
}

func newBwrapRunner(policy SandboxPolicy) CommandRunner {
	return &bwrapRunner{
		execCommand: exec.CommandContext,
		lookPath:    exec.LookPath,
		goos:        stdruntime.GOOS,
		policy:      policy,
	}
}

func (b *bwrapRunner) Probe(ctx context.Context) error {
	if b.goos != "linux" {
		return fmt.Errorf("bwrap sandbox is only supported on linux (current=%s)", b.goos)
	}
	if _, err := b.lookPath("bwrap"); err != nil {
		return fmt.Errorf("bwrap sandbox unavailable: bwrap not found: %w", err)
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
	probeArgs = append(probeArgs, "--", "/bin/sh", "-c", "echo bwrap-probe")
	cmd := b.execCommand(ctx, "bwrap", probeArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("bwrap sandbox probe failed: %w", err)
		}
		return fmt.Errorf("bwrap sandbox probe failed: %w; stderr=%s", err, msg)
	}
	return nil
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
	bwrapArgs := buildBwrapArgs(b.policy, workDir)

	args := append(bwrapArgs, "--", "bash", "-lc", req.Command)
	cmd := b.execCommand(runCtx, "bwrap", args...)
	applyNonInteractiveCommandDefaults(cmd)
	if strings.TrimSpace(req.Dir) != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = append(os.Environ(), defaultCommandEnvVars...)

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
			"tool: bwrap sandbox command produced no output for %s and was terminated (likely interactive/long-running; try larger idle_timeout_ms); %s",
			label,
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: bwrap sandbox command failed: %w; %s", waitErr, commandOutputSummary(result))
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

	// Always read-only root; writable access is granted via scoped binds.
	args = append(args, "--ro-bind", "/", "/")

	// /dev and /proc
	args = append(args, "--dev", "/dev")
	args = append(args, "--proc", "/proc")

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
