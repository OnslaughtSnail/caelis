package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/delegation"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

type runtimeTaskManager struct {
	runtime   *Runtime
	execenv   toolexec.Runtime
	registry  *task.Registry
	store     task.Store
	parent    *sessionContext
	runReq    RunRequest
	subagents *runtimeSubagentRunner

	turnMu      sync.Mutex
	turnTaskIDs []string
}

type sessionContext struct {
	appName   string
	userID    string
	sessionID string
}

const (
	taskSpecCommand       = "command"
	taskSpecWorkdir       = "workdir"
	taskSpecTTY           = "tty"
	taskSpecRoute         = "route"
	taskSpecExecSessionID = "exec_session_id"
	taskSpecChildSession  = "child_session_id"
	taskSpecDelegationID  = "delegation_id"
	taskSpecPrompt        = "task"
)

func newTaskManager(r *Runtime, execRuntime toolexec.Runtime, registry *task.Registry, store task.Store, parent *sessionContext, req RunRequest, runner *runtimeSubagentRunner) *runtimeTaskManager {
	if registry == nil {
		registry = task.NewRegistry(task.RegistryConfig{})
	}
	return &runtimeTaskManager{
		runtime:   r,
		execenv:   execRuntime,
		registry:  registry,
		store:     store,
		parent:    parent,
		runReq:    req,
		subagents: runner,
	}
}

func (m *runtimeTaskManager) persistRecord(ctx context.Context, record *task.Record) error {
	if m == nil || m.store == nil || record == nil {
		return nil
	}
	var entry *task.Entry
	record.WithLock(func(one *task.Record) {
		entry = &task.Entry{
			TaskID:         one.ID,
			Kind:           one.Kind,
			Session:        one.Session,
			Title:          one.Title,
			State:          one.State,
			Running:        one.Running,
			SupportsInput:  one.SupportsInput,
			SupportsCancel: one.SupportsCancel,
			CreatedAt:      one.CreatedAt,
			UpdatedAt:      one.UpdatedAt,
			HeartbeatAt:    one.UpdatedAt,
			Spec:           task.CloneEntry(&task.Entry{Spec: one.Spec}).Spec,
			Result:         task.CloneEntry(&task.Entry{Result: one.Result}).Result,
		}
	})
	if entry == nil {
		return nil
	}
	return m.store.Upsert(ctx, entry)
}

func (m *runtimeTaskManager) ensureRecord(ctx context.Context, taskID string) (*task.Record, error) {
	record, err := m.registry.Get(taskID)
	if err == nil {
		return record, nil
	}
	if m == nil || m.store == nil {
		return nil, err
	}
	entry, storeErr := m.store.Get(ctx, taskID)
	if storeErr != nil {
		return nil, storeErr
	}
	record = entryToRecord(entry)
	record.Backend = m.rebuildController(entry)
	m.registry.Put(record)
	return record, nil
}

func (m *runtimeTaskManager) rebuildController(entry *task.Entry) task.Controller {
	if entry == nil {
		return nil
	}
	switch entry.Kind {
	case task.KindBash:
		runner, ok := asyncBashRunnerForRoute(m.execenv, stringValue(entry.Spec, taskSpecRoute))
		if !ok || runner == nil {
			return nil
		}
		return &bashTaskController{
			runner:    runner,
			sessionID: stringValue(entry.Spec, taskSpecExecSessionID),
			command:   stringValue(entry.Spec, taskSpecCommand),
			workdir:   stringValue(entry.Spec, taskSpecWorkdir),
			tty:       boolValue(entry.Spec, taskSpecTTY),
			route:     stringValue(entry.Spec, taskSpecRoute),
			store:     m.store,
		}
	case task.KindDelegate:
		return &delegateTaskController{
			runtime:      m.runtime,
			appName:      entry.Session.AppName,
			userID:       entry.Session.UserID,
			sessionID:    stringValue(entry.Spec, taskSpecChildSession),
			delegationID: stringValue(entry.Spec, taskSpecDelegationID),
			store:        m.store,
		}
	default:
		return nil
	}
}

