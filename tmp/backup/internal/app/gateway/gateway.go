package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/sessionsvc"
)

type ChannelRef struct {
	ID           string
	AppName      string
	UserID       string
	WorkspaceKey string
	WorkspaceCWD string
}

type ActorRef struct {
	ID   string
	Kind string
}

type CapabilitySet map[string]bool

type ArtifactRef struct {
	ID       string
	Name     string
	URI      string
	MimeType string
}

type InboundEnvelope struct {
	Session  sessionsvc.SessionRef
	Actor    ActorRef
	Content  string
	Parts    []model.ContentPart
	Stream   bool
	Metadata map[string]any
}

type OutboundEnvelope struct {
	Session   sessionsvc.SessionRef
	Artifacts []ArtifactRef
	Metadata  map[string]any
}

type StartSessionRequest struct {
	Channel            ChannelRef
	PreferredSessionID string
}

type ResumeSessionRequest struct {
	Channel          ChannelRef
	SessionID        string
	ExcludeSessionID string
}

type RunTurnRequest struct {
	Channel             ChannelRef
	SessionID           string
	Input               string
	ContentParts        []model.ContentPart
	InvocationPrelude   []model.Message
	ControllerKind      string
	ControllerID        string
	EpochID             string
	Agent               agent.Agent
	Model               model.LLM
	ContextWindowTokens int
}

type RunTurnResult struct {
	Session sessionsvc.SessionInfo
	Handle  sessionsvc.TurnHandle
}

type channelBinding struct {
	current sessionsvc.SessionRef
}

type Gateway struct {
	service *sessionsvc.Service

	mu       sync.Mutex
	bindings map[string]channelBinding
}

func New(service *sessionsvc.Service) (*Gateway, error) {
	if service == nil {
		return nil, fmt.Errorf("gateway: session service is required")
	}
	return &Gateway{
		service:  service,
		bindings: map[string]channelBinding{},
	}, nil
}

func (g *Gateway) StartSession(ctx context.Context, req StartSessionRequest) (sessionsvc.SessionInfo, error) {
	if g == nil || g.service == nil {
		return sessionsvc.SessionInfo{}, fmt.Errorf("gateway: service is unavailable")
	}
	info, err := g.service.StartSession(ctx, sessionsvc.StartSessionRequest{
		AppName:            req.Channel.AppName,
		UserID:             req.Channel.UserID,
		Workspace:          sessionsvc.WorkspaceRef{Key: req.Channel.WorkspaceKey, CWD: req.Channel.WorkspaceCWD},
		PreferredSessionID: req.PreferredSessionID,
	})
	if err != nil {
		return sessionsvc.SessionInfo{}, err
	}
	g.bind(req.Channel.ID, info.SessionRef)
	return info, nil
}

func (g *Gateway) ForkSession(ctx context.Context, req StartSessionRequest) (sessionsvc.SessionInfo, error) {
	return g.StartSession(ctx, req)
}

func (g *Gateway) ResumeSession(ctx context.Context, req ResumeSessionRequest) (sessionsvc.LoadedSession, error) {
	if g == nil || g.service == nil {
		return sessionsvc.LoadedSession{}, fmt.Errorf("gateway: service is unavailable")
	}
	target := strings.TrimSpace(req.SessionID)
	if target != "" {
		resolved, ok, err := g.service.ResolveWorkspaceSession(ctx, req.Channel.WorkspaceKey, target)
		if err != nil {
			return sessionsvc.LoadedSession{}, err
		}
		if !ok {
			return sessionsvc.LoadedSession{}, fmt.Errorf("session %q not found in current workspace", target)
		}
		target = resolved.SessionID
	} else {
		exclude := strings.TrimSpace(req.ExcludeSessionID)
		if exclude == "" {
			if current, ok := g.CurrentSession(req.Channel.ID); ok {
				exclude = current.SessionID
			}
		}
		resolved, ok, err := g.service.MostRecentWorkspaceSession(ctx, req.Channel.WorkspaceKey, exclude)
		if err != nil {
			return sessionsvc.LoadedSession{}, err
		}
		if !ok {
			return sessionsvc.LoadedSession{}, fmt.Errorf("no resumable session found in current workspace")
		}
		target = resolved.SessionID
	}
	loaded, err := g.service.LoadSession(ctx, sessionsvc.LoadSessionRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      req.Channel.AppName,
			UserID:       req.Channel.UserID,
			SessionID:    target,
			WorkspaceKey: req.Channel.WorkspaceKey,
		},
		CWD:              req.Channel.WorkspaceCWD,
		Limit:            200,
		IncludeLifecycle: false,
	})
	if err != nil {
		return sessionsvc.LoadedSession{}, err
	}
	g.bind(req.Channel.ID, loaded.SessionRef)
	return loaded, nil
}

