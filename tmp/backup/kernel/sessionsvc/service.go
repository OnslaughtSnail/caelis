package sessionsvc

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runservice"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
)

type WorkspaceRef struct {
	Key string
	CWD string
}

type SessionRef struct {
	AppName      string
	UserID       string
	SessionID    string
	WorkspaceKey string
}

type SessionInfo struct {
	SessionRef
	CWD    string
	Title  string
	Loaded bool
	Exists bool
}

type LoadedSession struct {
	SessionInfo
	Events []*session.Event
	State  map[string]any
}

type StartSessionRequest struct {
	AppName            string
	UserID             string
	Workspace          WorkspaceRef
	PreferredSessionID string
	Mode               string
	Config             map[string]string
}

type LoadSessionRequest struct {
	SessionRef       SessionRef
	CWD              string
	Limit            int
	IncludeLifecycle bool
}

type RunTurnRequest struct {
	SessionRef          SessionRef
	Input               string
	ContentParts        []model.ContentPart
	InvocationPrelude   []model.Message
	ControllerKind      string
	ControllerID        string
	EpochID             string
	Agent               agent.Agent
	Model               model.LLM
	ContextWindowTokens int
	Mode                string
	Config              map[string]string
}

type RunTurnResult struct {
	Session SessionInfo
	Handle  TurnHandle
}

type SessionListRequest struct {
	AppName      string
	UserID       string
	WorkspaceKey string
	Cursor       string
	Limit        int
}

type SessionSummary struct {
	SessionRef
	CWD       string
	Title     string
	UpdatedAt time.Time
}

type SessionList struct {
	Sessions   []SessionSummary
	NextCursor string
}

type DelegationRef struct {
	ParentSessionID  string
	ChildSessionID   string
	DelegationID     string
	ParentToolCallID string
	ParentToolName   string
}

type InterruptSessionRequest struct {
	SessionRef SessionRef
	Reason     string
}

type TurnHandle interface {
	RunID() string
	Events() iter.Seq2[*session.Event, error]
	Submit(runtime.Submission) error
	Cancel() bool
	Close() error
}

type WorkspaceSessionIndex interface {
	ResolveWorkspaceSessionID(ctx context.Context, workspaceKey, prefix string) (string, bool, error)
	MostRecentWorkspaceSessionID(ctx context.Context, workspaceKey, excludeSessionID string) (string, bool, error)
	ListWorkspaceSessionsPage(ctx context.Context, workspaceKey string, page int, pageSize int) ([]SessionSummary, error)
}

type ServiceConfig struct {
	Runtime               *runtime.Runtime
	Store                 session.Store
	AppName               string
	UserID                string
	DefaultAgent          string
	WorkspaceRoot         string
	WorkspaceCWD          string
	Execution             toolexec.Runtime
	Tools                 []tool.Tool
	Policies              []policy.Hook
	TaskRegistry          *task.Registry
	EnablePlan            bool
	EnableSelfSpawn       bool
	Index                 WorkspaceSessionIndex
	SubagentRunnerFactory runtime.SubagentRunnerFactory
}

type Service struct {
	runtime               *runtime.Runtime
	store                 session.Store
	appName               string
	userID                string
	defaultAgent          string
	workspaceRoot         string
	workspaceCWD          string
	execution             toolexec.Runtime
	tools                 []tool.Tool
	policies              []policy.Hook
	taskRegistry          *task.Registry
	enablePlan            bool
	enableSelfSpawn       bool
	index                 WorkspaceSessionIndex
	subagentRunnerFactory runtime.SubagentRunnerFactory

	mu     sync.Mutex
	active map[string]*activeTurn
}

type activeTurn struct {
	cancel context.CancelFunc
	handle TurnHandle
}

type turnHandle struct {
	runner runtime.Runner
	done   func()
	once   sync.Once
}

