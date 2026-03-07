package execenv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// SessionState represents the current state of an async session.
type SessionState string

const (
	SessionStateRunning    SessionState = "running"
	SessionStateCompleted  SessionState = "completed"
	SessionStateTerminated SessionState = "terminated"
	SessionStateError      SessionState = "error"
)

// AsyncSession represents an asynchronous shell session that can receive input
// and stream output independently of the calling goroutine.
type AsyncSession struct {
	ID        string
	Command   string
	Dir       string
	StartTime time.Time

	cmd          *exec.Cmd
	stdinWriter  io.WriteCloser
	stdoutBuffer *RingBuffer
	stderrBuffer *RingBuffer
	outputChan   chan AsyncOutputChunk
	doneChan     chan struct{}  // signals readers to stop sending to outputChan
	readersWg    sync.WaitGroup // tracks reader goroutines for clean shutdown
	exitChan     chan int
	exitCode     atomic.Int32
	exited       atomic.Bool
	state        atomic.Value // SessionState
	lastActivity atomic.Int64
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
	closeOnce    sync.Once
	exitErr      error
	timeout      time.Duration // maximum session lifetime
	idleTimeout  time.Duration // idle timeout
}

// AsyncOutputChunk represents a chunk of output from stdout or stderr in async sessions.
type AsyncOutputChunk struct {
	Stream    string // "stdout" or "stderr"
	Data      []byte
	Timestamp time.Time
}

// SessionInfo provides summary information about a session.
type SessionInfo struct {
	ID           string
	Command      string
	State        SessionState
	StartTime    time.Time
	LastActivity time.Time
	ExitCode     int
	HasOutput    bool
}

// SessionStatus provides detailed status of a session.
type SessionStatus struct {
	ID           string
	Command      string
	Dir          string
	State        SessionState
	StartTime    time.Time
	LastActivity time.Time
	ExitCode     int
	StdoutBytes  int64
	StderrBytes  int64
	Error        string
}

// AsyncSessionConfig configures an async session.
type AsyncSessionConfig struct {
	Command         string
	Dir             string
	Env             []string
	OutputBufferCap int           // Capacity for output ring buffers (default 64KB)
	Timeout         time.Duration // Maximum session lifetime (0 = no limit)
	IdleTimeout     time.Duration // Idle timeout (0 = no idle limit)
}

const (
	defaultOutputBufferCap = 64 * 1024 // 64KB
	outputChannelBuffer    = 256
)

// NewAsyncSession creates a new async session but does not start it.
func NewAsyncSession(cfg AsyncSessionConfig) *AsyncSession {
	if cfg.OutputBufferCap <= 0 {
		cfg.OutputBufferCap = defaultOutputBufferCap
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &AsyncSession{
		ID:           uuid.New().String(),
		Command:      cfg.Command,
		Dir:          cfg.Dir,
		StartTime:    time.Now(),
		stdoutBuffer: NewRingBuffer(cfg.OutputBufferCap),
		stderrBuffer: NewRingBuffer(cfg.OutputBufferCap),
		outputChan:   make(chan AsyncOutputChunk, outputChannelBuffer),
		doneChan:     make(chan struct{}),
		exitChan:     make(chan int, 1),
		ctx:          ctx,
		cancel:       cancel,
		timeout:      cfg.Timeout,
		idleTimeout:  cfg.IdleTimeout,
	}
	session.state.Store(SessionStateRunning)
	session.lastActivity.Store(time.Now().UnixNano())
	session.exitCode.Store(-1)

	return session
}

// Start begins executing the command in the background.
func (s *AsyncSession) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.CommandContext(s.ctx, "bash", "-lc", s.Command)
	setProcessGroup(cmd)
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}

	// Set up environment
	cmd.Env = append(os.Environ(), defaultCommandEnvVars...)

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	s.stdinWriter = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}
	s.cmd = cmd

	// Start output readers
	s.readersWg.Add(2)
	go s.readOutput(stdout, "stdout", s.stdoutBuffer)
	go s.readOutput(stderr, "stderr", s.stderrBuffer)

	// Start wait goroutine
	go s.waitForExit()

	// Start timeout enforcement if configured
	if s.timeout > 0 || s.idleTimeout > 0 {
		go s.enforceTimeouts()
	}

	return nil
}

