package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
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
	subagents agent.SubagentRunner

	turnMu      sync.Mutex
	turnTaskIDs []string
}

type sessionContext struct {
	appName   string
	userID    string
	sessionID string
}

const (
	taskSpecCommand        = "command"
	taskSpecWorkdir        = "workdir"
	taskSpecTTY            = "tty"
	taskSpecRoute          = "route"
	taskSpecExecSessionID  = "exec_session_id"
	taskSpecChildSession   = "child_session_id"
	taskSpecDelegationID   = "delegation_id"
	taskSpecAgent          = "agent"
	taskSpecChildCWD       = "child_cwd"
	taskSpecPrompt         = "prompt"
	taskSpecTimeout        = "timeout_seconds"
	taskSpecIdleTimeout    = "idle_timeout_seconds"
	taskSpecParentToolCall = "parent_tool_call_id"
	taskSpecParentToolName = "parent_tool_name"
	taskSpecUISpawnID      = "ui_spawn_id"
	taskSpecUIAnchorTool   = "ui_anchor_tool"

	// taskSpecLegacyPrompt is the pre-rename key for the spawn prompt text.
	// Kept for backward-compatible reads of persisted task records.
	taskSpecLegacyPrompt = "task"
)

func newTaskManager(r *Runtime, execRuntime toolexec.Runtime, registry *task.Registry, store task.Store, parent *sessionContext, req RunRequest, runner agent.SubagentRunner) *runtimeTaskManager {
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

func (m *runtimeTaskManager) StartSpawn(ctx context.Context, req task.SpawnStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return task.Snapshot{}, fmt.Errorf("task: child prompt is required")
	}
	if m.subagents == nil {
		return task.Snapshot{}, fmt.Errorf("task: child session runtime is unavailable")
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "self"
	}
	if req.Yield < 0 {
		req.Yield = 0
	}
	kind, err := normalizeSpawnTaskKind(req.Kind)
	if err != nil {
		return task.Snapshot{}, err
	}
	label := strings.ToUpper(string(kind))
	record := m.registry.Create(kind, req.Prompt, nil, true, true)
	record.CleanupOnTurnEnd = false
	record.HeartbeatAt = time.Now()
	m.trackTurnTask(record.ID)
	runResult, err := m.subagents.RunSubagent(ctx, agent.SubagentRunRequest{
		Agent:       agentName,
		Prompt:      req.Prompt,
		ChildCWD:    "",
		Parts:       model.CloneParts(req.Parts),
		Yield:       req.Yield,
		Timeout:     req.Timeout,
		IdleTimeout: req.IdleTimeout,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	cancel := cancelSubagentFunc(m.subagents, runResult.SessionID)
	controller := &subagentTaskController{
		runtime:      m.runtime,
		appName:      m.runReq.AppName,
		userID:       m.runReq.UserID,
		sessionID:    runResult.SessionID,
		delegationID: runResult.DelegationID,
		cancel:       cancel,
		runner:       m.subagents,
		store:        m.store,
		agent:        runResult.Agent,
		childCWD:     runResult.ChildCWD,
		timeout:      runResult.Timeout,
		idleTimeout:  runResult.IdleTimeout,
	}
	record.Backend = controller
	record.Session = task.SessionRef{AppName: m.parent.appName, UserID: m.parent.userID, SessionID: m.parent.sessionID}
	record.Spec = map[string]any{
		taskSpecPrompt:       req.Prompt,
		taskSpecChildSession: runResult.SessionID,
		taskSpecDelegationID: runResult.DelegationID,
		taskSpecAgent:        runResult.Agent,
		taskSpecChildCWD:     runResult.ChildCWD,
	}
	if runResult.Timeout > 0 {
		record.Spec[taskSpecTimeout] = int(runResult.Timeout / time.Second)
	}
	if runResult.IdleTimeout > 0 {
		record.Spec[taskSpecIdleTimeout] = int(runResult.IdleTimeout / time.Second)
	} else if req.IdleTimeout > 0 {
		record.Spec[taskSpecIdleTimeout] = int(req.IdleTimeout / time.Second)
		controller.idleTimeout = req.IdleTimeout
	}
	if err := m.persistRecord(ctx, record); err != nil {
		if cancel != nil {
			cancel()
		}
		return task.Snapshot{}, err
	}
	taskstream.Emit(ctx, taskstream.Event{
		Label:  label,
		TaskID: record.ID,
		CallID: record.ID,
		State:  string(RunLifecycleStatusRunning),
		Reset:  true,
	})
	snapshot, err := controller.Wait(ctx, record, 0)
	if err != nil {
		return task.Snapshot{}, err
	}
	snapshot.Yielded = runResult.Yielded || snapshot.Running
	return snapshot, nil
}

func normalizeSpawnTaskKind(kind task.Kind) (task.Kind, error) {
	switch kind {
	case "", task.KindSpawn:
		return task.KindSpawn, nil
	default:
		return "", fmt.Errorf("task: unsupported spawn kind %q", kind)
	}
}

type cancelableSubagentRunner interface {
	CancelSubagent(string) bool
}

func cancelSubagentFunc(runner agent.SubagentRunner, sessionID string) context.CancelFunc {
	cancelable, ok := runner.(cancelableSubagentRunner)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return func() {
		cancelable.CancelSubagent(sessionID)
	}
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
		var running, supportsCancel, cleanupOnTurnEnd bool
		record.WithLock(func(one *task.Record) {
			running = one.Running
			supportsCancel = one.SupportsCancel
			cleanupOnTurnEnd = one.CleanupOnTurnEnd
		})
		if !running || !supportsCancel || !cleanupOnTurnEnd {
			continue
		}
		_, _ = record.Backend.Cancel(ctx, record)
	}
}

func (m *runtimeTaskManager) interruptTurn(ctx context.Context) {
	if m == nil {
		return
	}
	m.turnMu.Lock()
	taskIDs := append([]string(nil), m.turnTaskIDs...)
	m.turnMu.Unlock()
	for _, taskID := range taskIDs {
		record, err := m.ensureRecord(ctx, taskID)
		if err != nil || record == nil || record.Backend == nil {
			continue
		}
		var running, supportsCancel bool
		var kind task.Kind
		record.WithLock(func(one *task.Record) {
			running = one.Running
			supportsCancel = one.SupportsCancel
			kind = one.Kind
		})
		if !running || !supportsCancel || kind != task.KindSpawn {
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
	if m == nil || m.registry == nil {
		return nil, nil
	}
	registry := m.registry
	records := registry.List()
	out := make([]task.Snapshot, 0, len(records))
	seen := map[string]struct{}{}
	current := task.SessionRef{}
	if m.parent != nil {
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
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
