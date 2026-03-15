package runtime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/eventview"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// Config configures Runtime.
type Config struct {
	Store      session.Store
	TaskStore  task.Store
	Compaction CompactionConfig
}

// Runtime orchestrates session lifecycle and agent execution.
type Runtime struct {
	store              session.Store
	taskStore          task.Store
	taskRegistry       *task.Registry
	compaction         CompactionConfig
	compactionStrategy CompactionStrategy
	runMu              sync.Mutex
	activeRuns         map[string]struct{}
}

func New(cfg Config) (*Runtime, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("runtime: store is nil")
	}
	compactionCfg := normalizeCompactionConfig(cfg.Compaction)
	strategy := compactionCfg.Strategy
	if strategy == nil {
		strategy = DefaultCompactionStrategy()
	}
	return &Runtime{
		store:              cfg.Store,
		taskStore:          cfg.TaskStore,
		taskRegistry:       task.NewRegistry(task.RegistryConfig{}),
		compaction:         compactionCfg,
		compactionStrategy: strategy,
		activeRuns:         map[string]struct{}{},
	}, nil
}

// RunRequest defines one invocation input.
type RunRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Input     string

	// ContentParts carries multimodal content (e.g. images) alongside Input.
	// When non-empty, the runtime builds a user message with these parts
	// instead of using Input as plain text.
	ContentParts []model.ContentPart

	Agent                agent.Agent
	Model                model.LLM
	Tools                []tool.Tool
	CoreTools            tool.CoreToolsConfig
	Policies             []policy.Hook
	PersistPartialEvents bool
	ContextWindowTokens  int
}

type runErrorEmitter func(error) bool
type runEventYielder func(*session.Event, error) bool

func validateRunRequest(req RunRequest) error {
	if req.Agent == nil {
		return fmt.Errorf("runtime: agent is nil")
	}
	if req.Model == nil {
		return fmt.Errorf("runtime: model is nil")
	}
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context, req RunRequest) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if ctx == nil {
			ctx = context.Background()
		}
		yieldWithStream := func(ev *session.Event, err error) bool {
			if ev != nil {
				sessionstream.Emit(ctx, ev.SessionID, ev)
			}
			return yield(ev, err)
		}
		if err := validateRunRequest(req); err != nil {
			yieldWithStream(nil, err)
			return
		}
		leaseKey := runLeaseKey(req.AppName, req.UserID, req.SessionID)
		if !r.acquireRunLease(leaseKey) {
			yieldWithStream(nil, &SessionBusyError{AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID})
			return
		}
		defer r.releaseRunLease(leaseKey)

		if _, err := r.ReconcileSession(ctx, ReconcileSessionRequest{
			AppName:     req.AppName,
			UserID:      req.UserID,
			SessionID:   req.SessionID,
			ExecRuntime: req.CoreTools.Runtime,
		}); err != nil {
			yieldWithStream(nil, err)
			return
		}

		sess, err := r.store.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
		if err != nil {
			yieldWithStream(nil, err)
			return
		}
		if !r.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusRunning, "run", nil, yieldWithStream) {
			return
		}
		emitRunError := func(err error) bool {
			if err == nil {
				return true
			}
			status := lifecycleStatusForError(err)
			if !r.appendAndYieldLifecycle(ctx, sess, status, "run", err, yieldWithStream) {
				return false
			}
			return yieldWithStream(nil, err)
		}

		allEvents, ok := r.prepareRunContext(ctx, sess, req, emitRunError, yieldWithStream)
		if !ok {
			return
		}
		inv, err := r.buildInvocationContext(ctx, sess, req, allEvents)
		if err != nil {
			if !emitRunError(err) {
				return
			}
			return
		}
		defer func() {
			if inv == nil || inv.tasks == nil {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			inv.tasks.cleanupTurn(cleanupCtx)
		}()
		if !r.runAgentExecution(ctx, sess, req, inv, emitRunError, yieldWithStream) {
			return
		}
	}
}

