package runtime

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// Config configures Runtime.
type Config struct {
	Store      session.Store
	Compaction CompactionConfig
}

// Runtime orchestrates session lifecycle and agent execution.
type Runtime struct {
	store              session.Store
	compaction         CompactionConfig
	compactionStrategy CompactionStrategy
	runMu              sync.Mutex
	activeRuns         map[string]struct{}
	runStateMu         sync.RWMutex
	runStates          map[string]RunState
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
		compaction:         compactionCfg,
		compactionStrategy: strategy,
		activeRuns:         map[string]struct{}{},
		runStates:          map[string]RunState{},
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
		if err := validateRunRequest(req); err != nil {
			yield(nil, err)
			return
		}
		leaseKey := runLeaseKey(req.AppName, req.UserID, req.SessionID)
		if !r.acquireRunLease(leaseKey) {
			yield(nil, &SessionBusyError{AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID})
			return
		}
		defer r.releaseRunLease(leaseKey)

		sess, err := r.store.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
		if err != nil {
			yield(nil, err)
			return
		}
		if !r.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusRunning, "run", nil, yield) {
			return
		}
		emitRunError := func(err error) bool {
			if err == nil {
				return true
			}
			status := lifecycleStatusForError(err)
			if !r.appendAndYieldLifecycle(ctx, sess, status, "run", err, yield) {
				return false
			}
			return yield(nil, err)
		}

		allEvents, ok := r.prepareRunContext(ctx, sess, req, emitRunError, yield)
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
		if !r.runAgentExecution(ctx, sess, req, inv, emitRunError, yield) {
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
		recoveryEvent.SessionID = sess.ID
		if recoveryEvent.ID == "" {
			recoveryEvent.ID = eventID()
		}
		if recoveryEvent.Time.IsZero() {
			recoveryEvent.Time = time.Now()
		}
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
		parts := make([]model.ContentPart, 0, 1+len(req.ContentParts))
		if strings.TrimSpace(req.Input) != "" {
			parts = append(parts, model.ContentPart{
				Type: model.ContentPartText,
				Text: req.Input,
			})
		}
		parts = append(parts, req.ContentParts...)
		userMsg.ContentParts = parts
	}
	userEvent := &session.Event{
		ID:        eventID(),
		SessionID: sess.ID,
		Time:      time.Now(),
		Message:   userMsg,
	}
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
	compactionEvent, compactErr := r.compactIfNeeded(ctx, compactInput{
		Session:             sess,
		Model:               req.Model,
		Events:              allEvents,
		ContextWindowTokens: req.ContextWindowTokens,
		Trigger:             triggerAuto,
		Force:               false,
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

func (r *Runtime) buildInvocationContext(
	ctx context.Context,
	sess *session.Session,
	req RunRequest,
	allEvents []*session.Event,
) (*invocationContext, error) {
	history := agentHistoryEvents(contextWindowEvents(allEvents))
	allTools, err := tool.EnsureCoreTools(req.Tools, req.CoreTools)
	if err != nil {
		return nil, err
	}
	toolMap, err := tool.BuildMap(allTools)
	if err != nil {
		return nil, err
	}
	return &invocationContext{
		Context:  ctx,
		session:  sess,
		history:  history,
		model:    req.Model,
		tools:    allTools,
		toolMap:  toolMap,
		policies: append([]policy.Hook(nil), req.Policies...),
	}, nil
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
					compactionEvent, compactErr := r.compactIfNeeded(ctx, compactInput{
						Session:             sess,
						Model:               req.Model,
						Events:              allEvents,
						ContextWindowTokens: req.ContextWindowTokens,
						Trigger:             triggerOverflowRecovery,
						Force:               true,
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
					refreshed, refreshErr := r.listContextWindowEvents(ctx, sess)
					if refreshErr != nil {
						emitRunError(refreshErr)
						return false
					}
					inv.history = agentHistoryEvents(contextWindowEvents(refreshed))
					retry = true
					break
				}
				emitRunError(err)
				return false
			}
			if ev == nil {
				continue
			}
			if ev.ID == "" {
				ev.ID = eventID()
			}
			if ev.Time.IsZero() {
				ev.Time = time.Now()
			}
			ev.SessionID = sess.ID
			if shouldPersistEvent(ev, req.PersistPartialEvents) {
				if err := r.store.AppendEvent(ctx, sess, ev); err != nil {
					emitRunError(err)
					return false
				}
				cp := *ev
				if !isLifecycleEvent(&cp) {
					inv.history = append(inv.history, &cp)
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

func (r *Runtime) appendAndYieldLifecycle(
	ctx context.Context,
	sess *session.Session,
	status RunLifecycleStatus,
	phase string,
	cause error,
	yield func(*session.Event, error) bool,
) bool {
	_ = ctx
	if r == nil || sess == nil {
		return true
	}
	ev := lifecycleEvent(sess, status, phase, cause)
	r.updateCachedRunState(sess, ev)
	if !yield(ev, nil) {
		return false
	}
	return true
}
