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

// AsyncCommandRunner extends CommandRunner with async execution support.
type AsyncCommandRunner interface {
	CommandRunner

	// StartAsync starts a command asynchronously and returns a session ID.
	StartAsync(ctx context.Context, req CommandRequest) (sessionID string, err error)

	// WriteInput sends input to an async session's stdin.
	WriteInput(sessionID string, input []byte) error

	// ReadOutput reads new output from an async session.
	ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error)

	// GetSessionStatus returns the status of an async session.
	GetSessionStatus(sessionID string) (SessionStatus, error)

	// WaitSession waits for an async session to complete with optional timeout.
	WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (CommandResult, error)

	// TerminateSession forcefully terminates an async session.
	TerminateSession(sessionID string) error

	// ListSessions returns information about all active sessions.
	ListSessions() []SessionInfo
}

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

type hostRunner struct {
	sessionManager  *SessionManager
	smartIdleConfig SmartIdleConfig
	closed          atomic.Bool
}

// HostRunnerConfig configures the host runner.
type HostRunnerConfig struct {
	SessionManagerConfig SessionManagerConfig
	SmartIdleConfig      SmartIdleConfig
}

// DefaultHostRunnerConfig returns a default host runner configuration.
func DefaultHostRunnerConfig() HostRunnerConfig {
	return HostRunnerConfig{
		SessionManagerConfig: DefaultSessionManagerConfig(),
		SmartIdleConfig:      DefaultSmartIdleConfig(),
	}
}

func newHostRunner() CommandRunner {
	return newHostRunnerWithConfig(DefaultHostRunnerConfig())
}

func newHostRunnerWithConfig(cfg HostRunnerConfig) *hostRunner {
	return &hostRunner{
		sessionManager:  NewSessionManager(cfg.SessionManagerConfig),
		smartIdleConfig: cfg.SmartIdleConfig,
	}
}

func (h *hostRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	cmd, err := buildAsyncSessionCommand(runCtx, req.Command, req.TTY)
	if err != nil {
		return CommandResult{}, err
	}
	applyNonInteractiveCommandDefaults(cmd)
	if req.Dir != "" {
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
		return CommandResult{}, fmt.Errorf("tool: command start failed: %w", err)
	}

	// Use smart idle detection if enabled and idle timeout is long enough
	var runErr error
	if h.smartIdleConfig.Enabled && req.IdleTimeout > 0 && req.IdleTimeout >= h.smartIdleConfig.MinIdleDuration {
		runErr = h.waitWithSmartIdleDetection(runCtx, cmd, req.IdleTimeout, &lastOutput, &stdout, &stderr)
	} else {
		runErr = waitWithIdleTimeout(runCtx, cmd, req.IdleTimeout, &lastOutput)
	}

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if runErr == nil {
		return result, nil
	}
	result.ExitCode = resolveExitCode(runErr)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded) {
		label := "context deadline"
		if req.Timeout > 0 {
			label = req.Timeout.String()
		}
		return result, WrapCodedError(
			ErrorCodeHostCommandTimeout,
			runErr,
			"tool: command timed out after %s; %s",
			label,
			commandOutputSummary(result),
		)
	}
	if errors.Is(runErr, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, NewCodedError(
			ErrorCodeHostIdleTimeout,
			"tool: command produced no output for %s and was terminated (likely interactive or long-running); %s",
			label,
			commandOutputSummary(result),
		)
	}
	if errors.Is(runErr, errInteractivePrompt) {
		return result, NewCodedError(
			ErrorCodeHostIdleTimeout,
			"tool: command appears to be waiting for interactive input and was terminated; %s",
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: command failed: %w; %s", runErr, commandOutputSummary(result))
}

var errInteractivePrompt = errors.New("interactive prompt detected")

// waitWithSmartIdleDetection waits for command completion with intelligent idle detection.
func (h *hostRunner) waitWithSmartIdleDetection(
	ctx context.Context,
	cmd *exec.Cmd,
	idleTimeout time.Duration,
	lastOutput *atomic.Int64,
	stdout, stderr *bytes.Buffer,
) error {
	if cmd == nil {
		return errors.New("nil command")
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	detector := NewSmartIdleDetector()
	ticker := time.NewTicker(h.smartIdleConfig.CheckInterval)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case err := <-waitCh:
			return err
		case <-ctx.Done():
			_ = killProcess(cmd)
			<-waitCh
			return ctx.Err()
		case <-ticker.C:
			if lastOutput == nil {
				continue
			}

			last := time.Unix(0, lastOutput.Load())
			idleDuration := time.Since(last)

			// Only check after minimum idle duration
			if idleDuration < h.smartIdleConfig.MinIdleDuration {
				continue
			}

			// Get current output for analysis
			output := append(stdout.Bytes(), stderr.Bytes()...)
			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}

			result := detector.Analyze(output, pid, idleDuration)

			// If high confidence that it's waiting for input, terminate
			if result.IsLikelyWaitingForInput && result.Confidence >= 0.7 {
				_ = killProcess(cmd)
				<-waitCh
				return errInteractivePrompt
			}

			// Check standard idle timeout for non-interactive processes
			if idleDuration > idleTimeout && !result.IsLikelyWaitingForInput {
				_ = killProcess(cmd)
				<-waitCh
				return errIdleTimeout
			}

			// Fallback timeout (absolute maximum) — only applied when the
			// caller did not set an explicit timeout (the context deadline
			// already handles that case).  Without this guard the
			// FallbackTimeout could kill active commands whose caller
			// timeout is longer than FallbackTimeout.
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				if time.Since(startTime) > h.smartIdleConfig.FallbackTimeout {
					_ = killProcess(cmd)
					<-waitCh
					return errIdleTimeout
				}
			}
		}
	}
}