func New(cfg ServiceConfig) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("sessionsvc: store is required")
	}
	if strings.TrimSpace(cfg.AppName) == "" || strings.TrimSpace(cfg.UserID) == "" {
		return nil, fmt.Errorf("sessionsvc: app_name and user_id are required")
	}
	return &Service{
		runtime:               cfg.Runtime,
		store:                 cfg.Store,
		appName:               strings.TrimSpace(cfg.AppName),
		userID:                strings.TrimSpace(cfg.UserID),
		defaultAgent:          strings.TrimSpace(cfg.DefaultAgent),
		workspaceRoot:         strings.TrimSpace(cfg.WorkspaceRoot),
		workspaceCWD:          strings.TrimSpace(cfg.WorkspaceCWD),
		execution:             cfg.Execution,
		tools:                 append([]tool.Tool(nil), cfg.Tools...),
		policies:              append([]policy.Hook(nil), cfg.Policies...),
		taskRegistry:          cfg.TaskRegistry,
		enablePlan:            cfg.EnablePlan,
		enableSelfSpawn:       cfg.EnableSelfSpawn,
		index:                 cfg.Index,
		subagentRunnerFactory: cfg.SubagentRunnerFactory,
		active:                map[string]*activeTurn{},
	}, nil
}

func (s *Service) StartSession(ctx context.Context, req StartSessionRequest) (SessionInfo, error) {
	if ctx == nil {
		return SessionInfo{}, fmt.Errorf("sessionsvc: context is required")
	}
	ref := s.refFromStart(req)
	if strings.TrimSpace(ref.SessionID) == "" {
		ref.SessionID = idutil.NewSessionID()
	}
	sess, err := s.store.GetOrCreate(ctx, s.sessionFromRef(ref))
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
		SessionRef: SessionRef{
			AppName:      sess.AppName,
			UserID:       sess.UserID,
			SessionID:    sess.ID,
			WorkspaceKey: strings.TrimSpace(req.Workspace.Key),
		},
		CWD:    strings.TrimSpace(req.Workspace.CWD),
		Exists: true,
	}, nil
}

func (s *Service) LoadSession(ctx context.Context, req LoadSessionRequest) (LoadedSession, error) {
	if ctx == nil {
		return LoadedSession{}, fmt.Errorf("sessionsvc: context is required")
	}
	ref := s.normalizeRef(req.SessionRef)
	if err := s.ensureSessionExists(ctx, ref); err != nil {
		return LoadedSession{}, err
	}
	if s.runtime != nil && s.execution != nil {
		if _, err := s.runtime.ReconcileSession(ctx, runtime.ReconcileSessionRequest{
			AppName:     ref.AppName,
			UserID:      ref.UserID,
			SessionID:   ref.SessionID,
			ExecRuntime: s.execution,
		}); err != nil {
			return LoadedSession{}, err
		}
	}
	events, err := s.sessionEvents(ctx, ref, req.Limit, req.IncludeLifecycle)
	if err != nil {
		return LoadedSession{}, err
	}
	state, err := s.store.SnapshotState(ctx, s.sessionFromRef(ref))
	if err != nil && !isSessionNotFound(err) {
		return LoadedSession{}, err
	}
	return LoadedSession{
		SessionInfo: SessionInfo{
			SessionRef: ref,
			CWD:        strings.TrimSpace(req.CWD),
			Loaded:     true,
			Exists:     true,
		},
		Events: events,
		State:  cloneMap(state),
	}, nil
}

