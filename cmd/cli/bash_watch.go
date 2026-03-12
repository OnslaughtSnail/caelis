package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

const bashWatchPollInterval = 120 * time.Millisecond

func (c *cliConsole) syncBashTaskWatch(respToolID string, respToolName string, result map[string]any) {
	if c == nil || c.tuiSender == nil || c.execRuntime == nil {
		return
	}
	taskID := strings.TrimSpace(fmt.Sprint(result["task_id"]))
	sessionID := strings.TrimSpace(fmt.Sprint(result["session_id"]))
	if taskID == "" || sessionID == "" {
		return
	}
	running, _ := result["running"].(bool)
	route := strings.TrimSpace(fmt.Sprint(result["route"]))
	if running {
		c.ensureBashTaskWatch(taskID, respToolID, sessionID, route)
		return
	}
	c.stopBashTaskWatch(taskID)
}

func (c *cliConsole) shouldSuppressWatchedBashTaskStream(respToolName string, ev tuievents.TaskStreamMsg) bool {
	if c == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(ev.Label), toolshell.BashToolName) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(respToolName), toolshell.BashToolName) {
		return false
	}
	taskID := strings.TrimSpace(ev.TaskID)
	if taskID == "" {
		return false
	}
	c.bashWatchMu.Lock()
	_, ok := c.bashTaskWatches[taskID]
	c.bashWatchMu.Unlock()
	return ok
}

func (c *cliConsole) ensureBashTaskWatch(taskID string, callID string, sessionID string, route string) {
	taskID = strings.TrimSpace(taskID)
	sessionID = strings.TrimSpace(sessionID)
	if taskID == "" || sessionID == "" {
		return
	}
	runner, ok := asyncBashRunnerForConsole(c.execRuntime, route)
	if !ok || runner == nil {
		return
	}
	c.bashWatchMu.Lock()
	if _, exists := c.bashTaskWatches[taskID]; exists {
		c.bashWatchMu.Unlock()
		return
	}
	base := c.baseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	c.bashTaskWatches[taskID] = cancel
	c.bashWatchMu.Unlock()
	go c.runBashTaskWatch(ctx, runner, taskID, strings.TrimSpace(callID), sessionID)
}

func (c *cliConsole) stopBashTaskWatch(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	c.bashWatchMu.Lock()
	cancel, ok := c.bashTaskWatches[taskID]
	if ok {
		delete(c.bashTaskWatches, taskID)
	}
	c.bashWatchMu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
}

func (c *cliConsole) finishBashTaskWatch(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	c.bashWatchMu.Lock()
	delete(c.bashTaskWatches, taskID)
	c.bashWatchMu.Unlock()
}

func (c *cliConsole) runBashTaskWatch(ctx context.Context, runner toolexec.AsyncCommandRunner, taskID string, callID string, sessionID string) {
	defer c.finishBashTaskWatch(taskID)
	var stdoutMarker, stderrMarker int64
	lastState := ""
	ticker := time.NewTicker(bashWatchPollInterval)
	defer ticker.Stop()
	for {
		stdout, stderr, nextStdout, nextStderr, err := runner.ReadOutput(sessionID, stdoutMarker, stderrMarker)
		if err == nil {
			stdoutMarker, stderrMarker = nextStdout, nextStderr
			if text := string(stdout); strings.TrimSpace(text) != "" {
				c.tuiSender.Send(tuievents.TaskStreamMsg{
					Label:  toolshell.BashToolName,
					TaskID: taskID,
					CallID: callID,
					Stream: "stdout",
					Chunk:  text,
				})
			}
			if text := string(stderr); strings.TrimSpace(text) != "" {
				c.tuiSender.Send(tuievents.TaskStreamMsg{
					Label:  toolshell.BashToolName,
					TaskID: taskID,
					CallID: callID,
					Stream: "stderr",
					Chunk:  text,
				})
			}
		}
		status, statusErr := runner.GetSessionStatus(sessionID)
		if statusErr == nil {
			state := bashWatchState(status.State)
			if state != "" && state != lastState {
				c.tuiSender.Send(tuievents.TaskStreamMsg{
					Label:  toolshell.BashToolName,
					TaskID: taskID,
					CallID: callID,
					State:  state,
				})
				lastState = state
			}
			if status.State != toolexec.SessionStateRunning {
				c.tuiSender.Send(tuievents.TaskStreamMsg{
					Label:  toolshell.BashToolName,
					TaskID: taskID,
					CallID: callID,
					State:  state,
					Final:  true,
				})
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func asyncBashRunnerForConsole(execRuntime toolexec.Runtime, route string) (toolexec.AsyncCommandRunner, bool) {
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

func bashWatchState(state toolexec.SessionState) string {
	switch state {
	case toolexec.SessionStateRunning:
		return "running"
	case toolexec.SessionStateCompleted:
		return "completed"
	case toolexec.SessionStateError:
		return "failed"
	case toolexec.SessionStateTerminated:
		return "terminated"
	default:
		return ""
	}
}
