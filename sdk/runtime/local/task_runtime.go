package local

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksandbox "github.com/OnslaughtSnail/caelis/sdk/sandbox"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdksubagent "github.com/OnslaughtSnail/caelis/sdk/subagent"
	sdktask "github.com/OnslaughtSnail/caelis/sdk/task"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
	shelltool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/shell"
	spawntool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/spawn"
	tasktool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/task"
)

type taskRuntime struct {
	runtime *Runtime
	store   sdktask.Store

	mu        sync.RWMutex
	tasks     map[string]*bashTask
	subagents map[string]*subagentTask
	order     map[string][]string
	backends  map[sdksandbox.Backend]sdksandbox.Runtime
}

type bashTask struct {
	ref        sdktask.Ref
	sessionRef sdksession.SessionRef
	session    sdksandbox.Session
	command    string
	workdir    string
	title      string
	createdAt  time.Time

	mu           sync.Mutex
	state        sdktask.State
	running      bool
	stdoutCursor int64
	stderrCursor int64
	result       map[string]any
	metadata     map[string]any
}

type subagentTask struct {
	ref        sdktask.Ref
	sessionRef sdksession.SessionRef
	anchor     sdkdelegation.Anchor
	runner     sdksubagent.Runner
	agent      string
	title      string
	prompt     string
	createdAt  time.Time

	mu       sync.Mutex
	state    sdktask.State
	running  bool
	result   map[string]any
	metadata map[string]any
}

func newTaskRuntime(runtime *Runtime, store sdktask.Store) *taskRuntime {
	return &taskRuntime{
		runtime:   runtime,
		store:     store,
		tasks:     map[string]*bashTask{},
		subagents: map[string]*subagentTask{},
		order:     map[string][]string{},
		backends:  map[sdksandbox.Backend]sdksandbox.Runtime{},
	}
}

type runtimeToolContext struct {
	mode              string
	approvalRequester sdkruntime.ApprovalRequester
}

func (r *Runtime) wrapToolsForRuntime(session sdksession.Session, ref sdksession.SessionRef, spec sdkruntime.AgentSpec, toolCtx runtimeToolContext) []sdktool.Tool {
	if len(spec.Tools) == 0 {
		return spec.Tools
	}
	out := make([]sdktool.Tool, 0, len(spec.Tools)+1)
	hasBash := false
	hasSpawn := false
	hasTask := false
	for _, one := range spec.Tools {
		if one == nil {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(one.Definition().Name))
		switch name {
		case shelltool.BashToolName:
			hasBash = true
			if runtime, ok := sandboxRuntimeFromTool(one); ok {
				r.tasks.registerSandboxRuntime(runtime)
			}
			out = append(out, runtimeBashTool{
				base:       one,
				session:    sdksession.CloneSession(session),
				sessionRef: sdksession.NormalizeSessionRef(ref),
				tasks:      r.tasks,
			})
		case spawntool.ToolName:
			hasSpawn = true
			out = append(out, runtimeSpawnTool{
				base:       one,
				session:    sdksession.CloneSession(session),
				sessionRef: sdksession.NormalizeSessionRef(ref),
				tasks:      r.tasks,
				runner:     r.subagents,
				mode:       strings.TrimSpace(toolCtx.mode),
				approval:   toolCtx.approvalRequester,
			})
		case tasktool.ToolName:
			hasTask = true
			out = append(out, runtimeTaskTool{
				base:       one,
				sessionRef: sdksession.NormalizeSessionRef(ref),
				tasks:      r.tasks,
			})
		default:
			out = append(out, one)
		}
	}
	if (hasBash || hasSpawn) && !hasTask {
		out = append(out, runtimeTaskTool{
			base:       tasktool.New(),
			sessionRef: sdksession.NormalizeSessionRef(ref),
			tasks:      r.tasks,
		})
	}
	return out
}

