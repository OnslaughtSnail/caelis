package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

const (
	bashTaskPollInterval     = 120 * time.Millisecond
	bashTaskLiveStreamDelay  = time.Second
	bashTaskOriginalToolName = "BASH"
)

type bashTaskController struct {
	session toolexec.Session
	command string
	workdir string
	tty     bool
	route   string
	backend string
	store   task.Store
}

func (c *bashTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.session == nil {
		record.WithLock(func(one *task.Record) {
			one.State = task.StateInterrupted
			one.Running = false
			one.UpdatedAt = time.Now()
			if one.Result == nil {
				one.Result = map[string]any{}
			}
			one.Result["state"] = string(one.State)
			one.Result["interrupted"] = true
		})
		if c != nil {
			_ = persistControllerRecord(ctx, c.store, record)
		}
		return record.Snapshot(task.Output{}), nil
	}
	deadline := time.Time{}
	if yield > 0 {
		deadline = time.Now().Add(yield)
	}
	var output task.Output
	live := newBashTaskLiveStream(ctx, record, yield)
	for {
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		default:
		}
		var stdoutMarker, stderrMarker int64
		var previousPreview string
		record.WithLock(func(one *task.Record) {
			stdoutMarker = one.StdoutCursor
			stderrMarker = one.StderrCursor
			previousPreview = strings.TrimSpace(fmt.Sprint(one.Result["latest_output"]))
		})

		stdout, stderr, nextStdout, nextStderr, err := c.session.ReadOutput(stdoutMarker, stderrMarker)
		if err != nil {
			if errors.Is(err, toolexec.ErrSessionNotFound) {
				record.WithLock(func(one *task.Record) {
					one.State = task.StateInterrupted
					one.Running = false
					one.UpdatedAt = time.Now()
					if one.Result == nil {
						one.Result = map[string]any{}
					}
					one.Result["state"] = string(one.State)
					one.Result["interrupted"] = true
				})
				_ = persistControllerRecord(ctx, c.store, record)
				return record.Snapshot(task.Output{}), nil
			}
			return task.Snapshot{}, err
		}
		status, err := c.session.Status()
		if err != nil {
			return task.Snapshot{}, err
		}
		now := time.Now()
		var (
			snapshot     task.Snapshot
			finalOutput  task.Output
			latestOutput = task.MergeLatestOutput(previousPreview, bashOutputPreview(stdout, stderr))
			outputMeta   = bashTaskOutputMeta(status, c.tty)
		)
		if status.State != toolexec.SessionStateRunning && !c.tty {
			finalOutput = readRetainedOutput(c.session)
			latestOutput = task.MergeLatestOutput(previousPreview, bashOutputPreview([]byte(finalOutput.Stdout), []byte(finalOutput.Stderr)))
		}
		record.WithLock(func(one *task.Record) {
			prevState := one.State
			one.StdoutCursor = nextStdout
			one.StderrCursor = nextStderr
			one.State = bashTaskState(status, latestOutput)
			one.Running = status.State == toolexec.SessionStateRunning
			one.UpdatedAt = now
			if len(stdout) > 0 || len(stderr) > 0 || prevState != one.State {
				one.HeartbeatAt = now
			}
			one.Result = map[string]any{
				"command":              c.command,
				"workdir":              c.workdir,
				"tty":                  c.tty,
				"route":                c.route,
				"backend":              c.backend,
				"state":                string(one.State),
				"exit_code":            status.ExitCode,
				"session_id":           c.session.Ref().SessionID,
				"output_meta":          outputMeta,
				"stdout_bytes":         status.StdoutBytes,
				"stderr_bytes":         status.StderrBytes,
				"progress_seq":         status.StdoutBytes + status.StderrBytes,
				"progress_age_seconds": 0,
			}
			if latestOutput != "" {
				one.Result["latest_output"] = latestOutput
			}
			if text := strings.TrimSpace(status.Error); text != "" {
				one.Result["error"] = text
			}
			if one.Running {
				output.Stdout += string(stdout)
				output.Stderr += string(stderr)
				if !c.tty {
					snapshot = one.LockedSnapshot(output)
				} else {
					snapshot = one.LockedSnapshot(task.Output{})
				}
				return
			}
			if c.tty {
				snapshot = one.LockedSnapshot(task.Output{})
				return
			}
			snapshot = one.LockedSnapshot(finalOutput)
		})
		_ = persistControllerRecord(ctx, c.store, record)
		live.emit(ctx, snapshot)

		if !snapshot.Running {
			return snapshot, nil
		}
		if deadline.IsZero() || time.Now().After(deadline) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		case <-time.After(bashTaskPollInterval):
		}
	}
}

