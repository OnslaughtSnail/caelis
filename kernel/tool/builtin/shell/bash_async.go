package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

// handleAsyncReadOnlyAction handles read-only async session operations (read,
// status).  These do not execute commands or mutate session state, so they are
// safe to perform without going through resolveCommandDecision/requestApproval.
func (t *BashTool) handleAsyncReadOnlyAction(ctx context.Context, action string, args map[string]any) (map[string]any, error) {
	sessionID, err := argparse.String(args, "session_id", true)
	if err != nil {
		return nil, fmt.Errorf("tool: session_id is required for action %q", action)
	}

	asyncRunner := t.getAsyncRunner()
	if asyncRunner == nil {
		return nil, fmt.Errorf("tool: async execution is not supported in the current runtime")
	}

	// Validate that the session exists.
	if _, err := asyncRunner.GetSessionStatus(sessionID); err != nil {
		return nil, fmt.Errorf("tool: session %q not found: %w", sessionID, err)
	}

	switch action {
	case "read":
		return t.handleReadOutput(ctx, asyncRunner, sessionID, args)
	case "status":
		return t.handleGetStatus(ctx, asyncRunner, sessionID)
	default:
		return nil, fmt.Errorf("tool: unknown read-only action %q", action)
	}
}

// handleWriteInput sends input to an async session.
func (t *BashTool) handleWriteInput(ctx context.Context, runner toolexec.AsyncCommandRunner, sessionID string, args map[string]any) (map[string]any, error) {
	input, err := argparse.String(args, "input", true)
	if err != nil {
		return nil, fmt.Errorf("tool: input is required for write action")
	}

	if err := runner.WriteInput(sessionID, []byte(input)); err != nil {
		return nil, fmt.Errorf("tool: failed to write input: %w", err)
	}

	// Optionally read output after a short delay
	delayMS, _ := argparse.Int(args, "delay_ms", 0)
	if delayMS > 0 {
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
		return t.handleReadOutput(ctx, runner, sessionID, args)
	}

	return map[string]any{
		"session_id": sessionID,
		"written":    len(input),
		"success":    true,
	}, nil
}

// handleReadOutput reads output from an async session.
func (t *BashTool) handleReadOutput(ctx context.Context, runner toolexec.AsyncCommandRunner, sessionID string, args map[string]any) (map[string]any, error) {
	// Get optional markers for incremental reads
	stdoutMarker, _ := argparse.Int(args, "stdout_marker", 0)
	stderrMarker, _ := argparse.Int(args, "stderr_marker", 0)

	stdout, stderr, newStdoutMarker, newStderrMarker, err := runner.ReadOutput(sessionID, int64(stdoutMarker), int64(stderrMarker))
	if err != nil {
		return nil, fmt.Errorf("tool: failed to read output: %w", err)
	}

	status, err := runner.GetSessionStatus(sessionID)
	if err != nil {
		return nil, fmt.Errorf("tool: failed to get session status: %w", err)
	}

	result := map[string]any{
		"session_id":    sessionID,
		"stdout":        string(stdout),
		"stderr":        string(stderr),
		"stdout_marker": newStdoutMarker,
		"stderr_marker": newStderrMarker,
		"running":       status.State == toolexec.SessionStateRunning,
		"state":         string(status.State),
	}

	if status.State != toolexec.SessionStateRunning {
		result["exit_code"] = status.ExitCode
	}
	return appendBashTaskResultEvents(ctx, result, false), nil
}

// handleGetStatus returns the status of an async session.
func (t *BashTool) handleGetStatus(ctx context.Context, runner toolexec.AsyncCommandRunner, sessionID string) (map[string]any, error) {
	status, err := runner.GetSessionStatus(sessionID)
	if err != nil {
		return nil, fmt.Errorf("tool: failed to get session status: %w", err)
	}

	result := map[string]any{
		"session_id":    sessionID,
		"command":       status.Command,
		"dir":           status.Dir,
		"state":         string(status.State),
		"running":       status.State == toolexec.SessionStateRunning,
		"start_time":    status.StartTime.Format(time.RFC3339),
		"last_activity": status.LastActivity.Format(time.RFC3339),
		"exit_code":     status.ExitCode,
		"stdout_bytes":  status.StdoutBytes,
		"stderr_bytes":  status.StderrBytes,
		"error":         status.Error,
	}
	return appendBashTaskResultEvents(ctx, result, false), nil
}

// handleTerminate terminates an async session.
func (t *BashTool) handleTerminate(ctx context.Context, runner toolexec.AsyncCommandRunner, sessionID string) (map[string]any, error) {
	// Get final output before terminating
	stdout, stderr, _, _, _ := runner.ReadOutput(sessionID, 0, 0)

	if err := runner.TerminateSession(sessionID); err != nil {
		return nil, fmt.Errorf("tool: failed to terminate session: %w", err)
	}

	result := map[string]any{
		"session_id": sessionID,
		"terminated": true,
		"stdout":     string(stdout),
		"stderr":     string(stderr),
		"state":      string(toolexec.SessionStateTerminated),
		"running":    false,
	}
	return appendBashTaskResultEvents(ctx, result, false), nil
}

