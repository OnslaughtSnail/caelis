package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
)

var (
	errAuthenticationRequired = errors.New("authentication required")
	errSessionNotFound        = errors.New("session not found")
)

func (s *Server) newSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, func(), error) {
	state, err := s.svcs.newSession(ctx, req, s.state.clientCapabilities())
	if err != nil {
		return NewSessionResponse{}, nil, err
	}
	sess := &serverSession{id: state.SessionID}
	sess.applyState(state)
	s.state.storeSession(sess)
	resp := NewSessionResponse{
		SessionID:     sess.id,
		ConfigOptions: sess.configOptionsSnapshot(),
		Modes:         sess.modeState(),
	}
	updatedAt := currentTimestampRFC3339()
	return resp, func() {
		_ = s.syncSessionSnapshot(sess.id, sess, updatedAt)
	}, nil
}

func (s *Server) listSessions(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
	return s.svcs.listSessions(ctx, req)
}

func (s *Server) loadSession(ctx context.Context, req LoadSessionRequest) (LoadSessionResponse, error) {
	loaded, err := s.svcs.loadSession(ctx, req, s.state.clientCapabilities())
	if err != nil {
		return LoadSessionResponse{}, err
	}
	sess := s.state.loadedSession(loaded.Session.SessionID)
	if sess == nil {
		sess = &serverSession{id: loaded.Session.SessionID}
		s.state.storeSession(sess)
	}
	sess.applyState(loaded.Session)
	updatedAt := ""
	for _, ev := range loaded.Events {
		if ev == nil {
			continue
		}
		if updatedAt == "" && !ev.Time.IsZero() {
			updatedAt = ev.Time.UTC().Format(time.RFC3339)
		}
		if _, ok := runtime.LifecycleFromEvent(ev); ok {
			continue
		}
		if err := s.notifyEvent(req.SessionID, ev, nil); err != nil {
			return LoadSessionResponse{}, err
		}
	}
	if updatedAt == "" {
		updatedAt = currentTimestampRFC3339()
	}
	if err := s.syncSessionSnapshot(req.SessionID, sess, updatedAt); err != nil {
		return LoadSessionResponse{}, err
	}
	return LoadSessionResponse{
		ConfigOptions: sess.configOptionsSnapshot(),
		Modes:         sess.modeState(),
	}, nil
}

func (s *Server) setSessionMode(ctx context.Context, req SetSessionModeRequest) (SetSessionModeResponse, error) {
	state, err := s.svcs.setMode(ctx, req)
	if err != nil {
		return SetSessionModeResponse{}, err
	}
	sess, err := s.session(state.SessionID)
	if err != nil {
		return SetSessionModeResponse{}, err
	}
	sess.applyState(state)
	if err := s.notifyCurrentMode(req.SessionID, sess.currentMode()); err != nil {
		return SetSessionModeResponse{}, err
	}
	if err := s.notifyConfigOptions(req.SessionID, sess.configOptionsSnapshot()); err != nil {
		return SetSessionModeResponse{}, err
	}
	if err := s.notifyAvailableCommands(req.SessionID, sess); err != nil {
		return SetSessionModeResponse{}, err
	}
	return SetSessionModeResponse{}, nil
}

func (s *Server) setSessionConfigOption(ctx context.Context, req SetSessionConfigOptionRequest) (SetSessionConfigOptionResponse, error) {
	state, err := s.svcs.setConfigOption(ctx, req)
	if err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	sess, err := s.session(state.SessionID)
	if err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	sess.applyState(state)
	if err := s.notifyCurrentMode(req.SessionID, sess.currentMode()); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	options := sess.configOptionsSnapshot()
	if err := s.notifyConfigOptions(req.SessionID, options); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	if err := s.notifyAvailableCommands(req.SessionID, sess); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	return SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

func (s *Server) authenticate(ctx context.Context, req AuthenticateRequest) error {
	methodID := strings.TrimSpace(req.MethodID)
	if methodID == "" {
		return fmt.Errorf("authentication method is required")
	}
	if !s.svcs.hasAuthMethod(methodID) {
		return fmt.Errorf("unsupported authentication method %q", methodID)
	}
	if err := s.svcs.validateAuthentication(ctx, req); err != nil {
		return err
	}
	s.state.markAuthenticated()
	return nil
}

func (s *Server) requireAuthenticated() error {
	return s.state.requireAuthenticated()
}

func (s *Server) session(id string) (*serverSession, error) {
	return s.state.session(id)
}

func (s *Server) sessionFS(sessionID string) toolexec.FileSystem {
	return s.svcs.sessionFS(sessionID)
}

func (s *Server) cancelSession(id string) {
	s.svcs.cancelPrompt(id)
}

func currentModelID(options []SessionConfigOption) string {
	for _, item := range options {
		if strings.TrimSpace(item.Category) != "model" {
			continue
		}
		return strings.TrimSpace(item.CurrentValue)
	}
	return ""
}