func (r *Runtime) prepareRunContext(
	ctx context.Context,
	sess *session.Session,
	req RunRequest,
	emitRunError runErrorEmitter,
	yield runEventYielder,
) ([]*session.Event, bool) {
	existing, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		emitRunError(err)
		return nil, false
	}
	recoveryEvents := buildRecoveryEvents(existing)
	for _, recoveryEvent := range recoveryEvents {
		if recoveryEvent == nil {
			continue
		}
		prepareEvent(ctx, sess, recoveryEvent)
		if err := r.store.AppendEvent(ctx, sess, recoveryEvent); err != nil {
			emitRunError(err)
			return nil, false
		}
		if !yield(recoveryEvent, nil) {
			return nil, false
		}
	}
	userMsg := model.Message{Role: model.RoleUser, Text: req.Input}
	if len(req.ContentParts) > 0 {
		userMsg.ContentParts = prepareUserContentParts(req.Input, req.ContentParts)
	}
	userEvent := &session.Event{
		Message: userMsg,
	}
	prepareEvent(ctx, sess, userEvent)
	if err := r.store.AppendEvent(ctx, sess, userEvent); err != nil {
		emitRunError(err)
		return nil, false
	}
	if !yield(userEvent, nil) {
		return nil, false
	}
	allEvents, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		emitRunError(err)
		return nil, false
	}
	compactionEvent, compactErr := r.compactIfNeededWithNotify(ctx, compactInput{
		Session:             sess,
		Model:               req.Model,
		Events:              allEvents,
		ContextWindowTokens: req.ContextWindowTokens,
		Trigger:             triggerAuto,
		Force:               false,
	}, func(ev *session.Event) bool {
		if ev == nil {
			return true
		}
		return yield(ev, nil)
	})
	if compactErr != nil {
		emitRunError(compactErr)
		return nil, false
	}
	if compactionEvent != nil {
		if !yield(compactionEvent, nil) {
			return nil, false
		}
		allEvents, err = r.listContextWindowEvents(ctx, sess)
		if err != nil {
			emitRunError(err)
			return nil, false
		}
	}
	return allEvents, true
}

func prepareUserContentParts(input string, parts []model.ContentPart) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	prepared := append([]model.ContentPart(nil), parts...)
	if strings.TrimSpace(input) == "" || contentPartsContainText(prepared) {
		return prepared
	}
	return append([]model.ContentPart{{
		Type: model.ContentPartText,
		Text: input,
	}}, prepared...)
}

func contentPartsContainText(parts []model.ContentPart) bool {
	for _, part := range parts {
		if part.Type == model.ContentPartText && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

func (r *Runtime) buildInvocationContext(
	ctx context.Context,
	sess *session.Session,
	req RunRequest,
	allEvents []*session.Event,
) (*invocationContext, error) {
	subagentRunner := newSubagentRunner(r, sess, req)
	runnerImpl, _ := subagentRunner.(*runtimeSubagentRunner)
	coreTools := req.CoreTools
	if _, delegated := delegationLineageFromContext(ctx); delegated {
		coreTools.DisableDelegate = true
	}
	taskManager := newTaskManager(
		r,
		coreTools.Runtime,
		r.resolveTaskRegistry(coreTools.TaskRegistry),
		r.taskStore,
		&sessionContext{appName: req.AppName, userID: req.UserID, sessionID: sess.ID},
		req,
		runnerImpl,
	)
	ctx = task.WithManager(ctx, taskManager)
	ctx = session.WithStateContext(ctx, sess, r.store)
	allTools, err := tool.EnsureCoreTools(req.Tools, coreTools)
	if err != nil {
		return nil, err
	}
	toolMap, err := tool.BuildMap(allTools)
	if err != nil {
		return nil, err
	}
	state, err := r.snapshotReadonlyState(ctx, sess)
	if err != nil {
		return nil, err
	}
	return &invocationContext{
		Context:  ctx,
		session:  sess,
		events:   r.projectInvocationEvents(allEvents),
		state:    state,
		model:    req.Model,
		tools:    allTools,
		toolMap:  toolMap,
		policies: append([]policy.Hook(nil), req.Policies...),
		runner:   subagentRunner,
		tasks:    taskManager,
	}, nil
}

func (r *Runtime) projectInvocationEvents(allEvents []*session.Event) session.Events {
	return eventview.AgentVisibleView(allEvents)
}

func (r *Runtime) snapshotReadonlyState(ctx context.Context, sess *session.Session) (session.ReadonlyState, error) {
	values, err := r.store.SnapshotState(ctx, sess)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return session.NewReadonlyState(nil), nil
		}
		return nil, err
	}
	return session.NewReadonlyState(values), nil
}