func entryToRecord(entry *task.Entry) *task.Record {
	if entry == nil {
		return nil
	}
	record := &task.Record{
		ID:             entry.TaskID,
		Kind:           entry.Kind,
		Title:          entry.Title,
		State:          entry.State,
		Running:        entry.Running,
		SupportsInput:  entry.SupportsInput,
		SupportsCancel: entry.SupportsCancel,
		CreatedAt:      entry.CreatedAt,
		UpdatedAt:      entry.UpdatedAt,
		Session:        entry.Session,
		Spec:           task.CloneEntry(&task.Entry{Spec: entry.Spec}).Spec,
		Result:         task.CloneEntry(&task.Entry{Result: entry.Result}).Result,
	}
	return record
}

func persistControllerRecord(ctx context.Context, store task.Store, record *task.Record) error {
	if store == nil || record == nil {
		return nil
	}
	var entry *task.Entry
	record.WithLock(func(one *task.Record) {
		entry = &task.Entry{
			TaskID:         one.ID,
			Kind:           one.Kind,
			Session:        one.Session,
			Title:          one.Title,
			State:          one.State,
			Running:        one.Running,
			SupportsInput:  one.SupportsInput,
			SupportsCancel: one.SupportsCancel,
			CreatedAt:      one.CreatedAt,
			UpdatedAt:      one.UpdatedAt,
			HeartbeatAt:    one.UpdatedAt,
			Spec:           task.CloneEntry(&task.Entry{Spec: one.Spec}).Spec,
			Result:         task.CloneEntry(&task.Entry{Result: one.Result}).Result,
		}
	})
	return store.Upsert(ctx, entry)
}

