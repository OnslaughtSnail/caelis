package gateway

import (
	"context"
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
	resolver TurnResolver
	clock    func() time.Time

	mu     sync.Mutex
	active map[string]TurnHandle
	nextID atomic.Uint64
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
		resolver: cfg.Resolver,
		clock:    cfg.Clock,
		active:   map[string]TurnHandle{},
	}, nil
}

func (g *Gateway) StartSession(ctx context.Context, req StartSessionRequest) (sdksession.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return g.sessions.StartSession(ctx, sdksession.StartSessionRequest{
		AppName:            req.AppName,
		UserID:             req.UserID,
		Workspace:          req.Workspace,
		PreferredSessionID: req.PreferredSessionID,
		Title:              req.Title,
		Metadata:           cloneMap(req.Metadata),
	})
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
		return BeginTurnResult{}, err
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

type approvalRequesterFunc func(sdkruntime.ApprovalRequest) (sdkruntime.ApprovalResponse, error)

func (f approvalRequesterFunc) RequestApproval(ctx context.Context, req sdkruntime.ApprovalRequest) (sdkruntime.ApprovalResponse, error) {
	return f(req)
}