func (tm *taskRuntime) registerSandboxRuntime(runtime sdksandbox.Runtime) {
	if tm == nil || runtime == nil {
		return
	}
	desc := runtime.Describe()
	backend := desc.Backend
	if backend == "" {
		backend = sdksandbox.BackendHost
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.backends[backend] = runtime
}

type runtimeBashTool struct {
	base       sdktool.Tool
	session    sdksession.Session
	sessionRef sdksession.SessionRef
	tasks      *taskRuntime
}

func (t runtimeBashTool) Definition() sdktool.Definition {
	return sdktool.CloneDefinition(t.base.Definition())
}

func (t runtimeBashTool) Call(ctx context.Context, call sdktool.Call) (sdktool.Result, error) {
	runtime, ok := sandboxRuntimeFromTool(t.base)
	if !ok || runtime == nil {
		return t.base.Call(ctx, call)
	}
	args, err := decodeJSONMap(call.Input)
	if err != nil {
		return sdktool.Result{}, err
	}
	command, ok := stringArg(args, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return sdktool.Result{}, fmt.Errorf("tool: arg %q is required", "command")
	}
	workdir, _ := stringArg(args, "workdir")
	if strings.TrimSpace(workdir) == "" && runtime.FileSystem() != nil {
		workdir, _ = runtime.FileSystem().Getwd()
	}
	yieldMS, _ := intArg(args, "yield_time_ms")
	if yieldMS < 0 {
		yieldMS = 0
	}
	req := sdktask.BashStartRequest{
		Command:     strings.TrimSpace(command),
		Workdir:     strings.TrimSpace(workdir),
		Yield:       time.Duration(yieldMS) * time.Millisecond,
		ParentCall:  strings.TrimSpace(call.ID),
		ParentTool:  strings.TrimSpace(call.Name),
		Constraints: constraintsFromMetadata(call.Metadata),
	}
	snapshot, err := t.tasks.StartBash(ctx, t.session, t.sessionRef, runtime, req)
	if err != nil {
		return sdktool.Result{}, err
	}
	return taskSnapshotToolResult(call, t.base.Definition(), snapshot), nil
}

type runtimeSpawnTool struct {
	base       sdktool.Tool
	session    sdksession.Session
	sessionRef sdksession.SessionRef
	tasks      *taskRuntime
	runner     sdksubagent.Runner
	mode       string
	approval   sdkruntime.ApprovalRequester
}

func (t runtimeSpawnTool) Definition() sdktool.Definition {
	return sdktool.CloneDefinition(t.base.Definition())
}

func (t runtimeSpawnTool) Call(ctx context.Context, call sdktool.Call) (sdktool.Result, error) {
	if t.runner == nil {
		return sdktool.Result{}, fmt.Errorf("sdk/runtime/local: subagent runner is unavailable")
	}
	args, err := decodeJSONMap(call.Input)
	if err != nil {
		return sdktool.Result{}, err
	}
	prompt, ok := stringArg(args, "prompt")
	if !ok || strings.TrimSpace(prompt) == "" {
		return sdktool.Result{}, fmt.Errorf("tool: arg %q is required", "prompt")
	}
	agent, _ := stringArg(args, "agent")
	yieldMS, _ := intArg(args, "yield_time_ms")
	if yieldMS < 0 {
		yieldMS = 0
	}
	snapshot, err := t.tasks.StartSubagent(ctx, t.session, t.sessionRef, t.runner, sdktask.SubagentStartRequest{
		Agent:       strings.TrimSpace(agent),
		Prompt:      strings.TrimSpace(prompt),
		YieldTimeMS: yieldMS,
		ParentCall:  strings.TrimSpace(call.ID),
		ParentTool:  strings.TrimSpace(call.Name),
		Mode:        strings.TrimSpace(t.mode),
		Approval:    newSubagentApprovalRequester(t.approval, t.session, t.sessionRef),
	})
	if err != nil {
		return sdktool.Result{}, err
	}
	return taskSnapshotToolResult(call, t.base.Definition(), snapshot), nil
}

type runtimeTaskTool struct {
	base       sdktool.Tool
	sessionRef sdksession.SessionRef
	tasks      *taskRuntime
}

type subagentApprovalRequester struct {
	requester  sdkruntime.ApprovalRequester
	session    sdksession.Session
	sessionRef sdksession.SessionRef
}

func newSubagentApprovalRequester(
	requester sdkruntime.ApprovalRequester,
	session sdksession.Session,
	sessionRef sdksession.SessionRef,
) sdksubagent.ApprovalRequester {
	if requester == nil {
		return nil
	}
	return subagentApprovalRequester{
		requester:  requester,
		session:    sdksession.CloneSession(session),
		sessionRef: sdksession.NormalizeSessionRef(sessionRef),
	}
}

func (r subagentApprovalRequester) RequestSubagentApproval(
	ctx context.Context,
	req sdksubagent.ApprovalRequest,
) (sdksubagent.ApprovalResponse, error) {
	if r.requester == nil {
		return sdksubagent.ApprovalResponse{}, nil
	}
	options := make([]sdksession.ProtocolApprovalOption, 0, len(req.Options))
	for _, item := range req.Options {
		options = append(options, sdksession.ProtocolApprovalOption{
			ID:   strings.TrimSpace(item.ID),
			Name: strings.TrimSpace(item.Name),
			Kind: strings.TrimSpace(item.Kind),
		})
	}
	toolName := strings.TrimSpace(req.ToolCall.Name)
	if toolName == "" {
		toolName = "UNKNOWN"
	}
	resp, err := r.requester.RequestApproval(ctx, sdkruntime.ApprovalRequest{
		SessionRef: r.sessionRef,
		Session:    sdksession.CloneSession(r.session),
		Mode:       strings.TrimSpace(req.Mode),
		Tool: sdktool.Definition{
			Name: toolName,
		},
		Call: sdktool.Call{
			ID:   strings.TrimSpace(req.ToolCall.ID),
			Name: toolName,
		},
		Approval: &sdksession.ProtocolApproval{
			ToolCall: sdksession.ProtocolToolCall{
				ID:     strings.TrimSpace(req.ToolCall.ID),
				Name:   toolName,
				Kind:   strings.TrimSpace(req.ToolCall.Kind),
				Title:  strings.TrimSpace(req.ToolCall.Title),
				Status: strings.TrimSpace(req.ToolCall.Status),
			},
			Options: options,
		},
		Metadata: map[string]any{
			"subagent": true,
			"task_id":  strings.TrimSpace(req.TaskID),
			"agent":    strings.TrimSpace(req.Agent),
		},
	})
	if err != nil {
		return sdksubagent.ApprovalResponse{}, err
	}
	return sdksubagent.ApprovalResponse{
		Outcome:  strings.TrimSpace(resp.Outcome),
		OptionID: strings.TrimSpace(resp.OptionID),
		Approved: resp.Approved,
	}, nil
}

func (t runtimeTaskTool) Definition() sdktool.Definition {
	return sdktool.CloneDefinition(t.base.Definition())
}

func (t runtimeTaskTool) Call(ctx context.Context, call sdktool.Call) (sdktool.Result, error) {
	args, err := decodeJSONMap(call.Input)
	if err != nil {
		return sdktool.Result{}, err
	}
	action, ok := stringArg(args, "action")
	if !ok || strings.TrimSpace(action) == "" {
		return sdktool.Result{}, fmt.Errorf("tool: arg %q is required", "action")
	}
	taskID, ok := stringArg(args, "task_id")
	if !ok || strings.TrimSpace(taskID) == "" {
		return sdktool.Result{}, fmt.Errorf("tool: arg %q is required", "task_id")
	}
	yieldMS, _ := intArg(args, "yield_time_ms")
	if yieldMS < 0 {
		yieldMS = 0
	}
	input, _ := stringArg(args, "input")
	req := sdktask.ControlRequest{
		TaskID: strings.TrimSpace(taskID),
		Yield:  time.Duration(yieldMS) * time.Millisecond,
		Input:  input,
	}
	var snapshot sdktask.Snapshot
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		snapshot, err = t.tasks.Wait(ctx, t.sessionRef, req)
	case "write":
		snapshot, err = t.tasks.Write(ctx, t.sessionRef, req)
	case "cancel":
		snapshot, err = t.tasks.Cancel(ctx, t.sessionRef, req)
	default:
		return sdktool.Result{}, fmt.Errorf("tool: invalid action %q", action)
	}
	if err != nil {
		return sdktool.Result{}, err
	}
	return taskSnapshotToolResult(call, t.base.Definition(), snapshot), nil
}

