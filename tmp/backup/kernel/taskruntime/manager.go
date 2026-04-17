package taskruntime

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

// Task spec keys persisted into task.Entry.Spec.
const (
	SpecCommand        = "command"
	SpecWorkdir        = "workdir"
	SpecTTY            = "tty"
	SpecRoute          = "route"
	SpecBackend        = "backend"
	SpecExecSessionID  = "exec_session_id"
	SpecChildSession   = "child_session_id"
	SpecDelegationID   = "delegation_id"
	SpecAgent          = "agent"
	SpecChildCWD       = "child_cwd"
	SpecPrompt         = "prompt"
	SpecIdleTimeout    = "idle_timeout_seconds"
	SpecParentToolCall = "parent_tool_call_id"
	SpecParentToolName = "parent_tool_name"
	SpecUISpawnID      = "ui_spawn_id"
	SpecUIAnchorTool   = "ui_anchor_tool"
)

type Config struct {
	ExecRuntime            toolexec.Runtime
	Registry               *task.Registry
	Store                  task.Store
	Session                *task.SessionRef
	Subagents              agent.SubagentRunner
	ContinueSubagentRunner agent.SubagentRunner
	ContinuationAnchorTool string
}

type Manager struct {
	execenv                toolexec.Runtime
	registry               *task.Registry
	store                  task.Store
	session                *task.SessionRef
	subagents              agent.SubagentRunner
	continueSubagentRunner agent.SubagentRunner
	continuationAnchorTool string

	turnMu      sync.Mutex
	turnTaskIDs []string
}

func New(cfg Config) *Manager {
	registry := cfg.Registry
	if registry == nil {
		registry = task.NewRegistry(task.RegistryConfig{})
	}
	var sessionRef *task.SessionRef
	if cfg.Session != nil {
		cloned := *cfg.Session
		sessionRef = &cloned
	}
	return &Manager{
		execenv:                cfg.ExecRuntime,
		registry:               registry,
		store:                  cfg.Store,
		session:                sessionRef,
		subagents:              cfg.Subagents,
		continueSubagentRunner: cfg.ContinueSubagentRunner,
		continuationAnchorTool: strings.TrimSpace(cfg.ContinuationAnchorTool),
	}
}