func (s *Service) RunTurn(ctx context.Context, req RunTurnRequest) (RunTurnResult, error) {
	if s == nil {
		return RunTurnResult{}, fmt.Errorf("sessionsvc: service is nil")
	}
	if ctx == nil {
		return RunTurnResult{}, fmt.Errorf("sessionsvc: context is required")
	}
	if s.runtime == nil {
		return RunTurnResult{}, fmt.Errorf("sessionsvc: runtime is not configured")
	}
	ref := s.normalizeRef(req.SessionRef)
	if strings.TrimSpace(ref.SessionID) == "" {
		ref.SessionID = idutil.NewSessionID()
	}
	if _, err := s.store.GetOrCreate(ctx, s.sessionFromRef(ref)); err != nil {
		return RunTurnResult{}, err
	}
	if !s.trySetActive(ref.SessionID, &activeTurn{}) {
		return RunTurnResult{}, fmt.Errorf("sessionsvc: session %q already has an active run", ref.SessionID)
	}
	runSvc, err := runservice.New(runservice.ServiceConfig{
		Runtime:               s.runtime,
		AppName:               ref.AppName,
		UserID:                ref.UserID,
		DefaultAgent:          s.defaultAgent,
		WorkspaceRoot:         s.workspaceRoot,
		WorkspaceCWD:          s.workspaceCWD,
		Execution:             s.execution,
		Tools:                 s.tools,
		Policies:              s.policies,
		TaskRegistry:          s.taskRegistry,
		EnablePlan:            s.enablePlan,
		EnableSelfSpawn:       s.enableSelfSpawn,
		SubagentRunnerFactory: s.subagentRunnerFactory,
	})
	if err != nil {
		s.clearActive(ref.SessionID)
		return RunTurnResult{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	runResult, err := runSvc.RunTurn(runCtx, runservice.RunTurnRequest{
		SessionID:           ref.SessionID,
		Input:               req.Input,
		ContentParts:        append([]model.ContentPart(nil), req.ContentParts...),
		InvocationPrelude:   append([]model.Message(nil), req.InvocationPrelude...),
		ControllerKind:      strings.TrimSpace(req.ControllerKind),
		ControllerID:        strings.TrimSpace(req.ControllerID),
		EpochID:             strings.TrimSpace(req.EpochID),
		Agent:               req.Agent,
		Model:               req.Model,
		ContextWindowTokens: req.ContextWindowTokens,
	})
	if err != nil {
		cancel()
		s.clearActive(ref.SessionID)
		return RunTurnResult{}, err
	}
	ref.SessionID = strings.TrimSpace(runResult.SessionID)
	handle := &turnHandle{
		runner: runResult.Runner,
		done: func() {
			cancel()
			s.clearActive(ref.SessionID)
		},
	}
	s.replaceActive(ref.SessionID, &activeTurn{
		cancel: cancel,
		handle: handle,
	})
	return RunTurnResult{
		Session: SessionInfo{
			SessionRef: ref,
			Exists:     true,
		},
		Handle: handle,
	}, nil
}

func (s *Service) SessionState(ctx context.Context, ref SessionRef) (runtime.RunState, error) {
	if s == nil || s.runtime == nil {
		return runtime.RunState{}, fmt.Errorf("sessionsvc: runtime is not configured")
	}
	ref = s.normalizeRef(ref)
	return s.runtime.RunState(ctx, runtime.RunStateRequest{
		AppName:   ref.AppName,
		UserID:    ref.UserID,
		SessionID: ref.SessionID,
	})
}

func (s *Service) SessionEvents(ctx context.Context, ref SessionRef, limit int, includeLifecycle bool) ([]*session.Event, error) {
	ref = s.normalizeRef(ref)
	return s.sessionEvents(ctx, ref, limit, includeLifecycle)
}

func (s *Service) ListDelegations(ctx context.Context, ref SessionRef) ([]DelegationRef, error) {
	ref = s.normalizeRef(ref)
	if err := s.ensureSessionExists(ctx, ref); err != nil {
		return nil, err
	}
	events, err := s.sessionEvents(ctx, ref, 0, true)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]DelegationRef, 0)
	for _, ev := range events {
		item, ok := delegationRefFromParentEvent(ref.SessionID, ev)
		if !ok {
			continue
		}
		key := delegationKey(item.ChildSessionID, item.DelegationID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) ListSessions(ctx context.Context, req SessionListRequest) (SessionList, error) {
	_ = ctx
	if s == nil || s.index == nil {
		return SessionList{Sessions: []SessionSummary{}}, nil
	}
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" {
		return SessionList{}, fmt.Errorf("sessionsvc: workspace_key is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	items, err := s.index.ListWorkspaceSessionsPage(ctx, workspaceKey, 1, 200)
	if err != nil {
		return SessionList{}, err
	}
	start := 0
	cursor := strings.TrimSpace(req.Cursor)
	if cursor != "" {
		for i, item := range items {
			if sessionCursor(item) == cursor {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	resp := SessionList{
		Sessions: append([]SessionSummary(nil), items[start:end]...),
	}
	if end < len(items) && end > start {
		resp.NextCursor = sessionCursor(items[end-1])
	}
	return resp, nil
}

func (s *Service) InterruptSession(ctx context.Context, req InterruptSessionRequest) error {
	_ = ctx
	if s == nil {
		return fmt.Errorf("sessionsvc: service is nil")
	}
	ref := s.normalizeRef(req.SessionRef)
	active := s.activeTurn(ref.SessionID)
	if active == nil {
		return nil
	}
	if active.handle != nil && active.handle.Cancel() {
		return nil
	}
	if active.cancel != nil {
		active.cancel()
	}
	return nil
}

func (s *Service) ResolveWorkspaceSession(ctx context.Context, workspaceKey, prefix string) (SessionRef, bool, error) {
	if s == nil || s.index == nil {
		return SessionRef{}, false, nil
	}
	sessionID, ok, err := s.index.ResolveWorkspaceSessionID(ctx, strings.TrimSpace(workspaceKey), strings.TrimSpace(prefix))
	if err != nil || !ok {
		return SessionRef{}, ok, err
	}
	return SessionRef{
		AppName:      s.appName,
		UserID:       s.userID,
		SessionID:    sessionID,
		WorkspaceKey: strings.TrimSpace(workspaceKey),
	}, true, nil
}

func (s *Service) MostRecentWorkspaceSession(ctx context.Context, workspaceKey, excludeSessionID string) (SessionRef, bool, error) {
	if s == nil || s.index == nil {
		return SessionRef{}, false, nil
	}
	sessionID, ok, err := s.index.MostRecentWorkspaceSessionID(ctx, strings.TrimSpace(workspaceKey), strings.TrimSpace(excludeSessionID))
	if err != nil || !ok {
		return SessionRef{}, ok, err
	}
	return SessionRef{
		AppName:      s.appName,
		UserID:       s.userID,
		SessionID:    sessionID,
		WorkspaceKey: strings.TrimSpace(workspaceKey),
	}, true, nil
}

func (s *Service) VisibleTools() ([]tool.Tool, error) {
	runSvc, err := runservice.New(runservice.ServiceConfig{
		Runtime:               s.runtime,
		AppName:               s.appName,
		UserID:                s.userID,
		DefaultAgent:          s.defaultAgent,
		WorkspaceRoot:         s.workspaceRoot,
		WorkspaceCWD:          s.workspaceCWD,
		Execution:             s.execution,
		Tools:                 s.tools,
		Policies:              s.policies,
		TaskRegistry:          s.taskRegistry,
		EnablePlan:            s.enablePlan,
		EnableSelfSpawn:       s.enableSelfSpawn,
		SubagentRunnerFactory: s.subagentRunnerFactory,
	})
	if err != nil {
		return nil, err
	}
	return runSvc.VisibleTools()
}

func (h *turnHandle) RunID() string {
	if h == nil || h.runner == nil {
		return ""
	}
	return h.runner.RunID()
}

func (h *turnHandle) Events() iter.Seq2[*session.Event, error] {
	if h == nil || h.runner == nil {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, fmt.Errorf("sessionsvc: turn handle is nil"))
		}
	}
	seq := h.runner.Events()
	return func(yield func(*session.Event, error) bool) {
		defer h.finish()
		for ev, err := range seq {
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (h *turnHandle) Submit(sub runtime.Submission) error {
	if h == nil || h.runner == nil {
		return fmt.Errorf("sessionsvc: turn handle is nil")
	}
	return h.runner.Submit(sub)
}

func (h *turnHandle) Cancel() bool {
	if h == nil || h.runner == nil {
		return false
	}
	return h.runner.Cancel()
}

func (h *turnHandle) Close() error {
	if h == nil || h.runner == nil {
		return nil
	}
	err := h.runner.Close()
	h.finish()
	return err
}

func (h *turnHandle) finish() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.done != nil {
			h.done()
		}
	})
}

func (s *Service) replaceActive(sessionID string, active *activeTurn) {
	if s == nil || strings.TrimSpace(sessionID) == "" || active == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[strings.TrimSpace(sessionID)] = active
}

func (s *Service) trySetActive(sessionID string, active *activeTurn) bool {
	if s == nil || strings.TrimSpace(sessionID) == "" || active == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[sessionID] != nil {
		return false
	}
	s.active[sessionID] = active
	return true
}

func (s *Service) clearActive(sessionID string) {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, strings.TrimSpace(sessionID))
}

func (s *Service) activeTurn(sessionID string) *activeTurn {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[strings.TrimSpace(sessionID)]
}

func (s *Service) refFromStart(req StartSessionRequest) SessionRef {
	appName := strings.TrimSpace(req.AppName)
	if appName == "" {
		appName = s.appName
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = s.userID
	}
	return SessionRef{
		AppName:      appName,
		UserID:       userID,
		SessionID:    strings.TrimSpace(req.PreferredSessionID),
		WorkspaceKey: strings.TrimSpace(req.Workspace.Key),
	}
}

func (s *Service) normalizeRef(ref SessionRef) SessionRef {
	if strings.TrimSpace(ref.AppName) == "" {
		ref.AppName = s.appName
	}
	if strings.TrimSpace(ref.UserID) == "" {
		ref.UserID = s.userID
	}
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	ref.WorkspaceKey = strings.TrimSpace(ref.WorkspaceKey)
	return ref
}

func (s *Service) sessionFromRef(ref SessionRef) *session.Session {
	ref = s.normalizeRef(ref)
	return &session.Session{
		AppName: ref.AppName,
		UserID:  ref.UserID,
		ID:      ref.SessionID,
	}
}

func (s *Service) ensureSessionExists(ctx context.Context, ref SessionRef) error {
	sess := s.sessionFromRef(ref)
	if existing, ok := s.store.(session.ExistenceStore); ok {
		found, err := existing.SessionExists(ctx, sess)
		if err != nil {
			return err
		}
		if !found {
			return session.ErrSessionNotFound
		}
		return nil
	}
	_, err := s.store.SnapshotState(ctx, sess)
	if isSessionNotFound(err) {
		return err
	}
	if err == nil {
		return nil
	}
	_, listErr := s.store.ListEvents(ctx, sess)
	return listErr
}

func (s *Service) sessionEvents(ctx context.Context, ref SessionRef, limit int, includeLifecycle bool) ([]*session.Event, error) {
	if ctx == nil {
		return nil, fmt.Errorf("sessionsvc: context is required")
	}
	if s.runtime != nil {
		return s.runtime.SessionEvents(ctx, runtime.SessionEventsRequest{
			AppName:          ref.AppName,
			UserID:           ref.UserID,
			SessionID:        ref.SessionID,
			Limit:            limit,
			IncludeLifecycle: includeLifecycle,
		})
	}
	events, err := s.store.ListEvents(ctx, s.sessionFromRef(ref))
	if err != nil {
		if isSessionNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	filtered := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if !includeLifecycle {
			if _, ok := runtime.LifecycleFromEvent(ev); ok {
				continue
			}
		}
		filtered = append(filtered, ev)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return append([]*session.Event(nil), filtered...), nil
}

func isSessionNotFound(err error) bool {
	return errors.Is(err, session.ErrSessionNotFound)
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sessionCursor(item SessionSummary) string {
	return item.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + strings.TrimSpace(item.SessionID)
}

func delegationKey(childSessionID, delegationID string) string {
	return strings.TrimSpace(childSessionID) + "\x00" + strings.TrimSpace(delegationID)
}

func delegationRefFromParentEvent(parentSessionID string, ev *session.Event) (DelegationRef, bool) {
	if ev == nil {
		return DelegationRef{}, false
	}
	if meta, ok := runtime.DelegationMetadataFromEvent(ev); ok {
		if strings.TrimSpace(meta.ParentSessionID) == strings.TrimSpace(parentSessionID) && strings.TrimSpace(meta.ChildSessionID) != "" {
			return DelegationRef{
				ParentSessionID:  strings.TrimSpace(meta.ParentSessionID),
				ChildSessionID:   strings.TrimSpace(meta.ChildSessionID),
				DelegationID:     strings.TrimSpace(meta.DelegationID),
				ParentToolCallID: strings.TrimSpace(meta.ParentToolCall),
				ParentToolName:   strings.TrimSpace(meta.ParentToolName),
			}, true
		}
	}
	resp := ev.Message.ToolResponse()
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.SpawnToolName) {
		return DelegationRef{}, false
	}
	childSessionID := resultString(resp.Result, "child_session_id")
	if childSessionID == "" {
		return DelegationRef{}, false
	}
	delegationID := resultString(resp.Result, "delegation_id")
	return DelegationRef{
		ParentSessionID:  strings.TrimSpace(parentSessionID),
		ChildSessionID:   childSessionID,
		DelegationID:     delegationID,
		ParentToolCallID: strings.TrimSpace(resp.ID),
		ParentToolName:   strings.TrimSpace(resp.Name),
	}, true
}

func resultString(result map[string]any, key string) string {
	if len(result) == 0 {
		return ""
	}
	raw, ok := result[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text == "<nil>" {
			return ""
		}
		return text
	}
}
