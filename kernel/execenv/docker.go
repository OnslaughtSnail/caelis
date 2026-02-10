package execenv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	dockerSandboxType  = "docker"
	dockerWorkspaceDir = "/workspace"
	dockerDefaultImage = "alpine:3.20"
	dockerImageEnvKey  = "CAELIS_SANDBOX_DOCKER_IMAGE"
)

type dockerSandboxFactory struct{}

func (f dockerSandboxFactory) Type() string {
	return dockerSandboxType
}

func (f dockerSandboxFactory) Build(cfg Config) (Runtime, error) {
	filesystem := cfg.FileSystem
	if filesystem == nil {
		filesystem = newHostFileSystem()
	}
	runner := cfg.Runner
	if runner == nil {
		runner = newDockerRunner()
	}
	return &runtimeImpl{
		mode:        ModeSandbox,
		sandboxType: dockerSandboxType,
		fs:          filesystem,
		runner:      runner,
		bashPolicy:  deriveBashPolicy(ModeSandbox, cfg.BashPolicy),
	}, nil
}

type dockerRunner struct {
	execCommand func(context.Context, string, ...string) *exec.Cmd
	image       string
}

func newDockerRunner() CommandRunner {
	image := strings.TrimSpace(os.Getenv(dockerImageEnvKey))
	if image == "" {
		image = dockerDefaultImage
	}
	return &dockerRunner{
		execCommand: exec.CommandContext,
		image:       image,
	}
}

func (d *dockerRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	hostWorkDir, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve docker workdir failed: %w", err)
	}

	args := []string{
		"run", "--rm", "--network", "none",
		"-v", hostWorkDir + ":" + dockerWorkspaceDir,
		"-w", dockerWorkspaceDir,
		d.image,
		"sh", "-lc", req.Command,
	}
	cmd := d.execCommand(runCtx, "docker", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return result, nil
	}
	result.ExitCode = resolveExitCode(err)
	return result, fmt.Errorf("tool: docker sandbox command failed: %w; stderr=%s", err, result.Stderr)
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
