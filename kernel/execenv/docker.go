package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	dockerSandboxType  = "docker"
	dockerWorkspaceDir = "/workspace"
	dockerDefaultImage = "alpine:3.20"
	dockerImageEnvKey  = "CAELIS_SANDBOX_DOCKER_IMAGE"
	dockerDefaultNet   = "bridge"
	dockerNetEnvKey    = "CAELIS_SANDBOX_DOCKER_NETWORK"
	dockerSessionName  = "caelis-sandbox"
	dockerSetupTimeout = 20 * time.Second
)

var dockerSessionCounter atomic.Int64

type dockerSandboxFactory struct{}

func (f dockerSandboxFactory) Type() string {
	return dockerSandboxType
}

func (f dockerSandboxFactory) Build(cfg Config) (CommandRunner, error) {
	return newDockerRunnerWithConfig(cfg), nil
}

type dockerRunner struct {
	execCommand func(context.Context, string, ...string) *exec.Cmd
	image       string
	network     string
	readOnly    bool
	setupTTL    time.Duration
	mu          sync.Mutex
	container   string
	rootDir     string
	started     bool
	closed      bool
}

func newDockerRunner() CommandRunner {
	return newDockerRunnerWithConfig(Config{})
}

func newDockerRunnerWithConfig(cfg Config) CommandRunner {
	image := strings.TrimSpace(os.Getenv(dockerImageEnvKey))
	if image == "" {
		image = dockerDefaultImage
	}
	network := strings.TrimSpace(strings.ToLower(os.Getenv(dockerNetEnvKey)))
	if network == "" {
		network = dockerDefaultNet
	}
	readOnly := false
	if cfg.SandboxPolicy.Type == SandboxPolicyReadOnly {
		readOnly = true
	}
	// Runtime.New derives policy before building backends. Honor explicit
	// policy network intent without breaking plain newDockerRunner() defaults.
	if cfg.SandboxPolicy.Type != "" && !cfg.SandboxPolicy.NetworkAccess {
		network = "none"
	}
	return &dockerRunner{
		execCommand: exec.CommandContext,
		image:       image,
		network:     network,
		readOnly:    readOnly,
		setupTTL:    dockerSetupTimeout,
		container:   nextDockerContainerName(),
	}
}

func nextDockerContainerName() string {
	id := dockerSessionCounter.Add(1)
	return fmt.Sprintf("%s-%d-%d", dockerSessionName, os.Getpid(), id)
}

func (d *dockerRunner) Probe(ctx context.Context) error {
	if err := d.runCommand(ctx, "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("docker probe failed: %w", err)
	}
	if err := d.runCommand(ctx, "docker", "image", "inspect", d.image); err != nil {
		pullErr := d.runCommand(ctx, "docker", "pull", d.image)
		if pullErr != nil {
			return fmt.Errorf("docker image %q unavailable: inspect failed: %v; pull failed: %v", d.image, err, pullErr)
		}
	}
	if err := d.runCommand(ctx, "docker", "run", "--rm", "--network", "none", d.image, "sh", "-lc", "echo sandbox-ready"); err != nil {
		return fmt.Errorf("docker image %q is not runnable for shell sandbox: %w", d.image, err)
	}
	return nil
}

func (d *dockerRunner) runCommand(ctx context.Context, name string, args ...string) error {
	cmd := d.execCommand(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w; stderr=%s", err, msg)
	}
	return nil
}

func (d *dockerRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	hostWorkDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve docker workdir failed: %w", err)
	}
	setupCtx := ctx
	cancelSetup := func() {}
	if d.setupTTL > 0 {
		setupCtx, cancelSetup = context.WithTimeout(ctx, d.setupTTL)
	}
	if err := d.ensureSession(setupCtx, hostWorkDir); err != nil {
		cancelSetup()
		return CommandResult{}, fmt.Errorf("tool: docker sandbox session unavailable: %w", err)
	}
	cancelSetup()

	runCtx := ctx
	cancelRun := func() {}
	if req.Timeout > 0 {
		runCtx, cancelRun = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancelRun()
	containerDir, ok := d.containerWorkDir(hostWorkDir)
	if ok {
		args := []string{
			"exec",
			"-w", containerDir,
			"-e", "CI=1",
			"-e", "TERM=dumb",
			"-e", "GIT_TERMINAL_PROMPT=0",
			"-e", "PAGER=cat",
			"-e", "NO_COLOR=1",
			d.containerName(),
			"sh", "-lc", req.Command,
		}
		return d.runSandboxCommand(runCtx, req, args, "exec")
	}

	// Commands outside mounted workspace fallback to one-shot run with per-command mount.
	args := []string{
		"run", "--rm", "--network", d.network,
		"-e", "CI=1",
		"-e", "TERM=dumb",
		"-e", "GIT_TERMINAL_PROMPT=0",
		"-e", "PAGER=cat",
		"-e", "NO_COLOR=1",
		"-v", d.workspaceMountArg(hostWorkDir),
		"-w", dockerWorkspaceDir,
		d.image,
		"sh", "-lc", req.Command,
	}
	return d.runSandboxCommand(runCtx, req, args, "run")
}