// StartAsync starts a command asynchronously and returns a session ID.
func (h *hostRunner) StartAsync(_ context.Context, req CommandRequest) (string, error) {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return "", err
	}
	session, err := manager.StartSession(AsyncSessionConfig{
		Command:         req.Command,
		Dir:             req.Dir,
		Env:             mergeCommandEnv(req.EnvOverrides),
		OutputBufferCap: 256 * 1024, // 256KB for async sessions
		Timeout:         req.Timeout,
		IdleTimeout:     req.IdleTimeout,
		TTY:             req.TTY,
	})
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

// WriteInput sends input to an async session's stdin.
func (h *hostRunner) WriteInput(sessionID string, input []byte) error {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.WriteInput(sessionID, input)
}

// ReadOutput reads new output from an async session.
func (h *hostRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return manager.ReadOutput(sessionID, stdoutMarker, stderrMarker)
}

// GetSessionStatus returns the status of an async session.
func (h *hostRunner) GetSessionStatus(sessionID string) (SessionStatus, error) {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return SessionStatus{}, err
	}
	return manager.GetSessionStatus(sessionID)
}

// WaitSession waits for an async session to complete with optional timeout.
func (h *hostRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (CommandResult, error) {
	manager, err := h.asyncSessionManager()
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

// TerminateSession forcefully terminates an async session.
func (h *hostRunner) TerminateSession(sessionID string) error {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return err
	}
	return manager.TerminateSession(sessionID)
}

// ListSessions returns information about all active sessions.
func (h *hostRunner) ListSessions() []SessionInfo {
	manager, err := h.asyncSessionManager()
	if err != nil {
		return nil
	}
	return manager.ListSessions()
}

// Close closes the host runner and all its sessions.
func (h *hostRunner) Close() error {
	h.closed.Store(true)
	if h.sessionManager != nil {
		return h.sessionManager.Close()
	}
	return nil
}

func (h *hostRunner) asyncSessionManager() (*SessionManager, error) {
	if h == nil || h.closed.Load() || h.sessionManager == nil {
		return nil, fmt.Errorf("execenv: host runner is closed")
	}
	return h.sessionManager, nil
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
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	*target = exitErr
	return true
}