func (tm *taskRuntime) StartBash(
	ctx context.Context,
	session sdksession.Session,
	ref sdksession.SessionRef,
	runtime sdksandbox.Runtime,
	req sdktask.BashStartRequest,
) (sdktask.Snapshot, error) {
	sandboxReq := sdksandbox.CommandRequest{
		Command: req.Command,
		Dir:     req.Workdir,
	}
	if constraints, ok := req.Constraints.(sdksandbox.Constraints); ok {
		sandboxReq.Constraints = constraints
		sandboxReq.RouteHint = constraints.Route
		sandboxReq.Backend = constraints.Backend
		sandboxReq.Permission = constraints.Permission
	}
	sessionHandle, err := runtime.Start(ctx, sandboxReq)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	now := tm.runtime.now()
	taskID := tm.runtime.nextID("task", nil)
	task := &bashTask{
		ref: sdktask.Ref{
			TaskID:     taskID,
			SessionID:  strings.TrimSpace(sessionHandle.Ref().SessionID),
			TerminalID: strings.TrimSpace(sessionHandle.Terminal().TerminalID),
		},
		sessionRef: sdksession.NormalizeSessionRef(ref),
		session:    sessionHandle,
		command:    strings.TrimSpace(req.Command),
		workdir:    strings.TrimSpace(req.Workdir),
		title:      shelltool.BashToolName + " " + strings.TrimSpace(req.Command),
		createdAt:  now,
		state:      sdktask.StateRunning,
		running:    true,
	}
	tm.mu.Lock()
	tm.tasks[taskID] = task
	sessionID := strings.TrimSpace(ref.SessionID)
	tm.order[sessionID] = append(tm.order[sessionID], taskID)
	tm.mu.Unlock()
	if err := tm.persistTaskEntry(ctx, task.entrySnapshot(tm.runtime.now())); err != nil {
		return sdktask.Snapshot{}, err
	}
	return tm.waitBash(ctx, task, req.Yield)
}

func (tm *taskRuntime) StartSubagent(
	ctx context.Context,
	session sdksession.Session,
	ref sdksession.SessionRef,
	runner sdksubagent.Runner,
	req sdktask.SubagentStartRequest,
) (sdktask.Snapshot, error) {
	if runner == nil {
		return sdktask.Snapshot{}, fmt.Errorf("sdk/runtime/local: subagent runner is required")
	}
	taskID := tm.runtime.nextID("task", nil)
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = strings.TrimSpace(tm.runtime.defaultPolicyMode)
	}
	anchor, result, err := runner.Spawn(ctx, sdksubagent.SpawnContext{
		SessionRef:        sdksession.NormalizeSessionRef(ref),
		Session:           sdksession.CloneSession(session),
		CWD:               strings.TrimSpace(session.CWD),
		TaskID:            taskID,
		Mode:              mode,
		ApprovalRequester: req.Approval,
	}, sdkdelegation.Request{
		Agent:       strings.TrimSpace(req.Agent),
		Prompt:      strings.TrimSpace(req.Prompt),
		YieldTimeMS: req.YieldTimeMS,
	})
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	anchor.TaskID = taskID
	now := tm.runtime.now()
	task := &subagentTask{
		ref: sdktask.Ref{
			TaskID:    taskID,
			SessionID: strings.TrimSpace(anchor.SessionID),
		},
		sessionRef: sdksession.NormalizeSessionRef(ref),
		anchor:     sdkdelegation.CloneAnchor(anchor),
		runner:     runner,
		agent:      strings.TrimSpace(anchor.Agent),
		title:      spawntool.ToolName + " " + strings.TrimSpace(anchor.Agent),
		prompt:     strings.TrimSpace(req.Prompt),
		createdAt:  now,
		state:      taskStateFromDelegation(result.State),
		running:    result.State == sdkdelegation.StateRunning,
	}
	task.applyResult(result)
	tm.mu.Lock()
	tm.subagents[taskID] = task
	sessionID := strings.TrimSpace(ref.SessionID)
	tm.order[sessionID] = append(tm.order[sessionID], taskID)
	tm.mu.Unlock()
	if err := tm.persistTaskEntry(ctx, task.entrySnapshot(tm.runtime.now())); err != nil {
		return sdktask.Snapshot{}, err
	}
	if err := tm.attachSubagentParticipant(ctx, session, task, strings.TrimSpace(req.ParentCall)); err != nil {
		return sdktask.Snapshot{}, err
	}
	return task.snapshot(), nil
}

func (tm *taskRuntime) Wait(ctx context.Context, ref sdksession.SessionRef, req sdktask.ControlRequest) (sdktask.Snapshot, error) {
	if task, err := tm.lookupBash(ref, req.TaskID); err == nil {
		return tm.waitBash(ctx, task, req.Yield)
	}
	task, err := tm.lookupSubagent(ref, req.TaskID)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	return tm.waitSubagent(ctx, task, req.Yield)
}

func (tm *taskRuntime) Write(ctx context.Context, ref sdksession.SessionRef, req sdktask.ControlRequest) (sdktask.Snapshot, error) {
	task, err := tm.lookupBash(ref, req.TaskID)
	if err != nil {
		return sdktask.Snapshot{}, fmt.Errorf("sdk/runtime/local: task %q does not support write", req.TaskID)
	}
	if err := task.session.WriteInput(ctx, []byte(req.Input)); err != nil {
		return sdktask.Snapshot{}, err
	}
	return tm.waitBash(ctx, task, req.Yield)
}