func (m *Manager) StartBash(ctx context.Context, req task.BashStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Command) == "" {
		return task.Snapshot{}, fmt.Errorf("task: bash command is required")
	}
	if req.Yield < 0 {
		req.Yield = 0
	}
	sessionRef, err := m.execenv.Start(ctx, toolexec.CommandRequest{
		Command:               req.Command,
		Dir:                   req.Workdir,
		Timeout:               req.Timeout,
		IdleTimeout:           req.IdleTimeout,
		TTY:                   req.TTY,
		RouteHint:             toolexec.ExecutionRoute(strings.TrimSpace(req.Route)),
		BackendName:           strings.TrimSpace(req.Backend),
		EnvOverrides:          req.EnvOverrides,
		SandboxPolicyOverride: req.SandboxPolicyOverride,
	})
	if err != nil {
		return task.Snapshot{}, err
	}
	controller := &BashTaskController{
		Session: sessionRef,
		Command: req.Command,
		Workdir: req.Workdir,
		TTY:     req.TTY,
		Route:   strings.TrimSpace(req.Route),
		Backend: strings.TrimSpace(sessionRef.Ref().Backend),
		Store:   m.store,
	}
	record := m.registry.Create(task.KindBash, req.Command, controller, true, true)
	m.TrackTurnTask(record.ID)
	if m.session != nil {
		record.Session = *m.session
	}
	record.Spec = map[string]any{
		SpecCommand:       req.Command,
		SpecWorkdir:       req.Workdir,
		SpecTTY:           req.TTY,
		SpecRoute:         strings.TrimSpace(req.Route),
		SpecBackend:       strings.TrimSpace(sessionRef.Ref().Backend),
		SpecExecSessionID: strings.TrimSpace(sessionRef.Ref().SessionID),
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

func (m *Manager) StartSpawn(ctx context.Context, req task.SpawnStartRequest) (task.Snapshot, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return task.Snapshot{}, fmt.Errorf("task: child prompt is required")
	}
	if m.subagents == nil {
		return task.Snapshot{}, fmt.Errorf("task: child session runtime is unavailable")
	}
	agentName := cmp.Or(strings.TrimSpace(req.Agent), "self")
	if req.Yield < 0 {
		req.Yield = 0
	}
	kind, err := normalizeSpawnTaskKind(req.Kind)
	if err != nil {
		return task.Snapshot{}, err
	}
	record := m.registry.Create(kind, req.Prompt, nil, true, true)
	record.CleanupOnTurnEnd = false
	record.HeartbeatAt = time.Now()
	m.TrackTurnTask(record.ID)
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
	controller := &SubagentTaskController{
		SessionID:              runResult.SessionID,
		DelegationID:           runResult.DelegationID,
		CancelFunc:             cancelSubagentFunc(m.subagents, runResult.SessionID),
		Runner:                 m.subagents,
		ContinueRunner:         m.continueSubagentRunner,
		Store:                  m.store,
		Agent:                  runResult.Agent,
		ChildCWD:               runResult.ChildCWD,
		IdleTimeout:            runResult.IdleTimeout,
		ContinuationAnchorTool: m.continuationAnchorTool,
	}
	record.Backend = controller
	if m.session != nil {
		record.Session = *m.session
	}
	record.Spec = map[string]any{
		SpecPrompt:       req.Prompt,
		SpecChildSession: runResult.SessionID,
		SpecDelegationID: runResult.DelegationID,
		SpecAgent:        runResult.Agent,
		SpecChildCWD:     runResult.ChildCWD,
	}
	if runResult.IdleTimeout > 0 {
		record.Spec[SpecIdleTimeout] = int(runResult.IdleTimeout / time.Second)
	} else if req.IdleTimeout > 0 {
		record.Spec[SpecIdleTimeout] = int(req.IdleTimeout / time.Second)
		controller.IdleTimeout = req.IdleTimeout
	}
	if err := m.persistRecord(ctx, record); err != nil {
		if controller.CancelFunc != nil {
			controller.CancelFunc()
		}
		return task.Snapshot{}, err
	}
	taskstream.Emit(ctx, taskstream.Event{
		Label:  strings.ToUpper(string(kind)),
		TaskID: record.ID,
		CallID: record.ID,
		State:  "running",
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

func (m *Manager) TrackTurnTask(taskID string) {
	if m == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	m.turnTaskIDs = append(m.turnTaskIDs, strings.TrimSpace(taskID))
}

func (m *Manager) CleanupTurn(ctx context.Context) {
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

func (m *Manager) InterruptTurn(ctx context.Context) {
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
		var (
			running        bool
			supportsCancel bool
			kind           task.Kind
		)
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

func (m *Manager) Wait(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
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

func (m *Manager) Write(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
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

func (m *Manager) Cancel(ctx context.Context, req task.ControlRequest) (task.Snapshot, error) {
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

func (m *Manager) List(ctx context.Context) ([]task.Snapshot, error) {
	if m == nil || m.registry == nil {
		return nil, nil
	}
	records := m.registry.List()
	out := make([]task.Snapshot, 0, len(records))
	seen := map[string]struct{}{}
	for _, record := range records {
		if record == nil {
			continue
		}
		if m.session != nil && !SameTaskSession(record.Session, *m.session) {
			continue
		}
		seen[record.ID] = struct{}{}
		out = append(out, record.Snapshot(task.Output{}))
	}
	if m.store != nil && m.session != nil {
		entries, err := m.store.ListSession(ctx, *m.session)
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
	slices.SortFunc(out, func(a, b task.Snapshot) int {
		switch {
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		case a.CreatedAt.Before(b.CreatedAt):
			return 1
		default:
			return cmp.Compare(a.TaskID, b.TaskID)
		}
	})
	return out, nil
}