func (m *runtimeTaskManager) StartBash(ctx context.Context, req task.BashStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Command) == "" {
		return task.Snapshot{}, fmt.Errorf("task: bash command is required")
	}
	if req.Yield < 0 {
		return task.Snapshot{}, fmt.Errorf("task: async bash requires yield_time_ms >= 0")
	}
	asyncRunner, ok := asyncBashRunnerForRoute(m.execenv, strings.TrimSpace(req.Route))
	if !ok || asyncRunner == nil {
		return task.Snapshot{}, fmt.Errorf("task: async bash is not supported for route %q", strings.TrimSpace(req.Route))
	}
	sessionID, err := asyncRunner.StartAsync(ctx, toolexec.CommandRequest{
		Command:     req.Command,
		Dir:         req.Workdir,
		Timeout:     req.Timeout,
		IdleTimeout: req.IdleTimeout,
		TTY:         req.TTY,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	controller := &bashTaskController{
		runner:    asyncRunner,
		sessionID: sessionID,
		command:   req.Command,
		workdir:   req.Workdir,
		tty:       req.TTY,
		route:     strings.TrimSpace(req.Route),
		store:     m.store,
	}
	record := m.registry.Create(task.KindBash, req.Command, controller, true, true)
	m.trackTurnTask(record.ID)
	record.Session = task.SessionRef{AppName: m.parent.appName, UserID: m.parent.userID, SessionID: m.parent.sessionID}
	record.Spec = map[string]any{
		taskSpecCommand:       req.Command,
		taskSpecWorkdir:       req.Workdir,
		taskSpecTTY:           req.TTY,
		taskSpecRoute:         strings.TrimSpace(req.Route),
		taskSpecExecSessionID: sessionID,
	}
	if err := m.persistRecord(ctx, record); err != nil {
		return task.Snapshot{}, err
	}
	snapshot, err := controller.Wait(ctx, record, req.Yield)
	if err != nil {
		return task.Snapshot{}, err
	}
	snapshot.Yielded = snapshot.Running
	return snapshot, nil
}

func (m *runtimeTaskManager) StartDelegate(ctx context.Context, req task.DelegateStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Task) == "" {
		return task.Snapshot{}, fmt.Errorf("task: delegate task is required")
	}
	if m.subagents == nil {
		return task.Snapshot{}, fmt.Errorf("task: delegate runtime is unavailable")
	}
	if req.Yield < 0 {
		result, err := m.subagents.RunSubagent(ctx, delegation.RunRequest{Input: req.Task})
		if err != nil {
			return task.Snapshot{}, err
		}
		return task.Snapshot{
			Kind:           task.KindDelegate,
			Title:          req.Task,
			State:          task.StateCompleted,
			Running:        false,
			SupportsInput:  false,
			SupportsCancel: false,
			Result: map[string]any{
				"child_session_id": result.SessionID,
				"delegation_id":    result.DelegationID,
				"assistant":        result.Assistant,
				"summary":          result.Assistant,
				"state":            result.State,
			},
		}, nil
	}

	childReq, lineage, err := m.subagents.prepareChildRun(ctx, delegation.RunRequest{Input: req.Task})
	if err != nil {
		return task.Snapshot{}, err
	}
	record := m.registry.Create(task.KindDelegate, req.Task, nil, false, true)
	m.trackTurnTask(record.ID)
	lineage.TaskID = record.ID
	baseCtx, cancel := context.WithCancel(detachSubagentContext(ctx, lineage))
	controller := &delegateTaskController{
		runtime:      m.runtime,
		appName:      m.runReq.AppName,
		userID:       m.runReq.UserID,
		sessionID:    childReq.SessionID,
		delegationID: lineage.DelegationID,
		cancel:       cancel,
		store:        m.store,
	}
	record.Backend = controller
	record.Session = task.SessionRef{AppName: m.parent.appName, UserID: m.parent.userID, SessionID: m.parent.sessionID}
	record.Spec = map[string]any{
		taskSpecPrompt:       req.Task,
		taskSpecChildSession: childReq.SessionID,
		taskSpecDelegationID: lineage.DelegationID,
	}
	if err := m.persistRecord(ctx, record); err != nil {
		cancel()
		return task.Snapshot{}, err
	}
	taskstream.Emit(ctx, taskstream.Event{
		Label:  "DELEGATE",
		TaskID: record.ID,
		CallID: record.ID,
		State:  string(RunLifecycleStatusRunning),
		Reset:  true,
	})
	go m.subagents.runDetachedSubagent(baseCtx, childReq, lineage)
	snapshot, err := controller.Wait(ctx, record, req.Yield)
	if err != nil {
		return task.Snapshot{}, err
	}
	snapshot.Yielded = snapshot.Running
	return snapshot, nil
}

func (m *runtimeTaskManager) trackTurnTask(taskID string) {
	if m == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	m.turnTaskIDs = append(m.turnTaskIDs, strings.TrimSpace(taskID))
}

func (m *runtimeTaskManager) cleanupTurn(ctx context.Context) {
	if m == nil {
		return
	}
	m.turnMu.Lock()
	taskIDs := append([]string(nil), m.turnTaskIDs...)
	m.turnTaskIDs = nil
	m.turnMu.Unlock()
	for _, taskID := range taskIDs {
		record, err := m.ensureRecord(ctx, taskID)
		if err != nil || record == nil || record.Backend == nil {
			continue
		}
		var running, supportsCancel bool
		record.WithLock(func(one *task.Record) {
			running = one.Running
			supportsCancel = one.SupportsCancel
		})
		if !running || !supportsCancel {
			continue
		}
		_, _ = record.Backend.Cancel(ctx, record)
	}
}

func (m *runtimeTaskManager) Wait(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
	record, err := m.ensureRecord(ctx, req.TaskID)
	if err != nil {
		return task.Snapshot{}, err
	}
	if snapshot, ok := persistedFinalTaskSnapshot(record); ok {
		return snapshot, nil
	}
	if record.Backend == nil {
		return task.Snapshot{}, fmt.Errorf("task: controller missing for %q", req.TaskID)
	}
	snapshot, err := record.Backend.Wait(ctx, record, req.Yield)
	if err != nil {
		return task.Snapshot{}, err
	}
	snapshot.Yielded = snapshot.Running
	return snapshot, nil
}