func (tm *taskRuntime) Cancel(ctx context.Context, ref sdksession.SessionRef, req sdktask.ControlRequest) (sdktask.Snapshot, error) {
	if task, err := tm.lookupBash(ref, req.TaskID); err == nil {
		if err := task.session.Terminate(ctx); err != nil {
			return sdktask.Snapshot{}, err
		}
		return tm.waitBash(ctx, task, 10*time.Millisecond)
	}
	task, err := tm.lookupSubagent(ref, req.TaskID)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	return tm.cancelSubagent(ctx, task)
}

func (tm *taskRuntime) waitBash(ctx context.Context, task *bashTask, yield time.Duration) (sdktask.Snapshot, error) {
	if task == nil {
		return sdktask.Snapshot{}, fmt.Errorf("sdk/runtime/local: task is required")
	}
	status, err := task.session.Wait(ctx, yield)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	stdout, stderr, nextStdout, nextStderr, err := task.session.ReadOutput(ctx, task.stdoutCursor, task.stderrCursor)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	result, resultErr := task.session.Result(ctx)

	task.mu.Lock()
	task.stdoutCursor = nextStdout
	task.stderrCursor = nextStderr
	state := stateFromStatus(status)
	task.state = state
	task.running = status.Running
	task.metadata = map[string]any{
		"task_id":        task.ref.TaskID,
		"task_kind":      string(sdktask.KindBash),
		"state":          string(state),
		"running":        status.Running,
		"session_id":     task.ref.SessionID,
		"terminal_id":    task.ref.TerminalID,
		"supports_input": status.SupportsInput,
	}
	if status.Terminal.TerminalID != "" {
		task.metadata["terminal_id"] = status.Terminal.TerminalID
	}
	if status.Running {
		task.result = map[string]any{
			"task_id":         task.ref.TaskID,
			"state":           string(state),
			"output_preview":  taskOutputPreview(stdout, stderr),
			"supports_input":  status.SupportsInput,
			"supports_cancel": true,
		}
		snapshot := task.snapshotLocked(status)
		entry := task.entrySnapshot(tm.runtime.now())
		task.mu.Unlock()
		if err := tm.persistTaskEntry(ctx, entry); err != nil {
			return sdktask.Snapshot{}, err
		}
		return snapshot, nil
	}

	task.result = map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"result":    compactFinalOutput(result.Stdout, result.Stderr),
		"exit_code": result.ExitCode,
		"state":     string(state),
	}
	if resultErr != nil {
		task.result["error"] = strings.TrimSpace(resultErr.Error())
	}
	snapshot := task.snapshotLocked(status)
	entry := task.entrySnapshot(tm.runtime.now())
	task.mu.Unlock()
	if err := tm.persistTaskEntry(ctx, entry); err != nil {
		return sdktask.Snapshot{}, err
	}
	tm.mu.Lock()
	delete(tm.tasks, task.ref.TaskID)
	tm.mu.Unlock()
	return snapshot, nil
}

func (tm *taskRuntime) waitSubagent(ctx context.Context, task *subagentTask, yield time.Duration) (sdktask.Snapshot, error) {
	if task == nil {
		return sdktask.Snapshot{}, fmt.Errorf("sdk/runtime/local: task is required")
	}
	if task.runner == nil {
		task.mu.Lock()
		snapshot := task.snapshot()
		task.mu.Unlock()
		return snapshot, nil
	}
	result, err := task.runner.Wait(ctx, sdkdelegation.CloneAnchor(task.anchor), int(yield/time.Millisecond))
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	task.mu.Lock()
	task.applyResult(result)
	snapshot := task.snapshot()
	entry := task.entrySnapshot(tm.runtime.now())
	task.mu.Unlock()
	if err := tm.persistTaskEntry(ctx, entry); err != nil {
		return sdktask.Snapshot{}, err
	}
	if !snapshot.Running {
		tm.mu.Lock()
		delete(tm.subagents, task.ref.TaskID)
		tm.mu.Unlock()
		_ = tm.updateSubagentParticipant(ctx, task, "updated")
	}
	return snapshot, nil
}

func (tm *taskRuntime) cancelSubagent(ctx context.Context, task *subagentTask) (sdktask.Snapshot, error) {
	if task == nil {
		return sdktask.Snapshot{}, fmt.Errorf("sdk/runtime/local: task is required")
	}
	if task.runner == nil {
		task.mu.Lock()
		task.state = sdktask.StateCancelled
		task.running = false
		snapshot := task.snapshot()
		entry := task.entrySnapshot(tm.runtime.now())
		task.mu.Unlock()
		if err := tm.persistTaskEntry(ctx, entry); err != nil {
			return sdktask.Snapshot{}, err
		}
		return snapshot, nil
	}
	if err := task.runner.Cancel(ctx, sdkdelegation.CloneAnchor(task.anchor)); err != nil {
		return sdktask.Snapshot{}, err
	}
	result, err := task.runner.Wait(ctx, sdkdelegation.CloneAnchor(task.anchor), 10)
	if err != nil {
		return sdktask.Snapshot{}, err
	}
	task.mu.Lock()
	task.applyResult(result)
	task.state = sdktask.StateCancelled
	task.running = false
	snapshot := task.snapshot()
	entry := task.entrySnapshot(tm.runtime.now())
	task.mu.Unlock()
	if err := tm.persistTaskEntry(ctx, entry); err != nil {
		return sdktask.Snapshot{}, err
	}
	tm.mu.Lock()
	delete(tm.subagents, task.ref.TaskID)
	tm.mu.Unlock()
	_ = tm.updateSubagentParticipant(ctx, task, "detached")
	return snapshot, nil
}

