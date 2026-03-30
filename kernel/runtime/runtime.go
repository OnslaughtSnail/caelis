package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// Config configures Runtime.
type Config struct {
	LogStore   session.LogStore
	StateStore session.StateStore
	TaskStore  task.Store
	Compaction CompactionConfig
}

// Runtime orchestrates session lifecycle and agent execution.
type Runtime struct {
	logStore           session.LogStore
	stateStore         session.StateStore
	stateUpdater       session.StateUpdateStore
	taskStore          task.Store
	taskRegistry       *task.Registry
	compaction         CompactionConfig
	compactionStrategy CompactionStrategy
	runMu              sync.Mutex
	activeRuns         map[string]struct{}
}

type SubagentRunnerFactory func(*Runtime, *session.Session, RunRequest) agent.SubagentRunner

func newRuntime(cfg Config) (*Runtime, error) {
	if cfg.LogStore == nil {
		return nil, fmt.Errorf("runtime: log store is nil")
	}
	if cfg.StateStore == nil {
		return nil, fmt.Errorf("runtime: state store is nil")
	}
	compactionCfg := normalizeCompactionConfig(cfg.Compaction)
	strategy := compactionCfg.Strategy
	if strategy == nil {
		strategy = DefaultCompactionStrategy()
	}
	updater, _ := cfg.StateStore.(session.StateUpdateStore)
	return &Runtime{
		logStore:           cfg.LogStore,
		stateStore:         cfg.StateStore,
		stateUpdater:       updater,
		taskStore:          cfg.TaskStore,
		taskRegistry:       task.NewRegistry(task.RegistryConfig{}),
		compaction:         compactionCfg,
		compactionStrategy: strategy,
		activeRuns:         map[string]struct{}{},
	}, nil
}

func New(cfg Config) (*Runtime, error) {
	return newRuntime(cfg)
}

// RunRequest defines one invocation input.
// RunRequest describes one agent turn. Fields are logically grouped:
//   - Identity: AppName, UserID, SessionID
//   - Input: Input, ContentParts
//   - Capabilities: Agent, Model, Tools, CoreTools, Policies, ContextWindowTokens, SubagentRunnerFactory
type RunRequest struct {
	// Identity
	AppName   string
	UserID    string
	SessionID string

	// Input
	Input string
	// ContentParts carries multimodal content (e.g. images) alongside Input.
	// When non-empty, the runtime builds a user message with these parts
	// instead of using Input as plain text.
	ContentParts []model.ContentPart

	// Capabilities
	Agent                 agent.Agent
	Model                 model.LLM
	Tools                 []tool.Tool
	CoreTools             tool.CoreToolsConfig
	Policies              []policy.Hook
	ContextWindowTokens   int
	SubagentRunnerFactory SubagentRunnerFactory
}

func validateRunRequest(req RunRequest) error {
	if err := validateRunIdentity(req); err != nil {
		return err
	}
	return validateRunCapabilities(req)
}

func validateRunIdentity(req RunRequest) error {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	return nil
}

func validateRunCapabilities(req RunRequest) error {
	if req.Agent == nil {
		return fmt.Errorf("runtime: agent is nil")
	}
	if req.Model == nil {
		return fmt.Errorf("runtime: model is nil")
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context, req RunRequest) (Runner, error) {
	return r.newRunner(ctx, req)
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
	subagentRunner := r.newSubagentRunner(sess, req)
	coreTools := req.CoreTools
	taskManager := newTaskManager(
		r,
		coreTools.Runtime,
		r.resolveTaskRegistry(coreTools.TaskRegistry),
		r.taskStore,
		&sessionContext{appName: req.AppName, userID: req.UserID, sessionID: sess.ID},
		req,
		subagentRunner,
	)
	ctx = task.WithManager(ctx, taskManager)
	ctx = session.WithStoresContext(ctx, sess, r.logStore, r.stateStore)
	builtins, err := tool.BuildCoreTools(coreTools)
	if err != nil {
		return nil, fmt.Errorf("runtime: build core tools: %w", err)
	}
	allTools, err := tool.EnsureCoreTools(req.Tools, builtins)
	if err != nil {
		return nil, fmt.Errorf("runtime: merge tools: %w", err)
	}
	toolMap, err := tool.BuildMap(allTools)
	if err != nil {
		return nil, fmt.Errorf("runtime: build tool map: %w", err)
	}
	state, err := r.snapshotReadonlyState(ctx, sess)
	if err != nil {
		return nil, fmt.Errorf("runtime: snapshot readonly state: %w", err)
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

func (r *Runtime) newSubagentRunner(sess *session.Session, req RunRequest) agent.SubagentRunner {
	if req.SubagentRunnerFactory != nil {
		if runner := req.SubagentRunnerFactory(r, sess, req); runner != nil {
			return runner
		}
	}
	return newSubagentRunner(r, sess, req)
}

func (r *Runtime) projectInvocationEvents(allEvents []*session.Event) session.Events {
	return session.InvocationView(allEvents)
}

func (r *Runtime) snapshotReadonlyState(ctx context.Context, sess *session.Session) (session.ReadonlyState, error) {
	values, err := r.stateStore.SnapshotState(ctx, sess)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return session.NewReadonlyState(nil), nil
		}
		return nil, err
	}
	return session.NewReadonlyState(values), nil
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
		return nil, fmt.Errorf("runtime: context is required")
	}
	if req.Model == nil {
		return nil, fmt.Errorf("runtime: model is nil")
	}
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess, err := r.logStore.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
	if err != nil {
		return nil, err
	}
	ctx = withRequestTraceContext(ctx, r.logStore, sess, "")
	req.Model = model.WrapRequestTrace(req.Model)
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

func shouldPersistEvent(ev *session.Event) bool {
	return session.IsCanonicalHistoryEvent(ev)
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
		if err := r.mergeLifecycleStateSnapshot(ctx, sess, runStateSnapshot(state)); err != nil {
			return yield(nil, err)
		}
	}
	if !yield(ev, nil) {
		return false
	}
	return true
}

func (r *Runtime) mergeLifecycleStateSnapshot(ctx context.Context, sess *session.Session, snapshot map[string]any) error {
	if r == nil || sess == nil || len(snapshot) == 0 {
		return nil
	}
	if r.stateUpdater != nil {
		if err := r.stateUpdater.UpdateState(ctx, sess, func(existing map[string]any) (map[string]any, error) {
			if existing == nil {
				existing = map[string]any{}
			}
			for k, v := range snapshot {
				existing[k] = v
			}
			return existing, nil
		}); err != nil {
			return fmt.Errorf("lifecycle state merge: failed to update state: %w", err)
		}
		return nil
	}
	existing, err := r.stateStore.SnapshotState(ctx, sess)
	if err != nil {
		return fmt.Errorf("lifecycle state merge: failed to read existing state: %w", err)
	}
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range snapshot {
		existing[k] = v
	}
	if err := r.stateStore.ReplaceState(ctx, sess, existing); err != nil {
		return fmt.Errorf("lifecycle state merge: failed to replace state: %w", err)
	}
	return nil
}
