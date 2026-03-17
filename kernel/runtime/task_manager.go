package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/delegation"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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

func (m *runtimeTaskManager) StartBash(ctx context.Context, req task.BashStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Command) == "" {
		return task.Snapshot{}, fmt.Errorf("task: bash command is required")
	}
	if req.Yield < 0 {
		req.Yield = 0
	}
	asyncRunner, ok := asyncBashRunnerForRoute(m.execenv, strings.TrimSpace(req.Route))
	if !ok || asyncRunner == nil {
		return task.Snapshot{}, fmt.Errorf("task: async bash is not supported for route %q", strings.TrimSpace(req.Route))
	}
	sessionID, err := asyncRunner.StartAsync(ctx, toolexec.CommandRequest{
		Command:               req.Command,
		Dir:                   req.Workdir,
		Timeout:               req.Timeout,
		IdleTimeout:           req.IdleTimeout,
		TTY:                   req.TTY,
		EnvOverrides:          req.EnvOverrides,
		SandboxPolicyOverride: req.SandboxPolicyOverride,
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
		req.Yield = 0
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
	startedAt := time.Now()
	snapshot, err := controller.Wait(ctx, record, req.Yield)
	if err != nil {
		return task.Snapshot{}, err
	}
	if snapshot.Result == nil {
		snapshot.Result = map[string]any{}
	}
	snapshot.Result["waited_ms"] = int(time.Since(startedAt).Milliseconds())
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
	current := task.SessionRef{}
	if m != nil && m.parent != nil {
		current = task.SessionRef{
			AppName:   m.parent.appName,
			UserID:    m.parent.userID,
			SessionID: m.parent.sessionID,
		}
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		if !sameTaskSession(record.Session, current) {
			continue
		}
		seen[record.ID] = struct{}{}
		out = append(out, record.Snapshot(task.Output{}))
	}
	if m.store != nil && m.parent != nil {
		entries, err := m.store.ListSession(ctx, current)
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