type bashTaskLiveStream struct {
	enabled      bool
	taskID       string
	callID       string
	startedAt    time.Time
	started      bool
	lastState    string
	stdoutOffset int
	stderrOffset int
}

func newBashTaskLiveStream(ctx context.Context, record *task.Record, yield time.Duration) bashTaskLiveStream {
	if record == nil || strings.TrimSpace(record.ID) == "" || yield <= bashTaskLiveStreamDelay {
		return bashTaskLiveStream{}
	}
	info, ok := toolexec.ToolCallInfoFromContext(ctx)
	if !ok || !strings.EqualFold(strings.TrimSpace(info.Name), bashTaskOriginalToolName) {
		return bashTaskLiveStream{}
	}
	callID := strings.TrimSpace(info.ID)
	if callID == "" {
		return bashTaskLiveStream{}
	}
	return bashTaskLiveStream{
		enabled:   true,
		taskID:    strings.TrimSpace(record.ID),
		callID:    callID,
		startedAt: time.Now(),
	}
}

func (s *bashTaskLiveStream) emit(ctx context.Context, snapshot task.Snapshot) {
	if s == nil || !s.enabled {
		return
	}
	if !s.started {
		if snapshot.Running && time.Since(s.startedAt) < bashTaskLiveStreamDelay {
			return
		}
		s.started = true
		s.lastState = strings.TrimSpace(string(snapshot.State))
		taskstream.Emit(ctx, taskstream.Event{
			Label:  bashTaskOriginalToolName,
			TaskID: s.taskID,
			CallID: s.callID,
			State:  s.lastState,
		})
	}
	if text := snapshot.Output.Stdout; len(text) > s.stdoutOffset {
		taskstream.Emit(ctx, taskstream.Event{
			Label:  bashTaskOriginalToolName,
			TaskID: s.taskID,
			CallID: s.callID,
			Stream: "stdout",
			Chunk:  text[s.stdoutOffset:],
		})
		s.stdoutOffset = len(text)
	}
	if text := snapshot.Output.Stderr; len(text) > s.stderrOffset {
		taskstream.Emit(ctx, taskstream.Event{
			Label:  bashTaskOriginalToolName,
			TaskID: s.taskID,
			CallID: s.callID,
			Stream: "stderr",
			Chunk:  text[s.stderrOffset:],
		})
		s.stderrOffset = len(text)
	}
	state := strings.TrimSpace(string(snapshot.State))
	if state != "" && state != s.lastState {
		taskstream.Emit(ctx, taskstream.Event{
			Label:  bashTaskOriginalToolName,
			TaskID: s.taskID,
			CallID: s.callID,
			State:  state,
		})
		s.lastState = state
	}
	if !snapshot.Running {
		taskstream.Emit(ctx, taskstream.Event{
			Label:  bashTaskOriginalToolName,
			TaskID: s.taskID,
			CallID: s.callID,
			State:  state,
			Final:  true,
		})
	}
}

func (c *bashTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.session == nil {
		return task.Snapshot{}, fmt.Errorf("task: bash controller is unavailable")
	}
	if err := c.session.WriteInput([]byte(input)); err != nil {
		return task.Snapshot{}, err
	}
	return c.Wait(ctx, record, yield)
}

func (c *bashTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c == nil || c.session == nil {
		return task.Snapshot{}, fmt.Errorf("task: bash controller is unavailable")
	}
	if err := c.session.Terminate(); err != nil {
		return task.Snapshot{}, err
	}
	status, _ := c.session.Status()
	preview, _ := c.previewOutput()
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"command":     c.command,
			"workdir":     c.workdir,
			"tty":         c.tty,
			"route":       c.route,
			"backend":     c.backend,
			"state":       string(one.State),
			"session_id":  c.session.Ref().SessionID,
			"output_meta": bashTaskOutputMeta(status, c.tty),
		}
		if preview != "" {
			one.Result["latest_output"] = preview
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *bashTaskController) previewOutput() (string, error) {
	if c == nil || c.session == nil {
		return "", nil
	}
	stdout, stderr, _, _, err := c.session.ReadOutput(0, 0)
	if err != nil {
		if errors.Is(err, toolexec.ErrSessionNotFound) {
			return "", nil
		}
		return "", err
	}
	return bashOutputPreview(stdout, stderr), nil
}

