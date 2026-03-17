package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type bashTaskController struct {
	runner    toolexec.AsyncCommandRunner
	sessionID string
	command   string
	workdir   string
	tty       bool
	route     string
	store     task.Store
}

func (c *bashTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
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
	for {
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		default:
		}
		var stdoutMarker, stderrMarker int64
		record.WithLock(func(one *task.Record) {
			stdoutMarker = one.StdoutCursor
			stderrMarker = one.StderrCursor
		})

		stdout, stderr, nextStdout, nextStderr, err := c.runner.ReadOutput(c.sessionID, stdoutMarker, stderrMarker)
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
		status, err := c.runner.GetSessionStatus(c.sessionID)
		if err != nil {
			return task.Snapshot{}, err
		}
		var snapshot task.Snapshot
		latestOutput := bashOutputPreview(stdout, stderr)
		record.WithLock(func(one *task.Record) {
			one.StdoutCursor = nextStdout
			one.StderrCursor = nextStderr
			one.State = bashTaskState(status.State)
			one.Running = status.State == toolexec.SessionStateRunning
			one.UpdatedAt = time.Now()
			one.Result = map[string]any{
				"command":    c.command,
				"workdir":    c.workdir,
				"tty":        c.tty,
				"route":      c.route,
				"state":      string(one.State),
				"exit_code":  status.ExitCode,
				"session_id": c.sessionID,
			}
			if latestOutput != "" {
				one.Result["latest_output"] = latestOutput
			}
			output.Stdout += string(stdout)
			output.Stderr += string(stderr)
			snapshot = one.LockedSnapshot(output)
		})
		_ = persistControllerRecord(ctx, c.store, record)

		if !snapshot.Running {
			return snapshot, nil
		}
		if deadline.IsZero() || time.Now().After(deadline) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
	}
}

func (c *bashTaskController) Status(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
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
	status, err := c.runner.GetSessionStatus(c.sessionID)
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
	preview, err := c.previewOutput()
	if err != nil {
		return task.Snapshot{}, err
	}
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = bashTaskState(status.State)
		one.Running = status.State == toolexec.SessionStateRunning
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"command":    c.command,
			"workdir":    c.workdir,
			"tty":        c.tty,
			"route":      c.route,
			"state":      string(one.State),
			"exit_code":  status.ExitCode,
			"session_id": c.sessionID,
		}
		if preview != "" {
			one.Result["latest_output"] = preview
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *bashTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: bash controller is unavailable")
	}
	if err := c.runner.WriteInput(c.sessionID, []byte(input)); err != nil {
		return task.Snapshot{}, err
	}
	return c.Wait(ctx, record, yield)
}

func (c *bashTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c == nil || c.runner == nil {
		return task.Snapshot{}, fmt.Errorf("task: bash controller is unavailable")
	}
	if err := c.runner.TerminateSession(c.sessionID); err != nil {
		return task.Snapshot{}, err
	}
	preview, _ := c.previewOutput()
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"command":    c.command,
			"workdir":    c.workdir,
			"tty":        c.tty,
			"route":      c.route,
			"state":      string(one.State),
			"session_id": c.sessionID,
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
	if c == nil || c.runner == nil {
		return "", nil
	}
	stdout, stderr, _, _, err := c.runner.ReadOutput(c.sessionID, 0, 0)
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
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}

func asyncBashRunnerForRoute(execRuntime toolexec.Runtime, route string) (toolexec.AsyncCommandRunner, bool) {
	if execRuntime == nil {
		return nil, false
	}
	switch strings.TrimSpace(route) {
	case "", string(toolexec.ExecutionRouteSandbox):
		if execRuntime.SandboxRunner() == nil {
			return nil, false
		}
		runner, ok := execRuntime.SandboxRunner().(toolexec.AsyncCommandRunner)
		return runner, ok
	case string(toolexec.ExecutionRouteHost):
		if execRuntime.HostRunner() == nil {
			return nil, false
		}
		runner, ok := execRuntime.HostRunner().(toolexec.AsyncCommandRunner)
		return runner, ok
	default:
		return nil, false
	}
}

func bashTaskState(state toolexec.SessionState) task.State {
	switch state {
	case toolexec.SessionStateCompleted:
		return task.StateCompleted
	case toolexec.SessionStateTerminated:
		return task.StateTerminated
	case toolexec.SessionStateError:
		return task.StateFailed
	default:
		return task.StateRunning
	}
}