func (m *runtimeTaskManager) Status(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
	record, err := m.ensureRecord(ctx, req.TaskID)
	if err != nil {
		return task.Snapshot{}, err
	}
	if snapshot, ok := persistedFinalTaskSnapshot(record); ok {
		return snapshot, nil
	}
	if record.Backend == nil {
		return task.Snapshot{}, fmt.Errorf("task: controller missing for %q", req.TaskID)
	}
	return record.Backend.Status(ctx, record)
}

func (m *runtimeTaskManager) Write(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
	record, err := m.ensureRecord(ctx, req.TaskID)
	if err != nil {
		return task.Snapshot{}, err
	}
	if record.Backend == nil {
		return task.Snapshot{}, fmt.Errorf("task: controller missing for %q", req.TaskID)
	}
	if !record.SupportsInput {
		return task.Snapshot{}, fmt.Errorf("task: %q does not accept input", req.TaskID)
	}
	return record.Backend.Write(ctx, record, req.Input, req.Yield)
}

func (m *runtimeTaskManager) Cancel(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
	record, err := m.ensureRecord(ctx, req.TaskID)
	if err != nil {
		return task.Snapshot{}, err
	}
	if snapshot, ok := persistedFinalTaskSnapshot(record); ok {
		return snapshot, nil
	}
	if record.Backend == nil {
		return task.Snapshot{}, fmt.Errorf("task: controller missing for %q", req.TaskID)
	}
	if !record.SupportsCancel {
		return task.Snapshot{}, fmt.Errorf("task: %q cannot be cancelled", req.TaskID)
	}
	return record.Backend.Cancel(ctx, record)
}

