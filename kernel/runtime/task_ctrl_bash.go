package runtime

import (
	"context"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskruntime"
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

func (c *bashTaskController) delegate() *taskruntime.BashTaskController {
	if c == nil {
		return nil
	}
	return &taskruntime.BashTaskController{
		Session: c.session,
		Command: c.command,
		Workdir: c.workdir,
		TTY:     c.tty,
		Route:   c.route,
		Backend: c.backend,
		Store:   c.store,
	}
}

func (c *bashTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	return c.delegate().Wait(ctx, record, yield)
}

func (c *bashTaskController) Write(ctx context.Context, record *task.Record, input string, yield time.Duration) (task.Snapshot, error) {
	return c.delegate().Write(ctx, record, input, yield)
}

func (c *bashTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	return c.delegate().Cancel(ctx, record)
}

func bashTaskOutputMeta(status toolexec.SessionStatus, tty bool) map[string]any {
	return taskruntime.BashTaskOutputMeta(status, tty)
}

func recoveredBashPreview(session toolexec.Session) string {
	return taskruntime.RecoveredBashPreview(session)
}

func openBashSession(execRuntime toolexec.Runtime, backendName string, sessionID string) (toolexec.Session, error) {
	return taskruntime.OpenBashSession(execRuntime, backendName, sessionID)
}

func bashTaskState(status toolexec.SessionStatus, latestOutput string) task.State {
	return taskruntime.BashTaskState(status, latestOutput)
}
