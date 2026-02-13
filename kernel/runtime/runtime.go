package runtime

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
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
	}, nil
}

// RunRequest defines one invocation input.
type RunRequest struct {
	AppName   string
	UserID    string
	SessionID string
	Input     string

	Agent                agent.Agent
	Model                model.LLM
	Tools                []tool.Tool
	CoreTools            tool.CoreToolsConfig
	Policies             []policy.Hook
	LSPBroker            *lspbroker.Broker
	LSPActivationTools   []string
	AutoActivateLSP      []string
	PersistPartialEvents bool
	ContextWindowTokens  int
}

func (r *Runtime) Run(ctx context.Context, req RunRequest) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if ctx == nil {
			ctx = context.Background()
		}
		if req.Agent == nil {
			yield(nil, fmt.Errorf("runtime: agent is nil"))
			return
		}
		if req.Model == nil {
			yield(nil, fmt.Errorf("runtime: model is nil"))
			return
		}
		if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
			yield(nil, fmt.Errorf("runtime: app_name, user_id and session_id are required"))
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

		existing, err := r.listContextWindowEvents(ctx, sess)
		if err != nil {
			yield(nil, err)
			return
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
				yield(nil, err)
				return
			}
			if !yield(recoveryEvent, nil) {
				return
			}
		}

		userEvent := &session.Event{
			ID:        eventID(),
			SessionID: sess.ID,
			Time:      time.Now(),
			Message:   model.Message{Role: model.RoleUser, Text: req.Input},
		}
		if err := r.store.AppendEvent(ctx, sess, userEvent); err != nil {
			yield(nil, err)
			return
		}
		if !yield(userEvent, nil) {
			return
		}

		allEvents, err := r.listContextWindowEvents(ctx, sess)
		if err != nil {
			yield(nil, err)
			return
		}

		if r.compaction.Enabled {
			compactionEvent, compactErr := r.compactIfNeeded(ctx, compactInput{
				Session:             sess,
				Model:               req.Model,
				Events:              allEvents,
				ContextWindowTokens: req.ContextWindowTokens,
				Trigger:             triggerAuto,
				Force:               false,
			})
			if compactErr != nil {
				yield(nil, compactErr)
				return
			}
			if compactionEvent != nil {
				if !yield(compactionEvent, nil) {
					return
				}
				allEvents, err = r.listContextWindowEvents(ctx, sess)
				if err != nil {
					yield(nil, err)
					return
				}
			}
		}

		history := agentHistoryEvents(contextWindowEvents(allEvents))
		allTools, err := tool.EnsureCoreTools(req.Tools, req.CoreTools)
		if err != nil {
			yield(nil, err)
			return
		}
		toolMap, err := tool.BuildMap(allTools)
		if err != nil {
			yield(nil, err)
			return
		}
		inv := &invocationContext{
			Context:  ctx,
			session:  sess,
			history:  history,
			model:    req.Model,
			tools:    allTools,
			toolMap:  toolMap,
			policies: append([]policy.Hook(nil), req.Policies...),
			lsp:      req.LSPBroker,
			active:   map[string]struct{}{},
		}
		activateLanguages := mergeActivationLanguages(
			restoreActivatedLSPFromEvents(allEvents, req.LSPActivationTools),
			req.AutoActivateLSP,
		)
		for _, language := range activateLanguages {
			_, activateErr := inv.ActivateLSP(ctx, lspbroker.ActivateRequest{Language: language})
			if activateErr != nil {
				yield(nil, activateErr)
				return
			}
		}

		for attempt := 0; attempt < 2; attempt++ {
			retry := false
			for ev, err := range req.Agent.Run(inv) {
				if err != nil {
					if attempt == 0 && r.compaction.Enabled && isContextOverflowError(err) {
						allEvents, listErr := r.listContextWindowEvents(ctx, sess)
						if listErr != nil {
							yield(nil, listErr)
							return
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
							yield(nil, compactErr)
							return
						}
						if compactionEvent != nil {
							if !yield(compactionEvent, nil) {
								return
							}
						}
						refreshed, refreshErr := r.listContextWindowEvents(ctx, sess)
						if refreshErr != nil {
							yield(nil, refreshErr)
							return
						}
						inv.history = agentHistoryEvents(contextWindowEvents(refreshed))
						retry = true
						break
					}
					status := lifecycleStatusForError(err)
					if !r.appendAndYieldLifecycle(ctx, sess, status, "run", err, yield) {
						return
					}
					yield(nil, err)
					return
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
						yield(nil, err)
						return
					}
					cp := *ev
					if !isLifecycleEvent(&cp) {
						inv.history = append(inv.history, &cp)
					}
				}
				if !yield(ev, nil) {
					return
				}
			}
			if !retry {
				if !r.appendAndYieldLifecycle(ctx, sess, RunLifecycleStatusCompleted, "run", nil, yield) {
					return
				}
				return
			}
		}
	}
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

func restoreActivatedLSPFromEvents(events []*session.Event, activationToolNames []string) []string {
	if len(events) == 0 {
		return nil
	}
	names := activationToolNameSet(activationToolNames)
	if len(names) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 2)
	for _, ev := range events {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		resp := ev.Message.ToolResponse
		name := strings.ToLower(strings.TrimSpace(resp.Name))
		if _, ok := names[name]; !ok {
			continue
		}
		if resp.Result == nil {
			continue
		}
		language, _ := resp.Result["language"].(string)
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "" {
			continue
		}
		if _, exists := seen[language]; exists {
			continue
		}
		seen[language] = struct{}{}
		out = append(out, language)
	}
	return out
}

func activationToolNameSet(names []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, one := range names {
		name := strings.ToLower(strings.TrimSpace(one))
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func mergeActivationLanguages(groups ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, group := range groups {
		for _, one := range group {
			language := strings.ToLower(strings.TrimSpace(one))
			if language == "" {
				continue
			}
			if _, exists := seen[language]; exists {
				continue
			}
			seen[language] = struct{}{}
			out = append(out, language)
		}
	}
	return out
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
	if err := r.store.AppendEvent(ctx, sess, ev); err != nil {
		yield(nil, err)
		return false
	}
	if !yield(ev, nil) {
		return false
	}
	return true
}