func (m *runtimeTaskManager) List(ctx context.Context) ([]task.Snapshot, error) {
	records := m.registry.List()
	out := make([]task.Snapshot, 0, len(records))
	seen := map[string]struct{}{}
	for _, record := range records {
		if record == nil {
			continue
		}
		seen[record.ID] = struct{}{}
		out = append(out, record.Snapshot(task.Output{}))
	}
	if m.store != nil && m.parent != nil {
		entries, err := m.store.ListSession(ctx, task.SessionRef{
			AppName:   m.parent.appName,
			UserID:    m.parent.userID,
			SessionID: m.parent.sessionID,
		})
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			if _, ok := seen[entry.TaskID]; ok {
				continue
			}
			out = append(out, entryToRecord(entry).Snapshot(task.Output{}))
		}
	}
	return out, nil
}

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

		if strings.TrimSpace(output.Stdout) != "" || strings.TrimSpace(output.Stderr) != "" || !snapshot.Running {
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

type delegateTaskController struct {
	runtime      *Runtime
	appName      string
	userID       string
	sessionID    string
	delegationID string
	cancel       context.CancelFunc
	store        task.Store
}

func (c *delegateTaskController) Wait(ctx context.Context, record *task.Record, yield time.Duration) (task.Snapshot, error) {
	deadline := time.Time{}
	if yield > 0 {
		deadline = time.Now().Add(yield)
	}
	for {
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		default:
		}
		snapshot, err := c.inspect(ctx, record, true)
		if err != nil {
			return task.Snapshot{}, err
		}
		if strings.TrimSpace(snapshot.Output.Log) != "" || !snapshot.Running {
			return snapshot, nil
		}
		if deadline.IsZero() || time.Now().After(deadline) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return task.Snapshot{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (c *delegateTaskController) Status(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	return c.inspect(ctx, record, false)
}

func (c *delegateTaskController) Write(context.Context, *task.Record, string, time.Duration) (task.Snapshot, error) {
	return task.Snapshot{}, fmt.Errorf("task: delegate tasks do not accept input")
}

func (c *delegateTaskController) Cancel(ctx context.Context, record *task.Record) (task.Snapshot, error) {
	if c.cancel != nil {
		c.cancel()
	}
	var snapshot task.Snapshot
	record.WithLock(func(one *task.Record) {
		one.State = task.StateCancelled
		one.Running = false
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id": c.sessionID,
			"delegation_id":    c.delegationID,
			"state":            string(task.StateCancelled),
		}
		snapshot = one.LockedSnapshot(task.Output{})
	})
	_ = persistControllerRecord(ctx, c.store, record)
	return snapshot, nil
}

func (c *delegateTaskController) inspect(ctx context.Context, record *task.Record, advance bool) (task.Snapshot, error) {
	if c == nil || c.runtime == nil {
		return task.Snapshot{}, fmt.Errorf("task: delegate controller is unavailable")
	}
	state, err := c.runtime.RunState(ctx, RunStateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	events, err := c.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          c.appName,
		UserID:           c.userID,
		SessionID:        c.sessionID,
		IncludeLifecycle: false,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	var snapshot task.Snapshot
	var output task.Output
	var assistant string
	record.WithLock(func(one *task.Record) {
		if len(one.Result) > 0 {
			if text, ok := one.Result["assistant"].(string); ok {
				assistant = text
			}
		}
	})
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			assistant = text
		}
	}
	record.WithLock(func(one *task.Record) {
		start := one.EventCursor
		if start < 0 || start > len(events) {
			start = len(events)
		}
		if advance {
			for _, ev := range events[start:] {
				output.Log += delegateEventLogLine(ev)
			}
			one.EventCursor = len(events)
		}
		one.State = runtimeTaskState(state.Status)
		one.Running = state.Status == RunLifecycleStatusRunning || state.Status == RunLifecycleStatusWaitingApproval
		one.UpdatedAt = time.Now()
		one.Result = map[string]any{
			"child_session_id": c.sessionID,
			"delegation_id":    c.delegationID,
			"assistant":        assistant,
			"summary":          assistant,
			"state":            string(one.State),
		}
		if preview := delegatePreviewFromEvents(events); preview != "" {
			one.Result["latest_output"] = preview
		}
		snapshot = one.LockedSnapshot(output)
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

func delegatePreviewFromEvents(events []*session.Event) string {
	lines := make([]string, 0, 8)
	inFence := false
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if reasoning := strings.TrimSpace(ev.Message.Reasoning); reasoning != "" {
			for _, line := range strings.Split(reasoning, "\n") {
				line = delegatePreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, "· "+line)
			}
		}
		if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
			for _, line := range strings.Split(text, "\n") {
				line = delegatePreviewLine(line, &inFence)
				if line == "" {
					continue
				}
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}

func delegatePreviewLine(line string, inFence *bool) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		if inFence != nil {
			*inFence = !*inFence
		}
		return ""
	}
	if inFence != nil && *inFence {
		return ""
	}
	return trimmed
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

func runtimeTaskState(status RunLifecycleStatus) task.State {
	switch status {
	case RunLifecycleStatusCompleted:
		return task.StateCompleted
	case RunLifecycleStatusFailed:
		return task.StateFailed
	case RunLifecycleStatusInterrupted:
		return task.StateInterrupted
	case RunLifecycleStatusWaitingApproval:
		return task.StateWaitingApproval
	default:
		return task.StateRunning
	}
}

func delegateEventLogLine(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	var b strings.Builder
	if reasoning := strings.TrimSpace(ev.Message.Reasoning); reasoning != "" {
		b.WriteString(reasoning)
		b.WriteByte('\n')
	}
	if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String()
}

func persistedFinalTaskSnapshot(record *task.Record) (task.Snapshot, bool) {
	if record == nil {
		return task.Snapshot{}, false
	}
	var (
		snapshot task.Snapshot
		ok       bool
	)
	record.WithLock(func(one *task.Record) {
		if one == nil || one.Running {
			return
		}
		switch one.State {
		case task.StateCompleted, task.StateFailed, task.StateCancelled, task.StateInterrupted, task.StateTerminated:
			snapshot = one.LockedSnapshot(task.Output{})
			ok = true
		}
	})
	return snapshot, ok
}
