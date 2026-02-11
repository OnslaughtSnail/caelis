package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

type hostFileSystem struct{}

func newHostFileSystem() FileSystem {
	return &hostFileSystem{}
}

func (h *hostFileSystem) Getwd() (string, error) {
	return os.Getwd()
}

func (h *hostFileSystem) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}

func (h *hostFileSystem) Open(path string) (*os.File, error) {
	return os.Open(path)
}

func (h *hostFileSystem) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func (h *hostFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (h *hostFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (h *hostFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (h *hostFileSystem) Glob(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}

func (h *hostFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

type hostRunner struct{}

func newHostRunner() CommandRunner {
	return &hostRunner{}
}

func (h *hostRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", req.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if req.Dir != "" {
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
		return CommandResult{}, fmt.Errorf("tool: command start failed: %w", err)
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
		return result, fmt.Errorf("tool: command timed out after %s: %w; stderr=%s", label, err, result.Stderr)
	}
	if errors.Is(err, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, fmt.Errorf("tool: command produced no output for %s and was terminated (likely interactive/long-running); stderr=%s", label, result.Stderr)
	}
	return result, fmt.Errorf("tool: command failed: %w; stderr=%s", err, result.Stderr)
}

func resolveExitCode(err error) int {
	var exitErr *exec.ExitError
	if !asExitError(err, &exitErr) {
		return -1
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return -1
	}
	return status.ExitStatus()
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil || target == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}