func (g *Gateway) RunTurn(ctx context.Context, req RunTurnRequest) (RunTurnResult, error) {
	if g == nil || g.service == nil {
		return RunTurnResult{}, fmt.Errorf("gateway: service is unavailable")
	}
	ref, ok := g.boundOrExplicit(req.Channel, req.SessionID)
	if !ok {
		started, err := g.StartSession(ctx, StartSessionRequest{
			Channel:            req.Channel,
			PreferredSessionID: "",
		})
		if err != nil {
			return RunTurnResult{}, err
		}
		ref = started.SessionRef
	}
	result, err := g.service.RunTurn(ctx, sessionsvc.RunTurnRequest{
		SessionRef:          ref,
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
		return RunTurnResult{}, err
	}
	g.setCurrent(req.Channel.ID, result.Session.SessionRef)
	return RunTurnResult{
		Session: result.Session,
		Handle:  result.Handle,
	}, nil
}

func (g *Gateway) InterruptSession(ctx context.Context, channel ChannelRef, reason string) error {
	if g == nil || g.service == nil {
		return fmt.Errorf("gateway: service is unavailable")
	}
	ref, ok := g.CurrentSession(channel.ID)
	if !ok {
		return nil
	}
	return g.service.InterruptSession(ctx, sessionsvc.InterruptSessionRequest{
		SessionRef: ref,
		Reason:     reason,
	})
}

func (g *Gateway) VisibleTools() ([]string, error) {
	if g == nil || g.service == nil {
		return nil, fmt.Errorf("gateway: service is unavailable")
	}
	tools, err := g.service.VisibleTools()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tools))
	for _, one := range tools {
		if one == nil {
			continue
		}
		out = append(out, strings.TrimSpace(one.Name()))
	}
	return out, nil
}

func (g *Gateway) CurrentSession(channelID string) (sessionsvc.SessionRef, bool) {
	if g == nil || strings.TrimSpace(channelID) == "" {
		return sessionsvc.SessionRef{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	binding, ok := g.bindings[strings.TrimSpace(channelID)]
	if !ok {
		return sessionsvc.SessionRef{}, false
	}
	return binding.current, strings.TrimSpace(binding.current.SessionID) != ""
}

func (g *Gateway) bind(channelID string, ref sessionsvc.SessionRef) {
	if g == nil || strings.TrimSpace(channelID) == "" || strings.TrimSpace(ref.SessionID) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bindings[strings.TrimSpace(channelID)] = channelBinding{current: ref}
}

func (g *Gateway) setCurrent(channelID string, ref sessionsvc.SessionRef) {
	if g == nil || strings.TrimSpace(channelID) == "" || strings.TrimSpace(ref.SessionID) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := strings.TrimSpace(channelID)
	binding := g.bindings[key]
	binding.current = ref
	g.bindings[key] = binding
}

func (g *Gateway) boundOrExplicit(channel ChannelRef, sessionID string) (sessionsvc.SessionRef, bool) {
	if strings.TrimSpace(sessionID) != "" {
		return sessionsvc.SessionRef{
			AppName:      channel.AppName,
			UserID:       channel.UserID,
			SessionID:    strings.TrimSpace(sessionID),
			WorkspaceKey: channel.WorkspaceKey,
		}, true
	}
	ref, ok := g.CurrentSession(channel.ID)
	if ok {
		return ref, true
	}
	return sessionsvc.SessionRef{}, false
}
