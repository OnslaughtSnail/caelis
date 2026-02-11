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
	"syscall"
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
	return newSeatbeltRunner(cfg.SandboxPolicy), nil
}

type seatbeltRunner struct {
	execCommand func(context.Context, string, ...string) *exec.Cmd
	lookPath    func(string) (string, error)
	goos        string
	policy      SandboxPolicy
}

func newSeatbeltRunner(policy SandboxPolicy) CommandRunner {
	return &seatbeltRunner{
		execCommand: exec.CommandContext,
		lookPath:    exec.LookPath,
		goos:        stdruntime.GOOS,
		policy:      policy,
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
	profile := buildSeatbeltProfile(s.policy, workDir)

	args := []string{"-p", profile, "bash", "-lc", req.Command}
	cmd := s.execCommand(runCtx, "sandbox-exec", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if strings.TrimSpace(req.Dir) != "" {
		cmd.Dir = req.Dir
	}
	cmd.Env = append(os.Environ(),
		"CI=1",
		"TERM=dumb",
		"GIT_TERMINAL_PROMPT=0",
		"PAGER=cat",
		"NO_COLOR=1",
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lastOutput := atomic.Int64{}
	lastOutput.Store(time.Now().UnixNano())
	cmd.Stdout = &activityWriter{buffer: &stdout, lastOutput: &lastOutput}
	cmd.Stderr = &activityWriter{buffer: &stderr, lastOutput: &lastOutput}

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
		return result, fmt.Errorf("tool: seatbelt sandbox command timed out after %s: %w; stderr=%s", label, waitErr, result.Stderr)
	}
	if errors.Is(waitErr, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, fmt.Errorf("tool: seatbelt sandbox command produced no output for %s and was terminated (likely interactive/long-running); stderr=%s", label, result.Stderr)
	}
	return result, fmt.Errorf("tool: seatbelt sandbox command failed: %w; stderr=%s", waitErr, result.Stderr)
}

func buildSeatbeltProfile(policy SandboxPolicy, workDir string) string {
	lines := []string{
		"(version 1)",
		"(deny default)",
		"(import \"system.sb\")",
		"(allow process*)",
		"(allow signal (target self))",
		"(allow sysctl-read)",
		"(allow file-read*)",
	}
	if policy.NetworkAccess {
		lines = append(lines, "(allow network*)")
	}
	for _, root := range seatbeltWritableRoots(policy, workDir) {
		lines = append(lines, fmt.Sprintf("(allow file-write* (subpath %s))", sbplString(root)))
	}
	for _, sub := range seatbeltReadOnlySubpaths(policy, workDir) {
		lines = append(lines, fmt.Sprintf("(deny file-write* (subpath %s))", sbplString(sub)))
	}
	return strings.Join(lines, "\n")
}

func seatbeltWritableRoots(policy SandboxPolicy, workDir string) []string {
	if policy.Type == SandboxPolicyReadOnly {
		return nil
	}
	roots := make([]string, 0, len(policy.WritableRoots)+5)
	for _, one := range policy.WritableRoots {
		resolved := resolveSeatbeltPath(workDir, one)
		if resolved != "" {
			roots = append(roots, resolved)
		}
	}
	tmp := strings.TrimSpace(os.TempDir())
	if tmp != "" {
		roots = append(roots, filepath.Clean(tmp))
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots,
			filepath.Join(home, "Library", "Caches"),
			filepath.Join(home, ".cache"),
			filepath.Join(home, ".npm"),
		)
	}
	return normalizeStringList(roots)
}

func seatbeltReadOnlySubpaths(policy SandboxPolicy, workDir string) []string {
	values := make([]string, 0, len(policy.ReadOnlySubpaths))
	for _, one := range policy.ReadOnlySubpaths {
		resolved := resolveSeatbeltPath(workDir, one)
		if resolved != "" {
			values = append(values, resolved)
		}
	}
	return normalizeStringList(values)
}

func resolveSeatbeltPath(baseDir, value string) string {
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

func sbplString(v string) string {
	return strconv.Quote(v)
}

func init() {
	if err := RegisterSandboxFactory(seatbeltSandboxFactory{}); err != nil {
		panic(err)
	}
}