func (r *Runtime) runAgentExecution(
	ctx context.Context,
	sess *session.Session,
	req RunRequest,
	inv *invocationContext,
	emitRunError runErrorEmitter,
	yield runEventYielder,
) bool {
	for attempt := 0; attempt < 2; attempt++ {
		retry := false
		for ev, err := range req.Agent.Run(inv) {
			if err != nil {
				if attempt == 0 && isContextOverflowError(err) {
					allEvents, listErr := r.listContextWindowEvents(ctx, sess)
					if listErr != nil {
						emitRunError(listErr)
						return false
					}
					compactionEvent, compactErr := r.compactIfNeededWithNotify(ctx, compactInput{
						Session:             sess,
						Model:               req.Model,
						Events:              allEvents,
						ContextWindowTokens: req.ContextWindowTokens,
						Trigger:             triggerOverflowRecovery,
						Force:               true,
					}, func(ev *session.Event) bool {
						if ev == nil {
							return true
						}
						return yield(ev, nil)
					})
					if compactErr != nil {
						emitRunError(compactErr)
						return false
					}
					if compactionEvent != nil {
						if !yield(compactionEvent, nil) {
							return false
						}
					}
					if err := r.refreshInvocationState(ctx, sess, inv); err != nil {
						emitRunError(err)
						return false
					}
					retry = true
					break
				}
				emitRunError(err)
				return false
			}
			if ev == nil {
				continue
			}
			prepareEvent(ctx, sess, ev)
			if shouldPersistEvent(ev, req.PersistPartialEvents) {
				if err := r.store.AppendEvent(ctx, sess, ev); err != nil {
					emitRunError(err)
					return false
				}
				if !isLifecycleEvent(ev) {
					if err := r.refreshInvocationState(ctx, sess, inv); err != nil {
						emitRunError(err)
						return false
					}
				}
			}
			if !yield(ev, nil) {
				return false
			}
		}
		if !retry {
			return r.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusCompleted, "run", nil, yield)
		}
	}
	return true
}

func runLeaseKey(appName, userID, sessionID string) string {
	return strings.TrimSpace(appName) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)
}

func (r *Runtime) resolveTaskRegistry(override *task.Registry) *task.Registry {
	if override != nil {
		return override
	}
	if r == nil || r.taskRegistry == nil {
		return task.NewRegistry(task.RegistryConfig{})
	}
	return r.taskRegistry
}

func (r *Runtime) acquireRunLease(key string) bool {
	if r == nil || strings.TrimSpace(key) == "" {
		return false
	}
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.activeRuns == nil {
		r.activeRuns = map[string]struct{}{}
	}
	if _, exists := r.activeRuns[key]; exists {
		return false
	}
	r.activeRuns[key] = struct{}{}
	return true
}

func (r *Runtime) releaseRunLease(key string) {
	if r == nil || strings.TrimSpace(key) == "" {
		return
	}
	r.runMu.Lock()
	defer r.runMu.Unlock()
	delete(r.activeRuns, key)
}

