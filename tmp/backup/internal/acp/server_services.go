package acp

import (
	"context"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type serverServices struct {
	authMethods  []AuthMethod
	authenticate AuthValidator
	adapter      Adapter
}

func newServerServices(cfg ServerConfig) *serverServices {
	return &serverServices{
		authMethods:  append([]AuthMethod(nil), cfg.AuthMethods...),
		authenticate: cfg.Authenticate,
		adapter:      cfg.Adapter,
	}
}

func (s *serverServices) authMethodList() []AuthMethod {
	if s == nil {
		return nil
	}
	return append([]AuthMethod(nil), s.authMethods...)
}

func (s *serverServices) hasAuthMethod(methodID string) bool {
	if s == nil {
		return false
	}
	methodID = strings.TrimSpace(methodID)
	for _, method := range s.authMethods {
		if strings.TrimSpace(method.ID) == methodID {
			return true
		}
	}
	return false
}

func (s *serverServices) validateAuthentication(ctx context.Context, req AuthenticateRequest) error {
	if s == nil || s.authenticate == nil {
		return nil
	}
	return s.authenticate(ctx, req)
}

func (s *serverServices) capabilities() AdapterCapabilities {
	if s == nil || s.adapter == nil {
		return AdapterCapabilities{}
	}
	return s.adapter.Capabilities()
}

func (s *serverServices) newSession(ctx context.Context, req NewSessionRequest, caps ClientCapabilities) (AdapterSessionState, error) {
	return s.adapter.NewSession(ctx, AdapterNewSessionRequest{
		SessionID:  req.SessionID,
		CWD:        req.CWD,
		Meta:       CloneMeta(req.Meta),
		MCPServers: append([]MCPServer(nil), req.MCPServers...),
	}, caps)
}

func (s *serverServices) listSessions(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
	return s.adapter.ListSessions(ctx, req)
}

func (s *serverServices) loadSession(ctx context.Context, req LoadSessionRequest, caps ClientCapabilities) (LoadedSessionState, error) {
	return s.adapter.LoadSession(ctx, AdapterLoadSessionRequest{
		SessionID:  req.SessionID,
		CWD:        req.CWD,
		Meta:       CloneMeta(req.Meta),
		MCPServers: append([]MCPServer(nil), req.MCPServers...),
	}, caps)
}

func (s *serverServices) setMode(ctx context.Context, req SetSessionModeRequest) (AdapterSessionState, error) {
	return s.adapter.SetMode(ctx, req)
}

func (s *serverServices) setConfigOption(ctx context.Context, req SetSessionConfigOptionRequest) (AdapterSessionState, error) {
	return s.adapter.SetConfigOption(ctx, req)
}

func (s *serverServices) startPrompt(ctx context.Context, req StartPromptRequest) (StartPromptResult, error) {
	return s.adapter.StartPrompt(ctx, req)
}

func (s *serverServices) cancelPrompt(sessionID string) {
	if s == nil || s.adapter == nil {
		return
	}
	s.adapter.CancelPrompt(sessionID)
}

func (s *serverServices) sessionFS(sessionID string) toolexec.FileSystem {
	if s == nil || s.adapter == nil {
		return nil
	}
	return s.adapter.SessionFS(sessionID)
}
