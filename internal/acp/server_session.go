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
	state, err := s.cfg.Adapter.NewSession(ctx, req, s.clientCapabilities())
	if err != nil {
		return NewSessionResponse{}, nil, err
	}
	sess := &serverSession{id: state.SessionID}
	sess.applyState(state)
	s.storeSession(sess)
	resp := NewSessionResponse{
		SessionID:     sess.id,
		ConfigOptions: sess.configOptionsSnapshot(),
		Modes:         sess.modeState(),
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	return resp, func() {
		_ = s.syncSessionSnapshot(sess.id, sess, updatedAt)
	}, nil
}

func (s *Server) listSessions(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
	return s.cfg.Adapter.ListSessions(ctx, req)
}

func (s *Server) loadSession(ctx context.Context, req LoadSessionRequest) (LoadSessionResponse, error) {
	loaded, err := s.cfg.Adapter.LoadSession(ctx, req, s.clientCapabilities())
	if err != nil {
		return LoadSessionResponse{}, err
	}
	sess := s.loadedSession(loaded.Session.SessionID)
	if sess == nil {
		sess = &serverSession{id: loaded.Session.SessionID}
		s.storeSession(sess)
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
		updatedAt = time.Now().UTC().Format(time.RFC3339)
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
	state, err := s.cfg.Adapter.SetMode(ctx, req)
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
	state, err := s.cfg.Adapter.SetConfigOption(ctx, req)
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
	if !s.hasAuthMethod(methodID) {
		return fmt.Errorf("unsupported authentication method %q", methodID)
	}
	if s.cfg.Authenticate != nil {
		if err := s.cfg.Authenticate(ctx, req); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.authOK = true
	s.mu.Unlock()
	return nil
}

func (s *Server) requireAuthenticated() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authOK {
		return nil
	}
	return errAuthenticationRequired
}

func (s *Server) hasAuthMethod(methodID string) bool {
	methodID = strings.TrimSpace(methodID)
	for _, method := range s.cfg.AuthMethods {
		if strings.TrimSpace(method.ID) == methodID {
			return true
		}
	}
	return false
}

func (s *Server) loadedSession(id string) *serverSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[strings.TrimSpace(id)]
}

func (s *Server) session(id string) (*serverSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[strings.TrimSpace(id)]
	if !ok || sess == nil {
		return nil, fmt.Errorf("%w: %q", errSessionNotFound, id)
	}
	return sess, nil
}

func (s *Server) storeSession(sess *serverSession) {
	if sess == nil || strings.TrimSpace(sess.id) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[strings.TrimSpace(sess.id)] = sess
}

func (s *Server) syncSessionSnapshot(sessionID string, sess *serverSession, updatedAt string) error {
	if err := s.notifyAvailableCommands(sessionID, sess); err != nil {
		return err
	}
	if err := s.notifySessionInfo(sessionID, "", updatedAt); err != nil {
		return err
	}
	if err := s.notifyPlan(sessionID, sess.planSnapshot()); err != nil {
		return err
	}
	return nil
}

func (s *Server) sessionFS(sessionID string) toolexec.FileSystem {
	return s.cfg.Adapter.SessionFS(sessionID)
}

func (s *Server) cancelSession(id string) {
	s.cfg.Adapter.CancelPrompt(id)
}

func (s *Server) clientCapabilities() ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientCaps
}

func (s *serverSession) applyState(state AdapterSessionState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.id = strings.TrimSpace(state.SessionID)
	s.cwd = strings.TrimSpace(state.CWD)
	if state.Modes != nil {
		s.currentModeID = strings.TrimSpace(state.Modes.CurrentModeID)
		s.availableModes = append([]SessionMode(nil), state.Modes.AvailableModes...)
	}
	s.configOptions = append([]SessionConfigOption(nil), state.ConfigOptions...)
	s.availableCommands = append([]AvailableCommand(nil), state.AvailableCommands...)
	s.planEntries = append([]PlanEntry(nil), state.PlanEntries...)
}

func (s *serverSession) currentMode() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.currentModeID)
}

func (s *serverSession) permissionBridge(conn *Conn) *permissionBridge {
	if s == nil {
		return nil
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.approver == nil {
		s.approver = newPermissionBridge(conn, s.id, s.currentMode)
	}
	return s.approver
}

func (s *serverSession) modeState() *SessionModeState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(s.availableModes) == 0 && strings.TrimSpace(s.currentModeID) == "" {
		return nil
	}
	return &SessionModeState{
		AvailableModes: append([]SessionMode(nil), s.availableModes...),
		CurrentModeID:  strings.TrimSpace(s.currentModeID),
	}
}

func (s *serverSession) configOptionsSnapshot() []SessionConfigOption {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]SessionConfigOption(nil), s.configOptions...)
}

func (s *serverSession) availableCommandsSnapshot() []AvailableCommand {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]AvailableCommand(nil), s.availableCommands...)
}

func (s *serverSession) planSnapshot() []PlanEntry {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]PlanEntry(nil), s.planEntries...)
}

func (s *serverSession) setPlan(entries []PlanEntry) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.planEntries = append([]PlanEntry(nil), entries...)
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
