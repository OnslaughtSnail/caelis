package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type Config struct {
	Sessions sdksession.Service
	Runtime  sdkruntime.Runtime
	Resolver TurnResolver
	Clock    func() time.Time
}

type Gateway struct {
	sessions sdksession.Service
	runtime  sdkruntime.Runtime
	control  sdkruntime.ControlPlane
	resolver TurnResolver
	clock    func() time.Time

	mu       sync.Mutex
	active   map[string]*turnHandle
	bindings map[string]sessionBinding
	nextID   atomic.Uint64
}

type sessionBinding struct {
	current sdksession.SessionRef
	boundAt time.Time
}

func New(cfg Config) (*Gateway, error) {
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("gateway: sessions service is required")
	}
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("gateway: runtime is required")
	}
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("gateway: turn resolver is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Gateway{
		sessions: cfg.Sessions,
		runtime:  cfg.Runtime,
		control:  resolveControlPlane(cfg.Runtime),
		resolver: cfg.Resolver,
		clock:    cfg.Clock,
		active:   map[string]*turnHandle{},
		bindings: map[string]sessionBinding{},
	}, nil
}

func resolveControlPlane(runtime sdkruntime.Runtime) sdkruntime.ControlPlane {
	if control, ok := runtime.(sdkruntime.ControlPlane); ok {
		return control
	}
	return nil
}

func (g *Gateway) StartSession(ctx context.Context, req StartSessionRequest) (sdksession.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := g.sessions.StartSession(ctx, sdksession.StartSessionRequest{
		AppName:            req.AppName,
		UserID:             req.UserID,
		Workspace:          req.Workspace,
		PreferredSessionID: req.PreferredSessionID,
		Title:              req.Title,
		Metadata:           cloneMap(req.Metadata),
	})
	if err != nil {
		return sdksession.Session{}, wrapSessionError(err)
	}
	g.bind(req.BindingKey, session.SessionRef)
	return session, nil
}

func (g *Gateway) ForkSession(ctx context.Context, req ForkSessionRequest) (sdksession.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.SourceSessionRef.SessionID) == "" {
		return sdksession.Session{}, &Error{
			Kind:        KindValidation,
			Code:        CodeInvalidRequest,
			UserVisible: true,
			Message:     "gateway: source session ref is required",
		}
	}
	source, err := g.sessions.Session(ctx, req.SourceSessionRef)
	if err != nil {
		return sdksession.Session{}, wrapSessionError(err)
	}
	metadata := cloneMap(source.Metadata)
	for key, value := range req.Metadata {
		metadata[key] = value
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["forked_from_session_id"] = source.SessionID
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = source.Title
	}
	started, err := g.sessions.StartSession(ctx, sdksession.StartSessionRequest{
		AppName:            source.AppName,
		UserID:             source.UserID,
		Workspace:          sdksession.WorkspaceRef{Key: source.WorkspaceKey, CWD: source.CWD},
		PreferredSessionID: req.PreferredSessionID,
		Title:              title,
		Metadata:           metadata,
	})
	if err != nil {
		return sdksession.Session{}, wrapSessionError(err)
	}
	g.bind(req.BindingKey, started.SessionRef)
	return started, nil
}

func (g *Gateway) LoadSession(ctx context.Context, req LoadSessionRequest) (sdksession.LoadedSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, err := g.sessions.LoadSession(ctx, sdksession.LoadSessionRequest{
		SessionRef:       req.SessionRef,
		Limit:            req.Limit,
		IncludeTransient: req.IncludeTransient,
	})
	if err != nil {
		return sdksession.LoadedSession{}, wrapSessionError(err)
	}
	g.bind(req.BindingKey, loaded.Session.SessionRef)
	return loaded, nil
}