func (s *AsyncSession) readOutput(reader io.Reader, stream string, buffer *RingBuffer) {
	defer s.readersWg.Done()
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// Write to ring buffer (always safe)
			buffer.Write(data)

			// Update activity timestamp
			s.lastActivity.Store(time.Now().UnixNano())

			// Send to output channel only if not shutting down
			select {
			case <-s.doneChan:
				// Session is closing, stop sending
			case s.outputChan <- AsyncOutputChunk{
				Stream:    stream,
				Data:      data,
				Timestamp: time.Now(),
			}:
			default:
				// Channel full, skip
			}
		}
		if err != nil {
			if err != io.EOF {
				// Log error but don't fail
			}
			return
		}
	}
}

func (s *AsyncSession) waitForExit() {
	err := s.cmd.Wait()

	// Ensure stdout/stderr reader goroutines have drained the pipes into the
	// ring buffers before marking the session complete. Without this, callers
	// can observe HasExited/Wait returning before the final output is readable.
	s.readersWg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.exited.Store(true)
	s.exitErr = err

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	s.exitCode.Store(int32(exitCode))

	if s.state.Load() == SessionStateRunning {
		s.state.Store(SessionStateCompleted)
	}

	// Close stdin writer
	if s.stdinWriter != nil {
		s.stdinWriter.Close()
	}

	// Notify exit channel
	select {
	case s.exitChan <- exitCode:
	default:
	}
}

// enforceTimeouts monitors the session for absolute and idle timeouts and
// terminates the process when either limit is reached.
func (s *AsyncSession) enforceTimeouts() {
	checkInterval := 1 * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.doneChan:
			return
		case <-ticker.C:
			if s.exited.Load() {
				return
			}

			// Absolute timeout
			if s.timeout > 0 && time.Since(s.StartTime) > s.timeout {
				s.state.Store(SessionStateTerminated)
				s.Terminate()
				return
			}

			// Idle timeout
			if s.idleTimeout > 0 {
				last := time.Unix(0, s.lastActivity.Load())
				if time.Since(last) > s.idleTimeout {
					s.state.Store(SessionStateTerminated)
					s.Terminate()
					return
				}
			}
		}
	}
}

// WriteInput sends input to the session's stdin.
func (s *AsyncSession) WriteInput(input []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.exited.Load() {
		return errors.New("session has already exited")
	}

	if s.stdinWriter == nil {
		return errors.New("stdin not available")
	}

	_, err := s.stdinWriter.Write(input)
	if err != nil {
		return fmt.Errorf("failed to write to stdin: %w", err)
	}

	return nil
}

// ReadOutput returns accumulated output since the given markers.
// Returns stdout data, stderr data, and new markers.
func (s *AsyncSession) ReadOutput(stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64) {
	stdout, newStdoutMarker = s.stdoutBuffer.ReadNewSince(stdoutMarker)
	stderr, newStderrMarker = s.stderrBuffer.ReadNewSince(stderrMarker)
	return
}

// ReadAllOutput returns all buffered output.
func (s *AsyncSession) ReadAllOutput() (stdout, stderr string) {
	return string(s.stdoutBuffer.ReadAll()), string(s.stderrBuffer.ReadAll())
}

// OutputChannel returns a channel that receives output chunks in real-time.
func (s *AsyncSession) OutputChannel() <-chan AsyncOutputChunk {
	return s.outputChan
}

// ExitChannel returns a channel that receives the exit code when the process exits.
func (s *AsyncSession) ExitChannel() <-chan int {
	return s.exitChan
}