func (r *Runtime) hasActiveRun(appName, userID, sessionID string) bool {
	if r == nil {
		return false
	}
	key := runLeaseKey(appName, userID, sessionID)
	r.runMu.Lock()
	defer r.runMu.Unlock()
	_, ok := r.activeRuns[key]
	return ok
}

// CompactRequest defines one manual compaction call.
type CompactRequest struct {
	AppName             string
	UserID              string
	SessionID           string
	Model               model.LLM
	Note                string
	ContextWindowTokens int
}

// Compact triggers one manual compaction without sending user input to LLM.
func (r *Runtime) Compact(ctx context.Context, req CompactRequest) (*session.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Model == nil {
		return nil, fmt.Errorf("runtime: model is nil")
	}
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess, err := r.store.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
	if err != nil {
		return nil, err
	}
	allEvents, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		return nil, err
	}
	return r.compactIfNeeded(ctx, compactInput{
		Session:             sess,
		Model:               req.Model,
		Events:              allEvents,
		ContextWindowTokens: req.ContextWindowTokens,
		Trigger:             triggerManual,
		Note:                req.Note,
		Force:               true,
	})
}

func shouldPersistEvent(ev *session.Event, persistPartial bool) bool {
	if ev == nil {
		return false
	}
	if session.IsUIOnly(ev) {
		return false
	}
	if isLifecycleEvent(ev) {
		return false
	}
	if persistPartial {
		return true
	}
	if ev.Meta == nil {
		return true
	}
	raw, exists := ev.Meta["partial"]
	if !exists {
		return true
	}
	isPartial, ok := raw.(bool)
	if !ok {
		return true
	}
	return !isPartial
}

func eventID() string {
	return fmt.Sprintf("ev_%d", time.Now().UnixNano())
}

func (r *Runtime) refreshInvocationState(ctx context.Context, sess *session.Session, inv *invocationContext) error {
	if r == nil || sess == nil || inv == nil {
		return nil
	}
	persisted, err := r.listContextWindowEvents(ctx, sess)
	if err != nil {
		return err
	}
	state, err := r.snapshotReadonlyState(ctx, sess)
	if err != nil {
		return err
	}
	inv.events = r.projectInvocationEvents(persisted)
	inv.state = state
	return nil
}

func (r *Runtime) appendAndYieldLifecycle(
	ctx context.Context,
	sess *session.Session,
	status RunLifecycleStatus,
	phase string,
	cause error,
	yield func(*session.Event, error) bool,
) bool {
	if r == nil || sess == nil {
		return true
	}
	ev := lifecycleEvent(sess, status, phase, cause)
	prepareEvent(ctx, sess, ev)
	state, ok := runStateFromLifecycleEvent(ev)
	if ok {
		snapshot := runStateSnapshot(state)
		if updater, ok := r.store.(session.StateUpdateStore); ok {
			if err := updater.UpdateState(ctx, sess, func(existing map[string]any) (map[string]any, error) {
				if existing == nil {
					existing = map[string]any{}
				}
				for k, v := range snapshot {
					existing[k] = v
				}
				return existing, nil
			}); err != nil {
				return yield(nil, fmt.Errorf("lifecycle state merge: failed to update state: %w", err))
			}
		} else {
			// Merge lifecycle data into existing state instead of replacing
			// the entire map, so that unrelated state keys are preserved.
			existing, snapErr := r.store.SnapshotState(ctx, sess)
			if snapErr != nil {
				return yield(nil, fmt.Errorf("lifecycle state merge: failed to read existing state: %w", snapErr))
			}
			if existing == nil {
				existing = map[string]any{}
			}
			for k, v := range snapshot {
				existing[k] = v
			}
			if err := r.store.ReplaceState(ctx, sess, existing); err != nil {
				return yield(nil, err)
			}
		}
	}
	if !yield(ev, nil) {
		return false
	}
	return true
}