func (d *dockerRunner) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	started := d.started
	container := d.container
	d.mu.Unlock()
	if !started || strings.TrimSpace(container) == "" {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := d.runCommand(stopCtx, "docker", "rm", "-f", container)
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return nil
	}
	return fmt.Errorf("docker sandbox cleanup failed for %q: %w", container, err)
}

func (d *dockerRunner) ensureSession(ctx context.Context, workDir string) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("sandbox closed")
	}
	if d.started {
		d.mu.Unlock()
		return nil
	}
	container := strings.TrimSpace(d.container)
	if container == "" {
		container = nextDockerContainerName()
		d.container = container
	}
	rootDir := workDir
	if strings.TrimSpace(rootDir) == "" {
		rootDir = "."
	}
	d.mu.Unlock()

	args := []string{
		"run", "-d", "--rm",
		"--name", container,
		"--network", d.network,
		"-e", "CI=1",
		"-e", "TERM=dumb",
		"-e", "GIT_TERMINAL_PROMPT=0",
		"-e", "PAGER=cat",
		"-e", "NO_COLOR=1",
		"-v", d.workspaceMountArg(rootDir),
		"-w", dockerWorkspaceDir,
		d.image,
		"sh", "-lc", "trap 'exit 0' TERM INT; while :; do sleep 3600; done",
	}
	if err := d.runCommand(ctx, "docker", args...); err != nil {
		return err
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		_ = d.runCommand(context.Background(), "docker", "rm", "-f", container)
		return errors.New("sandbox closed")
	}
	d.rootDir = rootDir
	d.started = true
	d.mu.Unlock()
	return nil
}

func (d *dockerRunner) workspaceMountArg(hostDir string) string {
	mount := hostDir + ":" + dockerWorkspaceDir
	if d.readOnly {
		return mount + ":ro"
	}
	return mount
}

func (d *dockerRunner) containerWorkDir(hostDir string) (string, bool) {
	d.mu.Lock()
	rootDir := d.rootDir
	d.mu.Unlock()
	if strings.TrimSpace(rootDir) == "" {
		return "", false
	}
	rel, ok := relWithinRoot(rootDir, hostDir)
	if !ok {
		return "", false
	}
	if rel == "." {
		return dockerWorkspaceDir, true
	}
	return path.Join(dockerWorkspaceDir, filepath.ToSlash(rel)), true
}

func relWithinRoot(root string, target string) (string, bool) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	if rel == ".." {
		return "", false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func (d *dockerRunner) containerName() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.container
}

func (d *dockerRunner) runSandboxCommand(runCtx context.Context, req CommandRequest, args []string, mode string) (CommandResult, error) {
	cmd := d.execCommand(runCtx, "docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lastOutput := atomic.Int64{}
	lastOutput.Store(time.Now().UnixNano())
	cmd.Stdout = &activityWriter{buffer: &stdout, lastOutput: &lastOutput}
	cmd.Stderr = &activityWriter{buffer: &stderr, lastOutput: &lastOutput}
	if err := cmd.Start(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			label := "context deadline"
			if req.Timeout > 0 {
				label = req.Timeout.String()
			}
			return CommandResult{}, fmt.Errorf("tool: docker sandbox command timed out after %s (mode=%s network=%s): %w; stderr=", label, mode, d.network, err)
		}
		return CommandResult{}, fmt.Errorf("tool: docker sandbox command start failed (%s): %w", mode, err)
	}
	err := waitWithIdleTimeout(runCtx, cmd, req.IdleTimeout, &lastOutput)

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return result, nil
	}
	result.ExitCode = resolveExitCode(err)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		label := "context deadline"
		if req.Timeout > 0 {
			label = req.Timeout.String()
		}
		return result, fmt.Errorf("tool: docker sandbox command timed out after %s (mode=%s network=%s): %w; stderr=%s", label, mode, d.network, err, result.Stderr)
	}
	if errors.Is(err, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, fmt.Errorf("tool: docker sandbox command produced no output for %s and was terminated (mode=%s network=%s, likely interactive/long-running); stderr=%s", label, mode, d.network, result.Stderr)
	}
	return result, fmt.Errorf("tool: docker sandbox command failed (mode=%s network=%s): %w; stderr=%s", mode, d.network, err, result.Stderr)
}

func resolveHostWorkDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return os.Getwd()
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func init() {
	if err := RegisterSandboxFactory(dockerSandboxFactory{}); err != nil {
		panic(err)
	}
}
