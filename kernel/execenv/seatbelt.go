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
			"tool: seatbelt sandbox command produced no output for %s and was terminated (likely interactive/long-running; try larger idle_timeout_ms); %s",
			label,
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: seatbelt sandbox command failed: %w; %s", waitErr, commandOutputSummary(result))
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
	b.WriteString("(allow file-read*)\n")

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
		resolved := resolveSeatbeltPath(workDir, one)
		if resolved != "" {
			roots = append(roots, seatbeltPathVariants(resolved)...)
		}
	}

	// Temp directories — always writable.
	tmp := strings.TrimSpace(os.TempDir())
	if tmp != "" {
		roots = append(roots, seatbeltPathVariants(tmp)...)
	}
	// On macOS /tmp is a symlink to /private/tmp; os.TempDir() returns
	// $TMPDIR (e.g. /var/folders/...) which does NOT cover /tmp.
	roots = append(roots, seatbeltPathVariants("/tmp")...)
	// /var/tmp is another common temp location used by system tools.
	roots = append(roots, seatbeltPathVariants("/var/tmp")...)

	// Cache directories — low-risk, regenerable data that many dev tools
	// (pip, npm, go build, homebrew, playwright) require for normal
	// operation.  We intentionally do NOT open ~/Library/Application Support
	// or ~/.local because those contain persistent app state.
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, seatbeltPathVariants(filepath.Join(home, "Library", "Caches"))...)
		roots = append(roots, seatbeltPathVariants(filepath.Join(home, ".cache"))...)
	}

	// macOS per-user cache dir (/var/folders/xx/xxx/C/) — needed for TLS
	// certificate caching when network is enabled.  Matches codex's
	// DARWIN_USER_CACHE_DIR behaviour.
	if policy.NetworkAccess {
		if cacheDir := darwinUserCacheDir(); cacheDir != "" {
			roots = append(roots, seatbeltPathVariants(cacheDir)...)
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
		resolved := resolveSeatbeltPath(workDir, one)
		if resolved != "" {
			values = append(values, seatbeltPathVariants(resolved)...)
		}
	}
	return normalizeStringList(values)
}

func seatbeltPathVariants(path string) []string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return nil
	}
	variants := []string{cleaned}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && strings.TrimSpace(resolved) != "" {
		variants = append(variants, filepath.Clean(resolved))
	}
	return normalizeStringList(variants)
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