// Status returns the current status of the session.
func (s *AsyncSession) Status() SessionStatus {
	stdout, stderr := "", ""
	if s.stdoutBuffer != nil {
		stdout = string(s.stdoutBuffer.ReadAll())
	}
	if s.stderrBuffer != nil {
		stderr = string(s.stderrBuffer.ReadAll())
	}

	status := SessionStatus{
		ID:           s.ID,
		Command:      s.Command,
		Dir:          s.Dir,
		State:        s.state.Load().(SessionState),
		StartTime:    s.StartTime,
		LastActivity: time.Unix(0, s.lastActivity.Load()),
		ExitCode:     int(s.exitCode.Load()),
		StdoutBytes:  s.stdoutBuffer.TotalWritten(),
		StderrBytes:  s.stderrBuffer.TotalWritten(),
	}

	if s.exitErr != nil && s.state.Load() == SessionStateError {
		status.Error = s.exitErr.Error()
	}

	// Check if there's unread output
	_ = stdout
	_ = stderr

	return status
}

// Info returns summary information about the session.
func (s *AsyncSession) Info() SessionInfo {
	return SessionInfo{
		ID:           s.ID,
		Command:      s.Command,
		State:        s.state.Load().(SessionState),
		StartTime:    s.StartTime,
		LastActivity: time.Unix(0, s.lastActivity.Load()),
		ExitCode:     int(s.exitCode.Load()),
		HasOutput:    s.stdoutBuffer.Len() > 0 || s.stderrBuffer.Len() > 0,
	}
}

// HasExited returns true if the process has exited.
func (s *AsyncSession) HasExited() bool {
	return s.exited.Load()
}

// ExitCode returns the exit code if the process has exited, or -1.
func (s *AsyncSession) ExitCode() int {
	return int(s.exitCode.Load())
}

// LastActivityTime returns the timestamp of the last I/O activity.
func (s *AsyncSession) LastActivityTime() time.Time {
	return time.Unix(0, s.lastActivity.Load())
}

// Wait blocks until the session exits or the context is cancelled.
// Returns the exit code and any error.
func (s *AsyncSession) Wait(ctx context.Context) (int, error) {
	select {
	case code := <-s.exitChan:
		// Put it back for other waiters
		select {
		case s.exitChan <- code:
		default:
		}
		return code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

// WaitWithTimeout waits for the session to exit with a timeout.
func (s *AsyncSession) WaitWithTimeout(timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Wait(ctx)
}

// Terminate forcefully terminates the session.
func (s *AsyncSession) Terminate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.exited.Load() {
		return nil
	}

	s.state.Store(SessionStateTerminated)
	s.cancel()

	if s.cmd != nil && s.cmd.Process != nil {
		// Kill the process group
		if err := killProcessGroup(s.cmd.Process.Pid); err != nil {
			// Fall back to direct kill
			return s.cmd.Process.Kill()
		}
	}

	return nil
}

// Close releases all resources associated with the session.
func (s *AsyncSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		// Signal reader goroutines to stop sending to outputChan.
		close(s.doneChan)

		s.Terminate()

		// Wait for reader goroutines to finish so no goroutine can send
		// to outputChan after this point.  The goroutines will exit once
		// the pipes are closed (by process termination) and they see
		// doneChan is closed.
		s.readersWg.Wait()

		// Do NOT close outputChan — it is never consumed externally and
		// closing it was the source of a send-on-closed-channel panic.
		// The channel and its buffer will be reclaimed by GC.
	})
	return closeErr
}

// GetResult returns a CommandResult if the session has exited.
func (s *AsyncSession) GetResult() (CommandResult, error) {
	if !s.exited.Load() {
		return CommandResult{}, errors.New("session has not exited yet")
	}

	stdout, stderr := s.ReadAllOutput()
	return CommandResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: int(s.exitCode.Load()),
	}, nil
}
