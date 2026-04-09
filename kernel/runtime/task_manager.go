package runtime

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskruntime"
)

type runtimeTaskManager struct {
	*taskruntime.Manager
}

type sessionContext struct {
	appName   string
	userID    string
	sessionID string
}

const (
	taskSpecCommand        = taskruntime.SpecCommand
	taskSpecWorkdir        = taskruntime.SpecWorkdir
	taskSpecTTY            = taskruntime.SpecTTY
	taskSpecRoute          = taskruntime.SpecRoute
	taskSpecBackend        = taskruntime.SpecBackend
	taskSpecExecSessionID  = taskruntime.SpecExecSessionID
	taskSpecChildSession   = taskruntime.SpecChildSession
	taskSpecDelegationID   = taskruntime.SpecDelegationID
	taskSpecAgent          = taskruntime.SpecAgent
	taskSpecChildCWD       = taskruntime.SpecChildCWD
	taskSpecPrompt         = taskruntime.SpecPrompt
	taskSpecIdleTimeout    = taskruntime.SpecIdleTimeout
	taskSpecParentToolCall = taskruntime.SpecParentToolCall
	taskSpecParentToolName = taskruntime.SpecParentToolName
	taskSpecUISpawnID      = taskruntime.SpecUISpawnID
	taskSpecUIAnchorTool   = taskruntime.SpecUIAnchorTool
)

func newTaskManager(_ *Runtime, execRuntime toolexec.Runtime, registry *task.Registry, store task.Store, parent *sessionContext, _ RunRequest, runner agent.SubagentRunner) *runtimeTaskManager {
	var sessionRef *task.SessionRef
	if parent != nil {
		sessionRef = &task.SessionRef{
			AppName:   parent.appName,
			UserID:    parent.userID,
			SessionID: parent.sessionID,
		}
	}
	return &runtimeTaskManager{
		Manager: taskruntime.New(taskruntime.Config{
			ExecRuntime:            execRuntime,
			Registry:               registry,
			Store:                  store,
			Session:                sessionRef,
			Subagents:              runner,
			ContinueSubagentRunner: newSubagentContinuationRunner(runner),
			ContinuationAnchorTool: SubagentContinuationAnchorTool,
		}),
	}
}

func (m *runtimeTaskManager) trackTurnTask(taskID string) {
	if m != nil {
		m.TrackTurnTask(taskID)
	}
}

func (m *runtimeTaskManager) cleanupTurn(ctx context.Context) {
	if m != nil {
		m.CleanupTurn(ctx)
	}
}

func (m *runtimeTaskManager) interruptTurn(ctx context.Context) {
	if m != nil {
		m.InterruptTurn(ctx)
	}
}