func (g *Gateway) ResumeSession(ctx context.Context, req ResumeSessionRequest) (sdksession.LoadedSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	list, err := g.ListSessions(ctx, ListSessionsRequest{
		AppName:      req.AppName,
		UserID:       req.UserID,
		WorkspaceKey: req.Workspace.Key,
		Limit:        200,
	})
	if err != nil {
		return sdksession.LoadedSession{}, err
	}
	target, err := g.resolveResumeTarget(req, list.Sessions)
	if err != nil {
		return sdksession.LoadedSession{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	return g.LoadSession(ctx, LoadSessionRequest{
		SessionRef:       target.SessionRef,
		Limit:            limit,
		IncludeTransient: req.IncludeTransient,
		BindingKey:       req.BindingKey,
	})
}

func (g *Gateway) ListSessions(ctx context.Context, req ListSessionsRequest) (sdksession.SessionList, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	list, err := g.sessions.ListSessions(ctx, sdksession.ListSessionsRequest{
		AppName:      req.AppName,
		UserID:       req.UserID,
		WorkspaceKey: req.WorkspaceKey,
		Cursor:       req.Cursor,
		Limit:        req.Limit,
	})
	if err != nil {
		return sdksession.SessionList{}, wrapSessionError(err)
	}
	return list, nil
}

func (g *Gateway) Interrupt(ctx context.Context, req InterruptRequest) error {
	_ = ctx
	ref, err := g.interruptTarget(req)
	if err != nil {
		return err
	}
	g.mu.Lock()
	handle, ok := g.active[ref.SessionID]
	g.mu.Unlock()
	if !ok || handle == nil {
		return &Error{
			Kind:        KindConflict,
			Code:        CodeNoActiveRun,
			UserVisible: true,
			Message:     "gateway: session has no active run",
		}
	}
	if !handle.Cancel() {
		return &Error{
			Kind:        KindConflict,
			Code:        CodeNoActiveRun,
			UserVisible: true,
			Message:     "gateway: session has no active run",
		}
	}
	return nil
}

func (g *Gateway) HandoffController(ctx context.Context, req HandoffControllerRequest) (sdksession.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g.control == nil {
		return sdksession.Session{}, &Error{
			Kind:        KindUnsupported,
			Code:        CodeControlPlaneUnsupported,
			UserVisible: true,
			Message:     "gateway: control plane is not available",
		}
	}
	ref, err := g.sessionTarget(req.SessionRef, req.BindingKey)
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err := g.control.HandoffController(ctx, sdkruntime.HandoffControllerRequest{
		SessionRef: ref,
		Kind:       req.Kind,
		Agent:      strings.TrimSpace(req.Agent),
		Source:     strings.TrimSpace(req.Source),
		Reason:     strings.TrimSpace(req.Reason),
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	g.bind(req.BindingKey, session.SessionRef)
	return session, nil
}

func (g *Gateway) ControlPlaneState(ctx context.Context, req ControlPlaneStateRequest) (ControlPlaneState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ref, err := g.sessionTarget(req.SessionRef, req.BindingKey)
	if err != nil {
		return ControlPlaneState{}, err
	}
	session, err := g.sessions.Session(ctx, ref)
	if err != nil {
		return ControlPlaneState{}, wrapSessionError(err)
	}
	runState, err := g.runtime.RunState(ctx, ref)
	if err != nil && !errors.Is(err, sdksession.ErrSessionNotFound) {
		return ControlPlaneState{}, err
	}
	return buildControlPlaneState(session, runState), nil
}

func (g *Gateway) BeginTurn(ctx context.Context, req BeginTurnRequest) (BeginTurnResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	intent := TurnIntent(req)
	resolved, err := g.resolver.ResolveTurn(ctx, intent)
	if err != nil {
		return BeginTurnResult{}, err
	}
	session, err := g.sessions.Session(ctx, req.SessionRef)
	if err != nil {
		return BeginTurnResult{}, wrapSessionError(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	cancelFn := sync.OnceValue(func() bool {
		cancel()
		return true
	})
	g.mu.Lock()
	if _, ok := g.active[session.SessionID]; ok {
		g.mu.Unlock()
		return BeginTurnResult{}, &Error{
			Kind:        KindConflict,
			Code:        CodeActiveRunConflict,
			UserVisible: true,
			Message:     "gateway: session already has an active run",
		}
	}
	handle := newTurnHandle(turnHandleConfig{
		handleID:   g.allocateID("handle"),
		runID:      g.allocateID("run"),
		turnID:     g.allocateID("turn"),
		sessionRef: session.SessionRef,
		createdAt:  g.clock(),
		cancel: func() bool {
			return cancelFn()
		},
	})
	g.active[session.SessionID] = handle
	g.mu.Unlock()

	go g.runTurn(runCtx, session, req, resolved, handle)

	return BeginTurnResult{
		Session: session,
		Handle:  handle,
	}, nil
}

func (g *Gateway) allocateID(prefix string) string {
	id := g.nextID.Add(1)
	return fmt.Sprintf("%s-%d", prefix, id)
}

func (g *Gateway) runTurn(
	ctx context.Context,
	session sdksession.Session,
	req BeginTurnRequest,
	resolved ResolvedTurn,
	handle *turnHandle,
) {
	defer handle.finish()
	defer g.releaseActive(session.SessionID, handle)

	runReq := resolved.RunRequest
	runReq.SessionRef = session.SessionRef
	if strings.TrimSpace(runReq.Input) == "" {
		runReq.Input = req.Input
	}
	if len(runReq.ContentParts) == 0 && len(req.ContentParts) > 0 {
		runReq.ContentParts = append([]sdkmodel.ContentPart(nil), req.ContentParts...)
	}
	runReq.ApprovalRequester = approvalRequesterFunc(func(req sdkruntime.ApprovalRequest) (sdkruntime.ApprovalResponse, error) {
		wait := handle.publishApproval(&req)
		select {
		case decision := <-wait:
			return sdkruntime.ApprovalResponse{
				Outcome:  decision.Outcome,
				OptionID: decision.OptionID,
				Approved: decision.Approved,
			}, nil
		case <-ctx.Done():
			return sdkruntime.ApprovalResponse{}, ctx.Err()
		}
	})

	result, err := g.runtime.Run(ctx, runReq)
	if err != nil {
		handle.publish(EventEnvelope{
			Event: Event{
				Kind:       EventKindLifecycle,
				HandleID:   handle.handleID,
				RunID:      handle.runID,
				TurnID:     handle.turnID,
				SessionRef: handle.sessionRef,
			},
			Err: err,
		})
		return
	}
	if result.Handle == nil {
		return
	}
	handle.setRunner(result.Handle)
	defer result.Handle.Close()
	for event, seqErr := range result.Handle.Events() {
		if seqErr != nil {
			handle.publish(EventEnvelope{
				Event: Event{
					Kind:       EventKindLifecycle,
					HandleID:   handle.handleID,
					RunID:      handle.runID,
					TurnID:     handle.turnID,
					SessionRef: handle.sessionRef,
				},
				Err: seqErr,
			})
			return
		}
		handle.publishSessionEvent(event)
	}
}

func (g *Gateway) releaseActive(sessionID string, handle *turnHandle) {
	g.mu.Lock()
	defer g.mu.Unlock()
	current, ok := g.active[sessionID]
	if !ok || current != handle {
		return
	}
	delete(g.active, sessionID)
}

func (g *Gateway) CurrentSession(bindingKey string) (sdksession.SessionRef, bool) {
	if g == nil || strings.TrimSpace(bindingKey) == "" {
		return sdksession.SessionRef{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	binding, ok := g.bindings[strings.TrimSpace(bindingKey)]
	if !ok || strings.TrimSpace(binding.current.SessionID) == "" {
		return sdksession.SessionRef{}, false
	}
	return binding.current, true
}

func (g *Gateway) ClearBinding(bindingKey string) {
	if g == nil || strings.TrimSpace(bindingKey) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.bindings, strings.TrimSpace(bindingKey))
}

func (g *Gateway) bind(bindingKey string, ref sdksession.SessionRef) {
	if g == nil || strings.TrimSpace(bindingKey) == "" || strings.TrimSpace(ref.SessionID) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bindings[strings.TrimSpace(bindingKey)] = sessionBinding{
		current: ref,
		boundAt: g.clock(),
	}
}

func (g *Gateway) resolveResumeTarget(req ResumeSessionRequest, sessions []sdksession.SessionSummary) (sdksession.SessionSummary, error) {
	target := strings.TrimSpace(req.SessionID)
	if target != "" {
		return resolveSessionSummary(sessions, target)
	}
	exclude := strings.TrimSpace(req.ExcludeSessionID)
	if exclude == "" {
		if current, ok := g.CurrentSession(req.BindingKey); ok {
			exclude = current.SessionID
		}
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.SessionID) == "" || session.SessionID == exclude {
			continue
		}
		return session, nil
	}
	return sdksession.SessionSummary{}, &Error{
		Kind:        KindNotFound,
		Code:        CodeNoResumableSession,
		UserVisible: true,
		Message:     "gateway: no resumable session found",
	}
}

func resolveSessionSummary(sessions []sdksession.SessionSummary, target string) (sdksession.SessionSummary, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return sdksession.SessionSummary{}, &Error{
			Kind:        KindValidation,
			Code:        CodeInvalidRequest,
			UserVisible: true,
			Message:     "gateway: session id is required",
		}
	}
	var exact *sdksession.SessionSummary
	var prefixMatches []sdksession.SessionSummary
	for _, session := range sessions {
		id := strings.TrimSpace(session.SessionID)
		if id == "" {
			continue
		}
		if id == target {
			copy := session
			exact = &copy
			break
		}
		if strings.HasPrefix(id, target) {
			prefixMatches = append(prefixMatches, session)
		}
	}
	if exact != nil {
		return *exact, nil
	}
	switch len(prefixMatches) {
	case 1:
		return prefixMatches[0], nil
	case 0:
		return sdksession.SessionSummary{}, &Error{
			Kind:        KindNotFound,
			Code:        CodeSessionNotFound,
			UserVisible: true,
			Message:     "gateway: session not found",
		}
	default:
		return sdksession.SessionSummary{}, &Error{
			Kind:        KindConflict,
			Code:        CodeSessionAmbiguous,
			UserVisible: true,
			Message:     "gateway: session id is ambiguous",
		}
	}
}

func (g *Gateway) interruptTarget(req InterruptRequest) (sdksession.SessionRef, error) {
	return g.sessionTarget(req.SessionRef, req.BindingKey)
}

func (g *Gateway) sessionTarget(ref sdksession.SessionRef, bindingKey string) (sdksession.SessionRef, error) {
	if strings.TrimSpace(ref.SessionID) != "" {
		return ref, nil
	}
	if current, ok := g.CurrentSession(bindingKey); ok {
		return current, nil
	}
	if strings.TrimSpace(bindingKey) != "" {
		return sdksession.SessionRef{}, &Error{
			Kind:        KindNotFound,
			Code:        CodeBindingNotFound,
			UserVisible: true,
			Message:     "gateway: binding not found",
		}
	}
	return sdksession.SessionRef{}, &Error{
		Kind:        KindValidation,
		Code:        CodeInvalidRequest,
		UserVisible: true,
		Message:     "gateway: session ref or binding key is required",
	}
}

func wrapSessionError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, sdksession.ErrSessionNotFound):
		return &Error{
			Kind:        KindNotFound,
			Code:        CodeSessionNotFound,
			UserVisible: true,
			Message:     "gateway: session not found",
			Cause:       err,
		}
	case errors.Is(err, sdksession.ErrInvalidSession):
		return &Error{
			Kind:        KindValidation,
			Code:        CodeInvalidRequest,
			UserVisible: true,
			Message:     "gateway: invalid session request",
			Cause:       err,
		}
	default:
		return err
	}
}

type approvalRequesterFunc func(sdkruntime.ApprovalRequest) (sdkruntime.ApprovalResponse, error)

func (f approvalRequesterFunc) RequestApproval(ctx context.Context, req sdkruntime.ApprovalRequest) (sdkruntime.ApprovalResponse, error) {
	return f(req)
}