// handleListSessions returns information about all sessions.
func (t *BashTool) handleListSessions(ctx context.Context) (map[string]any, error) {
	asyncRunner := t.getAsyncRunner()
	if asyncRunner == nil {
		return map[string]any{
			"sessions": []any{},
			"count":    0,
		}, nil
	}

	sessions := asyncRunner.ListSessions()
	sessionList := make([]map[string]any, len(sessions))

	for i, s := range sessions {
		sessionList[i] = map[string]any{
			"session_id":    s.ID,
			"command":       truncateCommand(s.Command, 100),
			"state":         string(s.State),
			"running":       s.State == toolexec.SessionStateRunning,
			"start_time":    s.StartTime.Format(time.RFC3339),
			"last_activity": s.LastActivity.Format(time.RFC3339),
			"has_output":    s.HasOutput,
		}
		if s.State != toolexec.SessionStateRunning {
			sessionList[i]["exit_code"] = s.ExitCode
		}
	}

	return map[string]any{
		"sessions": sessionList,
		"count":    len(sessions),
	}, nil
}

// runAsync starts an async session and optionally waits for initial output.
func (t *BashTool) runAsync(ctx context.Context, runner toolexec.CommandRunner, command, workingDir string, initialWait time.Duration, route toolexec.ExecutionRoute, timeout, idleTimeout time.Duration) (map[string]any, error) {
	asyncRunner, ok := runner.(toolexec.AsyncCommandRunner)
	if !ok {
		return nil, fmt.Errorf("tool: async execution is not supported for route %s", route)
	}

	sessionID, err := asyncRunner.StartAsync(ctx, toolexec.CommandRequest{
		Command:     command,
		Dir:         workingDir,
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("tool: failed to start async command: %w", err)
	}

	result := map[string]any{
		"session_id": sessionID,
		"command":    command,
		"route":      string(route),
		"mode":       "async",
	}

	// If initial wait is specified, wait for some output
	if initialWait > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, initialWait)
		defer cancel()

		// Poll for output or completion
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		var stdout, stderr []byte
		var stdoutMarker, stderrMarker int64

	waitLoop:
		for {
			select {
			case <-waitCtx.Done():
				break waitLoop
			case <-ticker.C:
				newStdout, newStderr, newStdoutMarker, newStderrMarker, err := asyncRunner.ReadOutput(sessionID, stdoutMarker, stderrMarker)
				if err == nil {
					stdout = append(stdout, newStdout...)
					stderr = append(stderr, newStderr...)
					stdoutMarker = newStdoutMarker
					stderrMarker = newStderrMarker
				}

				status, err := asyncRunner.GetSessionStatus(sessionID)
				if err == nil && status.State != toolexec.SessionStateRunning {
					// Process has exited
					result["running"] = false
					result["exit_code"] = status.ExitCode
					result["state"] = string(status.State)
					break waitLoop
				}
			}
		}

		result["stdout"] = string(stdout)
		result["stderr"] = string(stderr)
		result["stdout_marker"] = stdoutMarker
		result["stderr_marker"] = stderrMarker

		// Check final status
		status, err := asyncRunner.GetSessionStatus(sessionID)
		if err == nil {
			result["running"] = status.State == toolexec.SessionStateRunning
			result["state"] = string(status.State)
			if status.State != toolexec.SessionStateRunning {
				result["exit_code"] = status.ExitCode
			}
		} else {
			result["running"] = true
			result["state"] = string(toolexec.SessionStateRunning)
		}
	} else {
		result["running"] = true
		result["state"] = string(toolexec.SessionStateRunning)
		result["stdout"] = ""
		result["stderr"] = ""
		result["stdout_marker"] = int64(0)
		result["stderr_marker"] = int64(0)
	}

	return appendBashTaskResultEvents(ctx, result, true), nil
}

// getAsyncRunner returns the async runner if available.
func (t *BashTool) getAsyncRunner() toolexec.AsyncCommandRunner {
	// Try host runner first
	if hostRunner := t.runtime.HostRunner(); hostRunner != nil {
		if asyncRunner, ok := hostRunner.(toolexec.AsyncCommandRunner); ok {
			return asyncRunner
		}
	}
	return nil
}

func truncateCommand(cmd string, maxLen int) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) <= maxLen {
		return cmd
	}
	return cmd[:maxLen-3] + "..."
}

func appendBashTaskResultEvents(ctx context.Context, result map[string]any, reset bool) map[string]any {
	sessionID := strings.TrimSpace(fmt.Sprint(result["session_id"]))
	if sessionID == "" {
		return result
	}
	callInfo, _ := toolexec.ToolCallInfoFromContext(ctx)
	if reset {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  BashToolName,
			TaskID: sessionID,
			CallID: callInfo.ID,
			State:  strings.TrimSpace(fmt.Sprint(result["state"])),
			Reset:  true,
		})
	}
	for _, item := range []struct {
		stream string
		key    string
	}{
		{stream: "stdout", key: "stdout"},
		{stream: "stderr", key: "stderr"},
	} {
		chunk := strings.TrimSpace(fmt.Sprint(result[item.key]))
		if chunk == "" {
			continue
		}
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  BashToolName,
			TaskID: sessionID,
			CallID: callInfo.ID,
			Stream: item.stream,
			Chunk:  chunk,
			State:  strings.TrimSpace(fmt.Sprint(result["state"])),
		})
	}
	if running, _ := result["running"].(bool); !running {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  BashToolName,
			TaskID: sessionID,
			CallID: callInfo.ID,
			State:  strings.TrimSpace(fmt.Sprint(result["state"])),
			Final:  true,
		})
	}
	return result
}