func (tm *taskRuntime) lookupBash(ref sdksession.SessionRef, taskID string) (*bashTask, error) {
	tm.mu.RLock()
	task, ok := tm.tasks[strings.TrimSpace(taskID)]
	tm.mu.RUnlock()
	if ok && task != nil {
		if strings.TrimSpace(task.sessionRef.SessionID) != strings.TrimSpace(ref.SessionID) {
			return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
		}
		return task, nil
	}
	if tm.store == nil {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	entry, err := tm.store.Get(context.Background(), strings.TrimSpace(taskID))
	if err != nil || entry == nil {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	if strings.TrimSpace(entry.Session.SessionID) != strings.TrimSpace(ref.SessionID) {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	if entry.Kind != sdktask.KindBash {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	rehydrated, err := tm.rehydrateBashTask(entry)
	if err != nil {
		return nil, err
	}
	tm.mu.Lock()
	tm.tasks[rehydrated.ref.TaskID] = rehydrated
	tm.mu.Unlock()
	return rehydrated, nil
}

func (tm *taskRuntime) lookupSubagent(ref sdksession.SessionRef, taskID string) (*subagentTask, error) {
	tm.mu.RLock()
	task, ok := tm.subagents[strings.TrimSpace(taskID)]
	tm.mu.RUnlock()
	if ok && task != nil {
		if strings.TrimSpace(task.sessionRef.SessionID) != strings.TrimSpace(ref.SessionID) {
			return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
		}
		return task, nil
	}
	if tm.store == nil {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	entry, err := tm.store.Get(context.Background(), strings.TrimSpace(taskID))
	if err != nil || entry == nil {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	if strings.TrimSpace(entry.Session.SessionID) != strings.TrimSpace(ref.SessionID) || entry.Kind != sdktask.KindSubagent {
		return nil, fmt.Errorf("sdk/runtime/local: task %q not found", taskID)
	}
	rehydrated := tm.rehydrateSubagentTask(entry)
	tm.mu.Lock()
	tm.subagents[rehydrated.ref.TaskID] = rehydrated
	tm.mu.Unlock()
	return rehydrated, nil
}

func (t *bashTask) snapshotLocked(status sdksandbox.SessionStatus) sdktask.Snapshot {
	return sdktask.CloneSnapshot(sdktask.Snapshot{
		Ref:            t.ref,
		Kind:           sdktask.KindBash,
		Title:          t.title,
		State:          t.state,
		Running:        t.running,
		SupportsInput:  status.SupportsInput,
		SupportsCancel: true,
		CreatedAt:      t.createdAt,
		UpdatedAt:      status.UpdatedAt,
		StdoutCursor:   t.stdoutCursor,
		StderrCursor:   t.stderrCursor,
		Result:         maps.Clone(t.result),
		Metadata:       maps.Clone(t.metadata),
		Terminal:       status.Terminal,
	})
}

func taskSnapshotToolResult(call sdktool.Call, def sdktool.Definition, snapshot sdktask.Snapshot) sdktool.Result {
	payload := taskToolPayload(snapshot)
	if payload == nil {
		payload = map[string]any{}
	}
	meta := maps.Clone(snapshot.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["tool_name"] = strings.TrimSpace(def.Name)
	meta["tool_call_id"] = strings.TrimSpace(call.ID)
	meta["state"] = string(snapshot.State)
	meta["running"] = snapshot.Running
	meta["task_id"] = snapshot.Ref.TaskID
	if snapshot.Terminal.TerminalID != "" {
		meta["terminal_id"] = snapshot.Terminal.TerminalID
	}
	raw, _ := json.Marshal(payload)
	isError := !snapshot.Running && snapshot.State != sdktask.StateCompleted
	return sdktool.Result{
		ID:      strings.TrimSpace(call.ID),
		Name:    strings.TrimSpace(def.Name),
		IsError: isError,
		Content: []sdkmodel.Part{sdkmodel.NewJSONPart(raw)},
		Meta:    meta,
	}
}

func taskToolPayload(snapshot sdktask.Snapshot) map[string]any {
	payload := map[string]any{
		"task_id": snapshot.Ref.TaskID,
		"state":   string(snapshot.State),
	}
	if snapshot.Running {
		if preview, _ := snapshot.Result["output_preview"].(string); strings.TrimSpace(preview) != "" {
			payload["output_preview"] = strings.TrimSpace(preview)
		}
		if supportsInput, ok := snapshot.Result["supports_input"].(bool); ok {
			payload["supports_input"] = supportsInput
		}
		if supportsCancel, ok := snapshot.Result["supports_cancel"].(bool); ok {
			payload["supports_cancel"] = supportsCancel
		}
		return payload
	}
	if output, _ := snapshot.Result["result"].(string); strings.TrimSpace(output) != "" {
		payload["result"] = strings.TrimSpace(output)
	}
	if exitCode, ok := snapshot.Result["exit_code"]; ok {
		payload["exit_code"] = exitCode
	}
	if errText, _ := snapshot.Result["error"].(string); strings.TrimSpace(errText) != "" {
		payload["error"] = strings.TrimSpace(errText)
	}
	return payload
}

func stateFromStatus(status sdksandbox.SessionStatus) sdktask.State {
	if status.Running {
		return sdktask.StateRunning
	}
	if status.ExitCode == 0 {
		return sdktask.StateCompleted
	}
	if status.ExitCode == -1 {
		return sdktask.StateCancelled
	}
	return sdktask.StateFailed
}

func (tm *taskRuntime) persistTaskEntry(ctx context.Context, entry *sdktask.Entry) error {
	if tm == nil || tm.store == nil || entry == nil {
		return nil
	}
	return tm.store.Upsert(ctx, entry)
}

func (tm *taskRuntime) listSessionEntries(ctx context.Context, ref sdksession.SessionRef) []*sdktask.Entry {
	if tm == nil {
		return nil
	}
	if tm.store != nil {
		listed, err := tm.store.ListSession(ctx, ref)
		if err == nil && len(listed) > 0 {
			out := make([]*sdktask.Entry, 0, len(listed))
			for _, entry := range listed {
				out = append(out, sdktask.CloneEntry(entry))
			}
			return out
		}
	}
	sessionID := strings.TrimSpace(ref.SessionID)
	tm.mu.RLock()
	ids := append([]string(nil), tm.order[sessionID]...)
	tm.mu.RUnlock()
	out := make([]*sdktask.Entry, 0, len(ids))
	for _, taskID := range ids {
		tm.mu.RLock()
		if task, ok := tm.tasks[taskID]; ok && task != nil {
			task.mu.Lock()
			out = append(out, task.entrySnapshot(tm.runtime.now()))
			task.mu.Unlock()
			tm.mu.RUnlock()
			continue
		}
		if task, ok := tm.subagents[taskID]; ok && task != nil {
			task.mu.Lock()
			out = append(out, task.entrySnapshot(tm.runtime.now()))
			task.mu.Unlock()
		}
		tm.mu.RUnlock()
	}
	return out
}

func (tm *taskRuntime) rehydrateBashTask(entry *sdktask.Entry) (*bashTask, error) {
	if entry == nil {
		return nil, fmt.Errorf("sdk/runtime/local: task entry is required")
	}
	task := &bashTask{
		ref: sdktask.Ref{
			TaskID:     strings.TrimSpace(entry.TaskID),
			SessionID:  strings.TrimSpace(entry.Terminal.SessionID),
			TerminalID: strings.TrimSpace(entry.Terminal.TerminalID),
		},
		sessionRef:   sdksession.NormalizeSessionRef(entry.Session),
		command:      taskSpecString(entry.Spec, "command"),
		workdir:      taskSpecString(entry.Spec, "workdir"),
		title:        strings.TrimSpace(entry.Title),
		createdAt:    entry.CreatedAt,
		state:        entry.State,
		running:      entry.Running,
		stdoutCursor: entry.StdoutCursor,
		stderrCursor: entry.StderrCursor,
		result:       maps.Clone(entry.Result),
		metadata:     maps.Clone(entry.Metadata),
	}
	if !entry.Running {
		task.session = completedTaskSession{entry: sdktask.CloneEntry(entry)}
		return task, nil
	}
	backend := entry.Terminal.Backend
	if backend == "" {
		backend = sdksandbox.BackendHost
	}
	tm.mu.RLock()
	runtime := tm.backends[backend]
	tm.mu.RUnlock()
	if runtime == nil {
		task.session = completedTaskSession{entry: sdktask.CloneEntry(entry)}
		task.running = false
		task.state = sdktask.StateInterrupted
		task.result = maps.Clone(entry.Result)
		if task.result == nil {
			task.result = map[string]any{}
		}
		task.result["state"] = string(sdktask.StateInterrupted)
		task.result["error"] = "task interrupted during resume"
		task.result["result"] = "task interrupted during resume"
		return task, nil
	}
	session, err := runtime.OpenSession(strings.TrimSpace(entry.Terminal.SessionID))
	if err != nil {
		task.session = completedTaskSession{entry: sdktask.CloneEntry(entry)}
		task.running = false
		task.state = sdktask.StateInterrupted
		if task.result == nil {
			task.result = map[string]any{}
		}
		task.result["state"] = string(sdktask.StateInterrupted)
		task.result["error"] = "task interrupted during resume"
		task.result["result"] = "task interrupted during resume"
		return task, nil
	}
	task.session = session
	return task, nil
}

func (tm *taskRuntime) rehydrateSubagentTask(entry *sdktask.Entry) *subagentTask {
	if entry == nil {
		return nil
	}
	agent := taskSpecString(entry.Spec, "agent")
	task := &subagentTask{
		ref: sdktask.Ref{
			TaskID:    strings.TrimSpace(entry.TaskID),
			SessionID: taskSpecString(entry.Spec, "session_id"),
		},
		sessionRef: sdksession.NormalizeSessionRef(entry.Session),
		anchor: sdkdelegation.Anchor{
			TaskID:    strings.TrimSpace(entry.TaskID),
			SessionID: taskSpecString(entry.Spec, "session_id"),
			Agent:     agent,
			AgentID:   taskSpecString(entry.Spec, "agent_id"),
		},
		runner:    tm.runtime.subagents,
		agent:     agent,
		title:     strings.TrimSpace(entry.Title),
		prompt:    taskSpecString(entry.Spec, "prompt"),
		createdAt: entry.CreatedAt,
		state:     entry.State,
		running:   entry.Running,
		result:    maps.Clone(entry.Result),
		metadata:  maps.Clone(entry.Metadata),
	}
	if task.runner == nil && task.running {
		task.running = false
		task.state = sdktask.StateInterrupted
		task.result["output_preview"] = "subagent session requires reconnect"
	}
	return task
}

func (tm *taskRuntime) attachSubagentParticipant(ctx context.Context, session sdksession.Session, task *subagentTask, parentCall string) error {
	if tm == nil || tm.runtime == nil || tm.runtime.sessions == nil || task == nil {
		return nil
	}
	_, err := tm.runtime.sessions.PutParticipant(ctx, sdksession.PutParticipantRequest{
		SessionRef: task.sessionRef,
		Binding: sdksession.ParticipantBinding{
			ID:            strings.TrimSpace(task.anchor.AgentID),
			Kind:          sdksession.ParticipantKindSubagent,
			Role:          sdksession.ParticipantRoleDelegated,
			Label:         strings.TrimSpace(task.agent),
			SessionID:     strings.TrimSpace(task.anchor.SessionID),
			Source:        "agent_spawn",
			ParentTurnID:  strings.TrimSpace(parentCall),
			DelegationID:  strings.TrimSpace(task.ref.TaskID),
			AttachedAt:    tm.runtime.now(),
			ControllerRef: strings.TrimSpace(session.Controller.EpochID),
		},
	})
	if err != nil {
		return err
	}
	_, err = tm.runtime.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: task.sessionRef,
		Event: &sdksession.Event{
			Type:       sdksession.EventTypeParticipant,
			Visibility: sdksession.VisibilityUIOnly,
			Time:       tm.runtime.now(),
			Actor: sdksession.ActorRef{
				Kind: sdksession.ActorKindSystem,
				ID:   "spawn",
				Name: "spawn",
			},
			Protocol: &sdksession.EventProtocol{
				Participant: &sdksession.ProtocolParticipant{Action: "attached"},
			},
			Scope: &sdksession.EventScope{
				Participant: sdksession.ParticipantRef{
					ID:           strings.TrimSpace(task.anchor.AgentID),
					Kind:         sdksession.ParticipantKindSubagent,
					Role:         sdksession.ParticipantRoleDelegated,
					DelegationID: strings.TrimSpace(task.ref.TaskID),
				},
			},
			Meta: map[string]any{
				"task_id":    task.ref.TaskID,
				"agent":      task.agent,
				"agent_id":   task.anchor.AgentID,
				"session_id": task.anchor.SessionID,
				"state":      string(task.state),
			},
		},
	})
	return err
}

func (tm *taskRuntime) updateSubagentParticipant(ctx context.Context, task *subagentTask, action string) error {
	if tm == nil || tm.runtime == nil || tm.runtime.sessions == nil || task == nil {
		return nil
	}
	_, err := tm.runtime.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: task.sessionRef,
		Event: &sdksession.Event{
			Type:       sdksession.EventTypeParticipant,
			Visibility: sdksession.VisibilityUIOnly,
			Time:       tm.runtime.now(),
			Actor: sdksession.ActorRef{
				Kind: sdksession.ActorKindSystem,
				ID:   "spawn",
				Name: "spawn",
			},
			Protocol: &sdksession.EventProtocol{
				Participant: &sdksession.ProtocolParticipant{Action: strings.TrimSpace(action)},
			},
			Scope: &sdksession.EventScope{
				Participant: sdksession.ParticipantRef{
					ID:           strings.TrimSpace(task.anchor.AgentID),
					Kind:         sdksession.ParticipantKindSubagent,
					Role:         sdksession.ParticipantRoleDelegated,
					DelegationID: strings.TrimSpace(task.ref.TaskID),
				},
			},
			Meta: map[string]any{
				"task_id":        task.ref.TaskID,
				"agent":          task.agent,
				"agent_id":       task.anchor.AgentID,
				"session_id":     task.anchor.SessionID,
				"state":          string(task.state),
				"output_preview": strings.TrimSpace(taskStringValue(task.result["output_preview"])),
			},
		},
	})
	return err
}

func taskSpecString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, _ := values[key]
	text, _ := raw.(string)
	return strings.TrimSpace(text)
}

func taskStringValue(raw any) string {
	text, _ := raw.(string)
	return strings.TrimSpace(text)
}

func (t *bashTask) entrySnapshot(now time.Time) *sdktask.Entry {
	if t == nil {
		return nil
	}
	return &sdktask.Entry{
		TaskID:         t.ref.TaskID,
		Kind:           sdktask.KindBash,
		Session:        t.sessionRef,
		Title:          t.title,
		State:          t.state,
		Running:        t.running,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      t.createdAt,
		UpdatedAt:      now,
		HeartbeatAt:    now,
		StdoutCursor:   t.stdoutCursor,
		StderrCursor:   t.stderrCursor,
		Spec: map[string]any{
			"command":    t.command,
			"workdir":    t.workdir,
			"session_id": t.ref.SessionID,
		},
		Result:   maps.Clone(t.result),
		Metadata: maps.Clone(t.metadata),
		Terminal: t.session.Terminal(),
	}
}

func (t *subagentTask) applyResult(result sdkdelegation.Result) {
	if t == nil {
		return
	}
	t.state = taskStateFromDelegation(result.State)
	t.running = result.State == sdkdelegation.StateRunning
	if t.result == nil {
		t.result = map[string]any{}
	}
	if t.metadata == nil {
		t.metadata = map[string]any{}
	}
	t.metadata["task_id"] = t.ref.TaskID
	t.metadata["task_kind"] = string(sdktask.KindSubagent)
	t.metadata["agent"] = t.agent
	t.metadata["agent_id"] = t.anchor.AgentID
	t.metadata["session_id"] = t.anchor.SessionID
	t.metadata["state"] = string(t.state)
	if preview := strings.TrimSpace(result.OutputPreview); preview != "" {
		t.result["output_preview"] = preview
	} else if t.result != nil {
		delete(t.result, "output_preview")
	}
	if text := strings.TrimSpace(result.Result); text != "" {
		t.result["result"] = text
	} else if t.result != nil {
		delete(t.result, "result")
	}
	t.result["task_id"] = t.ref.TaskID
	t.result["state"] = string(t.state)
}

func (t *subagentTask) snapshot() sdktask.Snapshot {
	if t == nil {
		return sdktask.Snapshot{}
	}
	return sdktask.CloneSnapshot(sdktask.Snapshot{
		Ref:            t.ref,
		Kind:           sdktask.KindSubagent,
		Title:          t.title,
		State:          t.state,
		Running:        t.running,
		SupportsInput:  false,
		SupportsCancel: true,
		CreatedAt:      t.createdAt,
		UpdatedAt:      time.Now(),
		Result:         maps.Clone(t.result),
		Metadata:       maps.Clone(t.metadata),
	})
}

func (t *subagentTask) entrySnapshot(now time.Time) *sdktask.Entry {
	if t == nil {
		return nil
	}
	return &sdktask.Entry{
		TaskID:         t.ref.TaskID,
		Kind:           sdktask.KindSubagent,
		Session:        t.sessionRef,
		Title:          t.title,
		State:          t.state,
		Running:        t.running,
		SupportsInput:  false,
		SupportsCancel: true,
		CreatedAt:      t.createdAt,
		UpdatedAt:      now,
		HeartbeatAt:    now,
		Spec: map[string]any{
			"agent":      t.agent,
			"prompt":     t.prompt,
			"session_id": t.anchor.SessionID,
			"agent_id":   t.anchor.AgentID,
		},
		Result:   maps.Clone(t.result),
		Metadata: maps.Clone(t.metadata),
	}
}

func taskStateFromDelegation(state sdkdelegation.State) sdktask.State {
	switch state {
	case sdkdelegation.StateCompleted:
		return sdktask.StateCompleted
	case sdkdelegation.StateCancelled:
		return sdktask.StateCancelled
	case sdkdelegation.StateInterrupted:
		return sdktask.StateInterrupted
	case sdkdelegation.StateWaitingApproval:
		return sdktask.StateWaitingApproval
	case sdkdelegation.StateFailed:
		return sdktask.StateFailed
	default:
		return sdktask.StateRunning
	}
}

type completedTaskSession struct {
	entry *sdktask.Entry
}

func (s completedTaskSession) Ref() sdksandbox.SessionRef {
	if s.entry == nil {
		return sdksandbox.SessionRef{}
	}
	return sdksandbox.SessionRef{
		Backend:   s.entry.Terminal.Backend,
		SessionID: s.entry.Terminal.SessionID,
	}
}

func (s completedTaskSession) Terminal() sdksandbox.TerminalRef {
	if s.entry == nil {
		return sdksandbox.TerminalRef{}
	}
	return sdksandbox.CloneTerminalRef(s.entry.Terminal)
}

func (completedTaskSession) WriteInput(ctx context.Context, input []byte) error {
	return fmt.Errorf("sdk/runtime/local: task is not running")
}

func (s completedTaskSession) ReadOutput(ctx context.Context, stdoutMarker, stderrMarker int64) ([]byte, []byte, int64, int64, error) {
	if s.entry == nil || s.entry.Result == nil {
		return nil, nil, 0, 0, nil
	}
	stdout, _ := s.entry.Result["stdout"].(string)
	stderr, _ := s.entry.Result["stderr"].(string)
	if stdoutMarker < 0 {
		stdoutMarker = 0
	}
	if stderrMarker < 0 {
		stderrMarker = 0
	}
	if stdoutMarker > int64(len(stdout)) {
		stdoutMarker = int64(len(stdout))
	}
	if stderrMarker > int64(len(stderr)) {
		stderrMarker = int64(len(stderr))
	}
	return []byte(stdout[stdoutMarker:]), []byte(stderr[stderrMarker:]), int64(len(stdout)), int64(len(stderr)), nil
}

func (s completedTaskSession) Status(context.Context) (sdksandbox.SessionStatus, error) {
	if s.entry == nil {
		return sdksandbox.SessionStatus{}, nil
	}
	exitCode, _ := s.entry.Result["exit_code"].(float64)
	return sdksandbox.SessionStatus{
		SessionRef:    s.Ref(),
		Terminal:      s.Terminal(),
		Running:       false,
		SupportsInput: false,
		ExitCode:      int(exitCode),
		StartedAt:     s.entry.CreatedAt,
		UpdatedAt:     s.entry.UpdatedAt,
	}, nil
}

func (s completedTaskSession) Wait(ctx context.Context, _ time.Duration) (sdksandbox.SessionStatus, error) {
	return s.Status(ctx)
}

func (s completedTaskSession) Result(context.Context) (sdksandbox.CommandResult, error) {
	if s.entry == nil || s.entry.Result == nil {
		return sdksandbox.CommandResult{}, nil
	}
	exitCode, _ := s.entry.Result["exit_code"].(float64)
	stdout, _ := s.entry.Result["stdout"].(string)
	stderr, _ := s.entry.Result["stderr"].(string)
	return sdksandbox.CommandResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: int(exitCode),
		Route:    sdksandbox.RouteHost,
		Backend:  s.entry.Terminal.Backend,
	}, nil
}

func (completedTaskSession) Terminate(context.Context) error { return nil }

func sandboxRuntimeFromTool(tool sdktool.Tool) (sdksandbox.Runtime, bool) {
	provider, ok := tool.(sandboxRuntimeProvider)
	if !ok || provider == nil {
		return nil, false
	}
	runtime := provider.SandboxRuntime()
	if runtime == nil {
		return nil, false
	}
	return runtime, true
}

func constraintsFromMetadata(meta map[string]any) sdksandbox.Constraints {
	if meta == nil {
		return sdksandbox.Constraints{}
	}
	raw, ok := meta["sandbox_constraints"]
	if !ok || raw == nil {
		return sdksandbox.Constraints{}
	}
	if typed, ok := raw.(sdksandbox.Constraints); ok {
		return sdksandbox.NormalizeConstraints(typed)
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return sdksandbox.Constraints{}
	}
	var out sdksandbox.Constraints
	if err := json.Unmarshal(bytes, &out); err != nil {
		return sdksandbox.Constraints{}
	}
	return sdksandbox.NormalizeConstraints(out)
}

func decodeJSONMap(raw []byte) (map[string]any, error) {
	var out map[string]any
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stringArg(values map[string]any, key string) (string, bool) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func intArg(values map[string]any, key string) (int, bool) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch typed := raw.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func taskOutputPreview(stdout, stderr []byte) string {
	lines := make([]string, 0, 8)
	appendLines := func(prefix string, raw []byte) {
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return
		}
		for _, line := range strings.Split(text, "\n") {
			line = compactLine(line)
			if line == "" {
				continue
			}
			lines = append(lines, prefix+line)
		}
	}
	appendLines("", stdout)
	appendLines("stderr: ", stderr)
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "\n")
}

func compactFinalOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout != "" && stderr != "":
		return compactBlock(stdout+"\nstderr:\n"+stderr, 1600)
	case stdout != "":
		return compactBlock(stdout, 1600)
	case stderr != "":
		return compactBlock("stderr:\n"+stderr, 1600)
	default:
		return ""
	}
}

func compactBlock(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 || len(text) <= limit {
		return text
	}
	const marker = "\n...[truncated]...\n"
	head := limit / 2
	tail := limit - head - len(marker)
	if tail < 0 {
		tail = 0
	}
	return text[:head] + marker + text[len(text)-tail:]
}

func compactLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	const limit = 160
	if len(text) <= limit {
		return text
	}
	const marker = " ...[truncated]... "
	head := 70
	tail := limit - head - len(marker)
	if tail < 0 {
		tail = 0
	}
	return text[:head] + marker + text[len(text)-tail:]
}
