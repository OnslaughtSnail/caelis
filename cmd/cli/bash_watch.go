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

func (c *cliConsole) syncBashTaskWatchContext(ctx context.Context, respToolID string, _ string, result map[string]any) {
	if c == nil || c.tuiSender == nil || c.execRuntime == nil {
		return
	}
	taskID := strings.TrimSpace(fmt.Sprint(result["task_id"]))
	sessionID := strings.TrimSpace(fmt.Sprint(result["session_id"]))
	backendName := strings.TrimSpace(fmt.Sprint(result["backend"]))
	if taskID == "" || sessionID == "" {
		return
	}
	state := strings.ToLower(strings.TrimSpace(fmt.Sprint(result["state"])))
	route := strings.TrimSpace(fmt.Sprint(result["route"]))
	if state == "" || state == "running" || state == "waiting_input" || state == "waiting_approval" {
		c.ensureBashTaskWatchContext(ctx, taskID, respToolID, sessionID, backendName, route)
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

func (c *cliConsole) ensureBashTaskWatchContext(ctx context.Context, taskID string, callID string, sessionID string, backendName string, route string) {
	taskID = strings.TrimSpace(taskID)
	sessionID = strings.TrimSpace(sessionID)
	if taskID == "" || sessionID == "" {
		return
	}
	sessionRef, err := openConsoleBashSession(c.execRuntime, backendName, route, sessionID)
	if err != nil || sessionRef == nil {
		return
	}
	c.bashWatchMu.Lock()
	if _, exists := c.bashTaskWatches[taskID]; exists {
		c.bashWatchMu.Unlock()
		return
	}
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(cliContext(ctx)))
	c.bashTaskWatches[taskID] = cancel
	c.bashWatchMu.Unlock()
	go c.runBashTaskWatch(watchCtx, sessionRef, taskID, strings.TrimSpace(callID))
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

func (c *cliConsole) runBashTaskWatch(ctx context.Context, sessionRef toolexec.Session, taskID string, callID string) {
	defer c.finishBashTaskWatch(taskID)
	var stdoutMarker, stderrMarker int64
	lastState := ""
	ticker := time.NewTicker(bashWatchPollInterval)
	defer ticker.Stop()
	for {
		stdout, stderr, nextStdout, nextStderr, err := sessionRef.ReadOutput(stdoutMarker, stderrMarker)
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
		status, statusErr := sessionRef.Status()
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

func openConsoleBashSession(execRuntime toolexec.Runtime, backendName string, route string, sessionID string) (toolexec.Session, error) {
	if execRuntime == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	backendName = strings.TrimSpace(backendName)
	if backendName == "" {
		switch strings.TrimSpace(route) {
		case string(toolexec.ExecutionRouteHost):
			backendName = "host"
		default:
			if usesLegacyConsoleACPTerminalBackend(route, sessionID) {
				backendName = "acp_terminal"
				break
			}
			state := execRuntime.State()
			backendName = strings.TrimSpace(state.ResolvedSandbox)
			if backendName == "" {
				backendName = "sandbox"
			}
		}
	}
	return execRuntime.OpenSession(toolexec.CommandSessionRef{
		Backend:   backendName,
		SessionID: strings.TrimSpace(sessionID),
	})
}

func usesLegacyConsoleACPTerminalBackend(route string, sessionID string) bool {
	switch strings.TrimSpace(route) {
	case "", string(toolexec.ExecutionRouteSandbox):
	default:
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sessionID)), "term-")
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