func bashOutputPreview(stdout []byte, stderr []byte) string {
	lines := make([]string, 0, 8)
	appendStreamPreview := func(prefix string, raw []byte) {
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, prefix+line)
		}
	}
	appendStreamPreview("", stdout)
	appendStreamPreview("! ", stderr)
	if len(lines) == 0 {
		return ""
	}
	return task.FormatLatestOutput(strings.Join(lines, "\n"))
}

func bashTaskOutputMeta(status toolexec.SessionStatus, tty bool) map[string]any {
	stdoutCapReached := status.StdoutDroppedBytes > 0
	stderrCapReached := status.StderrDroppedBytes > 0
	return map[string]any{
		"streamed":               tty,
		"tty":                    tty,
		"capture_cap_bytes":      status.CaptureCapBytes,
		"stdout_captured_bytes":  status.StdoutBytes,
		"stderr_captured_bytes":  status.StderrBytes,
		"stdout_retained_bytes":  status.StdoutRetainedBytes,
		"stderr_retained_bytes":  status.StderrRetainedBytes,
		"stdout_cap_reached":     stdoutCapReached,
		"stderr_cap_reached":     stderrCapReached,
		"stdout_dropped_bytes":   status.StdoutDroppedBytes,
		"stderr_dropped_bytes":   status.StderrDroppedBytes,
		"stdout_earliest_marker": status.StdoutEarliestMarker,
		"stderr_earliest_marker": status.StderrEarliestMarker,
		"capture_truncated":      stdoutCapReached || stderrCapReached,
		"model_truncated":        false,
	}
}

func readRetainedOutput(session toolexec.Session) task.Output {
	if session == nil || strings.TrimSpace(session.Ref().SessionID) == "" {
		return task.Output{}
	}
	stdout, stderr, _, _, err := session.ReadOutput(0, 0)
	if err != nil {
		return task.Output{}
	}
	return task.Output{Stdout: string(stdout), Stderr: string(stderr)}
}

func openBashSession(execRuntime toolexec.Runtime, backendName string, sessionID string) (toolexec.Session, error) {
	if execRuntime == nil {
		return nil, fmt.Errorf("task: exec runtime is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("task: async bash session reference is missing")
	}
	backendName = strings.TrimSpace(backendName)
	if backendName == "" {
		return nil, fmt.Errorf("task: async bash backend reference is missing")
	}
	return execRuntime.OpenSession(toolexec.CommandSessionRef{
		Backend:   backendName,
		SessionID: sessionID,
	})
}

func bashTaskState(status toolexec.SessionStatus, latestOutput string) task.State {
	switch status.State {
	case toolexec.SessionStateCompleted:
		return task.StateCompleted
	case toolexec.SessionStateTerminated:
		return task.StateTerminated
	case toolexec.SessionStateError:
		return task.StateFailed
	default:
		if bashTaskLikelyWaitingInput(latestOutput) {
			return task.StateWaitingInput
		}
		return task.StateRunning
	}
}

func bashTaskLikelyWaitingInput(preview string) bool {
	preview = strings.TrimSpace(strings.ToLower(preview))
	if preview == "" {
		return false
	}
	lines := strings.Split(preview, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return false
	}
	for _, marker := range []string{
		"waiting for",
		"press any key",
		"press enter",
		"hit enter",
		"type 'yes'",
		"enter password",
		"authentication required",
		"login:",
		"username:",
		"passphrase",
		"(y/n)",
		"[y/n]",
		"continue?",
		"proceed?",
		"confirm",
	} {
		if strings.Contains(last, marker) {
			return true
		}
	}
	if len(last) <= 120 && (strings.HasSuffix(last, ":") || strings.HasSuffix(last, "?")) {
		return true
	}
	return false
}
